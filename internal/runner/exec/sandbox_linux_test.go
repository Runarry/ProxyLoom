//go:build linux

package exec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"golang.org/x/sys/unix"
)

func runSandboxProbe(filename string) (int, bool) {
	data, _ := os.ReadFile(filename)
	var request struct {
		Probe   string `json:"runner_probe"`
		Outside string `json:"outside"`
	}
	if json.Unmarshal(data, &request) != nil || request.Probe == "" {
		return 0, false
	}
	if request.Probe == "sleep" {
		time.Sleep(30 * time.Second)
		return 0, true
	}
	if request.Probe != "restrictions" {
		return 2, true
	}
	for _, socket := range []struct{ domain, kind int }{{unix.AF_INET, unix.SOCK_STREAM}, {unix.AF_INET, unix.SOCK_DGRAM}, {unix.AF_INET6, unix.SOCK_STREAM}, {unix.AF_UNIX, unix.SOCK_STREAM}} {
		fd, err := unix.Socket(socket.domain, socket.kind, 0)
		if err != unix.EPERM {
			if fd >= 0 {
				_ = unix.Close(fd)
			}
			return 11, true
		}
	}
	if _, err := os.ReadFile(request.Outside); !errors.Is(err, os.ErrPermission) {
		return 12, true
	}
	if err := os.Chmod(request.Outside, 0o600); !errors.Is(err, os.ErrPermission) {
		return 13, true
	}
	pid, _, forkError := unix.RawSyscall(unix.SYS_CLONE, uintptr(unix.SIGCHLD), 0, 0)
	if forkError == 0 && pid == 0 {
		unix.RawSyscall(unix.SYS_EXIT_GROUP, 0, 0, 0)
		for {
		}
	}
	if forkError != unix.EPERM {
		if forkError == 0 {
			var status unix.WaitStatus
			_, _ = unix.Wait4(int(pid), &status, 0, nil)
		}
		return 14, true
	}
	if err := unix.Unshare(unix.CLONE_NEWNET); err != unix.EPERM {
		return 15, true
	}
	if fd, err := unix.MemfdCreate("forbidden", 0); err != unix.EPERM {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		return 20, true
	}
	var limit unix.Rlimit
	if unix.Getrlimit(unix.RLIMIT_AS, &limit) != nil || limit.Max != 1<<30 {
		return 16, true
	}
	if unix.Getrlimit(unix.RLIMIT_NPROC, &limit) != nil || limit.Max != 32 {
		return 17, true
	}
	if unix.Getrlimit(unix.RLIMIT_CORE, &limit) != nil || limit.Max != 0 {
		return 18, true
	}
	if err := os.WriteFile("tmp/inside", []byte("allowed"), 0o600); err != nil {
		return 19, true
	}
	fmt.Println("SANDBOX_PROBE_OK")
	return 0, true
}

func sandboxTestPolicy() SandboxPolicy {
	return SandboxPolicy{MemoryBytes: 1 << 30, Processes: 32, CPUSeconds: 15}
}

func TestConfigSandboxEnforcesNetworkFilesystemProcessAndMemory(t *testing.T) {
	if raceInstrumented {
		t.Skip("race helper requires a huge shadow address space; production sandbox is exercised by verify-runner-sandbox.mjs without race instrumentation")
	}
	id, registry, workspace := helperSetup(t)
	defer workspace.Close()
	digest, err := fileSHA256(registry.Files[id])
	if err != nil {
		t.Fatal(err)
	}
	registry.Builds = map[ir.ID]capability.Build{id: {ID: id, Family: ir.Xray, BinarySHA256: digest}}
	outside := filepath.Join(t.TempDir(), "control-private-key")
	if err := os.WriteFile(outside, []byte("EXAMPLE_CONTROL_SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]string{"runner_probe": "restrictions", "outside": outside})
	result, err := ValidateIsolated(context.Background(), registry, id, data, 15*time.Second, sandboxTestPolicy())
	if err != nil || result.ExitCode != 0 || !bytes.Contains(result.Output, []byte("SANDBOX_PROBE_OK")) {
		t.Fatalf("sandbox restrictions failed: exit=%d err=%v output=%s", result.ExitCode, err, result.Output)
	}
	if bytes.Contains(result.Output, []byte("EXAMPLE_CONTROL_SECRET")) {
		t.Fatal("control secret reached checker output")
	}
	data, err = os.ReadFile(outside)
	if err != nil || string(data) != "EXAMPLE_CONTROL_SECRET" {
		t.Fatal("checker changed control file")
	}
}

func TestConfigSandboxCancellationCleansBeforeReturn(t *testing.T) {
	if raceInstrumented {
		t.Skip("race helper cannot fit the enforced 1GiB address-space limit; covered by the non-race Linux sandbox verifier")
	}
	id, registry, workspace := helperSetup(t)
	defer workspace.Close()
	digest, err := fileSHA256(registry.Files[id])
	if err != nil {
		t.Fatal(err)
	}
	registry.Builds = map[ir.ID]capability.Build{id: {ID: id, Family: ir.Xray, BinarySHA256: digest}}
	parent := t.TempDir()
	t.Setenv("TMPDIR", parent)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(200 * time.Millisecond); cancel() }()
	start := time.Now()
	result, err := ValidateIsolated(ctx, registry, id, []byte(`{"runner_probe":"sleep"}`), 15*time.Second, sandboxTestPolicy())
	if err != nil || !result.Canceled || time.Since(start) > 5*time.Second {
		t.Fatalf("cancel did not reap within grace: %+v %v", result, err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("plaintext workspace was retained after cancellation")
	}
}

func TestConfigSandboxRejectsInvalidBounds(t *testing.T) {
	_, err := ValidateIsolated(context.Background(), nil, "", []byte("{}"), time.Second, SandboxPolicy{})
	if !errors.Is(err, ErrSandboxUnavailable) {
		t.Fatalf("invalid isolation policy accepted: %v", err)
	}
}

func TestConfigSandboxClassifiesFatalRuntimeExit(t *testing.T) {
	if raceInstrumented {
		t.Skip("fatal-runtime helper uses the production address-space budget; covered by the non-race Linux sandbox verifier")
	}
	id, registry, workspace := helperSetup(t)
	defer workspace.Close()
	digest, err := fileSHA256(registry.Files[id])
	if err != nil {
		t.Fatal(err)
	}
	registry.Builds = map[ir.ID]capability.Build{id: {ID: id, Family: ir.Xray, BinarySHA256: digest}}
	result, err := ValidateIsolated(context.Background(), registry, id, []byte(`{"runner_probe":"fatal_runtime_exit"}`), 15*time.Second, sandboxTestPolicy())
	if result.ExitCode != 2 || !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("Go runtime exit was treated as invalid configuration: %d %v", result.ExitCode, err)
	}
}

func TestSealedExecutableSurvivesReplacement(t *testing.T) {
	source := filepath.Join(t.TempDir(), "pinned-core")
	original := []byte("synthetic immutable executable bytes")
	if err := os.WriteFile(source, original, 0o600); err != nil {
		t.Fatal(err)
	}
	sealed, err := sealedExecutable(source, runnerprotocol.Digest(original))
	if err != nil {
		t.Fatal(err)
	}
	defer sealed.Close()
	if err := os.WriteFile(source, []byte("changed after final authentication"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sealed.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(sealed)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatal("sealed bytes changed with source pathname")
	}
	if _, err := sealed.WriteAt([]byte("x"), 0); !errors.Is(err, unix.EPERM) {
		t.Fatal("executable snapshot remained writable")
	}
	if err := sealed.Truncate(0); !errors.Is(err, unix.EPERM) {
		t.Fatal("executable snapshot remained resizable")
	}
	if _, err := sealedExecutable(source, runnerprotocol.Digest(original)); !errors.Is(err, ErrDigestMismatch) {
		t.Fatal("changed source passed original digest")
	}
}

func TestRealConfigSandbox(t *testing.T) {
	root := os.Getenv("PROXYLOOM_RUNNER_REAL_CORES")
	fixtures := os.Getenv("PROXYLOOM_RUNNER_FIXTURE_ROOT")
	if root == "" || fixtures == "" {
		t.Skip("requires locked core and validation fixture mounts")
	}
	catalog, err := capability.Load()
	if err != nil {
		t.Fatal(err)
	}
	registry := DiskRegistry{Root: root, Catalog: catalog}
	count := 0
	for _, build := range catalog.Builds() {
		if build.OS != "linux" || build.Arch != runtime.GOARCH {
			continue
		}
		count++
		t.Run(string(build.Family), func(t *testing.T) {
			stem := strings.ReplaceAll(string(build.Family), "-", "")
			ext := "json"
			if build.Family == ir.Mihomo {
				ext = "yaml"
			}
			for _, test := range []struct {
				name  string
				valid bool
			}{{"valid", true}, {"invalid", false}} {
				data, err := os.ReadFile(filepath.Join(fixtures, stem+"."+test.name+"."+ext))
				if err != nil {
					t.Fatal(err)
				}
				result, err := ValidateIsolated(context.Background(), registry, build.ID, data, 15*time.Second, sandboxTestPolicy())
				clear(data)
				if err != nil || (result.ExitCode == 0) != test.valid {
					t.Fatalf("%s checker: exit=%d err=%v redacted_output=%s", test.name, result.ExitCode, err, result.Output)
				}
			}
		})
	}
	if count != 3 {
		t.Fatalf("wanted exactly three locked builds, got %d", count)
	}
}
