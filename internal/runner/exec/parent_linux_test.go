//go:build linux

package exec

import (
	"context"
	"os"
	ose "os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/adapter"
)

func TestConfigSandboxSupervisorCrash(t *testing.T) {
	if raceInstrumented {
		t.Skip("sandbox address-space policy is exercised by the non-race Linux verifier")
	}
	if directory := os.Getenv("PROXYLOOM_CRASH_FIXTURE_DIRECTORY"); directory != "" {
		id, registry, workspace := helperSetup(t)
		defer workspace.Close()
		digest, err := fileSHA256(registry.Files[id])
		if err != nil {
			t.Fatal(err)
		}
		build := registry.Builds[id]
		build.BinarySHA256 = digest
		registry.Builds[id] = build
		policy := sandboxTestPolicy()
		policy.Processes = 128
		policy.Network = &NetworkPolicy{ListenerPort: 20070, RemotePorts: []uint16{20072}}
		if err := os.WriteFile(filepath.Join(workspace.Directory, workspace.ConfigFilename), []byte(`{"runner_probe":"crash-ready"}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, "workspace"), []byte(workspace.Directory), 0600); err != nil {
			t.Fatal(err)
		}
		spec := adapter.CommandSpec{ExecutableID: id, Args: []string{"run", "-c", "config.json"}, WorkingDir: workspace.Directory}
		result, err := Run(context.Background(), registry, spec, workspace, Options{Sandbox: &policy, Timeout: 30 * time.Second, Started: func(pid int) {
			if err := os.WriteFile(filepath.Join(directory, "core.pid"), []byte(strconv.Itoa(pid)), 0600); err != nil {
				t.Error(err)
			}
		}})
		t.Fatalf("supervisor should have been killed: %v; exit=%d; output=%s", err, result.ExitCode, result.Output)
		return
	}
	if err := prepareReaper(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	output, err := os.Create(filepath.Join(directory, "supervisor.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	supervisor := ose.Command(os.Args[0], "-test.run=^TestConfigSandboxSupervisorCrash$", "-test.timeout=40s")
	supervisor.Stdout, supervisor.Stderr = output, output
	supervisor.Env = append(os.Environ(), "PROXYLOOM_CRASH_FIXTURE_DIRECTORY="+directory, "TMPDIR="+directory)
	if err := supervisor.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = supervisor.Process.Kill()
			_ = supervisor.Wait()
		}
	})
	pid := 0
	ready := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(filepath.Join(directory, "core.pid"))
		pid, _ = strconv.Atoi(string(data))
		workspace, _ := os.ReadFile(filepath.Join(directory, "workspace"))
		corePID, _ := os.ReadFile(filepath.Join(string(workspace), "core.ready"))
		ready = pid > 1 && string(corePID) == strconv.Itoa(pid)
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ready {
		data, _ := os.ReadFile(output.Name())
		t.Fatalf("sandbox core never became ready: %s", data)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		var status syscall.WaitStatus
		_, _ = syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	})
	started := time.Now()
	if err := supervisor.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = supervisor.Wait()
	waited = true
	for time.Since(started) < 5*time.Second {
		var status syscall.WaitStatus
		found, err := syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		if found == pid {
			if !status.Signaled() || status.Signal() != syscall.SIGKILL {
				t.Fatal("core did not terminate with parent death")
			}
			t.Logf("PASS: killed supervisor's sandbox core reaped in %d ms", time.Since(started).Milliseconds())
			return
		}
		if err != nil && err != syscall.EINTR {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("orphan core survived supervisor SIGKILL")
}
