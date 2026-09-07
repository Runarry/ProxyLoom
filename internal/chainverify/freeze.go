// Package chainverify builds frozen IR for T-028 live-chain tests.
package chainverify

import (
	"fmt"

	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const (
	idScope   ir.ID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	idSnap    ir.ID = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	idNodeA   ir.ID = "11111111-1111-4111-8111-111111111111"
	idNodeB   ir.ID = "22222222-2222-4222-8222-222222222222"
	idNodeC   ir.ID = "44444444-4444-4444-8444-444444444444"
	idNodeD   ir.ID = "55555555-5555-4555-8555-555555555555"
	idChainAB ir.ID = "33333333-3333-4333-8333-333333333333"
	idChainAC ir.ID = "66666666-6666-4666-8666-666666666666"
	idChainDB ir.ID = "77777777-aaaa-4aaa-8aaa-aaaaaaaaaaa1"
	idChainBA ir.ID = "88888888-bbbb-4bbb-8bbb-bbbbbbbbbbb1"
	idPresetX ir.ID = "77777777-7777-4777-8777-777777777777"
	idPresetS ir.ID = "88888888-8888-4888-8888-888888888888"
	idPresetM ir.ID = "99999999-9999-4999-8999-999999999999"
)

type Endpoint struct {
	Host     string
	Port     int
	SNI      string
	Password string
	Name     string
}

func catalogTargets(catalog *capability.Catalog) ([]ir.Target, error) {
	type item struct {
		key     string
		family  ir.CoreFamily
		version string
		format  ir.OutputFormat
		preset  ir.ID
	}
	items := []item{
		{"xray-default", ir.Xray, "26.3.27", ir.XrayJSON, idPresetX},
		{"singbox-default", ir.SingBox, "1.14.0", ir.SingBoxJSON, idPresetS},
		{"mihomo-default", ir.Mihomo, "1.19.30", ir.MihomoYAML, idPresetM},
	}
	targets := make([]ir.Target, 0, len(items))
	for _, item := range items {
		build, err := catalog.Lookup(item.family, item.version, "linux", "amd64")
		if err != nil {
			return nil, err
		}
		targets = append(targets, ir.Target{
			Key: item.key, CoreFamily: item.family, CoreBuildID: build.ID,
			CoreBuildSHA256: build.BinarySHA256, AdapterVersion: catalog.AdapterVersion,
			ClientPresetID: item.preset, ClientPresetRevision: 1, Format: item.format,
		})
	}
	return targets, nil
}

func freezeChain(catalog *capability.Catalog, first, second Endpoint, firstID, secondID, chainID ir.ID, name string) (ir.FrozenInput, error) {
	return freeze(catalog, []ir.Resource{
		trojanNode(firstID, first),
		trojanNode(secondID, second),
		chainResource(chainID, name, firstID, secondID),
	}, []ir.FrozenRef{ref(chainID, ir.KindChain)})
}

func freezeIndependent(catalog *capability.Catalog, node Endpoint, id ir.ID) (ir.FrozenInput, error) {
	return freeze(catalog, []ir.Resource{trojanNode(id, node)}, []ir.FrozenRef{ref(id, ir.KindNode)})
}

func freezeMixedChainAndIndependentB(catalog *capability.Catalog, a, b Endpoint) (ir.FrozenInput, error) {
	return freeze(catalog, []ir.Resource{
		trojanNode(idNodeA, a),
		trojanNode(idNodeB, b),
		chainResource(idChainAB, "A → B", idNodeA, idNodeB),
	}, []ir.FrozenRef{
		ref(idChainAB, ir.KindChain),
		ref(idNodeB, ir.KindNode),
	})
}

func freeze(catalog *capability.Catalog, resources []ir.Resource, members []ir.FrozenRef) (ir.FrozenInput, error) {
	if catalog == nil {
		return ir.FrozenInput{}, fmt.Errorf("catalog is required")
	}
	targets, err := catalogTargets(catalog)
	if err != nil {
		return ir.FrozenInput{}, err
	}
	return ir.NewFrozenInput(ir.FrozenInputSpec{
		SchemaVersion:   ir.SchemaVersion,
		SnapshotID:      idSnap,
		ScopeID:         idScope,
		CatalogRevision: 1,
		SecurityEpoch:   1,
		Resources:       resources,
		Members:         members,
		Targets:         targets,
	})
}

func trojanNode(id ir.ID, endpoint Endpoint) ir.Resource {
	verify := true
	udp := false
	name := endpoint.Name
	if name == "" {
		name = endpoint.SNI
	}
	return ir.Resource{
		Metadata: ir.Metadata{
			ResourceID: id, ScopeID: idScope, Kind: ir.KindNode, Revision: 1,
			SchemaVersion: ir.SchemaVersion, Name: name, Tags: []string{}, Enabled: true, SecurityEpoch: 1,
		},
		Payload: &ir.Node{
			SchemaVersion: ir.SchemaVersion,
			Protocol:      ir.Trojan,
			Endpoint:      ir.Endpoint{Host: endpoint.Host, Port: endpoint.Port},
			Auth:          &ir.PasswordAuth{Kind: ir.AuthPassword, Password: ir.Secret(endpoint.Password)},
			Transport:     &ir.NativeTCPTransport{Kind: ir.NativeTCP},
			Security: &ir.TLSSecurity{
				Mode: ir.TLS, ServerName: endpoint.SNI, VerifyCertificate: &verify, ALPN: []string{"http/1.1"},
			},
			Features:   ir.Features{UDP: &udp},
			Extensions: ir.Extensions{},
		},
	}
}

func chainResource(id ir.ID, name string, hop1, hop2 ir.ID) ir.Resource {
	return ir.Resource{
		Metadata: ir.Metadata{
			ResourceID: id, ScopeID: idScope, Kind: ir.KindChain, Revision: 1,
			SchemaVersion: ir.SchemaVersion, Name: name, Tags: []string{}, Enabled: true, SecurityEpoch: 1,
		},
		Payload: &ir.Chain{
			SchemaVersion: ir.SchemaVersion,
			Hops:          []ir.NodeRef{{NodeID: hop1}, {NodeID: hop2}},
			FailurePolicy: ir.FailClosed,
		},
	}
}

func ref(id ir.ID, kind ir.ResourceKind) ir.FrozenRef {
	return ir.FrozenRef{ResourceID: id, Kind: kind, Revision: 1, SecurityEpoch: 1}
}
