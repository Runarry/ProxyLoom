package compiler

import (
	_ "embed"
	"encoding/json"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"slices"
	"strings"
)

//go:embed publication-evidence.json
var publicationEvidenceJSON []byte

// The evidence permits variable endpoints/credentials, but fixes protocol,
// transport, security and mapping implementation. Compile still rejects every
// unmapped parameter and each publication validates its own exact final bytes.
func matchPublicationEvidence(graph Graph) error {
	var evidence struct {
		VMessCiphers   []ir.VMessCipher `json:"vmess_ciphers"`
		AdapterVersion string           `json:"adapter_version"`
		Builds         []struct {
			ID       ir.ID    `json:"build_id"`
			SHA      string   `json:"build_sha256"`
			Fixtures []string `json:"fixtures"`
		} `json:"builds"`
	}
	fail := func(id ir.ID, path string) error {
		return ir.Diagnostics{compileIssue(ir.CapabilityUnverified, path, graph.Target.Key, id)}
	}
	if json.Unmarshal(publicationEvidenceJSON, &evidence) != nil || evidence.AdapterVersion != graph.Target.AdapterVersion {
		return fail(graph.Target.ClientPresetID, "/adapter_version")
	}
	var fixtures []string
	for _, b := range evidence.Builds {
		if b.ID == graph.Target.CoreBuildID && b.SHA == graph.Target.CoreBuildSHA256 {
			fixtures = b.Fixtures
		}
	}
	if len(fixtures) == 0 {
		return fail(graph.Target.ClientPresetID, "/core_build_id")
	}
	check := func(r ir.Resource) error {
		n := r.Payload.(*ir.Node)
		transport := "native_tcp"
		if _, ok := n.Transport.(*ir.WebSocketTransport); ok {
			transport = "websocket"
		}
		security := "none"
		switch n.Security.(type) {
		case *ir.TLSSecurity:
			security = "tls"
		case *ir.RealitySecurity:
			security = "reality"
		}
		prefix := "protocol-" + string(n.Protocol) + "-" + transport + "-" + security + "-" + string(graph.Target.CoreFamily) + "."
		found := false
		for _, name := range fixtures {
			if strings.HasPrefix(name, prefix) {
				found = true
			}
		}
		if !found {
			return fail(r.Metadata.ResourceID, "/payload/protocol")
		}
		if auth, ok := n.Auth.(*ir.MethodPasswordAuth); ok && auth.Method != ir.AES128GCM {
			name := "protocol-shadowsocks-" + string(auth.Method) + "-" + string(graph.Target.CoreFamily) + "."
			found = false
			for _, f := range fixtures {
				if strings.HasPrefix(f, name) {
					found = true
				}
			}
			if !found {
				return fail(r.Metadata.ResourceID, "/payload/auth/method")
			}
		}
		if auth, ok := n.Auth.(*ir.VMessAuth); ok && !slices.Contains(evidence.VMessCiphers, auth.Cipher) {
			return fail(r.Metadata.ResourceID, "/payload/auth/cipher")
		}
		return nil
	}
	for _, n := range graph.Independents {
		if err := check(n.Resource); err != nil {
			return err
		}
	}
	for _, chain := range graph.Chains {
		for _, hop := range []ir.Resource{chain.Hop1, chain.Hop2} {
			node := hop.Payload.(*ir.Node)
			_, tcp := node.Transport.(*ir.NativeTCPTransport)
			_, tls := node.Security.(*ir.TLSSecurity)
			if node.Protocol != ir.Trojan || !tcp || !tls {
				return fail(hop.Metadata.ResourceID, "/payload/chain_position")
			}
		}

		if err := check(chain.Hop1); err != nil {
			return err
		}
		if err := check(chain.Hop2); err != nil {
			return err
		}
	}
	// P0's compiler already enforces family-specific strategy, DNS and route
	// constraints. This guard additionally requires the full orchestration suite.
	ext := ".json"
	if graph.Target.CoreFamily == ir.Mihomo {
		ext = ".yaml"
	}
	if !slices.Contains(fixtures, "orchestration-"+string(graph.Target.CoreFamily)+ext) {
		return fail(graph.Target.ClientPresetID, "/client_preset_id")
	}
	return nil
}
