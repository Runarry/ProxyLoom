package xray

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestEmitChainUsesDialerProxyNotProxySettings(t *testing.T) {
	input := chainInput(t)
	artifact, _, err := Emit(input)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.ContentType != ContentType {
		t.Fatal(artifact.ContentType)
	}
	if bytes.Contains(artifact.Bytes, []byte("proxySettings")) || bytes.Contains(artifact.Bytes, []byte("freedom")) {
		t.Fatal("xray used proxySettings or implicit freedom")
	}
	if !bytes.Contains(artifact.Bytes, []byte(`"dialerProxy":"c_h1"`)) {
		t.Fatal("missing dialerProxy on chain exit")
	}
	var doc map[string]any
	if err := json.Unmarshal(artifact.Bytes, &doc); err != nil {
		t.Fatal(err)
	}
	outbounds := doc["outbounds"].([]any)
	first := outbounds[0].(map[string]any)
	if first["tag"] != "c_h2" {
		t.Fatalf("exit must be first outbound, got %v", first["tag"])
	}
	var h1, h2 map[string]any
	for _, raw := range outbounds {
		item := raw.(map[string]any)
		switch item["tag"] {
		case "c_h1":
			h1 = item
		case "c_h2":
			h2 = item
		}
	}
	if h1["streamSettings"].(map[string]any)["sockopt"] != nil {
		t.Fatal("h1 received a dialer")
	}
	sockopt := h2["streamSettings"].(map[string]any)["sockopt"].(map[string]any)
	if sockopt["dialerProxy"] != "c_h1" {
		t.Fatal("h2 must dial h1")
	}
	tls := h2["streamSettings"].(map[string]any)["tlsSettings"].(map[string]any)
	if tls["allowInsecure"] != false {
		t.Fatal("certificate verification was disabled")
	}
}

func TestEmitIndependentHasNoDialer(t *testing.T) {
	artifact, _, err := Emit(independentInput(t, "n_a", "positive/trojan-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(artifact.Bytes, []byte("dialerProxy")) {
		t.Fatal("independent outbound gained a chain dialer")
	}
}

func TestEmitRejectsUnmappedProtocol(t *testing.T) {
	if _, _, err := Emit(independentInput(t, "n_ss", "positive/shadowsocks-aead.json")); !hasCode(err, ir.CompileUnmappedField) {
		t.Fatalf("shadowsocks: %v", err)
	}
}

func chainInput(t *testing.T) adapter.EmitInput {
	t.Helper()
	return adapter.EmitInput{
		SnapshotID: "ffffffff-ffff-4fff-8fff-ffffffffffff",
		TargetKey:  "xray-default",
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
		TargetKey:  "xray-default",
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
