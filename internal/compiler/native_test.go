package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestNativeGoldens(t *testing.T) {
	compiler := mustCompiler(t)
	input := mustFrozen(t, "frozen-chain-a-b.json")
	cases := []struct {
		key  string
		name string
	}{
		{"xray-default", "xray-native-chain-a-b.json"},
		{"singbox-default", "singbox-native-chain-a-b.json"},
		{"mihomo-default", "mihomo-native-chain-a-b.yaml"},
	}
	for _, test := range cases {
		target, ok := input.Target(test.key)
		if !ok {
			t.Fatal(test.key)
		}
		artifact, diags, err := compiler.Compile(context.Background(), input, target)
		if err != nil {
			t.Fatal(err)
		}
		if !hasInfo(diags, ir.CapabilityUnverified) {
			t.Fatal("unverified must remain recorded")
		}
		goldenPath := filepath.Join("..", "..", "fixtures", "compiler", "golden", test.name)
		if os.Getenv("PROXYLOOM_UPDATE_GOLDEN") == "1" {
			if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(goldenPath, artifact.Bytes, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		want, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, artifact.Bytes) {
			t.Fatalf("%s golden mismatch\nwant %s\ngot  %s", test.key, want, artifact.Bytes)
		}
	}
}

func TestIndependentAAndBNativeConfigs(t *testing.T) {
	compiler := mustCompiler(t)
	base := mustFrozen(t, "frozen-chain-a-b.json")
	a := withMembers(t, base, "11111111-1111-4111-8111-111111111111")
	b := withMembers(t, base, "22222222-2222-4222-8222-222222222222")
	target, _ := a.Target("xray-default")
	artA, _, err := compiler.Compile(context.Background(), a, target)
	if err != nil {
		t.Fatal(err)
	}
	targetB, _ := b.Target("xray-default")
	artB, _, err := compiler.Compile(context.Background(), b, targetB)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(artA.Bytes, []byte("dialerProxy")) || bytes.Contains(artB.Bytes, []byte("dialerProxy")) {
		t.Fatal("independent node received a chain dialer")
	}
	if !bytes.Contains(artA.Bytes, []byte("a.example.invalid")) || !bytes.Contains(artB.Bytes, []byte("b.example.invalid")) {
		t.Fatal("independent configs dropped server mapping")
	}
	if bytes.Contains(artA.Bytes, []byte("b.example.invalid")) || bytes.Contains(artB.Bytes, []byte("a.example.invalid")) {
		t.Fatal("independent configs mixed A and B")
	}
	graphA := mustGraph(t, compiler, a, target)
	graphB := mustGraph(t, compiler, b, targetB)
	if len(graphA.Independents) != 1 || len(graphA.Chains) != 0 || len(graphB.Independents) != 1 {
		t.Fatal("independent expansion drifted")
	}
	if graphA.Independents[0].Tag == graphB.Independents[0].Tag {
		t.Fatal("A and B used the same tag")
	}
}

func TestMixedMembersKeepIndependentAndChainTagsIsolated(t *testing.T) {
	compiler := mustCompiler(t)
	base := mustFrozen(t, "frozen-chain-a-b.json")
	spec := base.Spec()
	spec.Members = append(spec.Members,
		ir.FrozenRef{ResourceID: "11111111-1111-4111-8111-111111111111", Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1},
		ir.FrozenRef{ResourceID: "22222222-2222-4222-8222-222222222222", Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1},
	)
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("xray-default")
	graph := mustGraph(t, compiler, input, target)
	artifact, _, err := compiler.Compile(context.Background(), input, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Independents) != 2 || len(graph.Chains) != 1 {
		t.Fatalf("graph %+v", graph)
	}
	h1 := graph.Chains[0].TagH1
	h2 := graph.Chains[0].TagH2
	for _, independent := range graph.Independents {
		if independent.Tag == h1 || independent.Tag == h2 {
			t.Fatal("independent tag reused a chain instance tag")
		}
	}
	if !bytes.Contains(artifact.Bytes, []byte(`"outboundTag":"`+h2+`"`)) && !bytes.Contains(artifact.Bytes, []byte(`"outboundTag": "`+h2+`"`)) {
		t.Fatal("business route must point at chain exit")
	}
	if bytes.Contains(artifact.Bytes, []byte(`"outboundTag":"`+h1+`"`)) {
		t.Fatal("business route pointed at the first hop")
	}
	countH1 := bytes.Count(artifact.Bytes, []byte(`"tag":"`+h1+`"`))
	countH2 := bytes.Count(artifact.Bytes, []byte(`"tag":"`+h2+`"`))
	if countH1 != 1 || countH2 != 1 {
		t.Fatalf("chain tags were reused: h1=%d h2=%d", countH1, countH2)
	}
}

func TestSameDisplayNameDoesNotShareTags(t *testing.T) {
	compiler := mustCompiler(t)
	spec := mustFrozen(t, "frozen-chain-a-b.json").Spec()
	spec.Members = []ir.FrozenRef{
		{ResourceID: "11111111-1111-4111-8111-111111111111", Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1},
		{ResourceID: "22222222-2222-4222-8222-222222222222", Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1},
	}
	spec.Resources = spec.Resources[:2]
	spec.Resources[0].Metadata.Name = "shared-name"
	spec.Resources[1].Metadata.Name = "shared-name"
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("xray-default")
	graph := mustGraph(t, compiler, input, target)
	if len(graph.Independents) != 2 || graph.Independents[0].Tag == graph.Independents[1].Tag {
		t.Fatalf("same-name nodes shared a tag: %+v", graph.Independents)
	}
}

func TestP0ShadowsocksIsMapped(t *testing.T) {
	compiler := mustCompiler(t)
	spec := mustFrozen(t, "frozen-chain-a-b.json").Spec()
	node, err := ir.DecodeNode(readIR(t, "positive/shadowsocks-aead.json"))
	if err != nil {
		t.Fatal(err)
	}
	spec.Resources[0].Payload = &node
	spec.Members = []ir.FrozenRef{{ResourceID: spec.Resources[0].Metadata.ResourceID, Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1}}
	spec.Resources = spec.Resources[:1]
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("xray-default")
	if _, _, err := compiler.Compile(context.Background(), input, target); err != nil {
		t.Fatalf("shadowsocks: %v", err)
	}
}

func TestExplicitUDPTrueIsRejected(t *testing.T) {
	compiler := mustCompiler(t)
	spec := mustFrozen(t, "frozen-chain-a-b.json").Spec()
	node := spec.Resources[0].Payload.(*ir.Node)
	cloned, err := cloneNodeForTest(*node)
	if err != nil {
		t.Fatal(err)
	}
	udp := true
	cloned.Features.UDP = &udp
	spec.Resources[0].Payload = &cloned
	spec.Members = []ir.FrozenRef{{ResourceID: spec.Resources[0].Metadata.ResourceID, Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1}}
	spec.Resources = spec.Resources[:1]
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("singbox-default")
	if _, _, err := compiler.Compile(context.Background(), input, target); !hasCode(err, ir.CompileUnmappedField) {
		t.Fatalf("udp true: %v", err)
	}
}

func TestNativeCompileRepeatsAreByteIdentical(t *testing.T) {
	compiler := mustCompiler(t)
	input := mustFrozen(t, "frozen-chain-a-b.json")
	for _, key := range []string{"xray-default", "singbox-default", "mihomo-default"} {
		target, _ := input.Target(key)
		var last []byte
		for i := 0; i < 100; i++ {
			artifact, _, err := compiler.Compile(context.Background(), input, target)
			if err != nil {
				t.Fatal(err)
			}
			if last != nil && !bytes.Equal(last, artifact.Bytes) {
				t.Fatalf("%s repeat %d drifted", key, i)
			}
			last = artifact.Bytes
		}
	}
}

func TestChainDoesNotMutateOriginalNode(t *testing.T) {
	compiler := mustCompiler(t)
	input := mustFrozen(t, "frozen-chain-a-b.json")
	before := mustFrozen(t, "frozen-chain-a-b.json")
	target, _ := input.Target("xray-default")
	artifact, _, err := compiler.Compile(context.Background(), input, target)
	if err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(input.Spec())
	if err != nil {
		t.Fatal(err)
	}
	original, err := json.Marshal(before.Spec())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("compile mutated the frozen input")
	}
	if strings.Contains(string(original), "dialerProxy") || strings.Contains(string(original), "detour") || strings.Contains(string(original), "dialer-proxy") {
		t.Fatal("frozen node gained a chain dialer field")
	}
	graph := mustGraph(t, compiler, input, target)
	if !bytes.Contains(artifact.Bytes, []byte(`"tag":"`+graph.Chains[0].TagH2+`"`)) {
		t.Fatal("missing chain exit outbound")
	}
}

func withMembers(t *testing.T, input ir.FrozenInput, ids ...ir.ID) ir.FrozenInput {
	t.Helper()
	spec := input.Spec()
	resources := make(map[ir.ID]ir.Resource, len(spec.Resources))
	for _, resource := range spec.Resources {
		resources[resource.Metadata.ResourceID] = resource
	}
	keep := map[ir.ID]struct{}{}
	var members []ir.FrozenRef
	for _, id := range ids {
		resource, ok := resources[id]
		if !ok {
			t.Fatalf("missing %s", id)
		}
		members = append(members, ir.FrozenRef{
			ResourceID:    id,
			Kind:          resource.Metadata.Kind,
			Revision:      resource.Metadata.Revision,
			SecurityEpoch: resource.Metadata.SecurityEpoch,
		})
		keep[id] = struct{}{}
		chain, ok := resource.Payload.(*ir.Chain)
		if !ok {
			continue
		}
		for _, hop := range chain.Hops {
			keep[hop.NodeID] = struct{}{}
		}
	}
	var kept []ir.Resource
	for _, resource := range spec.Resources {
		if _, ok := keep[resource.Metadata.ResourceID]; ok {
			kept = append(kept, resource)
		}
	}
	spec.Resources = kept
	spec.Members = members
	next, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	return next
}
