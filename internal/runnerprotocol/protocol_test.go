package runnerprotocol

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestStrictDecodePreservesDestinationAndRejectsAliases(t *testing.T) {
	for _, input := range []string{
		`{"job_id":"10000000-0000-4000-8000-000000000001","lease_seq":"1"}`,
		`{"job_id":"10000000-0000-4000-8000-000000000001","attempt":null,"lease_seq":"1"}`,
		`{"job_id":"10000000-0000-4000-8000-000000000001","job_id":"20000000-0000-4000-8000-000000000001","attempt":1,"lease_seq":"1"}`,
		`{"job_id":"10000000-0000-4000-8000-000000000001","Attempt":1,"lease_seq":"1"}`,
		`{"job_id":"10000000-0000-4000-8000-000000000001","attempt":1,"lease_seq":"01"}`,
		`{"job_id":"10000000-0000-4000-8000-000000000001","attempt":1,"lease_seq":1}`,
		`{"job_id":"10000000-0000-4000-8000-000000000001","attempt":1,"lease_seq":"1","command":"synthetic"}`,
		`{"job_id":"10000000-0000-4000-8000-000000000001","attempt":1,"lease_seq":"1"} {}`,
	} {
		out := HeartbeatRequest{Attempt: 9}
		if DecodeStrict([]byte(input), &out) == nil || out.Attempt != 9 {
			t.Fatal("strict decoder accepted malformed input or partially assigned destination")
		}
	}
	var request HeartbeatRequest
	if DecodeStrict([]byte(`{"job_id":"10000000-0000-4000-8000-000000000001","attempt":1,"lease_seq":"9007199254740993"}`), &request) != nil || request.LeaseSeq != 9007199254740993 {
		t.Fatal("valid decimal sequence lost precision")
	}
	var arbitrary any
	if DecodeStrict([]byte(strings.Repeat("[", 40)+"0"+strings.Repeat("]", 40)), &arbitrary) == nil {
		t.Fatal("excessive nesting was accepted")
	}
}
func TestResultHashUsesOnlyCanonicalSafeOutcome(t *testing.T) {
	first := ResultRequest{State: "failed", Error: &SafeError{Code: "CORE_CONFIG_INVALID", Message: "synthetic-private-stderr", Details: []Detail{{FieldPath: "/synthetic-private-path"}}}}
	second := ResultRequest{State: "failed", Error: Safe("CORE_CONFIG_INVALID"), Attempt: 2, LeaseSeq: 99}
	a, err := ResultHash(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ResultHash(second)
	if err != nil || a != b {
		t.Fatal("untrusted runner text or attempt altered canonical outcome hash")
	}
	second.State = "succeeded"
	second.Verdict = "fail"
	b, err = ResultHash(second)
	if err != nil || a == b {
		t.Fatal("execution outcome was omitted from result hash")
	}
	nan := math.NaN()
	if ValidateConfigMetrics(Metrics{ConfigCheckMS: &nan}) == nil {
		t.Fatal("nonfinite measured metric accepted")
	}
	count := int64(1)
	if ValidateConfigMetrics(Metrics{BodyBytes: &count}) == nil {
		t.Fatal("offline validation accepted network accounting")
	}
}
func TestSensitivePayloadFormattingAndEmbeddedWireShape(t *testing.T) {
	lease := Lease{FrozenPayload: FrozenPayload{SchemaVersion: 1, Type: "config_validate", Artifact: Artifact{ContentBase64: "SYNTHETIC_PRIVATE_ARTIFACT"}}}
	for _, value := range []any{lease, lease.FrozenPayload, lease.Artifact} {
		for _, format := range []string{"%v", "%+v", "%#v"} {
			if strings.Contains(fmt.Sprintf(format, value), "SYNTHETIC_PRIVATE_ARTIFACT") {
				t.Fatal("formatting disclosed artifact")
			}
		}
	}
	data, err := json.Marshal(lease)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Lease
	if DecodeStrict(data, &decoded) != nil || decoded.Type != "config_validate" || decoded.Artifact.ContentBase64 != lease.Artifact.ContentBase64 {
		t.Fatal("embedded frozen payload failed strict round trip")
	}
}
