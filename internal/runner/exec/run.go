package exec

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ose "os/exec"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/adapter/mihomo"
	"github.com/Runarry/ProxyLoom/internal/adapter/singbox"
	"github.com/Runarry/ProxyLoom/internal/adapter/xray"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const (
	DefaultLogLimit = 64 << 10
	DefaultTimeout  = 15 * time.Second
	killGrace       = 2 * time.Second
)

type Options struct {
	Timeout  time.Duration
	LogLimit int
	Redact   func([]byte) []byte
}

type Result struct {
	ExitCode  int
	Output    []byte
	Truncated bool
	TimedOut  bool
	Canceled  bool
}

func ValidateConfig(ctx context.Context, registry Registry, buildID ir.ID, config []byte) (Result, error) {
	return execute(ctx, registry, buildID, config, false)
}

func Start(ctx context.Context, registry Registry, buildID ir.ID, config []byte) (Result, error) {
	return execute(ctx, registry, buildID, config, true)
}

func execute(ctx context.Context, registry Registry, buildID ir.ID, config []byte, start bool) (Result, error) {
	if registry == nil {
		return Result{}, ErrUnknownExecutable
	}
	_, build, err := registry.Executable(buildID)
	if err != nil {
		return Result{}, err
	}
	filename := xray.ConfigFilename
	switch build.Family {
	case ir.Mihomo:
		filename = mihomo.ConfigFilename
	case ir.SingBox:
		filename = singbox.ConfigFilename
	case ir.Xray:
		filename = xray.ConfigFilename
	default:
		return Result{}, ErrUnknownExecutable
	}
	workspace, err := Allocate("", filename, config)
	if err != nil {
		return Result{}, err
	}
	defer workspace.Close()
	core, err := familyAdapter(build.Family, buildID)
	if err != nil {
		return Result{}, err
	}
	var spec adapter.CommandSpec
	if start {
		spec, err = core.RunSpec(workspace.Job())
	} else {
		spec, err = core.ValidateSpec(workspace.Job())
	}
	if err != nil {
		return Result{}, err
	}
	return Run(ctx, registry, spec, workspace, Options{Redact: core.RedactLog})
}

func familyAdapter(family ir.CoreFamily, id ir.ID) (adapter.RunnerAdapter, error) {
	switch family {
	case ir.Xray:
		return xray.Adapter{ExecutableID: id}, nil
	case ir.SingBox:
		return singbox.Adapter{ExecutableID: id}, nil
	case ir.Mihomo:
		return mihomo.Adapter{ExecutableID: id}, nil
	default:
		return nil, ErrUnknownExecutable
	}
}

func Run(ctx context.Context, registry Registry, spec adapter.CommandSpec, workspace Workspace, opts Options) (Result, error) {
	if registry == nil {
		return Result{}, ErrUnknownExecutable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.LogLimit <= 0 {
		opts.LogLimit = DefaultLogLimit
	}
	if opts.Redact == nil {
		opts.Redact = adapter.RedactLogLine
	}
	path, _, err := registry.Executable(spec.ExecutableID)
	if err != nil {
		return Result{}, err
	}
	if err := spec.Validate(spec.ExecutableID, workspace.Directory); err != nil {
		return Result{}, err
	}
	if err := validateArgs(spec.Args); err != nil {
		return Result{}, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Result{}, err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	if err := forbidShell(abs); err != nil {
		return Result{}, err
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return Result{}, ErrMissingBinary
	}
	runCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	cmd := ose.Command(abs, spec.Args...)
	cmd.Dir = workspace.Directory
	cmd.Env = cleanEnv(workspace.Directory)
	cmd.SysProcAttr = sysProcAttr()
	limit := &limitBuffer{limit: opts.LogLimit}
	cmd.Stdout = limit
	cmd.Stderr = limit
	prepareReaper()
	liveMu.Lock()
	inFlight++
	err = cmd.Start()
	if err != nil {
		inFlight--
		liveMu.Unlock()
		return Result{}, err
	}
	pid := cmd.Process.Pid
	if pid > 0 {
		livePids[pid] = struct{}{}
	}
	liveMu.Unlock()
	defer func() {
		liveMu.Lock()
		delete(livePids, pid)
		if inFlight > 0 {
			inFlight--
		}
		liveMu.Unlock()
		reapOrphans()
	}()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	result := Result{}
	select {
	case err := <-done:
		result.ExitCode = exitCode(err)
	case <-runCtx.Done():
		result.Canceled = errors.Is(ctx.Err(), context.Canceled)
		result.TimedOut = errors.Is(runCtx.Err(), context.DeadlineExceeded) && !result.Canceled
		terminate(cmd)
		select {
		case err := <-done:
			if result.ExitCode == 0 {
				result.ExitCode = exitCode(err)
			}
		case <-time.After(killGrace):
			killProcess(cmd)
			reapOrphans()
			err := <-done
			if result.ExitCode == 0 {
				result.ExitCode = exitCode(err)
			}
		}
	}
	result.Truncated = limit.truncated
	result.Output = opts.Redact(limit.Bytes())
	return result, nil
}

func validateArgs(args []string) error {
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return ErrForbiddenArgs
		}
		if arg == "." || arg == "-t" || arg == "-d" || arg == "-f" || arg == "-c" || arg == "-test" || arg == "run" || arg == "check" {
			continue
		}
		if strings.Contains(arg, "..") || filepath.IsAbs(arg) {
			return ErrForbiddenArgs
		}
		if strings.Contains(arg, "://") || strings.ContainsAny(arg, ";&|`$") {
			return ErrForbiddenArgs
		}
		if filepath.Base(arg) != arg {
			return ErrForbiddenArgs
		}
	}
	return nil
}

func forbidShell(path string) error {
	switch strings.ToLower(filepath.Base(path)) {
	case "sh", "bash", "zsh", "fish", "sh.exe", "bash.exe", "zsh.exe", "fish.exe", "cmd.exe", "cmd", "powershell.exe", "powershell", "pwsh.exe", "pwsh":
		return ErrForbiddenPath
	default:
		return nil
	}
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *ose.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

type limitBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (l *limitBuffer) Write(p []byte) (int, error) {
	remain := l.limit - l.buf.Len()
	if remain <= 0 {
		l.truncated = true
		return len(p), nil
	}
	if len(p) > remain {
		_, _ = l.buf.Write(p[:remain])
		l.truncated = true
		return len(p), nil
	}
	return l.buf.Write(p)
}

func (l *limitBuffer) Bytes() []byte {
	return append([]byte(nil), l.buf.Bytes()...)
}

var (
	liveMu   sync.Mutex
	livePids = map[int]struct{}{}
	inFlight int
)

func isLivePidLocked(pid int) bool {
	_, ok := livePids[pid]
	return ok
}
