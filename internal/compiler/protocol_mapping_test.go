package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestP0ProtocolNativeGoldens(t *testing.T) {
	c := mustCompiler(t)
	for _, protocol := range []ir.Protocol{ir.Shadowsocks, ir.VMess, ir.VLESS, ir.Trojan, ir.SOCKS5, ir.HTTP} {
		transports := []ir.TransportKind{ir.NativeTCP}
		if protocol == ir.VMess || protocol == ir.VLESS || protocol == ir.Trojan {
			transports = append(transports, ir.WebSocket)
		}
		for _, transport := range transports {
			securities := []ir.SecurityMode{ir.SecurityNone}
			if protocol == ir.Trojan {
				securities = []ir.SecurityMode{ir.TLS}
			} else if protocol == ir.VMess || protocol == ir.VLESS || protocol == ir.HTTP {
				securities = append(securities, ir.TLS)
			}
			if protocol == ir.VLESS && transport == ir.NativeTCP {
				securities = append(securities, ir.Reality)
			}
			for _, security := range securities {
				name := string(protocol) + "-" + string(transport) + "-" + string(security)
				t.Run(name, func(t *testing.T) {
					spec := mustFrozen(t, "frozen-chain-a-b.json").Spec()
					spec.Resources = spec.Resources[:1]
					node := spec.Resources[0].Payload.(*ir.Node)
					node.Protocol = protocol
					node.Endpoint.Host = "192.0.2.10"
					switch protocol {
					case ir.Shadowsocks:
						node.Auth = &ir.MethodPasswordAuth{Kind: ir.AuthMethodPassword, Method: ir.AES128GCM, Password: "EXAMPLE_ONLY_SS"}
					case ir.VMess:
						node.Auth = &ir.VMessAuth{Kind: ir.AuthVMessAEAD, UUID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", Cipher: ir.VMessAuto}
					case ir.VLESS:
						node.Auth = &ir.UUIDAuth{Kind: ir.AuthUUID, UUID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd"}
					case ir.SOCKS5, ir.HTTP:
						node.Auth = &ir.UsernamePasswordAuth{Kind: ir.AuthUsernamePassword, Username: "EXAMPLE_ONLY_USER", Password: "EXAMPLE_ONLY_PASSWORD"}
					}
					if transport == ir.WebSocket {
						host := "ws.example.invalid"
						node.Transport = &ir.WebSocketTransport{Kind: ir.WebSocket, Path: "/socket", Host: &host}
					}
					switch security {
					case ir.SecurityNone:
						node.Security = &ir.NoSecurity{Mode: ir.SecurityNone}
					case ir.Reality:
						node.Security = &ir.RealitySecurity{Mode: ir.Reality, ServerName: "reality.example.invalid", PublicKey: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", ShortID: "0123abcd", ClientFingerprint: "chrome"}
						flow := ir.XTLSVision
						node.Features.ProtocolVariant = &flow
					}
					spec.Members = []ir.FrozenRef{{ResourceID: spec.Resources[0].Metadata.ResourceID, Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1}}
					input, err := ir.NewFrozenInput(spec)
					if err != nil {
						t.Fatal(err)
					}
					for _, target := range input.Spec().Targets {
						a, d, err := c.Compile(context.Background(), input, target)
						if err != nil {
							t.Fatal(err)
						}
						if !hasInfo(d, ir.CapabilityUnverified) {
							t.Fatal("native compile promoted verification")
						}
						ext := ".json"
						if target.CoreFamily == ir.Mihomo {
							ext = ".yaml"
						}
						checkNativeGolden(t, "protocol-"+name+"-"+string(target.CoreFamily)+ext, a.Bytes)
						if !bytes.Contains(a.Bytes, []byte("192.0.2.10")) {
							t.Fatal("node endpoint lost")
						}
						negative := input.Spec()
						enabled := true
						negative.Resources[0].Payload.(*ir.Node).Features.UDP = &enabled
						bad, err := ir.NewFrozenInput(negative)
						if err != nil {
							t.Fatal(err)
						}
						artifact, diagnostics, err := c.Compile(context.Background(), bad, target)
						if err == nil || len(artifact.Bytes) != 0 || len(diagnostics) != 1 || diagnostics[0].Code != ir.CompileUnmappedField || diagnostics[0].ResourceID != negative.Resources[0].Metadata.ResourceID || diagnostics[0].TargetKey != target.Key || diagnostics[0].FieldPath != "/independents/0/features/udp" {
							t.Fatal("unsupported UDP silently mapped or diagnostic lost identity")
						}
						encoded, err := json.MarshalIndent(diagnostics, "", "  ")
						if err != nil {
							t.Fatal(err)
						}
						checkNativeGolden(t, "negative/protocol-"+name+"-"+string(target.CoreFamily)+".json", append(encoded, '\n'))
					}
				})
			}
		}
	}
}

func TestRealityALPNFailsOnlyOnInexpressibleTarget(t *testing.T) {
	spec := mustFrozen(t, "frozen-chain-a-b.json").Spec()
	spec.Resources = spec.Resources[:1]
	node, err := ir.DecodeNode(readIR(t, "positive/vless-reality.json"))
	if err != nil {
		t.Fatal(err)
	}
	spec.Resources[0].Payload = &node
	spec.Members = []ir.FrozenRef{{ResourceID: spec.Resources[0].Metadata.ResourceID, Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1}}
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range input.Spec().Targets {
		a, d, err := mustCompiler(t).Compile(context.Background(), input, target)
		if target.CoreFamily == ir.Xray {
			if err == nil || len(a.Bytes) != 0 || len(d) != 1 || d[0].FieldPath != "/independents/0/security/alpn" {
				t.Fatal("Reality ALPN silently dropped")
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
}

func TestP0AdditionalAuthenticationAndCiphers(t *testing.T) {
	for _, variant := range []string{"socks5-noauth", "http-noauth", "http-tls-noauth", "shadowsocks-aes-256-gcm", "shadowsocks-chacha20-ietf-poly1305"} {
		t.Run(variant, func(t *testing.T) {
			spec := mustFrozen(t, "frozen-chain-a-b.json").Spec()
			spec.Resources = spec.Resources[:1]
			n := spec.Resources[0].Payload.(*ir.Node)
			n.Endpoint.Host = "192.0.2.10"
			n.Protocol = ir.HTTP
			n.Auth = &ir.NoAuth{Kind: ir.AuthNone}
			if variant != "http-tls-noauth" {
				n.Security = &ir.NoSecurity{Mode: ir.SecurityNone}
			}
			if variant == "socks5-noauth" {
				n.Protocol = ir.SOCKS5
			}
			if strings.HasPrefix(variant, "shadowsocks-") {
				n.Protocol = ir.Shadowsocks
				n.Auth = &ir.MethodPasswordAuth{Kind: ir.AuthMethodPassword, Method: ir.ShadowsocksMethod(strings.TrimPrefix(variant, "shadowsocks-")), Password: "EXAMPLE_ONLY_SS"}
			}
			spec.Members = []ir.FrozenRef{{ResourceID: spec.Resources[0].Metadata.ResourceID, Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1}}
			input, err := ir.NewFrozenInput(spec)
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range input.Spec().Targets {
				a, _, err := mustCompiler(t).Compile(context.Background(), input, target)
				if err != nil {
					t.Fatal(err)
				}
				ext := ".json"
				if target.CoreFamily == ir.Mihomo {
					ext = ".yaml"
				}
				checkNativeGolden(t, "protocol-"+variant+"-"+string(target.CoreFamily)+ext, a.Bytes)
			}
		})
	}
}
