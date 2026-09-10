package apicontract

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestDNSPatchPreservesSourceAndIndexedSemanticErrors(t *testing.T) {
	const valid = `{"name":"DNS","dns_profile":{"schema_version":1,"bootstrap":[{"resolver_id":"bootstrap","kind":"local"}],"resolvers":[{"resolver_id":"local","kind":"local"},{"resolver_id":"https","kind":"https","url":"https://dns.example.invalid/query","bootstrap_resolver_id":"bootstrap","outbound":{"type":"builtin","builtin":"direct"}}],"rules":[{"match":{"domain_suffix":["example.invalid"]},"resolver_id":"https","enabled":true,"comment":"first"}],"final_resolver":"https"}}`
	var request DNSProfileCreateRequest
	if err := Decode([]byte(valid), "DNSProfileCreateRequest", &request); err != nil {
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
	before, _ := json.Marshal(old)
	var patch DNSProfilePatchRequest
	if err := Decode([]byte(`{"dns_profile":{"rules":[],"final_resolver":"local"}}`), "DNSProfilePatchRequest", &patch); err != nil {
		t.Fatal(err)
	}
	merged, err := patch.Merge(old)
	if err != nil {
		t.Fatal(err)
	}
	updated := merged.Payload.(*ir.DNSProfile)
	updated.Resolvers[1].Outbound.Builtin = ir.Reject
	after, _ := json.Marshal(old)
	if !bytes.Equal(before, after) || len(updated.Rules) != 0 || updated.FinalResolver != "local" {
		t.Fatal("DNS merge aliased or changed original profile")
	}
	for _, test := range []struct{ body, path string }{
		{`{"dns_profile":{"resolvers":[{"resolver_id":"local","kind":"local"},{"resolver_id":"https","kind":"https","url":"https://dns.example.invalid:70000/query","bootstrap_resolver_id":"bootstrap","outbound":{"type":"builtin","builtin":"direct"}}]}}`, "/dns_profile/resolvers/1/url"},
		{`{"dns_profile":{"final_resolver":"missing"}}`, "/dns_profile/final_resolver"},
		{`{"dns_profile":{"bootstrap":[{"resolver_id":"other","kind":"local"}]}}`, "/dns_profile/resolvers/1/bootstrap_resolver_id"},
	} {
		var invalid DNSProfilePatchRequest
		err := Decode([]byte(test.body), "DNSProfilePatchRequest", &invalid)
		if err == nil {
			_, err = invalid.Merge(old)
		}
		if err == nil || AsError(err).Code() != ValidationFailed {
			t.Fatalf("DNS patch error for %s = %v (%s)", test.path, err, AsError(err).Code())
		}
		details := AsError(err).Response("test").Error.Details
		if len(details) == 0 || details[0].FieldPath != test.path {
			t.Fatalf("DNS semantic error path = %v, want %s", details, test.path)
		}
		encoded, _ := json.Marshal(AsError(err).Response("test"))
		if bytes.Contains(encoded, []byte("password")) || bytes.Contains(encoded, []byte("dns.example.invalid")) {
			t.Fatal("DNS error echoed resolver data")
		}
	}
}
