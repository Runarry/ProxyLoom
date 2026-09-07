package chainverify

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sync"
)

type scenarioResult struct {
	Family   string `json:"family"`
	Scenario string `json:"scenario"`
	Status   string `json:"status"`
	Expected string `json:"expected,omitempty"`
	Observed string `json:"observed,omitempty"`
}

type liveReport struct {
	Task           string           `json:"task"`
	AdapterVersion string           `json:"adapter_version"`
	GOOS           string           `json:"goos"`
	GOARCH         string           `json:"goarch"`
	CoresRootSet   bool             `json:"cores_root_set"`
	Results        []scenarioResult `json:"results"`
	mu             sync.Mutex       `json:"-"`
}

var reportBundle = liveReport{Task: "T-028", AdapterVersion: "0.1.0-m0-native", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}

var secretPattern = regexp.MustCompile(`(?i)(EXAMPLE_ONLY_|password|secret|private[-_]?key)`)

func recordResult(family, scenario, status, expected, observed string) {
	reportBundle.mu.Lock()
	defer reportBundle.mu.Unlock()
	reportBundle.Results = append(reportBundle.Results, scenarioResult{
		Family: family, Scenario: scenario, Status: status, Expected: expected, Observed: observed,
	})
}

func writeReport(path string) error {
	if path == "" {
		return nil
	}
	reportBundle.mu.Lock()
	defer reportBundle.mu.Unlock()
	reportBundle.CoresRootSet = os.Getenv("PROXYLOOM_CORES_ROOT") != ""
	data, err := json.MarshalIndent(struct {
		Task           string           `json:"task"`
		AdapterVersion string           `json:"adapter_version"`
		GOOS           string           `json:"goos"`
		GOARCH         string           `json:"goarch"`
		CoresRootSet   bool             `json:"cores_root_set"`
		Results        []scenarioResult `json:"results"`
	}{reportBundle.Task, reportBundle.AdapterVersion, reportBundle.GOOS, reportBundle.GOARCH, reportBundle.CoresRootSet, reportBundle.Results}, "", "  ")
	if err != nil {
		return err
	}
	if secretPattern.Match(data) {
		return fmt.Errorf("live report contained secrets")
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
