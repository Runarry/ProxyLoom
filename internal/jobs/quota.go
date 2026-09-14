package jobs

import "errors"

var ErrBudgetExceeded = errors.New("jobs_budget_exceeded")

// Quota is persisted configuration. Requests can lower these limits but cannot
// increase the immutable host limits used by the Runner.
type Quota struct {
	DailyDownloadBytes           int64 `json:"daily_download_bytes"`
	ConnectivityConcurrency      int32 `json:"connectivity_concurrency"`
	ThroughputConcurrency        int32 `json:"throughput_concurrency"`
	MaxTestDurationMS            int64 `json:"max_test_duration_ms"`
	MaxTestBytes                 int64 `json:"max_test_bytes"`
	MinimumThroughputSampleBytes int64 `json:"minimum_throughput_sample_bytes"`
}

func DefaultQuota() Quota {
	return Quota{1 << 30, 4, 1, 60000, 20 << 20, 1 << 20}
}

func (q Quota) Valid() bool {
	return q.DailyDownloadBytes > 0 && q.DailyDownloadBytes <= 9007199254740991 &&
		q.ConnectivityConcurrency >= 1 && q.ConnectivityConcurrency <= 64 &&
		q.ThroughputConcurrency >= 1 && q.ThroughputConcurrency <= 16 &&
		q.MaxTestDurationMS >= 1000 && q.MaxTestDurationMS <= 60000 &&
		q.MaxTestBytes >= 1 && q.MaxTestBytes <= 1<<30 &&
		q.MinimumThroughputSampleBytes >= 1 && q.MinimumThroughputSampleBytes <= q.MaxTestBytes
}

func (kind Type) Network() bool { return kind == Connectivity || kind == DownloadThroughput }
