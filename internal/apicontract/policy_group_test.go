package apicontract

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestPolicyDTOReplacementAndAtomicMembershipPatch(t *testing.T) {
	a := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: "11111111-1111-4111-8111-111111111111"}
	b := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindChain, ResourceID: "22222222-2222-4222-8222-222222222222"}
	request := PolicyGroupCreateRequest{Name: "selection", PolicyGroup: ir.PolicyGroup{SchemaVersion: 1, Strategy: ir.PolicyManualSelect,
		Members: []ir.TargetRef{a}, DefaultMember: a, HealthCheck: ir.PolicyHealthCheck{Enabled: false}, OnUnavailable: ir.FailClosed}}
	input, err := request.Input()
	if err != nil || !input.Enabled || input.Tags == nil {
		t.Fatal("policy DTO defaults failed")
	}
	old, err := catalog.New("10000000-0000-4000-8000-000000000001", input)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(old)
	members := []ir.TargetRef{b}
	patch := PolicyGroupPatchRequest{PolicyGroup: &PolicyGroupPatch{Members: &members}}
	if _, err := patch.Merge(old); err == nil || AsError(err).Code() != ValidationFailed {
		t.Fatal("patch removed the default member without replacing it")
	}
	patch.PolicyGroup.DefaultMember = &b
	merged, err := patch.Merge(old)
	after, _ := json.Marshal(old)
	if err != nil || !bytes.Equal(before, after) || merged.Payload.(*ir.PolicyGroup).DefaultMember != b || merged.Payload.(*ir.PolicyGroup).Strategy != ir.PolicyManualSelect {
		t.Fatal("atomic membership/default replacement mutated the base or strategy")
	}
	for _, body := range []string{`{}`, `{"policy_group":{}}`, `{"policy_group":null}`, `{"policy_group":{"members":null}}`, `{"policy_group":{"arbitrary_config":{}}}`} {
		var next PolicyGroupPatchRequest
		if Decode([]byte(body), "PolicyGroupPatchRequest", &next) == nil {
			t.Fatal("malformed policy patch accepted")
		}
	}
	request.PolicyGroup.DefaultMember = b
	encoded, _ := json.Marshal(request)
	var next PolicyGroupCreateRequest
	err = Decode(encoded, "PolicyGroupCreateRequest", &next)
	if err == nil || AsError(err).Code() != ValidationFailed || AsError(err).Response("test").Error.Details[0].FieldPath != "/policy_group/default_member" {
		t.Fatal("nonmember default did not produce a located semantic error")
	}
	request.PolicyGroup.DefaultMember = a
	request.PolicyGroup.HealthCheck.URL = "https://health.example.invalid:65536/check"
	encoded, _ = json.Marshal(request)
	err = Decode(encoded, "PolicyGroupCreateRequest", &next)
	if err == nil || AsError(err).Response("test").Error.Details[0].FieldPath != "/policy_group/health_check/url" {
		t.Fatal("policy create health error did not identify the nested URL field")
	}
	var invalidPatch PolicyGroupPatchRequest
	err = Decode([]byte(`{"policy_group":{"health_check":{"enabled":false,"url":"https://health.example.invalid:65536/check"}}}`), "PolicyGroupPatchRequest", &invalidPatch)
	if err == nil || AsError(err).Response("test").Error.Details[0].FieldPath != "/policy_group/health_check/url" {
		t.Fatal("policy patch health error did not identify the nested URL field")
	}
}
