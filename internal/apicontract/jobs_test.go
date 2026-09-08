package apicontract

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestAsyncContractsSeparateExecutorStateAndVerdict(t *testing.T) {
	now := time.Date(2026, 9, 8, 1, 2, 3, 0, time.UTC)
	job := Job{JobID: nodeID, Revision: 1, Executor: Runner, Type: Connectivity, State: Succeeded, Attempt: 1, LeaseSeq: Counter(math.MaxInt64), CreatedAt: now, CoreBuildID: scopeID, Verdict: Fail, FinishedAt: &now}
	if err := job.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDTO("JobResponse", JobResponse{RequestID: "request-test", Data: job}); err != nil {
		t.Fatal(err)
	}
	wrong := job
	wrong.Executor = APIWorker
	expectStatus(t, wrong.Validate(), 400)
	worker := Job{JobID: nodeID, Revision: 1, Executor: APIWorker, Type: Compile, State: Queued, CreatedAt: now}
	if err := worker.Validate(); err != nil {
		t.Fatal(err)
	}
	badState := job
	badState.State = JobState("pass")
	expectStatus(t, badState.Validate(), 400)
	request := TestCreateRequest{Subjects: []TestSubject{{Kind: ir.KindChain, ID: nodeID}}, CoreBuildID: scopeID, Type: DownloadThroughput, TestTargetID: scopeID, Limits: TestLimits{DurationMS: 10000, MaxBytes: 20971520}}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	request.TestTargetID = ""
	expectStatus(t, request.Validate(), 400)
	request.Type = Compile
	expectStatus(t, request.Validate(), 400)
	batch := TestBatchResponse{RequestID: "request-test", Data: TestBatch{BatchID: scopeID, Revision: 1, State: Succeeded, Verdict: Fail, JobIDs: []ir.ID{nodeID}, Children: []Job{job}, Completed: 1, Total: 1, EffectiveLimits: TestLimits{DurationMS: 10000, MaxBytes: 20971520}, CreatedAt: now}}
	if err := ValidateDTO("TestBatchResponse", batch); err != nil {
		t.Fatal(err)
	}
	publish := PublishRequest{BatchID: nodeID, ExpectedGeneration: Counter(math.MaxInt64), EffectivePreviewHash: strings.Repeat("a", 64), Confirmation: PublicationConfirmation{Acknowledged: true}}
	if err := publish.Validate(); err != nil {
		t.Fatal(err)
	}
	publish.Confirmation.Acknowledged = false
	expectStatus(t, publish.Validate(), 400)
}

func TestRunnerLeaseAndSSEContracts(t *testing.T) {
	heartbeat := RunnerJobHeartbeatRequest{JobID: nodeID, Attempt: 1, LeaseSeq: Revision(math.MaxInt64)}
	if err := ValidateDTO("RunnerJobHeartbeatRequest", heartbeat); err != nil {
		t.Fatal(err)
	}
	heartbeat.Attempt = 0
	expectStatus(t, ValidateDTO("RunnerJobHeartbeatRequest", heartbeat), 400)
	result := RunnerJobResultRequest{JobID: nodeID, Attempt: 1, LeaseSeq: 1, ResultHash: strings.Repeat("a", 64), State: Succeeded, Verdict: Fail, Metrics: TestMetrics{}}
	if err := ValidateDTO("RunnerJobResultRequest", result); err != nil {
		t.Fatal(err)
	}
	result.State = Running
	expectStatus(t, ValidateDTO("RunnerJobResultRequest", result), 400)
	for _, test := range []struct {
		values []string
		ok     bool
	}{
		{nil, true}, {[]string{"0"}, true}, {[]string{"9223372036854775807"}, true},
		{[]string{"9223372036854775808"}, false}, {[]string{"01"}, false}, {[]string{"1", "2"}, false}, {[]string{"1\nevent: injected"}, false},
	} {
		header := http.Header{}
		for _, value := range test.values {
			header.Add("Last-Event-ID", value)
		}
		seq, present, err := ParseLastEventID(header)
		if test.ok {
			if err != nil || present != (len(test.values) > 0) {
				t.Fatal("Last-Event-ID failed")
			}
			if len(test.values) > 0 && test.values[0] == "9223372036854775807" && seq != Counter(math.MaxInt64) {
				t.Fatal("SSE counter precision lost")
			}
		} else {
			expectStatus(t, err, 400)
		}
	}
	var output bytes.Buffer
	event := JobEvent{JobID: nodeID, Seq: Revision(math.MaxInt64), Phase: "running", Completed: 1, Total: 1, Verdict: Fail, Error: &ErrorBody{Code: ValidationFailed, Message: "SYNTHETIC-RAW-STDOUT", Details: []Detail{{FieldPath: "/password/SYNTHETIC-RAW-STDOUT", ResourceID: "SYNTHETIC-RAW-STDOUT"}}}}
	if err := WriteJobEvent(&output, event); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "SYNTHETIC-RAW-STDOUT") || !strings.Contains(output.String(), "id: 9223372036854775807\nevent: job_event\n") {
		t.Fatal("SSE leaked or lost event ID")
	}
	output.Reset()
	if err := WriteSnapshotReset(&output, nodeID, Counter(math.MaxInt64)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "event: snapshot_reset\n") || !strings.Contains(output.String(), `"latest_seq":"9223372036854775807"`) {
		t.Fatal("snapshot reset contract")
	}
	var counter Counter
	expectStatus(t, json.Unmarshal([]byte(`1`), &counter), 400)
	var revision Revision
	expectStatus(t, json.Unmarshal([]byte(`"0"`), &revision), 400)
}
