package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

var (
	ErrProcessCleanupTimeout = errors.New("runner process group did not exit after TERM and KILL grace periods")
	ErrLogDrainTimeout       = errors.New("runner log pipe did not reach EOF before cleanup deadline")
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
	return executeWithCleanup(ctx, registry, buildID, config, start, Workspace.Close)
}

func executeWithCleanup(ctx context.Context, registry Registry, buildID ir.ID, config []byte, start bool, closeWorkspace func(Workspace) error) (result Result, err error) {
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
	defer func() {
		if closeErr := closeWorkspace(workspace); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("clean runner workspace: %w", closeErr))
		}
	}()
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
	if err := prepareReaper(); err != nil {
		return Result{}, err
	}
	logReader, logWriter, err := os.Pipe()
	if err != nil {
		return Result{}, fmt.Errorf("create runner log pipe: %w", err)
	}
	defer logReader.Close()
	// Files bypass os/exec's copying goroutines, so cmd.Wait only waits for the
	// root process even when a descendant keeps an inherited log handle open.
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter
	limit := &limitBuffer{limit: opts.LogLimit}
	if err := cmd.Start(); err != nil {
		return Result{}, errors.Join(err, logWriter.Close())
	}
	var pipeErr error
	if err := logWriter.Close(); err != nil {
		pipeErr = fmt.Errorf("close parent runner log writer: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	logsDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(limit, logReader)
		logsDone <- err
	}()
	result, err := supervise(ctx, runCtx, cmd, logReader, done, logsDone, pipeErr)
	output, truncated := limit.snapshot()
	result.Truncated = result.Truncated || truncated
	result.Output = opts.Redact(output)
	return result, err
}

func supervise(ctx, runCtx context.Context, cmd *ose.Cmd, logReader *os.File, done, logsDone <-chan error, pipeErr error) (Result, error) {
	result := Result{ExitCode: -1}
	var waitErr, logErr, reapErr, termErr, killErr error
	waited, drained, groupGone := false, false, false
	recordWait := func(err error) {
		waited, done = true, nil
		result.ExitCode = exitCode(err)
		var exitErr *ose.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			waitErr = fmt.Errorf("wait for runner root: %w", err)
		}
	}
	recordLogs := func(err error) {
		drained, logsDone = true, nil
		if err != nil {
			logErr = fmt.Errorf("read runner logs: %w", err)
		}
	}
	running := pipeErr == nil
	for running {
		select {
		case err := <-done:
			recordWait(err)
			running = false
		case err := <-logsDone:
			recordLogs(err)
			running = err == nil
		case <-runCtx.Done():
			result.Canceled = errors.Is(ctx.Err(), context.Canceled)
			result.TimedOut = errors.Is(runCtx.Err(), context.DeadlineExceeded) && !result.Canceled
			running = false
		}
	}

	// Both normal root exit and cancellation start the same bounded cleanup.
	// Only cmd.Wait owns the root; group reaping starts after it has finished.
	pollGroup := func() {
		if waited && !groupGone {
			gone, err := processGroupDone(cmd)
			groupGone = gone
			if err != nil && reapErr == nil {
				reapErr = fmt.Errorf("reap runner process group: %w", err)
			}
		}
	}
	pollGroup()
	stage := time.NewTimer(killGrace)
	defer stage.Stop()
	if !groupGone {
		termErr = terminate(cmd)
	}
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	killing := false
	for {
		pollGroup()
		if waited && groupGone && drained {
			return result, errors.Join(pipeErr, waitErr, logErr, reapErr, termErr, killErr)
		}
		select {
		case err := <-done:
			recordWait(err)
		case err := <-logsDone:
			recordLogs(err)
		case <-poll.C:
		case <-stage.C:
			if !killing {
				killing = true
				stage.Reset(killGrace)
				if !groupGone {
					killErr = killProcess(cmd)
				}
				continue
			}
			if !waited || !groupGone {
				pipeErr = errors.Join(pipeErr, ErrProcessCleanupTimeout)
			}
			if !drained {
				// Closing the pipe interrupts its reader even if a process outside
				// this group's ownership retains a writer. Never wait without a bound.
				result.Truncated = true
				pipeErr = errors.Join(pipeErr, ErrLogDrainTimeout, logReader.Close())
			}
			return result, errors.Join(pipeErr, waitErr, logErr, reapErr, termErr, killErr)
		}
	}
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
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (l *limitBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
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

func (l *limitBuffer) snapshot() ([]byte, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]byte(nil), l.buf.Bytes()...), l.truncated
}
