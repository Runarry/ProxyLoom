package importparse

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestEncodeURIRoundTripSixProtocols(t *testing.T) {
	s := syntheticValues(t)
	tls := true
	cases := []struct {
		name string
		node ir.Node
	}{
		{"ss-中文", ir.Node{SchemaVersion: 1, Protocol: ir.Shadowsocks, Endpoint: ir.Endpoint{Host: "ss.example.invalid", Port: 8388}, Auth: &ir.MethodPasswordAuth{Kind: ir.AuthMethodPassword, Method: ir.AES128GCM, Password: ir.Secret(s.password)}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}}},
		{"vmess", ir.Node{SchemaVersion: 1, Protocol: ir.VMess, Endpoint: ir.Endpoint{Host: "2001:db8::2", Port: 443}, Auth: &ir.VMessAuth{Kind: ir.AuthVMessAEAD, UUID: ir.Secret(s.uuid), Cipher: ir.VMessAuto}, Transport: &ir.WebSocketTransport{Kind: ir.WebSocket, Path: "/ws", Host: str("ws.example.invalid")}, Security: &ir.TLSSecurity{Mode: ir.TLS, ServerName: "tls.example.invalid", VerifyCertificate: &tls}}},
		{"vless", ir.Node{SchemaVersion: 1, Protocol: ir.VLESS, Endpoint: ir.Endpoint{Host: "vless.example.invalid", Port: 443}, Auth: &ir.UUIDAuth{Kind: ir.AuthUUID, UUID: ir.Secret(s.uuid)}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.TLSSecurity{Mode: ir.TLS, ServerName: "vless.example.invalid", VerifyCertificate: &tls}}},
		{"trojan", ir.Node{SchemaVersion: 1, Protocol: ir.Trojan, Endpoint: ir.Endpoint{Host: "trojan.example.invalid", Port: 443}, Auth: &ir.PasswordAuth{Kind: ir.AuthPassword, Password: ir.Secret(s.password)}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.TLSSecurity{Mode: ir.TLS, ServerName: "trojan.example.invalid", VerifyCertificate: &tls}}},
		{"socks", ir.Node{SchemaVersion: 1, Protocol: ir.SOCKS5, Endpoint: ir.Endpoint{Host: "socks.example.invalid", Port: 1080}, Auth: &ir.UsernamePasswordAuth{Kind: ir.AuthUsernamePassword, Username: ir.Secret(s.username), Password: ir.Secret(s.password)}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}}},
		{"http", ir.Node{SchemaVersion: 1, Protocol: ir.HTTP, Endpoint: ir.Endpoint{Host: "http.example.invalid", Port: 8080}, Auth: &ir.NoAuth{Kind: ir.AuthNone}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}}},
	}
	for _, test := range cases {
		uri, err := EncodeURI(test.name, test.node)
		if err != nil {
			t.Fatalf("%s encode: %v", test.name, err)
		}
		got := ParseURI(uri)
		if !got.Valid() {
			t.Fatalf("%s parse failed: %v %s", test.name, got.Diagnostics, uri)
		}
		if got.Node.Protocol != test.node.Protocol || got.Node.Endpoint != test.node.Endpoint {
			t.Fatalf("%s connection semantics changed: %#v vs %#v", test.name, got.Node.Endpoint, test.node.Endpoint)
		}
		if got.Name != test.name {
			t.Fatalf("%s name lost: %q", test.name, got.Name)
		}
		if !reflect.DeepEqual(got.Node.Auth, test.node.Auth) || !reflect.DeepEqual(got.Node.Transport, test.node.Transport) || !reflect.DeepEqual(got.Node.Security, test.node.Security) {
			t.Fatalf("%s authentication, transport or security changed", test.name)
		}
	}
}

func TestEncodeURIRejectsInsecureTLS(t *testing.T) {
	verify := false
	node := ir.Node{SchemaVersion: 1, Protocol: ir.Trojan, Endpoint: ir.Endpoint{Host: "trojan.example.invalid", Port: 443}, Auth: &ir.PasswordAuth{Kind: ir.AuthPassword, Password: "secret"}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.TLSSecurity{Mode: ir.TLS, ServerName: "trojan.example.invalid", VerifyCertificate: &verify}}
	_, err := EncodeURI("x", node)
	var diagnostic *ExportError
	if !errors.As(err, &diagnostic) || diagnostic.FieldPath != "/node/security/verify_certificate" {
		t.Fatal("insecure TLS URI was emitted")
	}
}

func TestEncodeURIPreservesExplicitFeaturePresence(t *testing.T) {
	disabled := false
	node := ir.Node{SchemaVersion: 1, Protocol: ir.HTTP, Endpoint: ir.Endpoint{Host: "export.example.invalid", Port: 8080},
		Auth: &ir.NoAuth{Kind: ir.AuthNone}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}}
	for _, field := range []string{"udp", "multiplex"} {
		node.Features = ir.Features{}
		if field == "udp" {
			node.Features.UDP = &disabled
		} else {
			node.Features.Multiplex = &disabled
		}
		_, err := EncodeURI("node", node)
		var diagnostic *ExportError
		if !errors.As(err, &diagnostic) || diagnostic.FieldPath != "/node/features/"+field {
			t.Fatal("explicit false was silently dropped")
		}
	}
}

func str(value string) *string { return &value }
