package runner

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
	coreexec "github.com/Runarry/ProxyLoom/internal/runner/exec"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

var ErrControlUnavailable = errors.New("runner_control_unavailable")
var ErrControlRejected = errors.New("runner_control_rejected")
var ErrUnsafeCleanup = errors.New("runner_cleanup_failed")

const controlTimeout = 3 * time.Second
const leaseSafetyMargin = 5 * time.Second // TERM + KILL + one second transport allowance.

type ClientConfig struct {
	RunnerID ir.ID
	APIURL   string
	TLS      *tls.Config
	CoreRoot string
}

type Client struct {
	config      ClientConfig
	http        *http.Client
	registry    coreexec.Registry
	builds      []runnerprotocol.BuildReport
	clockOffset time.Duration
	ready       atomic.Bool
	validate    func(context.Context, coreexec.Registry, ir.ID, []byte, time.Duration, coreexec.SandboxPolicy) (coreexec.Result, error)
}

func NewClient(config ClientConfig) (*Client, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return nil, coreexec.ErrSandboxUnavailable
	}
	catalog, err := capability.Load()
	if err != nil {
		return nil, ErrControlRejected
	}
	registry := coreexec.DiskRegistry{Root: config.CoreRoot, Catalog: catalog}
	builds := []runnerprotocol.BuildReport{}
	for _, build := range catalog.Builds() {
		if build.OS != runtime.GOOS || build.Arch != runtime.GOARCH {
			continue
		}
		if _, _, err := registry.Executable(build.ID); err != nil {
			return nil, coreexec.ErrDigestMismatch
		}
		builds = append(builds, runnerprotocol.BuildReport{CoreBuildID: build.ID, BuildSHA256: build.BinarySHA256})
	}
	return newClient(config, registry, builds, coreexec.ValidateIsolated)
}

func newClient(config ClientConfig, registry coreexec.Registry, builds []runnerprotocol.BuildReport, validate func(context.Context, coreexec.Registry, ir.ID, []byte, time.Duration, coreexec.SandboxPolicy) (coreexec.Result, error)) (*Client, error) {
	u, err := url.Parse(config.APIURL)
	if err != nil || config.RunnerID.Validate() != nil || u.Scheme != "https" || u.Hostname() == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || config.TLS == nil || config.TLS.InsecureSkipVerify || config.TLS.RootCAs == nil || len(config.TLS.Certificates) != 1 || len(builds) == 0 || registry == nil || validate == nil {
		return nil, ErrControlRejected
	}
	tlsConfig := config.TLS.Clone()
	tlsConfig.MinVersion = tls.VersionTLS13
	tlsConfig.ServerName = u.Hostname()
	transport := &http.Transport{TLSClientConfig: tlsConfig, Proxy: nil, DisableCompression: true, MaxConnsPerHost: 2, TLSHandshakeTimeout: controlTimeout, ResponseHeaderTimeout: controlTimeout}
	client := &http.Client{Transport: transport, Timeout: controlTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Client{config: config, http: client, registry: registry, builds: append([]runnerprotocol.BuildReport(nil), builds...), validate: validate}, nil
}

func (c *Client) Ready() bool { return c.ready.Load() }

// Run owns one local validation slot. The queue's lease predicate and the
// server's registered slot limit enforce capacity independently of client data.
func (c *Client) Run(ctx context.Context) error {
	defer c.http.CloseIdleConnections()
	defer c.ready.Store(false)
	for ctx.Err() == nil {
		if err := c.register(ctx); err != nil {
			c.ready.Store(false)
			if !waitControl(ctx) {
				break
			}
			continue
		}
		var response runnerprotocol.Response[runnerprotocol.LeaseData]
		issuedAt := time.Now()
		err := c.post(ctx, "/internal/v1/jobs/lease", runnerprotocol.LeaseRequest{RunnerID: c.config.RunnerID, AvailableSlots: runnerprotocol.Slots{ConfigValidate: 1}}, &response)
		if err != nil || response.Data.Lease == nil {
			if err != nil {
				c.ready.Store(false)
			}
			if !waitControl(ctx) {
				break
			}
			continue
		}
		if err := c.executeLeaseFrom(ctx, response.Data.Lease, issuedAt); err != nil {
			c.ready.Store(false)
			if errors.Is(err, ErrUnsafeCleanup) {
				return err
			}
		}
	}
	return nil
}

func waitControl(ctx context.Context) bool {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (c *Client) register(ctx context.Context) error {
	request := runnerprotocol.RegisterRequest{RunnerID: c.config.RunnerID, Platform: "linux", Architecture: runtime.GOARCH, Builds: c.builds, AvailableSlots: runnerprotocol.Slots{ConfigValidate: 1}, Load: runnerprotocol.Load{MemoryAvailableBytes: 0}}
	var response runnerprotocol.Response[runnerprotocol.RegisterData]
	before := time.Now()
	if err := c.post(ctx, "/internal/v1/runners/heartbeat", request, &response); err != nil {
		return err
	}
	data := response.Data
	if data.RunnerID != c.config.RunnerID || data.HeartbeatIntervalMS != 5000 || data.LeaseDurationMS != 30000 || data.ServerTime.IsZero() || len(data.AcceptedBuildIDs) != len(c.builds) {
		return ErrControlRejected
	}
	seen := make(map[ir.ID]bool)
	for _, id := range data.AcceptedBuildIDs {
		seen[id] = true
	}
	for _, build := range c.builds {
		if !seen[build.CoreBuildID] {
			return ErrControlRejected
		}
	}
	// Starting the offset at request dispatch overestimates server time by at
	// most round-trip latency, making the local stop deadline conservative.
	c.clockOffset = data.ServerTime.Sub(before)
	c.ready.Store(true)
	return nil
}

func (c *Client) executeLease(ctx context.Context, lease *runnerprotocol.Lease) error {
	return c.executeLeaseFrom(ctx, lease, time.Now())
}

func (c *Client) executeLeaseFrom(ctx context.Context, lease *runnerprotocol.Lease, issuedAt time.Time) error {
	defer func() { lease.Artifact.ContentBase64 = "" }()
	if lease.JobID.Validate() != nil || lease.Attempt < 1 || lease.Attempt > 100 || lease.LeaseSeq < 1 || lease.LeaseExpiresAt.IsZero() {
		return ErrControlRejected
	}
	result := runnerprotocol.ResultRequest{JobID: lease.JobID, Attempt: lease.Attempt, LeaseSeq: lease.LeaseSeq, State: "failed", Error: runnerprotocol.Safe("INVALID_CONFIG"), Metrics: runnerprotocol.Metrics{}}
	config, err := c.validateLease(lease)
	if err != nil {
		return c.sendResult(ctx, result)
	}
	defer clear(config)
	remaining := c.stopAfter(lease.LeaseExpiresAt, issuedAt)
	if remaining <= 0 || remaining > 30*time.Second {
		return ErrControlRejected
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	var stopReason atomic.Int32 // 1=lease lost, 2=server cancellation.
	guard := time.AfterFunc(remaining, func() { stopReason.CompareAndSwap(0, 1); stop() })
	defer guard.Stop()
	identity := runnerprotocol.HeartbeatRequest{JobID: lease.JobID, Attempt: lease.Attempt, LeaseSeq: lease.LeaseSeq}
	event := runnerprotocol.EventRequest{JobID: lease.JobID, Attempt: lease.Attempt, LeaseSeq: lease.LeaseSeq, EventID: capability.NameUUID(lease.JobID, "validating|"+time.Now().UTC().Format(time.RFC3339Nano)), Phase: "validating", Total: 1}
	var eventResponse runnerprotocol.Response[runnerprotocol.EventReceipt]
	if err := c.post(runCtx, "/internal/v1/jobs/"+string(lease.JobID)+"/events", event, &eventResponse); err != nil {
		return err
	}
	if receipt := eventResponse.Data; receipt.JobID != event.JobID || receipt.Attempt != event.Attempt || receipt.LeaseSeq != event.LeaseSeq || receipt.EventID != event.EventID || receipt.Seq < 1 {
		return ErrControlRejected
	}
	type checked struct {
		result coreexec.Result
		err    error
	}
	done := make(chan checked, 1)
	started := time.Now()
	go func() {
		p := lease.ExecutionPolicy
		res, err := c.validate(runCtx, c.registry, lease.Core.CoreBuildID, config, time.Duration(lease.Limits.DurationMS)*time.Millisecond, coreexec.SandboxPolicy{MemoryBytes: uint64(p.MemoryLimitBytes), Processes: uint64(p.ProcessLimit), CPUSeconds: uint64((lease.Limits.DurationMS + 999) / 1000)})
		done <- checked{res, err}
	}()
	heartbeats := time.NewTicker(5 * time.Second)
	defer heartbeats.Stop()
	for {
		select {
		case outcome := <-done:
			guard.Stop()
			if errors.Is(outcome.err, coreexec.ErrWorkspaceCleanup) || errors.Is(outcome.err, coreexec.ErrProcessCleanupTimeout) || errors.Is(outcome.err, coreexec.ErrLogDrainTimeout) {
				clear(outcome.result.Output)
				return ErrUnsafeCleanup
			}
			elapsed := float64(time.Since(started).Microseconds()) / 1000
			result.Metrics.ConfigCheckMS = &elapsed
			switch {
			case stopReason.Load() == 1:
				result.Error = runnerprotocol.Safe("LEASE_LOST")
			case stopReason.Load() == 2 || ctx.Err() != nil || outcome.result.Canceled:
				result.State, result.Error = "canceled", runnerprotocol.Safe("CANCELED")
			case outcome.result.TimedOut:
				result.State, result.Error = "timed_out", runnerprotocol.Safe("JOB_TIMEOUT")
			case outcome.err != nil:
				result.Error = runnerprotocol.Safe("RUNNER_RESOURCE_LIMIT")
				if errors.Is(outcome.err, coreexec.ErrDigestMismatch) {
					result.Error = runnerprotocol.Safe("CORE_BINARY_MISMATCH")
				}
			case outcome.result.ExitCode != 0:
				result.Verdict, result.Error = "fail", runnerprotocol.Safe("CORE_CONFIG_INVALID")
			default:
				result.State, result.Verdict, result.Error = "succeeded", "pass", nil
			}
			clear(outcome.result.Output)
			// ValidateIsolated returns only after killing, reaping and removing
			// its job workspace. Completion cannot publish before that boundary.
			return c.sendResult(ctx, result)
		case <-heartbeats.C:
			var response runnerprotocol.Response[runnerprotocol.HeartbeatData]
			renewalIssuedAt := time.Now()
			err := c.post(runCtx, "/internal/v1/jobs/"+string(lease.JobID)+"/heartbeat", identity, &response)
			if err != nil {
				if errors.Is(err, ErrControlRejected) {
					stopReason.CompareAndSwap(0, 1)
					stop()
				}
				continue // The independent local watchdog still stops on time.
			}
			hb := response.Data
			if hb.JobID != identity.JobID || hb.Attempt != identity.Attempt || hb.LeaseSeq != identity.LeaseSeq {
				stopReason.CompareAndSwap(0, 1)
				stop()
				continue
			}
			if hb.CancelRequested {
				stopReason.CompareAndSwap(0, 2)
				stop()
				continue
			}
			extension := c.stopAfter(hb.LeaseExpiresAt, renewalIssuedAt)
			if runCtx.Err() != nil || !hb.LeaseExpiresAt.After(lease.LeaseExpiresAt) || extension <= 0 || extension > 30*time.Second {
				stopReason.CompareAndSwap(0, 1)
				stop()
				continue
			}
			lease.LeaseExpiresAt = hb.LeaseExpiresAt
			guard.Reset(extension)
		}
	}
}

func (c *Client) stopAfter(expiresAt, issuedAt time.Time) time.Duration {
	remaining := expiresAt.Sub(time.Now().Add(c.clockOffset)) - leaseSafetyMargin
	// PostgreSQL owns the 30-second lease clock. A conservative local bound
	// starting before the request prevents API/DB wall-clock skew or a delayed
	// response from extending execution beyond that actual lease duration.
	localBudget := time.Until(issuedAt.Add(30*time.Second)) - leaseSafetyMargin
	return min(remaining, localBudget)
}

func (c *Client) validateLease(lease *runnerprotocol.Lease) ([]byte, error) {
	if runnerprotocol.ValidateConfigPayload(lease.FrozenPayload) != nil || lease.ExecutionPolicy.TerminationGraceMS != 2000 {
		return nil, ErrControlRejected
	}
	hash, err := runnerprotocol.PayloadHash(lease.FrozenPayload)
	if err != nil || hash != lease.PayloadSHA256 {
		return nil, ErrControlRejected
	}
	_, build, err := c.registry.Executable(lease.Core.CoreBuildID)
	core := lease.Core
	if err != nil || core.BuildSHA256 != build.BinarySHA256 || core.CoreFamily != build.Family || core.Version != build.Version || core.Platform != build.OS || core.Architecture != build.Arch || core.Architecture != runtime.GOARCH || core.AdapterVersion != build.AdapterVersion {
		return nil, ErrControlRejected
	}
	return base64.StdEncoding.Strict().DecodeString(lease.Artifact.ContentBase64)
}

func (c *Client) sendResult(ctx context.Context, request runnerprotocol.ResultRequest) error {
	request.ResultHash, _ = runnerprotocol.ResultHash(request)
	var response runnerprotocol.Response[runnerprotocol.ResultReceipt]
	// Shutdown/cancellation still gets a bounded chance to settle the attempt
	// after cleanup. Repeating this identical request is safe under queue fencing.
	for attempt := 0; attempt < 2; attempt++ {
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), controlTimeout)
		err := c.post(settleCtx, "/internal/v1/jobs/"+string(request.JobID)+"/result", request, &response)
		cancel()
		if err == nil {
			r := response.Data
			if r.JobID != request.JobID || r.Attempt != request.Attempt || r.LeaseSeq != request.LeaseSeq || r.ResultHash != request.ResultHash || r.SettledBytes != 0 {
				return ErrControlRejected
			}
			return nil
		}
		if errors.Is(err, ErrControlRejected) {
			return err
		}
	}
	return ErrControlUnavailable
}

func (c *Client) post(ctx context.Context, path string, data, out any) error {
	body, err := json.Marshal(data)
	if err != nil {
		return ErrControlRejected
	}
	defer clear(body)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.APIURL+path, bytes.NewReader(body))
	if err != nil {
		return ErrControlRejected
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return ErrControlUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode >= 400 && response.StatusCode < 500 {
			return ErrControlRejected
		}
		return ErrControlUnavailable
	}
	reader := io.LimitReader(response.Body, (15<<20)+1)
	raw, err := io.ReadAll(reader)
	defer clear(raw)
	if err != nil || len(raw) > 15<<20 {
		return ErrControlRejected
	}
	if runnerprotocol.DecodeStrict(raw, out) != nil {
		return ErrControlRejected
	}
	return nil
}
