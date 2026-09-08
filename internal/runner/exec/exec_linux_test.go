//go:build linux

package exec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	ose "os/exec"

	"github.com/Runarry/ProxyLoom/internal/adapter"
)

func runPlatformHelper(args []string) int {
	switch args[0] {
	case "adopt-while-live":
		if err := os.WriteFile("root.pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			return 1
		}
		child := ose.Command(os.Args[0], helperFlag, "orphan-holder", "hold-logs")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Run(); err != nil {
			return 1
		}
		time.Sleep(30 * time.Second)
		return 0
	case "orphan-holder", "escape-holder":
		if len(args) != 2 {
			return 2
		}
		child := ose.Command(os.Args[0], helperFlag, args[1])
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if args[0] == "escape-holder" {
			child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		}
		if err := child.Start(); err != nil {
			return 1
		}
		// The child installs its signal behavior and writes its PID before the
		// intermediate exits, making adoption and inherited logs deterministic.
		if !helperWaitFile("holder.pid") {
			_ = child.Process.Kill()
			_ = child.Wait()
			return 1
		}
		return 0
	case "hold-logs", "ignore-term", "term-output", "external-owned":
		var signals chan os.Signal
		if args[0] == "ignore-term" {
			signal.Ignore(syscall.SIGTERM)
		} else if args[0] == "term-output" {
			signals = make(chan os.Signal, 1)
			signal.Notify(signals, syscall.SIGTERM)
			defer signal.Stop(signals)
		}
		fmt.Println("stdout-open")
		fmt.Fprintln(os.Stderr, "stderr-open")
		if err := os.WriteFile("holder.pid", []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			return 1
		}
		switch args[0] {
		case "term-output":
			select {
			case <-signals:
				fmt.Println("stdout-term")
				fmt.Fprintln(os.Stderr, "stderr-term")
				return 0
			case <-time.After(30 * time.Second):
				return 1
			}
		case "external-owned":
			if !helperWaitFile("exit") {
				return 1
			}
			return 23
		default:
			time.Sleep(30 * time.Second)
			return 0
		}
	default:
		return 2
	}
}

func helperWaitFile(name string) bool {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(name); err == nil && len(data) > 0 {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

type helperOutcome struct {
	result Result
	err    error
}

type activeHelper struct {
	workspace Workspace
	cancel    context.CancelFunc
	done      chan helperOutcome
	finished  bool
}

func startLinuxHelper(t *testing.T, timeout time.Duration, args ...string) *activeHelper {
	t.Helper()
	id, registry, workspace := helperSetup(t)
	t.Cleanup(func() { _ = workspace.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	helper := &activeHelper{workspace: workspace, cancel: cancel, done: make(chan helperOutcome, 1)}
	spec := adapter.CommandSpec{ExecutableID: id, Args: append([]string{helperFlag}, args...), WorkingDir: workspace.Directory}
	go func() {
		result, err := Run(ctx, registry, spec, workspace, Options{Timeout: timeout})
		helper.done <- helperOutcome{result: result, err: err}
	}()
	t.Cleanup(func() {
		cancel()
		if !helper.finished {
			helper.await(t)
		}
	})
	return helper
}

func (h *activeHelper) await(t *testing.T) helperOutcome {
	t.Helper()
	select {
	case outcome := <-h.done:
		h.finished = true
		return outcome
	case <-time.After(2*killGrace + 2*time.Second):
		t.Fatal("Run did not return within its cleanup bound")
		return helperOutcome{}
	}
}

func waitHelperPID(t *testing.T, workspace Workspace, filename string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(filepath.Join(workspace.Directory, filename))
		if err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil && pid > 1 {
				return pid
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("helper never wrote %s", filename)
	return 0
}

func assertLinuxProcess(t *testing.T, pid, ppid, pgid int, zombie bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		state, parent, group, ok := linuxProcState(pid)
		if ok && parent == ppid && group == pgid && (state == 'Z') == zombie {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("unexpected process ownership/state: want pid=%d ppid=%d pgid=%d zombie=%t; %s", pid, ppid, pgid, zombie, pidProcInfo(pid))
}

func TestRunKeepsOtherRunsAdoptedDescendantsAlive(t *testing.T) {
	a := startLinuxHelper(t, 30*time.Second, "adopt-while-live")
	root := waitHelperPID(t, a.workspace, "root.pid")
	child := waitHelperPID(t, a.workspace, "holder.pid")
	assertLinuxProcess(t, child, os.Getpid(), root, false)
	for _, timeout := range []bool{false, true} {
		mode, limit := "both-logs", 5*time.Second
		if timeout {
			mode, limit = "sleep", 100*time.Millisecond
		}
		b := startLinuxHelper(t, limit, mode)
		outcome := b.await(t)
		if outcome.err != nil || outcome.result.TimedOut != timeout || outcome.result.Canceled || (!timeout && outcome.result.ExitCode != 0) {
			t.Fatalf("Run B timeout=%t: %v %+v", timeout, outcome.err, outcome.result)
		}
		assertLinuxProcess(t, child, os.Getpid(), root, false)
		select {
		case <-a.done:
			a.finished = true
			t.Fatal("Run B ended active Run A")
		default:
		}
	}
	a.cancel()
	outcome := a.await(t)
	if outcome.err != nil || !outcome.result.Canceled || outcome.result.TimedOut {
		t.Fatalf("cancel Run A: %v %+v", outcome.err, outcome.result)
	}
	waitPIDReaped(t, root)
	waitPIDReaped(t, child)
}

func TestRunDoesNotSignalOrWaitForUnrelatedChildren(t *testing.T) {
	_, _, workspace := helperSetup(t)
	defer workspace.Close()
	child := ose.Command(os.Args[0], helperFlag, "external-owned")
	child.Dir = workspace.Directory
	child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	pid := waitHelperPID(t, workspace, "holder.pid")
	for _, mode := range []string{"both-logs", "sleep"} {
		limit := 5 * time.Second
		if mode == "sleep" {
			limit = 100 * time.Millisecond
		}
		b := startLinuxHelper(t, limit, mode)
		outcome := b.await(t)
		if outcome.err != nil || outcome.result.TimedOut != (mode == "sleep") {
			t.Fatalf("Run while external child lives: %v %+v", outcome.err, outcome.result)
		}
		assertLinuxProcess(t, pid, os.Getpid(), pid, false)
	}
	if err := os.WriteFile(filepath.Join(workspace.Directory, "exit"), []byte("exit"), 0o600); err != nil {
		t.Fatal(err)
	}
	assertLinuxProcess(t, pid, os.Getpid(), pid, true)
	b := startLinuxHelper(t, 5*time.Second, "both-logs")
	if outcome := b.await(t); outcome.err != nil || outcome.result.ExitCode != 0 {
		t.Fatalf("Run while external child awaits its owner: %v %+v", outcome.err, outcome.result)
	}
	err := child.Wait()
	waited = true
	var exitErr *ose.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("external owner's Wait was stolen or exit status changed: %v", err)
	}
}

func TestRunCleansDescendantHoldingLogsAfterRootExit(t *testing.T) {
	start := time.Now()
	h := startLinuxHelper(t, 10*time.Second, "orphan-holder", "term-output")
	child := waitHelperPID(t, h.workspace, "holder.pid")
	outcome := h.await(t)
	if outcome.err != nil || outcome.result.ExitCode != 0 || outcome.result.Canceled || outcome.result.TimedOut || outcome.result.Truncated {
		t.Fatalf("normal root exit: %v %+v", outcome.err, outcome.result)
	}
	if elapsed := time.Since(start); elapsed >= 2*killGrace+time.Second {
		t.Fatalf("normal root exit waited for the execution timeout: %s", elapsed)
	}
	for _, text := range []string{"stdout-open", "stderr-open", "stdout-term", "stderr-term"} {
		if !bytes.Contains(outcome.result.Output, []byte(text)) {
			t.Fatalf("missing drained descendant output %q: %s", text, outcome.result.Output)
		}
	}
	waitPIDReaped(t, child)
}

func TestRunEscalatesWithinBoundsWhenTERMIgnored(t *testing.T) {
	for _, mode := range []string{"normal", "cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			args, limit := []string{"ignore-term"}, 20*time.Second
			if mode == "normal" {
				args = []string{"orphan-holder", "ignore-term"}
			} else if mode == "timeout" {
				limit = time.Second
			}
			h := startLinuxHelper(t, limit, args...)
			child := waitHelperPID(t, h.workspace, "holder.pid")
			start := time.Now()
			if mode == "cancel" {
				h.cancel()
			}
			outcome := h.await(t)
			if outcome.err != nil || outcome.result.Canceled != (mode == "cancel") || outcome.result.TimedOut != (mode == "timeout") {
				t.Fatalf("%s: %v %+v", mode, outcome.err, outcome.result)
			}
			if mode == "normal" && outcome.result.ExitCode != 0 {
				t.Fatalf("root exit changed: %+v", outcome.result)
			}
			if elapsed := time.Since(start); elapsed < killGrace-200*time.Millisecond || elapsed >= 2*killGrace+time.Second {
				t.Fatalf("unexpected TERM/KILL duration: %s", elapsed)
			}
			waitPIDReaped(t, child)
		})
	}
}

func TestRunReportsLogDeadlineForHandleOutsideItsGroup(t *testing.T) {
	h := startLinuxHelper(t, 10*time.Second, "escape-holder", "hold-logs")
	child := waitHelperPID(t, h.workspace, "holder.pid")
	defer func() {
		// This deliberate escape belongs to the test, not Run's process group.
		_ = syscall.Kill(child, syscall.SIGKILL)
		var status syscall.WaitStatus
		_, _ = syscall.Wait4(child, &status, 0, nil)
	}()
	assertLinuxProcess(t, child, os.Getpid(), child, false)
	start := time.Now()
	outcome := h.await(t)
	if !errors.Is(outcome.err, ErrLogDrainTimeout) || errors.Is(outcome.err, ErrProcessCleanupTimeout) || outcome.result.ExitCode != 0 || outcome.result.TimedOut || outcome.result.Canceled || !outcome.result.Truncated {
		t.Fatalf("log handle outside the owned group: %v %+v", outcome.err, outcome.result)
	}
	if elapsed := time.Since(start); elapsed >= 2*killGrace+2*time.Second {
		t.Fatalf("log drain exceeded cleanup bound: %s", elapsed)
	}
	assertLinuxProcess(t, child, os.Getpid(), child, false)
}
