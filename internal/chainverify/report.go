package chainverify

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sync"

	"github.com/Runarry/ProxyLoom/internal/adapter"
)

type scenarioResult struct {
	Family          string           `json:"family"`
	Scenario        string           `json:"scenario"`
	Status          string           `json:"status"`
	Expected        string           `json:"expected,omitempty"`
	Observed        string           `json:"observed,omitempty"`
	CoreBuildID     string           `json:"core_build_id,omitempty"`
	CoreBuildSHA256 string           `json:"core_build_sha256,omitempty"`
	Sessions        []*sessionResult `json:"sessions,omitempty"`
	Probes          []probeResult    `json:"probes,omitempty"`
	Traffic         []trafficResult  `json:"traffic,omitempty"`
}

type sessionResult struct {
	TestConfigSHA256    string `json:"test_config_sha256"`
	ExpectedTermination string `json:"expected_termination"`
	CleanupStatus       string `json:"cleanup_status"`
	CleanupError        string `json:"cleanup_error,omitempty"`
	ExitCode            int    `json:"exit_code"`
	Canceled            bool   `json:"canceled"`
	TimedOut            bool   `json:"timed_out"`
}

type probeResult struct {
	TestConfigSHA256  string `json:"test_config_sha256"`
	RequestID         string `json:"request_id"`
	ReturnedRequestID string `json:"returned_request_id,omitempty"`
	ExitIP            string `json:"exit_ip,omitempty"`
	OK                bool   `json:"ok"`
	Error             string `json:"error,omitempty"`
}

type trafficResult struct {
	Phase                 string `json:"phase"`
	TargetAcceptDelta     int    `json:"target_accept_delta"`
	TargetOKDelta         int    `json:"target_ok_delta"`
	AOKDelta              int    `json:"a_ok_delta"`
	BOKDelta              int    `json:"b_ok_delta"`
	AForbiddenSourceDelta int    `json:"a_forbidden_source_delta"`
	BForbiddenSourceDelta int    `json:"b_forbidden_source_delta"`
}

type liveReport struct {
	Task                  string           `json:"task"`
	AdapterVersion        string           `json:"adapter_version"`
	GOOS                  string           `json:"goos"`
	GOARCH                string           `json:"goarch"`
	CoresRootSet          bool             `json:"cores_root_set"`
	TestConfigDigestScope string           `json:"test_config_digest_scope"`
	Results               []scenarioResult `json:"results"`
	mu                    sync.Mutex       `json:"-"`
}

var reportBundle = liveReport{
	Task: "T-028", AdapterVersion: "0.2.0-m1-orchestration", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
	TestConfigDigestScope: "SHA-256 of test-only synthetic configuration; not a production content HMAC",
}

var secretPattern = regexp.MustCompile(`(?i)(EXAMPLE_ONLY_|password|secret|private[-_]?key)`)

func recordResult(result scenarioResult) {
	reportBundle.mu.Lock()
	defer reportBundle.mu.Unlock()
	reportBundle.Results = append(reportBundle.Results, result)
}

func reportError(err error) string {
	if err == nil {
		return ""
	}
	message := truncate(adapter.RedactLogLine([]byte(err.Error())), 800)
	if secretPattern.MatchString(message) {
		return "error details redacted"
	}
	return message
}

func writeReport(path string) error {
	if path == "" {
		return nil
	}
	reportBundle.mu.Lock()
	defer reportBundle.mu.Unlock()
	reportBundle.CoresRootSet = os.Getenv("PROXYLOOM_CORES_ROOT") != ""
	data, err := json.MarshalIndent(struct {
		Task                  string           `json:"task"`
		AdapterVersion        string           `json:"adapter_version"`
		GOOS                  string           `json:"goos"`
		GOARCH                string           `json:"goarch"`
		CoresRootSet          bool             `json:"cores_root_set"`
		TestConfigDigestScope string           `json:"test_config_digest_scope"`
		Results               []scenarioResult `json:"results"`
	}{reportBundle.Task, reportBundle.AdapterVersion, reportBundle.GOOS, reportBundle.GOARCH, reportBundle.CoresRootSet, reportBundle.TestConfigDigestScope, reportBundle.Results}, "", "  ")
	if err != nil {
		return err
	}
	if secretPattern.Match(data) {
		return fmt.Errorf("live report contained secrets")
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
