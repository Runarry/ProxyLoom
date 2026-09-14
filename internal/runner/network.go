package runner

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/netip"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Runarry/ProxyLoom/internal/adapter/mihomo"
	"github.com/Runarry/ProxyLoom/internal/adapter/singbox"
	"github.com/Runarry/ProxyLoom/internal/adapter/xray"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/networktest"
	coreexec "github.com/Runarry/ProxyLoom/internal/runner/exec"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

var testPorts = struct {
	sync.Mutex
	used map[int]bool
}{used: map[int]bool{}}

func reserveTestPort() (int, func(), error) {
	testPorts.Lock()
	defer testPorts.Unlock()
	for port := 20000; port <= 20127; port++ {
		if testPorts.used[port] {
			continue
		}
		listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			continue
		}
		_ = listener.Close()
		testPorts.used[port] = true
		return port, func() { testPorts.Lock(); delete(testPorts.used, port); testPorts.Unlock() }, nil
	}
	return 0, nil, errors.New("test_port_busy")
}
func (c *Client) networkPhase(ctx context.Context, lease *runnerprotocol.Lease, phase string) error {
	event := runnerprotocol.EventRequest{JobID: lease.JobID, Attempt: lease.Attempt, LeaseSeq: lease.LeaseSeq, EventID: capability.NameUUID(lease.JobID, phase+time.Now().UTC().Format(time.RFC3339Nano)), Phase: phase, Total: 1}
	var response runnerprotocol.Response[runnerprotocol.EventReceipt]
	return c.post(ctx, "/internal/v1/jobs/"+string(lease.JobID)+"/events", event, &response)
}
func (c *Client) executeNetwork(ctx context.Context, lease *runnerprotocol.Lease, issuedAt time.Time) error {
	if !c.config.NetworkEnabled || runnerprotocol.ValidatePayload(lease.FrozenPayload) != nil {
		return ErrControlRejected
	}
	hash, err := runnerprotocol.PayloadHash(lease.FrozenPayload)
	if err != nil || hash != lease.PayloadSHA256 {
		return ErrControlRejected
	}
	_, build, err := c.registry.Executable(lease.Core.CoreBuildID)
	core := lease.Core
	if err != nil || core.BuildSHA256 != build.BinarySHA256 || core.CoreFamily != build.Family || core.Version != build.Version || core.Platform != build.OS || core.Architecture != build.Arch || core.Architecture != runtime.GOARCH || core.AdapterVersion != build.AdapterVersion {
		return ErrControlRejected
	}
	if err = c.networkPhase(ctx, lease, "preparing"); err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	remaining := c.stopAfter(lease.LeaseExpiresAt, issuedAt)
	if remaining <= 0 || remaining > 30*time.Second {
		return ErrControlRejected
	}
	var reason atomic.Int32
	guard := time.AfterFunc(remaining, func() { reason.CompareAndSwap(0, 1); cancel() })
	defer guard.Stop()
	type outcome struct {
		result runnerprotocol.ResultRequest
		err    error
	}
	done := make(chan outcome, 1)
	go func() { r, e := c.runNetwork(runCtx, lease); done <- outcome{r, e} }()
	// Five seconds is the negotiated maximum heartbeat interval. Online work
	// polls sooner so cancellation plus process cleanup can finish in five.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case out := <-done:
			guard.Stop()
			if out.err != nil {
				return out.err
			}
			out.result.JobID = lease.JobID
			out.result.Attempt = lease.Attempt
			out.result.LeaseSeq = lease.LeaseSeq
			switch {
			case reason.Load() == 1:
				return ErrControlUnavailable
			case reason.Load() == 2 || ctx.Err() != nil:
				out.result.Metrics.ThroughputMbps = nil
				out.result.State = "canceled"
				out.result.Verdict = "inconclusive"
				out.result.Error = runnerprotocol.Safe("CANCELED")
			case runCtx.Err() != nil:
				out.result.Metrics.ThroughputMbps = nil
				out.result.State = "timed_out"
				out.result.Verdict = "inconclusive"
				out.result.Error = runnerprotocol.Safe("JOB_TIMEOUT")
			}
			return c.sendResult(ctx, out.result)
		case <-ticker.C:
			if runCtx.Err() != nil {
				continue
			}
			var response runnerprotocol.Response[runnerprotocol.HeartbeatData]
			before := time.Now()
			err := c.post(runCtx, "/internal/v1/jobs/"+string(lease.JobID)+"/heartbeat", runnerprotocol.HeartbeatRequest{JobID: lease.JobID, Attempt: lease.Attempt, LeaseSeq: lease.LeaseSeq}, &response)
			if err != nil {
				if errors.Is(err, ErrControlRejected) {
					reason.CompareAndSwap(0, 1)
					cancel()
				}
				continue
			}
			hb := response.Data
			if hb.JobID != lease.JobID || hb.Attempt != lease.Attempt || hb.LeaseSeq != lease.LeaseSeq {
				reason.CompareAndSwap(0, 1)
				cancel()
				continue
			}
			if hb.CancelRequested {
				reason.CompareAndSwap(0, 2)
				cancel()
				continue
			}
			extension := c.stopAfter(hb.LeaseExpiresAt, before)
			if extension <= 0 || extension > 30*time.Second || !hb.LeaseExpiresAt.After(lease.LeaseExpiresAt) {
				reason.CompareAndSwap(0, 1)
				cancel()
				continue
			}
			lease.LeaseExpiresAt = hb.LeaseExpiresAt
			guard.Reset(extension)
		}
	}
}

func (c *Client) runNetwork(ctx context.Context, lease *runnerprotocol.Lease) (result runnerprotocol.ResultRequest, finalErr error) {
	zero := int64(0)
	result = runnerprotocol.ResultRequest{State: "failed", Verdict: "inconclusive", Metrics: runnerprotocol.Metrics{BodyBytes: &zero}, Error: runnerprotocol.Safe("RUNNER_RESOURCE_LIMIT")}
	policy := c.networkPolicy
	for _, endpoint := range lease.ApprovedEndpoints {
		ip, err := netip.ParseAddr(endpoint.IP)
		if err != nil || !policy.Approved(ip) {
			result.Error = runnerprotocol.Safe("INVALID_CONFIG")
			return result, nil
		}
	}
	for _, value := range lease.TestTarget.ValidatedIPs {
		ip, err := netip.ParseAddr(value)
		if err != nil || !policy.Approved(ip) {
			result.Error = runnerprotocol.Safe("INVALID_CONFIG")
			return result, nil
		}
	}
	port, release, err := reserveTestPort()
	if err != nil {
		result.Error = runnerprotocol.Safe("PORT_BUSY")
		return result, nil
	}
	defer release()
	raw, err := base64.StdEncoding.Strict().DecodeString(lease.Artifact.ContentBase64)
	if err != nil {
		result.Error = runnerprotocol.Safe("INVALID_CONFIG")
		return result, nil
	}
	defer clear(raw)
	var config []byte
	switch lease.Core.CoreFamily {
	case ir.Xray:
		config, err = xray.BindTestListener(raw, port)
	case ir.SingBox:
		config, err = singbox.BindTestListener(raw, port)
	case ir.Mihomo:
		config, err = mihomo.BindTestListener(raw, port)
	}
	if err != nil || len(config) == 0 {
		result.Error = runnerprotocol.Safe("INVALID_CONFIG")
		return result, nil
	}
	defer clear(config)
	location := c.config.Location
	if location == "" {
		location = "self-hosted"
	}
	result.Observation = &runnerprotocol.NetworkObservation{Location: location, ExecutionSHA256: runnerprotocol.Digest(config), TruncatedBy: "none"}
	before, observed := cpuThrottleCount()
	defer func() {
		if after, ok := cpuThrottleCount(); observed && ok && after >= before {
			throttled := after > before
			result.Observation.CPUThrottled = &throttled
		}
	}()
	p := lease.ExecutionPolicy
	budget := coreexec.SandboxPolicy{MemoryBytes: uint64(p.MemoryLimitBytes), Processes: uint64(p.ProcessLimit), CPUSeconds: 60}
	if err = c.networkPhase(ctx, lease, "validating"); err != nil {
		return result, err
	}
	start := time.Now()
	check, err := c.validate(ctx, c.registry, lease.Core.CoreBuildID, config, 10*time.Second, budget)
	elapsed := float64(time.Since(start).Microseconds()) / 1000
	result.Metrics.ConfigCheckMS = &elapsed
	clear(check.Output)
	if unsafeCleanup(err) {
		return result, ErrUnsafeCleanup
	}
	if err != nil {
		return result, nil
	}
	if check.ExitCode != 0 || check.Canceled || check.TimedOut {
		result.State = "succeeded"
		result.Verdict = "fail"
		result.Error = runnerprotocol.Safe("CORE_CONFIG_INVALID")
		return result, nil
	}
	if err = c.networkPhase(ctx, lease, "starting"); err != nil {
		return result, err
	}
	budget.Network = &coreexec.NetworkPolicy{ListenerPort: uint16(port)}
	for _, endpoint := range lease.ApprovedEndpoints {
		budget.Network.RemotePorts = append(budget.Network.RemotePorts, uint16(endpoint.Port))
	}
	coreCtx, stop := context.WithCancel(ctx)
	defer stop()
	var pid atomic.Int32
	type exit struct {
		result coreexec.Result
		err    error
	}
	exited := make(chan exit, 1)
	start = time.Now()
	go func() {
		r, e := c.start(coreCtx, c.registry, lease.Core.CoreBuildID, config, budget, func(p int) { pid.Store(int32(p)) })
		exited <- exit{r, e}
	}()
	var ended *exit
	defer func() {
		if ended == nil {
			stop()
			e := <-exited
			clear(e.result.Output)
			if unsafeCleanup(e.err) {
				finalErr = ErrUnsafeCleanup
			}
		}
	}()
	ready := false
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	poll := time.NewTicker(25 * time.Millisecond)
	defer poll.Stop()
wait:
	for {
		select {
		case <-ctx.Done():
			break wait
		case e := <-exited:
			ended = &e
			clear(e.result.Output)
			if unsafeCleanup(e.err) {
				return result, ErrUnsafeCleanup
			}
			return result, nil
		case <-timer.C:
			result.Error = runnerprotocol.Safe("PORT_BUSY")
			break wait
		case <-poll.C:
			if ownsListener(int(pid.Load()), port) {
				ready = true
				break wait
			}
		}
	}
	if !ready {
		return result, nil
	}
	startup := float64(time.Since(start).Microseconds()) / 1000
	result.Metrics.CoreStartMS = &startup
	phase := "probing"
	if lease.Type == "download_throughput" {
		phase = "downloading"
	}
	if err = c.networkPhase(ctx, lease, phase); err != nil {
		return result, err
	}
	probe := networktest.Probe(ctx, lease.FrozenPayload, networktest.ProbeOptions{SOCKSAddress: net.JoinHostPort("127.0.0.1", strconv.Itoa(port))})
	probe.Metrics.ConfigCheckMS = &elapsed
	probe.Metrics.CoreStartMS = &startup
	result.State = "succeeded"
	result.Verdict = probe.Verdict
	result.Metrics = probe.Metrics
	result.Error = probe.Error
	result.Observation.TruncatedBy = probe.TruncatedBy
	stop()
	e := <-exited
	ended = &e
	clear(e.result.Output)
	if unsafeCleanup(e.err) {
		return result, ErrUnsafeCleanup
	}
	if e.err != nil {
		result.State = "failed"
		result.Verdict = "inconclusive"
		result.Error = runnerprotocol.Safe("RUNNER_RESOURCE_LIMIT")
	}
	return result, nil
}
func unsafeCleanup(err error) bool {
	return errors.Is(err, coreexec.ErrWorkspaceCleanup) || errors.Is(err, coreexec.ErrProcessCleanupTimeout) || errors.Is(err, coreexec.ErrLogDrainTimeout)
}
