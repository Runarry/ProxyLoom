package adapter

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestMapTrojanNativeTLSAcceptsPrototype(t *testing.T) {
	resource := fixtureResource(t, "11111111-1111-4111-8111-111111111111", "positive/trojan-a.json")
	mapped, err := MapTrojanNativeTLS(resource, "/independents/0", "xray-default")
	if err != nil {
		t.Fatal(err)
	}
	if mapped.Endpoint.Host != "a.example.invalid" || mapped.ServerName != "a.example.invalid" || !mapped.VerifyCert || !mapped.UDPFalse {
		t.Fatalf("mapping %+v", mapped)
	}
	if SecretBytes(mapped.Password) != "EXAMPLE_ONLY_A" {
		t.Fatal("password not taken from IR")
	}
	if strings.Contains(fmt.Sprintf("%v", mapped), "EXAMPLE_ONLY") {
		t.Fatal("mapped node leaked password")
	}
}

func TestMapTrojanNativeTLSRejectsUnmapped(t *testing.T) {
	resource := fixtureResource(t, "11111111-1111-4111-8111-111111111111", "positive/shadowsocks-aead.json")
	if _, err := MapTrojanNativeTLS(resource, "/independents/0", "xray-default"); !hasCode(err, ir.CompileUnmappedField) {
		t.Fatalf("shadowsocks: %v", err)
	}
	websocket := fixtureResource(t, "11111111-1111-4111-8111-111111111111", "positive/trojan-a.json")
	node := websocket.Payload.(*ir.Node)
	node.Transport = &ir.WebSocketTransport{Kind: ir.WebSocket, Path: "/path"}
	if _, err := MapTrojanNativeTLS(websocket, "/independents/0", "xray-default"); !hasCode(err, ir.CompileUnmappedField) {
		t.Fatalf("websocket: %v", err)
	}
	udp := fixtureResource(t, "11111111-1111-4111-8111-111111111111", "positive/trojan-a.json")
	enabled := true
	udp.Payload.(*ir.Node).Features.UDP = &enabled
	if _, err := MapTrojanNativeTLS(udp, "/independents/0", "xray-default"); !hasCode(err, ir.CompileUnmappedField) {
		t.Fatalf("udp: %v", err)
	}
}

func TestEmitInputLoggingRedactsSecrets(t *testing.T) {
	resource := fixtureResource(t, "11111111-1111-4111-8111-111111111111", "positive/trojan-a.json")
	input := EmitInput{
		SnapshotID: "ffffffff-ffff-4fff-8fff-ffffffffffff",
		TargetKey:  "xray-default",
		Independents: []IndependentOutbound{{
			Tag:      "n_deadbeef",
			Resource: resource,
		}},
	}
	if strings.Contains(fmt.Sprintf("%v", input), "EXAMPLE_ONLY") {
		t.Fatal("emit input format leaked")
	}
	var buf strings.Builder
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("emit", "input", input, "outbound", input.Independents[0])
	if strings.Contains(buf.String(), "EXAMPLE_ONLY") {
		t.Fatal("emit input log leaked")
	}
}

func TestReservedAndDuplicateTagsFailClosed(t *testing.T) {
	resource := fixtureResource(t, "11111111-1111-4111-8111-111111111111", "positive/trojan-a.json")
	input := EmitInput{TargetKey: "xray-default", Independents: []IndependentOutbound{{Tag: "direct", Resource: resource}}}
	if _, err := input.OutboundRefs(); !hasCode(err, ir.CompileLabelCollision) {
		t.Fatalf("reserved: %v", err)
	}
	dup := EmitInput{TargetKey: "xray-default", Independents: []IndependentOutbound{
		{Tag: "n_aaaa", Resource: resource},
		{Tag: "n_aaaa", Resource: resource},
	}}
	if _, err := dup.OutboundRefs(); !hasCode(err, ir.CompileLabelCollision) {
		t.Fatalf("duplicate: %v", err)
	}
}

func fixtureResource(t *testing.T, id ir.ID, name string) ir.Resource {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "ir", name))
	if err != nil {
		t.Fatal(err)
	}
	node, err := ir.DecodeNode(data)
	if err != nil {
		t.Fatal(err)
	}
	return ir.Resource{
		Metadata: ir.Metadata{
			ResourceID:    id,
			ScopeID:       "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
			Kind:          ir.KindNode,
			Revision:      1,
			SchemaVersion: 1,
			Name:          "fixture",
			Enabled:       true,
			SecurityEpoch: 1,
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
