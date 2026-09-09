package override

import (
	"slices"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func trojan(host string, password ir.Secret) ir.Node {
	verify := true
	return ir.Node{
		SchemaVersion: ir.SchemaVersion, Protocol: ir.Trojan,
		Endpoint:  ir.Endpoint{Host: host, Port: 443},
		Auth:      &ir.PasswordAuth{Kind: ir.AuthPassword, Password: password},
		Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP},
		Security:  &ir.TLSSecurity{Mode: ir.TLS, ServerName: host, VerifyCertificate: &verify},
	}
}

func TestMergeKeepsOverriddenNameAndEndpoint(t *testing.T) {
	base := trojan("source.example.invalid", "source-secret")
	name := "Custom"
	host := "override.example.invalid"
	patch := Document{SchemaVersion: 1, Name: &name, Node: &apicontract.NodePatch{Endpoint: &ir.Endpoint{Host: host, Port: 8443}}}
	node, effectiveName, fields, err := Merge(base, "Source", patch)
	if err != nil || effectiveName != "Custom" || node.Endpoint.Host != host || node.Endpoint.Port != 8443 {
		t.Fatalf("merge lost overlay: %v %s %#v", err, effectiveName, node.Endpoint)
	}
	if auth, ok := node.Auth.(*ir.PasswordAuth); !ok || auth.Password != "source-secret" {
		t.Fatal("merge replaced the source credential")
	}
	if !slices.Equal(fields, []string{"/endpoint", "/name"}) {
		t.Fatalf("overridden fields: %v", fields)
	}
}

func TestMergeRejectsProtocolOverride(t *testing.T) {
	base := trojan("source.example.invalid", "source-secret")
	protocol := ir.VLESS
	_, _, _, err := Merge(base, "Source", Document{Node: &apicontract.NodePatch{Protocol: &protocol}})
	if err == nil {
		t.Fatal("protocol overlay was accepted")
	}
}

func TestRestoreNameReturnsSourceValue(t *testing.T) {
	name := "Custom"
	document, err := Document{SchemaVersion: 1, Name: &name, Node: &apicontract.NodePatch{Endpoint: &ir.Endpoint{Host: "override.example.invalid", Port: 443}}}.Restore([]string{"/name"})
	if err != nil || document.Name != nil {
		t.Fatalf("name restore failed: %v %#v", err, document.Name)
	}
	node, effectiveName, fields, err := Merge(trojan("source.example.invalid", "source-secret"), "Source", document)
	if err != nil || effectiveName != "Source" || node.Endpoint.Host != "override.example.invalid" || !slices.Equal(fields, []string{"/endpoint"}) {
		t.Fatalf("partial restore: %v %s %v", err, effectiveName, fields)
	}
}

func TestCombineRejectsUnknownRestorePath(t *testing.T) {
	if _, err := Combine(Document{}, nil, nil, []string{"/native"}); err == nil {
		t.Fatal("unknown restore path was accepted")
	}
}

func TestEmptyCanonicalIsNil(t *testing.T) {
	plain, err := Document{SchemaVersion: 1}.Canonical()
	if err != nil || plain != nil {
		t.Fatalf("empty overlay must be omitted: %v %q", err, plain)
	}
}
