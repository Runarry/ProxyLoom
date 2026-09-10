package apicontract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestRoutingDTOReplacementAndIndexedDiagnostics(t *testing.T) {
	const valid = `{"name":"routing","routing_profile":{"schema_version":1,"rules":[{"match":{"domain_suffix":["example.invalid"]},"action":{"type":"builtin","builtin":"reject"},"enabled":true,"comment":"first"}],"final":{"type":"builtin","builtin":"direct"},"domain_resolution_mode":"preserve_domain"}}`
	var request RoutingProfileCreateRequest
	if err := Decode([]byte(valid), "RoutingProfileCreateRequest", &request); err != nil {
		t.Fatal(err)
	}
	input, err := request.Input()
	if err != nil || !input.Enabled || input.Tags == nil {
		t.Fatal("routing defaults invalid", err)
	}
	old, err := catalog.New("10000000-0000-4000-8000-000000000001", input)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(old)
	var patch RoutingProfilePatchRequest
	if err := Decode([]byte(`{"routing_profile":{"rules":[]}}`), "RoutingProfilePatchRequest", &patch); err != nil {
		t.Fatal(err)
	}
	merged, err := patch.Merge(old)
	after, _ := json.Marshal(old)
	if err != nil || !bytes.Equal(before, after) || len(merged.Payload.(*ir.RoutingProfile).Rules) != 0 || merged.Payload.(*ir.RoutingProfile).Final.Builtin != ir.Direct {
		t.Fatal("replacement changed the base or implicit final", err)
	}
	for _, test := range []struct{ body, path string }{
		{`{"routing_profile":{"rules":[{"match":{"destination_ports":[{"from":443,"to":80}]},"action":{"type":"builtin","builtin":"reject"},"enabled":true,"comment":""}]}}`, "/routing_profile/rules/0/match/destination_ports/0/to"},
		{`{"routing_profile":{"rules":[{"match":{"ip_cidrs":["192.0.2.1/24"]},"action":{"type":"builtin","builtin":"reject"},"enabled":true,"comment":""}]}}`, "/routing_profile/rules/0/match/ip_cidrs/0"},
	} {
		var bad RoutingProfilePatchRequest
		err := Decode([]byte(test.body), "RoutingProfilePatchRequest", &bad)
		if err == nil || AsError(err).Code() != ValidationFailed || AsError(err).Response("test").Error.Details[0].FieldPath != test.path {
			t.Fatalf("nested semantic path missing: %v", err)
		}
	}
	for _, body := range []string{`{}`, `{"routing_profile":null}`, `{"routing_profile":{"rules":null}}`, strings.Replace(valid, `"domain_suffix":["example.invalid"]`, `"domain_suffix":[]`, 1), strings.Replace(valid, `,"final":{"type":"builtin","builtin":"direct"}`, ``, 1)} {
		var target any
		schema := "RoutingProfilePatchRequest"
		if strings.Contains(body, `"name"`) {
			schema = "RoutingProfileCreateRequest"
		}
		if Decode([]byte(body), schema, &target) == nil {
			t.Fatal("invalid routing shape accepted")
		}
	}
}

func TestRuleSetDTOHashHistoryAndSafeEntryPointers(t *testing.T) {
	const valid = `{"name":"rules","rule_set":{"schema_version":1,"format":"domain_cidr_text","entries":[{"kind":"domain","domain":"example.invalid","match":"suffix"},{"kind":"cidr","cidr":"192.0.2.0/24"}]}}`
	var request RuleSetCreateRequest
	if err := Decode([]byte(valid), "RuleSetCreateRequest", &request); err != nil {
		t.Fatal(err)
	}
	input, err := request.Input()
	if err != nil {
		t.Fatal(err)
	}
	old, err := catalog.New("10000000-0000-4000-8000-000000000001", input)
	if err != nil {
		t.Fatal(err)
	}
	var patch RuleSetPatchRequest
	if err := Decode([]byte(`{"rule_set":{"entries":[{"kind":"cidr","cidr":"2001:db8::/32"}]}}`), "RuleSetPatchRequest", &patch); err != nil {
		t.Fatal(err)
	}
	merged, err := patch.Merge(old)
	if err != nil || merged.Payload.(*ir.RuleSet).ContentHash == old.Payload.(*ir.RuleSet).ContentHash || len(old.Payload.(*ir.RuleSet).Entries) != 2 {
		t.Fatal("rule replacement changed history or did not update hash", err)
	}
	for _, schema := range []string{"RuleSetCreateRequest", "RuleSetPatchRequest"} {
		body := strings.Replace(valid, "192.0.2.0/24", "192.0.2.1/24", 1)
		var err error
		if schema == "RuleSetCreateRequest" {
			var bad RuleSetCreateRequest
			err = Decode([]byte(body), schema, &bad)
		} else {
			var bad RuleSetPatchRequest
			err = Decode([]byte(`{"rule_set":{"entries":[{"kind":"domain","domain":"example.invalid","match":"exact"},{"kind":"cidr","cidr":"192.0.2.1/24"}]}}`), schema, &bad)
		}
		if err == nil || AsError(err).Code() != ValidationFailed || !strings.HasPrefix(AsError(err).Response("test").Error.Details[0].FieldPath, "/rule_set/entries/1") {
			t.Fatal("invalid entry did not identify its safe index", err)
		}
		encoded, _ := json.Marshal(AsError(err).Response("test"))
		if bytes.Contains(encoded, []byte("192.0.2.1")) {
			t.Fatal("validation echoed rule entry")
		}
	}
	for _, body := range []string{strings.Replace(valid, `"format":"domain_cidr_text"`, `"format":"domain_cidr_text","content_hash":"`+strings.Repeat("0", 64)+`"`, 1), strings.Replace(valid, `{"kind":"cidr","cidr":"192.0.2.0/24"}`, `{"kind":"cidr","cidr":"192.0.2.0/24","domain":"example.invalid"}`, 1)} {
		var bad RuleSetCreateRequest
		if Decode([]byte(body), "RuleSetCreateRequest", &bad) == nil {
			t.Fatal("write accepted a client hash or mixed union")
		}
	}
}
