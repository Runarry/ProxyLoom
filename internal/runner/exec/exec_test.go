package exec

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	ose "os/exec"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const helperFlag = "proxyloom-exec-helper"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == helperFlag {
		os.Exit(runHelper(os.Args[2:]))
	}
	if len(os.Args) > 1 && coreArgv(os.Args[1:]) {
		os.Exit(runCoreHelper(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func coreArgv(args []string) bool {
	switch strings.Join(args, " ") {
	case "run -test -c config.json", "run -c config.json", "check -c config.json", "-t -d . -f config.yaml", "-d . -f config.yaml":
		return true
	default:
		return false
	}
}

func runCoreHelper(args []string) int {
	filename := "config.json"
	if strings.Contains(strings.Join(args, " "), "config.yaml") {
		filename = "config.yaml"
	}
	if _, err := os.Stat(filename); err != nil {
		fmt.Fprintln(os.Stderr, "missing config")
		return 2
	}
	fmt.Println("core_argv=" + strings.Join(args, " "))
	wd, _ := os.Getwd()
	fmt.Println("cwd=" + wd)
	if strings.Join(args, " ") == "run -c config.json" || strings.Join(args, " ") == "-d . -f config.yaml" {
		time.Sleep(30 * time.Second)
	}
	return 0
}

func runHelper(args []string) int {
	if len(args) == 0 {
		return 2
	}
	switch args[0] {
	case "printenv":
		for _, item := range os.Environ() {
			fmt.Println(item)
		}
		wd, _ := os.Getwd()
		fmt.Println("cwd=" + wd)
		return 0
	case "sleep":
		time.Sleep(30 * time.Second)
		return 0
	case "spam":
		chunk := bytes.Repeat([]byte("x"), 1024)
		for i := 0; i < 128; i++ {
			_, _ = os.Stdout.Write(chunk)
		}
		return 0
	case "spawn-child":
		cmd := ose.Command(os.Args[0], helperFlag, "sleep")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("child_pid=%d\n", cmd.Process.Pid)
		_ = cmd.Wait()
		return 0
	case "args":
		fmt.Println(strings.Join(args[1:], "\n"))
		return 0
	default:
		return 2
	}
}

func TestRunClearsProxyEnvironmentAndUsesJobDirectory(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:9")
	id, registry, workspace := helperSetup(t)
	defer workspace.Close()
	spec := adapter.CommandSpec{ExecutableID: id, Args: []string{helperFlag, "printenv"}, WorkingDir: workspace.Directory}
	result, err := Run(context.Background(), registry, spec, workspace, Options{Timeout: 5 * time.Second})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("run: %v %+v", err, result)
	}
	out := string(result.Output)
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy"} {
		if strings.Contains(out, name+"=") {
			t.Fatalf("inherited %s: %s", name, out)
		}
	}
	if !strings.Contains(out, "cwd="+workspace.Directory) && !strings.Contains(strings.ReplaceAll(out, "/", "\\"), "cwd="+workspace.Directory) {
		t.Fatalf("working directory not applied: %s", out)
	}
	tmp := filepath.Join(workspace.Directory, "tmp")
	for _, name := range []string{"TMPDIR", "TMP", "TEMP", "XDG_CACHE_HOME", "XDG_CONFIG_HOME"} {
		if !strings.Contains(out, name+"="+tmp) && !strings.Contains(strings.ReplaceAll(out, "/", "\\"), name+"="+tmp) {
			t.Fatalf("missing %s=%s in %s", name, tmp, out)
		}
	}
}

func TestRunRejectsShellAndEscapingArgs(t *testing.T) {
	id, registry, workspace := helperSetup(t)
	defer workspace.Close()
	shellPath := filepath.Join(t.TempDir(), "sh")
	if err := os.WriteFile(shellPath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	shellAbs, err := filepath.Abs(shellPath)
	if err != nil {
		t.Fatal(err)
	}
	shell := adapter.CommandSpec{ExecutableID: id, Args: []string{helperFlag, "printenv"}, WorkingDir: workspace.Directory}
	shellReg := MapRegistry{Files: map[ir.ID]string{id: shellAbs}}
	if _, err := Run(context.Background(), shellReg, shell, workspace, Options{}); err != ErrForbiddenPath {
		t.Fatalf("shell executable: %v", err)
	}
	bad := adapter.CommandSpec{ExecutableID: id, Args: []string{"-c", "https://example.invalid/config.json"}, WorkingDir: workspace.Directory}
	if _, err := Run(context.Background(), registry, bad, workspace, Options{}); err != ErrForbiddenArgs {
		t.Fatalf("remote config: %v", err)
	}
	parent := adapter.CommandSpec{ExecutableID: id, Args: []string{"-c", filepath.Join("..", "config.json")}, WorkingDir: workspace.Directory}
	if _, err := Run(context.Background(), registry, parent, workspace, Options{}); err != ErrForbiddenArgs {
		t.Fatalf("parent path: %v", err)
	}
	exe := filepath.Join(t.TempDir(), "sh.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	exeAbs, err := filepath.Abs(exe)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), MapRegistry{Files: map[ir.ID]string{id: exeAbs}}, shell, workspace, Options{}); err != ErrForbiddenPath {
		t.Fatalf("sh.exe: %v", err)
	}
}

func TestRunRejectsSymlinkToShell(t *testing.T) {
	id, _, workspace := helperSetup(t)
	defer workspace.Close()
	dir := t.TempDir()
	sh := filepath.Join(dir, "sh")
	if err := os.WriteFile(sh, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "xray")
	if err := os.Symlink(sh, link); err != nil {
		t.Skip("symlink creation is not permitted on this host")
	}
	linkAbs, err := filepath.Abs(link)
	if err != nil {
		t.Fatal(err)
	}
	spec := adapter.CommandSpec{ExecutableID: id, Args: []string{helperFlag, "printenv"}, WorkingDir: workspace.Directory}
	if _, err := Run(context.Background(), MapRegistry{Files: map[ir.ID]string{id: linkAbs}}, spec, workspace, Options{}); err != ErrForbiddenPath {
		t.Fatalf("symlink to shell: %v", err)
	}
}

func TestTimeoutAndCancelReapHelper(t *testing.T) {
	id, registry, workspace := helperSetup(t)
	defer workspace.Close()
	spec := adapter.CommandSpec{ExecutableID: id, Args: []string{helperFlag, "sleep"}, WorkingDir: workspace.Directory}
	result, err := Run(context.Background(), registry, spec, workspace, Options{Timeout: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !result.TimedOut || result.Canceled {
		t.Fatalf("timeout result %+v", result)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() {
		childSpec := adapter.CommandSpec{ExecutableID: id, Args: []string{helperFlag, "spawn-child"}, WorkingDir: workspace.Directory}
		res, err := Run(ctx, registry, childSpec, workspace, Options{Timeout: 10 * time.Second})
		if err != nil {
			t.Errorf("spawn: %v", err)
		}
		done <- res
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	res := <-done
	if !res.Canceled {
		t.Fatalf("expected cancel %+v", res)
	}
	if pid := childPID(res.Output); pid != 0 {
		waitPIDReaped(t, pid)
	}
}

func TestConcurrentRunDoesNotStealWait(t *testing.T) {
	id, registry := coreRegistry(t, ir.Xray)
	ws1, err := Allocate(t.TempDir(), "config.json", []byte(`{"listen":"127.0.0.1"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer ws1.Close()
	ws2, err := Allocate(t.TempDir(), "config.json", []byte(`{"listen":"127.0.0.1"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer ws2.Close()
	spec := adapter.CommandSpec{ExecutableID: id, Args: []string{helperFlag, "spawn-child"}, WorkingDir: ""}
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()
	var (
		wg         sync.WaitGroup
		res1, res2 Result
		err1, err2 error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		s := spec
		s.WorkingDir = ws1.Directory
		res1, err1 = Run(ctx1, registry, s, ws1, Options{Timeout: 10 * time.Second})
	}()
	go func() {
		defer wg.Done()
		s := spec
		s.WorkingDir = ws2.Directory
		res2, err2 = Run(ctx2, registry, s, ws2, Options{Timeout: 10 * time.Second})
	}()
	time.Sleep(300 * time.Millisecond)
	cancel1()
	time.Sleep(300 * time.Millisecond)
	cancel2()
	wg.Wait()
	if err1 != nil {
		t.Fatalf("run1: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("run2: %v", err2)
	}
	if !res1.Canceled {
		t.Fatalf("run1 expected cancel %+v", res1)
	}
	if !res2.Canceled {
		t.Fatalf("run2 expected cancel %+v", res2)
	}
	if pid := childPID(res1.Output); pid != 0 {
		waitPIDReaped(t, pid)
	}
	if pid := childPID(res2.Output); pid != 0 {
		waitPIDReaped(t, pid)
	}
}

func TestLogLimitTruncatesWithoutBlocking(t *testing.T) {
	id, registry, workspace := helperSetup(t)
	defer workspace.Close()
	spec := adapter.CommandSpec{ExecutableID: id, Args: []string{helperFlag, "spam"}, WorkingDir: workspace.Directory}
	result, err := Run(context.Background(), registry, spec, workspace, Options{Timeout: 5 * time.Second, LogLimit: 4096})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || len(result.Output) > 4096 {
		t.Fatalf("limit %+v len=%d", result, len(result.Output))
	}
}

func TestDiskRegistryRejectsMissingAndWrongDigest(t *testing.T) {
	catalog, err := capability.Load()
	if err != nil {
		t.Fatal(err)
	}
	build, err := catalog.Lookup(ir.Xray, "26.3.27", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	reg := DiskRegistry{Root: root, Catalog: catalog}
	if _, _, err := reg.Executable(build.ID); err != ErrMissingBinary {
		t.Fatalf("missing: %v", err)
	}
	path := filepath.Join(root, string(build.Family), build.GitTag, build.Arch, build.BinaryName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not-a-core"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reg.Executable(build.ID); err != ErrDigestMismatch {
		t.Fatalf("digest: %v", err)
	}
}

func TestWorkspacePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes are not enforced on Windows")
	}
	ws, err := Allocate(t.TempDir(), "config.json", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	info, err := os.Stat(ws.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("directory mode %v", info.Mode())
	}
	cfg, err := os.Stat(filepath.Join(ws.Directory, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config mode %v", cfg.Mode())
	}
	tmp, err := os.Stat(filepath.Join(ws.Directory, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if tmp.Mode().Perm()&0o077 != 0 {
		t.Fatalf("tmp mode %v", tmp.Mode())
	}
}

func TestValidateConfigUsesFamilyAdapterArgv(t *testing.T) {
	id, registry := coreRegistry(t, ir.Xray)
	result, err := ValidateConfig(context.Background(), registry, id, []byte(`{"listen":"127.0.0.1"}`))
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("validate: %v %+v", err, result)
	}
	if !strings.Contains(string(result.Output), "core_argv=run -test -c config.json") {
		t.Fatalf("argv %s", result.Output)
	}
	if _, err := ValidateConfig(context.Background(), MapRegistry{Files: map[ir.ID]string{id: registry.Files[id]}}, id, []byte(`{}`)); err != ErrUnknownExecutable {
		t.Fatalf("empty family: %v", err)
	}
	if _, err := ValidateConfig(context.Background(), registry, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", []byte(`{}`)); err != ErrUnknownExecutable {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestStartCancelsFamilyAdapterProcess(t *testing.T) {
	id, registry := coreRegistry(t, ir.Xray)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() {
		result, err := Start(ctx, registry, id, []byte(`{"listen":"127.0.0.1"}`))
		if err != nil {
			t.Errorf("start: %v", err)
		}
		done <- result
	}()
	time.Sleep(200 * time.Millisecond)
	cancel()
	result := <-done
	if !result.Canceled {
		t.Fatalf("expected cancel %+v", result)
	}
}

func helperSetup(t *testing.T) (ir.ID, MapRegistry, Workspace) {
	t.Helper()
	id, registry := coreRegistry(t, ir.Xray)
	ws, err := Allocate(t.TempDir(), "config.json", []byte(`{"listen":"127.0.0.1"}`))
	if err != nil {
		t.Fatal(err)
	}
	return id, registry, ws
}

func coreRegistry(t *testing.T, family ir.CoreFamily) (ir.ID, MapRegistry) {
	t.Helper()
	id := ir.ID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	bin, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	return id, MapRegistry{
		Files:  map[ir.ID]string{id: bin},
		Builds: map[ir.ID]capability.Build{id: {ID: id, Family: family}},
	}
}

func childPID(output []byte) int {
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "child_pid=") {
			pid, _ := strconv.Atoi(strings.TrimPrefix(line, "child_pid="))
			return pid
		}
	}
	return 0
}

func pidAlive(t *testing.T, pid int) bool {
	t.Helper()
	if pid <= 0 {
		return false
	}
	if runtime.GOOS != "windows" {
		return signalZero(pid) == nil
	}
	root := os.Getenv("SYSTEMROOT")
	if root == "" {
		root = `C:\Windows`
	}
	cmd := ose.Command(filepath.Join(root, "System32", "tasklist.exe"), "/FO", "CSV", "/NH", "/FI", "PID eq "+strconv.Itoa(pid))
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return bytes.Contains(out, []byte(`"`+strconv.Itoa(pid)+`"`))
}

func waitPIDReaped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var last string
	for {
		last = pidProcInfo(pid)
		if !pidAlive(t, pid) {
			return
		}
		state, _, _, ok := linuxProcState(pid)
		running := ok && (state == 'R' || state == 'S' || state == 'D' || state == 'I')
		if time.Now().After(deadline) {
			if running {
				t.Fatalf("child %d still running: %s", pid, last)
			}
			t.Fatalf("child %d still present: %s", pid, last)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func pidProcInfo(pid int) string {
	if pid <= 0 {
		return "pid<=0"
	}
	if runtime.GOOS == "windows" {
		return "windows"
	}
	path := fmt.Sprintf("/proc/%d/stat", pid)
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("proc_stat_err=%v", err)
	}
	state, ppid, pgrp, ok := parseProcStat(raw)
	if !ok {
		return fmt.Sprintf("stat=%q", strings.TrimSpace(string(raw)))
	}
	status := fmt.Sprintf("state=%c ppid=%d pgrp=%d stat=%s", state, ppid, pgrp, strings.TrimSpace(string(raw)))
	if extra, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		var lines []string
		for _, line := range strings.Split(string(extra), "\n") {
			if strings.HasPrefix(line, "Name:") || strings.HasPrefix(line, "State:") || strings.HasPrefix(line, "PPid:") || strings.HasPrefix(line, "NSpid:") {
				lines = append(lines, line)
			}
		}
		if len(lines) > 0 {
			status += " status=[" + strings.Join(lines, "; ") + "]"
		}
	}
	return status
}

func linuxProcState(pid int) (state byte, ppid, pgrp int, ok bool) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, 0, 0, false
	}
	return parseProcStat(raw)
}

func parseProcStat(raw []byte) (state byte, ppid, pgrp int, ok bool) {
	s := string(raw)
	rparen := strings.LastIndex(s, ")")
	if rparen < 0 || rparen+2 >= len(s) {
		return 0, 0, 0, false
	}
	fields := strings.Fields(s[rparen+2:])
	if len(fields) < 3 || len(fields[0]) != 1 {
		return 0, 0, 0, false
	}
	ppid, _ = strconv.Atoi(fields[1])
	pgrp, _ = strconv.Atoi(fields[2])
	return fields[0][0], ppid, pgrp, true
}
