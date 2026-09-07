package mihomo

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"gopkg.in/yaml.v3"
)

func TestEmitChainUsesDialerProxyAndDisablesDownloads(t *testing.T) {
	artifact, _, err := Emit(chainInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if artifact.ContentType != ContentType {
		t.Fatal(artifact.ContentType)
	}
	if bytes.Contains(artifact.Bytes, []byte("dialerProxy")) || bytes.Contains(artifact.Bytes, []byte(`"detour"`)) {
		t.Fatal("mihomo emitted another family's dialer field")
	}
	var doc document
	if err := yaml.Unmarshal(artifact.Bytes, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Mode != "rule" || doc.GeoAutoUpdate || doc.GeodataMode || doc.AllowLAN || doc.ExternalController != "" {
		t.Fatalf("unsafe defaults %+v", doc)
	}
	if implicitDirect(doc) {
		t.Fatal("implicit DIRECT survived")
	}
	if len(doc.Rules) != 1 || doc.Rules[0] != "MATCH,c_h2" {
		t.Fatalf("rules %v", doc.Rules)
	}
	var h1, h2 *proxy
	for i := range doc.Proxies {
		switch doc.Proxies[i].Name {
		case "c_h1":
			h1 = &doc.Proxies[i]
		case "c_h2":
			h2 = &doc.Proxies[i]
		}
	}
	if h1 == nil || h2 == nil {
		t.Fatal("missing chain proxies")
	}
	if h1.DialerProxy != "" {
		t.Fatal("h1 received dialer-proxy")
	}
	if h2.DialerProxy != "c_h1" {
		t.Fatal("h2 must dial h1")
	}
	if h1.SkipCertVerify || h2.SkipCertVerify {
		t.Fatal("certificate verification was disabled")
	}
	if h1.Name == "Trojan A" || h2.Name == "Trojan B" {
		t.Fatal("used display names as proxy names")
	}
}

func TestEmitIndependentHasNoDialerAndNoDirect(t *testing.T) {
	artifact, _, err := Emit(independentInput(t, "n_a", "positive/trojan-a.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(artifact.Bytes, []byte("dialer-proxy")) {
		t.Fatal("independent proxy gained dialer-proxy")
	}
	text := string(artifact.Bytes)
	if strings.Contains(strings.ToUpper(text), ",DIRECT") || strings.Contains(text, "mode: direct") {
		t.Fatal("independent config selected DIRECT")
	}
}

func TestEmitRejectsUnmappedProtocol(t *testing.T) {
	if _, _, err := Emit(independentInput(t, "n_ss", "positive/shadowsocks-aead.json")); !hasCode(err, ir.CompileUnmappedField) {
		t.Fatalf("shadowsocks: %v", err)
	}
}

func TestImplicitDirectGuard(t *testing.T) {
	doc := document{Mode: "rule", Rules: []string{"MATCH,DIRECT"}}
	if !implicitDirect(doc) {
		t.Fatal("MATCH,DIRECT accepted")
	}
	doc.Rules = []string{"MATCH,c_h2"}
	if implicitDirect(doc) {
		t.Fatal("valid MATCH rejected")
	}
}

func chainInput(t *testing.T) adapter.EmitInput {
	t.Helper()
	return adapter.EmitInput{
		SnapshotID: "ffffffff-ffff-4fff-8fff-ffffffffffff",
		TargetKey:  "mihomo-default",
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
		TargetKey:  "mihomo-default",
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
