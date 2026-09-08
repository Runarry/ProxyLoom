package chainverify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/adapter/mihomo"
	"github.com/Runarry/ProxyLoom/internal/adapter/singbox"
	"github.com/Runarry/ProxyLoom/internal/adapter/xray"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/compiler"
	"github.com/Runarry/ProxyLoom/internal/ir"
	runnerexec "github.com/Runarry/ProxyLoom/internal/runner/exec"
)

type Family struct {
	Key         string
	Family      ir.CoreFamily
	Listen      string
	BuildID     ir.ID
	BuildSHA256 string
}

type coreSession struct {
	bytes     []byte
	listen    string
	workspace runnerexec.Workspace
	cancel    context.CancelFunc
	done      <-chan runResult
	stopOnce  sync.Once
	stopped   runResult
}

type runResult struct {
	result runnerexec.Result
	err    error
}

func liveFamilies(catalog *capability.Catalog) ([]Family, error) {
	targets, err := catalogTargets(catalog)
	if err != nil {
		return nil, err
	}
	out := make([]Family, 0, len(targets))
	for _, target := range targets {
		listen := ""
		switch target.CoreFamily {
		case ir.Xray:
			listen = net.JoinHostPort(adapter.LoopbackAddr, strconv.Itoa(xray.ListenPort))
		case ir.SingBox:
			listen = net.JoinHostPort(adapter.LoopbackAddr, strconv.Itoa(singbox.ListenPort))
		case ir.Mihomo:
			listen = net.JoinHostPort(adapter.LoopbackAddr, strconv.Itoa(mihomo.MixedPort))
		default:
			return nil, fmt.Errorf("unknown family %s", target.CoreFamily)
		}
		out = append(out, Family{Key: target.Key, Family: target.CoreFamily, Listen: listen, BuildID: target.CoreBuildID, BuildSHA256: target.CoreBuildSHA256})
	}
	return out, nil
}

func coresRegistry() (runnerexec.DiskRegistry, *capability.Catalog, error) {
	catalog, err := capability.Load()
	if err != nil {
		return runnerexec.DiskRegistry{}, nil, err
	}
	root := os.Getenv("PROXYLOOM_CORES_ROOT")
	if root == "" {
		repo, err := repoRoot()
		if err != nil {
			return runnerexec.DiskRegistry{}, nil, err
		}
		root = filepath.Join(repo, ".cache", "cores")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return runnerexec.DiskRegistry{}, nil, err
	}
	return runnerexec.DiskRegistry{Root: abs, Catalog: catalog}, catalog, nil
}

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("repository root not found")
		}
		dir = parent
	}
}

func compileFamily(cmp *compiler.Compiler, input ir.FrozenInput, fam Family) (adapter.Artifact, ir.Target, error) {
	target, ok := input.Target(fam.Key)
	if !ok {
		return adapter.Artifact{}, ir.Target{}, fmt.Errorf("missing target %s", fam.Key)
	}
	artifact, _, err := cmp.Compile(context.Background(), input, target)
	if err != nil {
		return adapter.Artifact{}, target, err
	}
	if err := assertSecureEmit(fam.Family, artifact.Bytes); err != nil {
		return adapter.Artifact{}, target, err
	}
	return artifact, target, nil
}

func assertSecureEmit(family ir.CoreFamily, payload []byte) error {
	text := string(payload)
	switch family {
	case ir.Xray:
		if strings.Contains(text, `"allowInsecure":true`) || strings.Contains(text, `"freedom"`) {
			return fmt.Errorf("xray emit disabled TLS verify or added freedom")
		}
		if !strings.Contains(text, `"allowInsecure":false`) {
			return fmt.Errorf("xray emit missing allowInsecure false")
		}
	case ir.SingBox:
		if strings.Contains(text, `"insecure":true`) {
			return fmt.Errorf("sing-box emit disabled TLS verify")
		}
		if !strings.Contains(text, `"insecure":false`) {
			return fmt.Errorf("sing-box emit missing insecure false")
		}
	case ir.Mihomo:
		upper := strings.ToUpper(text)
		if strings.Contains(text, "skip-cert-verify: true") || strings.Contains(upper, ",DIRECT") {
			return fmt.Errorf("mihomo emit disabled TLS verify or added DIRECT")
		}
		if !strings.Contains(text, "skip-cert-verify: false") {
			return fmt.Errorf("mihomo emit missing skip-cert-verify false")
		}
	}
	return nil
}

func startCore(registry runnerexec.Registry, fam Family, config []byte) (*coreSession, error) {
	payload := bytes.Clone(config)
	validate, err := runnerexec.ValidateConfig(context.Background(), registry, fam.BuildID, payload)
	if err != nil {
		return nil, err
	}
	if validate.ExitCode != 0 {
		return nil, fmt.Errorf("config check exit %d: %s", validate.ExitCode, truncate(validate.Output, 800))
	}
	filename := xray.ConfigFilename
	var runner adapter.RunnerAdapter
	switch fam.Family {
	case ir.Xray:
		runner = xray.Adapter{ExecutableID: fam.BuildID}
	case ir.SingBox:
		runner = singbox.Adapter{ExecutableID: fam.BuildID}
		filename = singbox.ConfigFilename
	case ir.Mihomo:
		runner = mihomo.Adapter{ExecutableID: fam.BuildID}
		filename = mihomo.ConfigFilename
	default:
		return nil, fmt.Errorf("unknown family")
	}
	ws, err := runnerexec.Allocate("", filename, payload)
	if err != nil {
		return nil, err
	}
	spec, err := runner.RunSpec(ws.Job())
	if err != nil {
		return nil, errors.Join(err, ws.Close())
	}
	if err := waitPortClosed(fam.Listen, 5*time.Second); err != nil {
		return nil, errors.Join(fmt.Errorf("inbound %s still occupied before start: %w", fam.Listen, err), ws.Close())
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan runResult, 1)
	go func() {
		result, runErr := runnerexec.Run(ctx, registry, spec, ws, runnerexec.Options{
			Timeout: 2 * time.Minute, Redact: runner.RedactLog,
		})
		done <- runResult{result: result, err: runErr}
	}()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer waitCancel()
	early, err := waitListen(waitCtx, fam.Listen, done)
	if early != nil {
		cancel()
		res := finishCore(*early, ws, fam.Listen)
		return nil, errors.Join(err, res.err)
	}
	if err != nil {
		cancel()
		res := finishCore(<-done, ws, fam.Listen)
		return nil, errors.Join(fmt.Errorf("%w; core output: %s", err, truncate(res.result.Output, 800)), res.err)
	}
	select {
	case res := <-done:
		cancel()
		res = finishCore(res, ws, fam.Listen)
		return nil, errors.Join(fmt.Errorf("core exited after listen %s: exit=%d output=%s", fam.Listen, res.result.ExitCode, truncate(res.result.Output, 800)), res.err)
	default:
	}
	return &coreSession{bytes: payload, listen: fam.Listen, workspace: ws, cancel: cancel, done: done}, nil
}

func waitListen(ctx context.Context, addr string, done <-chan runResult) (*runResult, error) {
	dialer := net.Dialer{Timeout: 200 * time.Millisecond}
	for {
		select {
		case res := <-done:
			return &res, fmt.Errorf("core exited before listen %s: err=%v exit=%d output=%s", addr, res.err, res.result.ExitCode, truncate(res.result.Output, 800))
		case <-ctx.Done():
			return nil, fmt.Errorf("core inbound %s not listening: %w", addr, ctx.Err())
		default:
		}
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		greetErr := socks5Greeting(conn)
		_ = conn.Close()
		if greetErr == nil {
			return nil, nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *coreSession) probe(requestID, destHost string, destPort int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	return socks5HTTPProbe(ctx, s.listen, destHost, destPort, "target.proxyloom.test", requestID)
}

func (s *coreSession) stop() runResult {
	if s == nil {
		return runResult{}
	}
	s.stopOnce.Do(func() {
		s.cancel()
		s.stopped = finishCore(<-s.done, s.workspace, s.listen)
	})
	return s.stopped
}

func finishCore(res runResult, workspace runnerexec.Workspace, listen string) runResult {
	if err := workspace.Close(); err != nil {
		res.err = errors.Join(res.err, fmt.Errorf("close core workspace: %w", err))
	}
	if err := waitPortClosed(listen, 5*time.Second); err != nil {
		res.err = errors.Join(res.err, fmt.Errorf("close core inbound: %w", err))
	}
	return res
}

func waitPortClosed(addr string, d time.Duration) error {
	if addr == "" {
		return nil
	}
	deadline := time.Now().Add(d)
	closed := 0
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 150*time.Millisecond)
		if err != nil {
			closed++
			if closed >= 3 {
				return nil
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		closed = 0
		_ = conn.Close()
		time.Sleep(30 * time.Millisecond)
	}
	return fmt.Errorf("still accepting on %s", addr)
}

func (s *coreSession) leftoverWorkspace() bool {
	if s == nil || s.workspace.Directory == "" {
		return false
	}
	_, err := os.Stat(s.workspace.Directory)
	return err == nil
}

func installLiveCA(pem []byte) (func(), error) {
	if os.Getenv("PROXYLOOM_LIVE_CHAIN_INSTALL_CA") != "1" {
		return func() {}, nil
	}
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("CA install is only permitted in the linux live-chain runner")
	}
	dir := "/usr/local/share/ca-certificates"
	path := filepath.Join(dir, "proxyloom-livechain.crt")
	if err := os.WriteFile(path, pem, 0o644); err != nil {
		return nil, err
	}
	cmd := exec.Command("update-ca-certificates")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("update-ca-certificates: %w %s", err, truncate(out, 400))
	}
	return func() {
		_ = os.Remove(path)
		_ = exec.Command("update-ca-certificates").Run()
	}, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

func configFilename(fam Family) string {
	if fam.Family == ir.Mihomo {
		return mihomo.ConfigFilename
	}
	return xray.ConfigFilename
}

func familyAdapter(fam Family) (adapter.RunnerAdapter, error) {
	switch fam.Family {
	case ir.Xray:
		return xray.Adapter{ExecutableID: fam.BuildID}, nil
	case ir.SingBox:
		return singbox.Adapter{ExecutableID: fam.BuildID}, nil
	case ir.Mihomo:
		return mihomo.Adapter{ExecutableID: fam.BuildID}, nil
	default:
		return nil, fmt.Errorf("unknown family")
	}
}

func runSpec(fam Family, ws runnerexec.Workspace) (adapter.CommandSpec, error) {
	core, err := familyAdapter(fam)
	if err != nil {
		return adapter.CommandSpec{}, err
	}
	return core.RunSpec(ws.Job())
}

func redactFor(fam Family) func([]byte) []byte {
	core, err := familyAdapter(fam)
	if err != nil {
		return adapter.RedactLogLine
	}
	return core.RedactLog
}
