package chainverify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/capability"
	runnerexec "github.com/Runarry/ProxyLoom/internal/runner/exec"
)

func TestCoreSessionStopIsIdempotent(t *testing.T) {
	ws, err := runnerexec.Allocate(t.TempDir(), "config.json", []byte("test-only"))
	if err != nil {
		t.Fatal(err)
	}
	runErr := errors.New("injected run cleanup failure")
	want := runResult{result: runnerexec.Result{ExitCode: -1, Canceled: true, Output: []byte("test output")}, err: runErr}
	done := make(chan runResult, 1)
	done <- want
	var canceled atomic.Int32
	session := &coreSession{workspace: ws, cancel: func() { canceled.Add(1) }, done: done}

	const callers = 8
	results := make(chan runResult, callers)
	for range callers {
		go func() { results <- session.stop() }()
	}
	for range callers {
		select {
		case got := <-results:
			if got.err != want.err || got.result.ExitCode != want.result.ExitCode || !got.result.Canceled || !bytes.Equal(got.result.Output, want.result.Output) {
				t.Fatalf("stop did not return its original result: %+v", got)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("repeated stop blocked waiting for an already consumed run result")
		}
	}
	if canceled.Load() != 1 {
		t.Fatalf("cancel called %d times", canceled.Load())
	}
	if _, err := os.Stat(ws.Directory); !os.IsNotExist(err) {
		t.Fatalf("stopped workspace remains: %v", err)
	}
	// A later stop must reuse the cached result, including cleanup. Recreating
	// this owned directory makes an accidental second Close observable.
	if err := os.Mkdir(ws.Directory, 0o700); err != nil {
		t.Fatal(err)
	}
	go func() { results <- session.stop() }()
	select {
	case got := <-results:
		if got.err != runErr {
			t.Fatalf("repeated stop lost the run error: %v", got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop after completion blocked")
	}
	if _, err := os.Stat(ws.Directory); err != nil {
		t.Fatalf("repeated stop ran workspace cleanup again: %v", err)
	}
}

func TestCoreSessionStopJoinsRunWorkspaceAndInboundErrors(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
		<-acceptDone
	})
	runErr := errors.New("injected run cleanup failure")
	done := make(chan runResult, 1)
	done <- runResult{result: runnerexec.Result{Canceled: true}, err: runErr}
	session := &coreSession{
		listen: listener.Addr().String(),
		// NUL is rejected on every supported OS, including privileged Linux
		// runners where a read-only directory is not a reliable failure fixture.
		workspace: runnerexec.Workspace{Directory: filepath.Join(t.TempDir(), "invalid") + "\x00"},
		cancel:    func() {}, done: done,
	}
	res := session.stop()
	if !errors.Is(res.err, runErr) {
		t.Fatalf("run error was lost: %v", res.err)
	}
	var pathErr *os.PathError
	if !errors.As(res.err, &pathErr) || !strings.Contains(res.err.Error(), "close core workspace") {
		t.Fatalf("workspace error was lost: %v", res.err)
	}
	if !strings.Contains(res.err.Error(), "close core inbound") {
		t.Fatalf("inbound error was lost behind another failure: %v", res.err)
	}
}

func TestLifecycleCleanupControlsExitAndReport(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		status        string
		cleanupStatus string
		leftover      bool
	}{
		{name: "canceled", status: "pass", cleanupStatus: "pass"},
		{name: "timed_out", status: "pass", cleanupStatus: "pass"},
		{name: "canceled_cleanup_error", status: "fail", cleanupStatus: "fail"},
		{name: "timed_out_cleanup_error", status: "fail", cleanupStatus: "fail"},
		{name: "workspace_cleanup_error", status: "fail", cleanupStatus: "fail", leftover: true},
		{name: "unexpected_timeout", status: "fail", cleanupStatus: "fail"},
		{name: "fatal_after_start", status: "fail", cleanupStatus: "pass"},
		{name: "nonfatal_test_error", status: "fail", cleanupStatus: "pass"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			jobs := filepath.Join(dir, "jobs")
			if err := os.Mkdir(jobs, 0o700); err != nil {
				t.Fatal(err)
			}
			reportPath := filepath.Join(dir, "report.json")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestLifecycleReportChild$", "-test.v")
			for _, value := range os.Environ() {
				key, _, _ := strings.Cut(value, "=")
				if key != "PROXYLOOM_LIVE_REPORT" && key != "PROXYLOOM_LIVE_CHAIN" && key != "PROXYLOOM_LIVE_CHAIN_INSTALL_CA" && key != "PROXYLOOM_LIFECYCLE_CHILD" && key != "PROXYLOOM_LIFECYCLE_JOBS" {
					cmd.Env = append(cmd.Env, value)
				}
			}
			cmd.Env = append(cmd.Env, "PROXYLOOM_LIVE_REPORT="+reportPath, "PROXYLOOM_LIFECYCLE_CHILD="+tc.name, "PROXYLOOM_LIFECYCLE_JOBS="+jobs)
			output, runErr := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("child cleanup blocked: %v\n%s", ctx.Err(), output)
			}
			if tc.status == "pass" {
				if runErr != nil {
					t.Fatalf("successful cleanup failed: %v\n%s", runErr, output)
				}
			} else {
				var exitErr *exec.ExitError
				if !errors.As(runErr, &exitErr) || exitErr.ExitCode() == 0 {
					t.Fatalf("injected failure did not fail the test process: %v\n%s", runErr, output)
				}
			}
			data, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatalf("report missing: %v\n%s", err, output)
			}
			var report liveReport
			if err := json.Unmarshal(data, &report); err != nil {
				t.Fatal(err)
			}
			if len(report.Results) != 1 || report.Results[0].Status != tc.status || report.Results[0].Scenario != tc.name {
				t.Fatalf("report did not reflect final test status: %s\n%s", data, output)
			}
			entry := report.Results[0]
			if entry.CoreBuildID == "" || entry.CoreBuildSHA256 == "" || !strings.Contains(report.TestConfigDigestScope, "test-only") {
				t.Fatalf("report missing build or digest scope: %s", data)
			}
			if len(entry.Sessions) != 1 || entry.Sessions[0].CleanupStatus != tc.cleanupStatus {
				t.Fatalf("report missing actual cleanup result: %s", data)
			}
			cleanup := entry.Sessions[0]
			if cleanup.TestConfigSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte("synthetic lifecycle fixture"))) {
				t.Fatalf("report config digest does not identify the fixture: %s", data)
			}
			if tc.cleanupStatus == "fail" && cleanup.CleanupError == "" {
				t.Fatalf("report discarded cleanup error: %s", data)
			}
			leftovers, err := os.ReadDir(jobs)
			if err != nil || (len(leftovers) > 0) != tc.leftover {
				t.Fatalf("cleanup did not remove the workspace as expected: entries=%d err=%v\n%s", len(leftovers), err, output)
			}
		})
	}
}

func TestLifecycleReportChild(t *testing.T) {
	mode := os.Getenv("PROXYLOOM_LIFECYCLE_CHILD")
	if mode == "" {
		return
	}
	catalog, err := capability.Load()
	if err != nil {
		t.Fatal(err)
	}
	families, err := liveFamilies(catalog)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, fam: families[0]}
	h.run(mode, func() {
		config := []byte("synthetic lifecycle fixture")
		ws, err := runnerexec.Allocate(os.Getenv("PROXYLOOM_LIFECYCLE_JOBS"), "config.json", config)
		if err != nil {
			h.t.Fatal(err)
		}
		res := runResult{result: runnerexec.Result{ExitCode: -1, Canceled: true}}
		expected := expectCanceled
		if strings.HasPrefix(mode, "timed_out") || mode == "unexpected_timeout" {
			res.result.Canceled, res.result.TimedOut = false, true
			if mode != "unexpected_timeout" {
				expected = expectTimedOut
			}
		}
		if mode == "canceled_cleanup_error" || mode == "timed_out_cleanup_error" {
			res.err = errors.New("injected run cleanup failure")
		}
		if mode == "workspace_cleanup_error" {
			ws.Directory += "\x00"
		}
		done := make(chan runResult, 1)
		done <- res
		session := &coreSession{bytes: config, workspace: ws, cancel: func() {}, done: done}
		manageCore(h.t, session, h.configEvidence(config, expected))
		switch mode {
		case "fatal_after_start":
			h.t.Fatal("injected failure after successful session creation")
		case "nonfatal_test_error":
			h.t.Error("injected nonfatal assertion failure")
		}
	})
}
