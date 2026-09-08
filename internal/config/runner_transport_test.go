package config

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func runnerTransportEnvironment(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	return []string{"PROXYLOOM_RUNNER_API_URL=https://api:9091", "PROXYLOOM_RUNNER_ID=76000000-0000-4000-8000-000000000001", "PROXYLOOM_RUNNER_CORE_ROOT=" + filepath.Join(root, "cores"), "PROXYLOOM_RUNNER_CA_FILE=" + filepath.Join(root, "ca"), "PROXYLOOM_RUNNER_CERT_FILE=" + filepath.Join(root, "cert"), "PROXYLOOM_RUNNER_KEY_FILE=" + filepath.Join(root, "key")}
}

func TestRunnerTransportRequiresIsolatedHTTPSIdentity(t *testing.T) {
	env := runnerTransportEnvironment(t)
	cfg, err := LoadRunnerTransport(env)
	if err != nil || cfg.HTTPAddr != "127.0.0.1:9092" {
		t.Fatalf("configured runner rejected: %v", err)
	}
	for _, extra := range []string{"PROXYLOOM_DATABASE_DSN_FILE=EXAMPLE_FORBIDDEN", "PROXYLOOM_MASTER_KEY_FILE=EXAMPLE_FORBIDDEN", "PROXYLOOM_TOKEN_PEPPER_FILE=EXAMPLE_FORBIDDEN", "proxyloom_runner_id=EXAMPLE_FORBIDDEN", "PROXYLOOM_RUNNER_ID=EXAMPLE_FORBIDDEN"} {
		if _, err := LoadRunnerTransport(append(append([]string(nil), env...), extra)); err == nil || strings.Contains(err.Error(), "EXAMPLE_FORBIDDEN") {
			t.Fatal("runner accepted secret, case alias or duplicate setting")
		}
	}
	userinfo := (&url.URL{Scheme: "https", Host: "api:9091", User: url.UserPassword("example-user", "example-password")}).String()
	for _, origin := range []string{"http://api:9091", userinfo, "https://api:9091/path", "https://api:9091?x=1", "https://api:9091#x"} {
		bad := append([]string(nil), env...)
		bad[0] = "PROXYLOOM_RUNNER_API_URL=" + origin
		if _, err := LoadRunnerTransport(bad); err == nil {
			t.Fatal("invalid control origin accepted")
		}
	}
	if _, err := LoadRunnerTransport(env[:1]); err == nil {
		t.Fatal("partial configuration silently fell back")
	}
}

func TestRunnerListenerRequiresCompleteDedicatedConfiguration(t *testing.T) {
	if cfg, err := LoadRunnerListener(func(string) string { return "" }); err != nil || cfg != (RunnerListener{}) {
		t.Fatal("unconfigured listener is not optional")
	}
	if _, err := LoadRunnerListener(func(name string) string {
		if name == "PROXYLOOM_RUNNER_LISTEN_ADDR" {
			return ":9091"
		}
		return ""
	}); err == nil {
		t.Fatal("listener permitted missing trust/registration files")
	}
	root := t.TempDir()
	values := map[string]string{"PROXYLOOM_RUNNER_LISTEN_ADDR": ":9091", "PROXYLOOM_RUNNER_CA_FILE": filepath.Join(root, "ca"), "PROXYLOOM_RUNNER_CERT_FILE": filepath.Join(root, "cert"), "PROXYLOOM_RUNNER_KEY_FILE": filepath.Join(root, "key"), "PROXYLOOM_RUNNER_REGISTRY_FILE": filepath.Join(root, "registered.json")}
	if _, err := LoadRunnerListener(func(name string) string { return values[name] }); err != nil {
		t.Fatal(err)
	}
}
