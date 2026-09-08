package apicontract

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

type Executor string
type JobType string
type JobState string
type Verdict string

const (
	APIWorker          Executor = "api_worker"
	Runner             Executor = "runner"
	ImportParse        JobType  = "import_parse"
	SourceRefresh      JobType  = "source_refresh"
	Compile            JobType  = "compile"
	ConfigValidate     JobType  = "config_validate"
	Connectivity       JobType  = "connectivity"
	DownloadThroughput JobType  = "download_throughput"
	Queued             JobState = "queued"
	Leased             JobState = "leased"
	Running            JobState = "running"
	Succeeded          JobState = "succeeded"
	Failed             JobState = "failed"
	Canceled           JobState = "canceled"
	TimedOut           JobState = "timed_out"
	Pass               Verdict  = "pass"
	Fail               Verdict  = "fail"
	Inconclusive       Verdict  = "inconclusive"
)

type TestSubject struct {
	Kind ir.ResourceKind `json:"kind"`
	ID   ir.ID           `json:"id"`
}
type FrozenTestSubject struct {
	Kind          ir.ResourceKind `json:"kind"`
	ID            ir.ID           `json:"id"`
	Revision      Revision        `json:"revision"`
	SecurityEpoch Revision        `json:"security_epoch"`
}
type TestLimits struct {
	DurationMS int64 `json:"duration_ms"`
	MaxBytes   int64 `json:"max_bytes"`
}
type TestCreateRequest struct {
	Subjects     []TestSubject `json:"subjects"`
	CoreBuildID  ir.ID         `json:"core_build_id"`
	Type         JobType       `json:"type"`
	TestTargetID ir.ID         `json:"test_target_id,omitempty"`
	Limits       TestLimits    `json:"limits"`
}

type Job struct {
	JobID           ir.ID              `json:"job_id"`
	Revision        Revision           `json:"revision"`
	Executor        Executor           `json:"executor"`
	Type            JobType            `json:"type"`
	State           JobState           `json:"state"`
	Attempt         int32              `json:"attempt"`
	LeaseSeq        Counter            `json:"lease_seq"`
	CancelRequested bool               `json:"cancel_requested"`
	CreatedAt       time.Time          `json:"created_at"`
	BatchID         ir.ID              `json:"batch_id,omitempty"`
	Subject         *FrozenTestSubject `json:"subject,omitempty"`
	CoreBuildID     ir.ID              `json:"core_build_id,omitempty"`
	TestTargetID    ir.ID              `json:"test_target_id,omitempty"`
	Verdict         Verdict            `json:"verdict,omitempty"`
	StartedAt       *time.Time         `json:"started_at,omitempty"`
	FinishedAt      *time.Time         `json:"finished_at,omitempty"`
	Error           *ErrorBody         `json:"error,omitempty"`
}
type JobResponse struct {
	RequestID string `json:"request_id"`
	Data      Job    `json:"data"`
}

type TestBatch struct {
	BatchID         ir.ID      `json:"batch_id"`
	Revision        Revision   `json:"revision"`
	State           JobState   `json:"state"`
	Verdict         Verdict    `json:"verdict,omitempty"`
	JobIDs          []ir.ID    `json:"job_ids"`
	Children        []Job      `json:"children"`
	Completed       int32      `json:"completed"`
	Total           int32      `json:"total"`
	CancelRequested bool       `json:"cancel_requested"`
	EffectiveLimits TestLimits `json:"effective_limits"`
	CreatedAt       time.Time  `json:"created_at"`
}
type TestBatchResponse struct {
	RequestID string    `json:"request_id"`
	Data      TestBatch `json:"data"`
}

type PublicationConfirmation struct {
	Acknowledged bool `json:"acknowledged"`
}
type PublishRequest struct {
	BatchID              ir.ID                   `json:"batch_id"`
	ExpectedGeneration   Counter                 `json:"expected_generation"`
	EffectivePreviewHash string                  `json:"effective_preview_hash"`
	Confirmation         PublicationConfirmation `json:"confirmation"`
}

// Publication confirmation still requires a server-owned current-actor record
// for this batch/hash; decoding this request alone never authorizes publishing.
func (request PublishRequest) Validate() error    { return ValidateDTO("PublishRequest", request) }
func (request TestCreateRequest) Validate() error { return ValidateDTO("TestCreateRequest", request) }
func (job Job) Validate() error                   { return ValidateDTO("Job", job) }

type RunnerJobHeartbeatRequest struct {
	JobID    ir.ID    `json:"job_id"`
	Attempt  int32    `json:"attempt"`
	LeaseSeq Revision `json:"lease_seq"`
}
type RunnerJobEventRequest struct {
	JobID     ir.ID      `json:"job_id"`
	Attempt   int32      `json:"attempt"`
	LeaseSeq  Revision   `json:"lease_seq"`
	EventID   ir.ID      `json:"event_id"`
	Phase     string     `json:"phase"`
	Completed int32      `json:"completed"`
	Total     int32      `json:"total"`
	Verdict   Verdict    `json:"verdict,omitempty"`
	Error     *ErrorBody `json:"error,omitempty"`
}

type TestMetrics struct {
	ConfigCheckMS  *float64 `json:"config_check_ms,omitempty"`
	CoreStartMS    *float64 `json:"core_start_ms,omitempty"`
	ProxyDialMS    *float64 `json:"proxy_dial_ms,omitempty"`
	TargetTLSMS    *float64 `json:"target_tls_ms,omitempty"`
	HTTPTTFBMS     *float64 `json:"http_ttfb_ms,omitempty"`
	HTTPTotalMS    *float64 `json:"http_total_ms,omitempty"`
	BodyBytes      *int64   `json:"body_bytes,omitempty"`
	BodyDurationMS *float64 `json:"body_duration_ms,omitempty"`
	ThroughputMbps *float64 `json:"throughput_mbps,omitempty"`
	EgressIP       *string  `json:"egress_ip,omitempty"`
	SampleCount    *int32   `json:"sample_count,omitempty"`
	FailureCount   *int32   `json:"failure_count,omitempty"`
}
type RunnerJobResultRequest struct {
	JobID      ir.ID       `json:"job_id"`
	Attempt    int32       `json:"attempt"`
	LeaseSeq   Revision    `json:"lease_seq"`
	ResultHash string      `json:"result_hash"`
	State      JobState    `json:"state"`
	Verdict    Verdict     `json:"verdict,omitempty"`
	Metrics    TestMetrics `json:"metrics"`
	Error      *ErrorBody  `json:"error,omitempty"`
}

type JobEvent struct {
	JobID     ir.ID      `json:"job_id"`
	Seq       Revision   `json:"seq"`
	Phase     string     `json:"phase"`
	Completed int32      `json:"completed"`
	Total     int32      `json:"total"`
	Verdict   Verdict    `json:"verdict,omitempty"`
	Error     *ErrorBody `json:"error,omitempty"`
}
type SnapshotResetEvent struct {
	JobID       ir.ID   `json:"job_id"`
	LatestSeq   Counter `json:"latest_seq"`
	SnapshotURL string  `json:"snapshot_url"`
}

func ParseLastEventID(header http.Header) (Counter, bool, error) {
	values := header.Values("Last-Event-ID")
	if len(values) == 0 {
		return 0, false, nil
	}
	if len(values) != 1 {
		return 0, false, NewError(MalformedRequest)
	}
	data, _ := json.Marshal(values[0])
	number, err := decimal(data, false)
	if err != nil {
		return 0, false, err
	}
	return Counter(number), true, nil
}

// WriteJobEvent accepts a typed, schema-validated event. Stable sequence IDs come
// from persistent storage; this component does not invent an in-memory replay
// source or forward stdout. The caller first authorizes the current job scope.
func WriteJobEvent(writer io.Writer, event JobEvent) error {
	if event.Error != nil {
		safe := NewError(event.Error.Code, event.Error.Details...).Response("unavailable").Error
		event.Error = &safe
	}
	if err := ValidateDTO("JobEvent", event); err != nil {
		return err
	}
	if event.Completed > event.Total {
		return NewError(InternalError)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return NewError(InternalError)
	}
	_, err = fmt.Fprintf(writer, "id: %s\nevent: job_event\ndata: %s\n\n", strconv.FormatInt(int64(event.Seq), 10), data)
	return err
}

func WriteSnapshotReset(writer io.Writer, jobID ir.ID, latest Counter) error {
	event := SnapshotResetEvent{JobID: jobID, LatestSeq: latest, SnapshotURL: "/api/v1/jobs/" + string(jobID)}
	if err := ValidateDTO("SnapshotResetEvent", event); err != nil {
		return err
	}
	data, err := json.Marshal(event)
	if err != nil {
		return NewError(InternalError)
	}
	_, err = fmt.Fprintf(writer, "event: snapshot_reset\ndata: %s\n\n", data)
	return err
}
