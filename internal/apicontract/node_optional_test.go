package apicontract_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func optionalFieldFixture(t *testing.T, fixture string) ir.Resource {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "ir", "positive", fixture+".json"))
	if err != nil {
		t.Fatal("optional field fixture unavailable")
	}
	node, err := ir.DecodeNode(data)
	if err != nil {
		t.Fatal("optional field fixture invalid")
	}
	return ir.Resource{Metadata: ir.Metadata{ResourceID: "11111111-1111-4111-8111-111111111111", ScopeID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		Kind: ir.KindNode, Revision: 1, SchemaVersion: 1, Name: "Optional field fixture", Tags: []string{}, Enabled: true, SecurityEpoch: 1}, Payload: &node}
}

func decodeOptionalPatch(t *testing.T, body string) apicontract.NodePatchRequest {
	t.Helper()
	var patch apicontract.NodePatchRequest
	if err := apicontract.Decode([]byte(body), "NodePatchRequest", &patch); err != nil {
		t.Fatalf("optional patch decoding failed with %s", apicontract.AsError(err).Code())
	}
	return patch
}

func optionalPatchStatus(t *testing.T, err error, status int) {
	t.Helper()
	if err == nil || apicontract.AsError(err).HTTPStatus() != status {
		t.Fatalf("optional patch should fail with HTTP %d", status)
	}
}

func TestTLSOptionalFieldsClearReplaceAndPreserve(t *testing.T) {
	for _, test := range []struct {
		name, fields, fingerprint string
		alpn                      []string
	}{
		{"absent_preserves", "", "chrome", []string{"h2"}},
		{"clear_both", `,"alpn":[],"client_fingerprint":null`, "", nil},
		{"clear_alpn_only", `,"alpn":[]`, "chrome", nil},
		{"clear_fingerprint_only", `,"client_fingerprint":null`, "", []string{"h2"}},
		{"replace_both", `,"alpn":["http/1.1"],"client_fingerprint":"firefox"`, "firefox", []string{"http/1.1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			old := optionalFieldFixture(t, "trojan-a")
			prior := old.Payload.(*ir.Node).Security.(*ir.TLSSecurity)
			fingerprint := "chrome"
			prior.ClientFingerprint, prior.ALPN = &fingerprint, []string{"h2"}
			original, err := json.Marshal(old)
			if err != nil {
				t.Fatal("original resource encoding failed")
			}
			patch := decodeOptionalPatch(t, `{"node":{"security":{"mode":"tls"`+test.fields+`}}}`)
			input, err := patch.Merge(old)
			if err != nil {
				t.Fatalf("optional merge failed with %s", apicontract.AsError(err).Code())
			}
			next, err := catalog.Apply(old, input)
			if err != nil {
				t.Fatal("optional update failed catalog validation")
			}
			security := next.Payload.(*ir.Node).Security.(*ir.TLSSecurity)
			if !reflect.DeepEqual(security.ALPN, test.alpn) {
				t.Fatal("optional ALPN did not reach the expected canonical state")
			}
			if test.fingerprint == "" {
				if security.ClientFingerprint != nil {
					t.Fatal("cleared TLS fingerprint was not omitted")
				}
			} else if security.ClientFingerprint == nil || *security.ClientFingerprint != test.fingerprint {
				t.Fatal("TLS fingerprint replacement/preservation failed")
			}
			if next.Metadata.ResourceID != old.Metadata.ResourceID || next.Metadata.Revision != 2 || next.Metadata.SecurityEpoch != 1 || !reflect.DeepEqual(next.Payload.(*ir.Node).Auth, old.Payload.(*ir.Node).Auth) {
				t.Fatal("optional edit changed identity, authentication or security epoch")
			}
			after, err := json.Marshal(old)
			if err != nil || !bytes.Equal(original, after) {
				t.Fatal("optional edit changed the original immutable snapshot")
			}
			read, err := apicontract.NewNodeReadResponse("optional-read", next)
			if err != nil || apicontract.ValidateDTO("NodeReadResponse", read) != nil {
				t.Fatal("optional omission invalidated the ordinary read response")
			}
			wire, err := json.Marshal(read.Data.Node.Security)
			if err != nil {
				t.Fatal("security read encoding failed")
			}
			if test.alpn == nil && bytes.Contains(wire, []byte(`"alpn"`)) {
				t.Fatal("cleared ALPN persisted in the read representation")
			}
			if test.fingerprint == "" && bytes.Contains(wire, []byte(`"client_fingerprint"`)) {
				t.Fatal("cleared fingerprint persisted in the read representation")
			}
		})
	}
}

func TestOptionalClearSurvivesTypedImportOverride(t *testing.T) {
	for _, test := range []struct {
		name, fields string
		clear        bool
	}{
		{"absent", "", false},
		{"explicit_clear", `,"alpn":[],"client_fingerprint":null`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			patch := decodeOptionalPatch(t, `{"node":{"security":{"mode":"tls"`+test.fields+`}}}`)
			decision := imports.Decision{CandidateID: "22222222-2222-4222-8222-222222222222", Action: "create", Override: &patch}
			encoded, err := json.Marshal(decision)
			if err != nil {
				t.Fatal("typed import override encoding failed")
			}
			if bytes.Contains(encoded, []byte(`"client_fingerprint":null`)) != test.clear || bytes.Contains(encoded, []byte(`"alpn":[]`)) != test.clear {
				t.Fatal("typed override lost omitted versus explicit clear fields")
			}
			var decoded imports.Decision
			if json.Unmarshal(encoded, &decoded) != nil || decoded.Override == nil {
				t.Fatal("typed import override round trip failed")
			}
			if decoded.Override.Node.Security.ClientFingerprint.Present() != test.clear || decoded.Override.Node.Security.ClientFingerprint.IsNull() != test.clear {
				t.Fatal("fingerprint three-state round trip failed")
			}
			if apicontract.ValidateDTO("NodePatchRequest", decoded.Override) != nil {
				t.Fatal("round-trip override no longer matched its contract")
			}
			old := optionalFieldFixture(t, "trojan-a")
			prior := old.Payload.(*ir.Node).Security.(*ir.TLSSecurity)
			fingerprint := "chrome"
			prior.ClientFingerprint, prior.ALPN = &fingerprint, []string{"h2"}
			input, err := decoded.Override.Merge(old)
			if err != nil {
				t.Fatal("round-trip override merge failed")
			}
			security := input.Payload.(*ir.Node).Security.(*ir.TLSSecurity)
			if test.clear && (security.ClientFingerprint != nil || len(security.ALPN) != 0) {
				t.Fatal("import override did not clear optional TLS settings")
			}
			if !test.clear && (security.ClientFingerprint == nil || *security.ClientFingerprint != fingerprint || len(security.ALPN) != 1) {
				t.Fatal("omitted import override changed optional TLS settings")
			}
		})
	}
}

func TestOptionalPatchKeepsRealityFingerprintRequired(t *testing.T) {
	old := optionalFieldFixture(t, "vless-reality")
	prior := old.Payload.(*ir.Node).Security.(*ir.RealitySecurity)
	prior.ALPN = []string{"h2"}
	clearFingerprint := decodeOptionalPatch(t, `{"node":{"security":{"mode":"reality","client_fingerprint":null}}}`)
	_, err := clearFingerprint.Merge(old)
	optionalPatchStatus(t, err, 422)
	clearALPN := decodeOptionalPatch(t, `{"node":{"security":{"mode":"reality","alpn":[]}}}`)
	input, err := clearALPN.Merge(old)
	if err != nil {
		t.Fatal("optional REALITY ALPN clear failed")
	}
	security := input.Payload.(*ir.Node).Security.(*ir.RealitySecurity)
	if len(security.ALPN) != 0 || security.ClientFingerprint != prior.ClientFingerprint || security.PublicKey != prior.PublicKey || security.ShortID != prior.ShortID {
		t.Fatal("REALITY ALPN clear changed its required fields")
	}
	if len(prior.ALPN) != 1 {
		t.Fatal("REALITY optional clear modified the original snapshot")
	}
}

func TestOptionalPatchRejectsInvalidValuesAndPreservesCreateContract(t *testing.T) {
	for _, field := range []string{`"alpn":null`, `"alpn":"h2"`, `"client_fingerprint":""`, `"client_fingerprint":42`, `"client_fingerprint":false`, `"client_fingerprint":[]`, `"client_fingerprint":{}`} {
		var patch apicontract.NodePatchRequest
		err := apicontract.Decode([]byte(`{"node":{"security":{"mode":"tls",`+field+`}}}`), "NodePatchRequest", &patch)
		optionalPatchStatus(t, err, 400)
	}
	for _, security := range []string{
		`{"mode":"tls","server_name":"example.com","verify_certificate":true,"alpn":[]}`,
		`{"mode":"tls","server_name":"example.com","verify_certificate":true,"client_fingerprint":null}`,
	} {
		var input apicontract.SecurityInput
		optionalPatchStatus(t, apicontract.Decode([]byte(security), "TLSSecurity", &input), 400)
	}
	patch := apicontract.NodePatch{Security: &apicontract.SecurityPatch{Mode: ir.TLS, ClientFingerprint: apicontract.ReplaceOptionalString(string([]byte{0xff}))}}
	optionalPatchStatus(t, apicontract.ValidateDTO("NodePatch", patch), 400)
	clear := apicontract.SecurityPatch{Mode: ir.TLS, ClientFingerprint: apicontract.ClearOptionalString()}
	encoded, err := json.Marshal(clear)
	if err != nil || !bytes.Contains(encoded, []byte(`"client_fingerprint":null`)) {
		t.Fatal("direct Go clear construction lost its explicit null")
	}
}
