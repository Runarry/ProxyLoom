package singbox

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestEmitChainUsesDetour(t *testing.T) {
	artifact, _, err := Emit(chainInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(artifact.Bytes, []byte("dialerProxy")) || bytes.Contains(artifact.Bytes, []byte("dialer-proxy")) {
		t.Fatal("sing-box emitted another family's dialer field")
	}
	var doc map[string]any
	if err := json.Unmarshal(artifact.Bytes, &doc); err != nil {
		t.Fatal(err)
	}
	route := doc["route"].(map[string]any)
	if route["final"] != "c_h2" {
		t.Fatalf("final %v", route["final"])
	}
	var h1, h2 map[string]any
	for _, raw := range doc["outbounds"].([]any) {
		item := raw.(map[string]any)
		switch item["tag"] {
		case "c_h1":
			h1 = item
		case "c_h2":
			h2 = item
		}
	}
	if _, ok := h1["detour"]; ok {
		t.Fatal("h1 received detour")
	}
	if h2["detour"] != "c_h1" {
		t.Fatal("h2 must detour via h1")
	}
	if DialFieldConflict(outbound{Type: "trojan", Tag: "c_h2", Detour: "c_h1"}) != "" {
		t.Fatal("clean detour reported a conflict")
	}
}

func TestDialFieldConflictRejectsIgnoredDialOptions(t *testing.T) {
	field := MapDialFieldConflict(map[string]any{"detour": "c_h1", "bind_interface": "eth0"})
	if field != "bind_interface" {
		t.Fatalf("conflict %q", field)
	}
	if MapDialFieldConflict(map[string]any{"detour": "c_h1", "server": "b.example.invalid"}) != "" {
		t.Fatal("server is not a dial field")
	}
}

func TestEmitRejectsUnmappedAndKeepsVerify(t *testing.T) {
	if _, _, err := Emit(independentInput(t, "n_ss", "positive/shadowsocks-aead.json")); !hasCode(err, ir.CompileUnmappedField) {
		t.Fatalf("shadowsocks: %v", err)
	}
	artifact, _, err := Emit(independentInput(t, "n_a", "positive/trojan-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(artifact.Bytes, []byte(`"insecure":true`)) {
		t.Fatal("certificate verification was disabled")
	}
}

func chainInput(t *testing.T) adapter.EmitInput {
	t.Helper()
	return adapter.EmitInput{
		SnapshotID: "ffffffff-ffff-4fff-8fff-ffffffffffff",
		TargetKey:  "singbox-default",
		Chains: []adapter.ChainInstance{{
			ResourceID: "33333333-3333-4333-8333-333333333333",
			Revision:   1,
			TagH1:      "c_h1",
			TagH2:      "c_h2",
			Hop1:       loadResource(t, "11111111-1111-4111-8111-111111111111", "positive/trojan-a.json"),
			Hop2:       loadResource(t, "22222222-2222-4222-8222-222222222222", "positive/trojan-b.json"),
		}},
	}
}

func independentInput(t *testing.T, tag, fixture string) adapter.EmitInput {
	t.Helper()
	return adapter.EmitInput{
		SnapshotID: "ffffffff-ffff-4fff-8fff-ffffffffffff",
		TargetKey:  "singbox-default",
		Independents: []adapter.IndependentOutbound{{
			Tag:      tag,
			Resource: loadResource(t, "11111111-1111-4111-8111-111111111111", fixture),
		}},
	}
}

func loadResource(t *testing.T, id ir.ID, name string) ir.Resource {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "fixtures", "ir", name))
	if err != nil {
		t.Fatal(err)
	}
	node, err := ir.DecodeNode(data)
	if err != nil {
		t.Fatal(err)
	}
	return ir.Resource{
		Metadata: ir.Metadata{
			ResourceID: id, ScopeID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", Kind: ir.KindNode,
			Revision: 1, SchemaVersion: 1, Name: "fixture", Enabled: true, SecurityEpoch: 1,
		},
		Payload: &node,
	}
}

func hasCode(err error, code ir.DiagnosticCode) bool {
	diags, ok := err.(ir.Diagnostics)
	if !ok {
		return false
	}
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}
