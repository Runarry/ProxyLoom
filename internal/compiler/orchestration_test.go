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

const policyID ir.ID = "55555555-5555-4555-8555-555555555555"

func orchestrationSpec(t *testing.T) ir.FrozenInputSpec {
	t.Helper()
	spec := mustFrozen(t, "frozen-chain-a-b.json").Spec()
	spec.Resources = spec.Resources[:1]
	spec.Resources[0].Payload.(*ir.Node).Endpoint.Host = "192.0.2.10"
	meta := spec.Resources[0].Metadata
	spec.Members = []ir.FrozenRef{{ResourceID: meta.ResourceID, Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1}}
	meta.ResourceID, meta.Kind = "66666666-6666-4666-8666-666666666666", ir.KindDNSProfile
	dns := ir.DNSProfile{SchemaVersion: 1, Bootstrap: []ir.BootstrapResolver{{ResolverID: "bootstrap", Kind: ir.DNSLocal}}, Resolvers: []ir.DNSResolver{{ResolverID: "business", Kind: ir.DNSLocal}}, Rules: []ir.DNSRule{}, FinalResolver: "business"}
	spec.Resources = append(spec.Resources, ir.Resource{Metadata: meta, Payload: &dns})
	spec.DNSProfile = &ir.FrozenRef{ResourceID: meta.ResourceID, Kind: ir.KindDNSProfile, Revision: 1, SecurityEpoch: 1}
	for _, target := range spec.Targets {
		meta.ResourceID, meta.Kind = target.ClientPresetID, ir.KindClientPreset
		p := ir.ClientPreset{SchemaVersion: 1, CoreFamily: target.CoreFamily, Platform: "linux", Format: target.Format, LocalListener: ir.LocalListener{Protocol: "socks5", Listen: "127.0.0.1", Port: 1080}, DNSMode: "profile", ControlAPI: ir.ControlAPIPreset{Enabled: false}, ImportMethod: "file", ReviewStatus: "approved", ReviewedAt: "2026-09-10T00:00:00Z"}
		spec.Resources = append(spec.Resources, ir.Resource{Metadata: meta, Payload: &p})
	}
	return spec
}

func policySpec(t *testing.T, strategy ir.PolicyStrategy) ir.FrozenInputSpec {
	t.Helper()
	spec := orchestrationSpec(t)
	member := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: spec.Resources[0].Metadata.ResourceID}
	meta := spec.Resources[0].Metadata
	meta.Kind, meta.ResourceID = ir.KindPolicyGroup, policyID
	p := ir.PolicyGroup{SchemaVersion: 1, Strategy: strategy, Members: []ir.TargetRef{member}, DefaultMember: member, HealthCheck: ir.PolicyHealthCheck{Enabled: false}, OnUnavailable: ir.FailClosed}
	spec.Resources = append(spec.Resources, ir.Resource{Metadata: meta, Payload: &p})
	spec.Members = []ir.FrozenRef{{ResourceID: policyID, Kind: ir.KindPolicyGroup, Revision: 1, SecurityEpoch: 1}}
	meta.ResourceID, meta.Kind = "44444444-4444-4444-8444-444444444444", ir.KindRoutingProfile
	routing := ir.RoutingProfile{SchemaVersion: 1, Rules: []ir.RoutingRule{}, Final: ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindPolicyGroup, ResourceID: policyID}, DomainResolutionMode: ir.PreserveDomain}
	spec.Resources = append(spec.Resources, ir.Resource{Metadata: meta, Payload: &routing})
	spec.RoutingProfile = &ir.FrozenRef{ResourceID: meta.ResourceID, Kind: ir.KindRoutingProfile, Revision: 1, SecurityEpoch: 1}
	return spec
}

func routingSpec(t *testing.T) ir.FrozenInputSpec {
	t.Helper()
	spec := orchestrationSpec(t)
	meta := spec.Resources[0].Metadata
	meta.ResourceID, meta.Kind = "44444444-4444-4444-8444-444444444444", ir.KindRoutingProfile
	p := ir.RoutingProfile{SchemaVersion: 1, DomainResolutionMode: ir.PreserveDomain, Final: ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Reject}, Rules: []ir.RoutingRule{
		{Enabled: true, Match: ir.RouteMatch{DomainExact: []string{"api.example.invalid", "other.invalid"}, DomainSuffix: []string{"example.invalid"}, DestinationPorts: []ir.PortRange{{From: 443, To: 443}}, Network: []ir.Network{ir.NetworkTCP}}, Action: ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: spec.Resources[0].Metadata.ResourceID}},
		{Enabled: false, Match: ir.RouteMatch{DomainSuffix: []string{"disabled.invalid"}}, Action: ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Direct}},
		{Enabled: true, Match: ir.RouteMatch{IPCIDRs: []string{"192.0.2.0/24"}}, Action: ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Direct}},
	}}
	spec.Resources = append(spec.Resources, ir.Resource{Metadata: meta, Payload: &p})
	spec.RoutingProfile = &ir.FrozenRef{ResourceID: meta.ResourceID, Kind: ir.KindRoutingProfile, Revision: 1, SecurityEpoch: 1}
	return spec
}

func TestRoutingPresetNativeGoldens(t *testing.T) {
	input, err := ir.NewFrozenInput(routingSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	c := mustCompiler(t)
	for _, target := range input.Spec().Targets {
		a, diagnostics, err := c.Compile(context.Background(), input, target)
		if err != nil {
			t.Fatal(err)
		}
		if !hasInfo(diagnostics, ir.CapabilityUnverified) {
			t.Fatal("native compile promoted verification")
		}
		if bytes.Contains(a.Bytes, []byte("disabled.invalid")) {
			t.Fatal("disabled rule emitted")
		}
		ext := ".json"
		if target.CoreFamily == ir.Mihomo {
			ext = ".yaml"
		}
		checkNativeGolden(t, "orchestration-"+string(target.CoreFamily)+ext, a.Bytes)
		b, _, err := c.Compile(context.Background(), input, target)
		if err != nil || !bytes.Equal(a.Bytes, b.Bytes) {
			t.Fatal("orchestration bytes drifted")
		}
	}
}

func TestPolicyNativeGoldens(t *testing.T) {
	for _, strategy := range []ir.PolicyStrategy{ir.PolicyFixed, ir.PolicyRoundRobin} {
		input, err := ir.NewFrozenInput(policySpec(t, strategy))
		if err != nil {
			t.Fatal(err)
		}
		for _, target := range input.Spec().Targets {
			compileInput := input
			if target.CoreFamily == ir.SingBox && strategy == ir.PolicyRoundRobin {
				continue
			}
			if target.CoreFamily == ir.Mihomo && strategy == ir.PolicyRoundRobin {
				spec := policySpec(t, strategy)
				interval, timeout, tolerance := 1000, 500, 0
				for _, r := range spec.Resources {
					if p, ok := r.Payload.(*ir.PolicyGroup); ok {
						p.HealthCheck = ir.PolicyHealthCheck{Enabled: true, URL: "https://health.example.invalid/check", IntervalMS: &interval, TimeoutMS: &timeout, ToleranceMS: &tolerance}
					}
				}
				compileInput, err = ir.NewFrozenInput(spec)
				if err != nil {
					t.Fatal(err)
				}
			}
			a, _, err := mustCompiler(t).Compile(context.Background(), compileInput, target)
			if err != nil {
				t.Fatal(err)
			}
			ext := ".json"
			if target.CoreFamily == ir.Mihomo {
				ext = ".yaml"
			}
			checkNativeGolden(t, "policy-"+string(strategy)+"-"+string(target.CoreFamily)+ext, a.Bytes)
			if strategy == ir.PolicyRoundRobin && !bytes.Contains(a.Bytes, []byte("round")) {
				t.Fatal("round robin was replaced")
			}
			if bytes.Contains(a.Bytes, []byte(",DIRECT")) || bytes.Contains(a.Bytes, []byte(`"protocol":"freedom"`)) {
				t.Fatal("policy inserted direct fallback")
			}
		}
	}
}

func TestExplicitPolicyOverridePreservesOriginalAndTargetIdentity(t *testing.T) {
	spec := policySpec(t, ir.PolicyRoundRobin)
	for i := range spec.Targets {
		if spec.Targets[i].CoreFamily == ir.SingBox {
			fixed := ir.PolicyFixed
			spec.Targets[i].PolicyOverrides = map[ir.ID]ir.PolicyOverride{policyID: {PolicyGroupID: policyID, Strategy: &fixed}}
		}
	}
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("singbox-default")
	artifact, _, err := mustCompiler(t).Compile(context.Background(), input, target)
	if err != nil || len(artifact.Bytes) == 0 {
		t.Fatal(err)
	}
	for _, r := range input.Spec().Resources {
		if r.Metadata.ResourceID == policyID && r.Payload.(*ir.PolicyGroup).Strategy != ir.PolicyRoundRobin {
			t.Fatal("override mutated original group")
		}
	}
	target.PolicyOverrides = nil
	if _, _, err := mustCompiler(t).Compile(context.Background(), input, target); !hasCode(err, ir.CompileTargetMismatch) {
		t.Fatal("altered frozen target accepted")
	}
}

func TestHealthFieldsFailExplicitlyWhenNativeCannotHonorThem(t *testing.T) {
	spec := policySpec(t, ir.PolicyLatencyBest)
	interval, timeout, tolerance := 1000, 500, 5
	for _, r := range spec.Resources {
		if p, ok := r.Payload.(*ir.PolicyGroup); ok {
			p.HealthCheck = ir.PolicyHealthCheck{Enabled: true, URL: "https://health.example.invalid/check", IntervalMS: &interval, TimeoutMS: &timeout, ToleranceMS: &tolerance}
		}
	}
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"xray-default", "singbox-default"} {
		target, _ := input.Target(key)
		a, d, err := mustCompiler(t).Compile(context.Background(), input, target)
		if err == nil || len(a.Bytes) != 0 || len(d) != 1 || d[0].Code != ir.CompileUnmappedField || d[0].FieldPath != "/payload/health_check/timeout_ms" {
			t.Fatal("native timeout was silently dropped")
		}
	}
	target, _ := input.Target("mihomo-default")
	a, _, err := mustCompiler(t).Compile(context.Background(), input, target)
	if err != nil {
		t.Fatal(err)
	}
	checkNativeGolden(t, "policy-latency_best-mihomo.yaml", a.Bytes)
}

func TestControlledManualSelectionNativeGoldens(t *testing.T) {
	spec := policySpec(t, ir.PolicyManualSelect)
	for _, r := range spec.Resources {
		if p, ok := r.Payload.(*ir.ClientPreset); ok && p.CoreFamily != ir.Xray {
			port := ir.SingBoxControlPort
			if p.CoreFamily == ir.Mihomo {
				port = ir.MihomoControlPort
			}
			p.ControlAPI = ir.ControlAPIPreset{Enabled: true, Listen: ir.PresetLoopbackAddress, Port: port}
		}
	}
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"singbox-default", "mihomo-default"} {
		target, _ := input.Target(key)
		a, _, err := mustCompiler(t).Compile(context.Background(), input, target)
		if err != nil {
			t.Fatal(err)
		}
		ext := ".json"
		if target.CoreFamily == ir.Mihomo {
			ext = ".yaml"
		}
		checkNativeGolden(t, "policy-manual_select-"+string(target.CoreFamily)+ext, a.Bytes)
		if !bytes.Contains(a.Bytes, []byte("127.0.0.1:1781")) {
			t.Fatal("explicit loopback controller not mapped")
		}
	}
}

func TestDNSUDPViaTCPNodeAndMissingPresetFailClosed(t *testing.T) {
	spec := orchestrationSpec(t)
	for _, r := range spec.Resources {
		if p, ok := r.Payload.(*ir.DNSProfile); ok {
			p.Resolvers[0] = ir.DNSResolver{ResolverID: "business", Kind: ir.DNSUDP, Address: "192.0.2.53", Port: 53, Outbound: &ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: spec.Resources[0].Metadata.ResourceID}}
		}
	}
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("singbox-default")
	a, d, err := mustCompiler(t).Compile(context.Background(), input, target)
	if err == nil || len(a.Bytes) != 0 || len(d) != 1 || d[0].FieldPath != "/payload/resolvers/0/outbound" {
		t.Fatal("UDP resolver silently crossed TCP-only outbound")
	}
	spec = orchestrationSpec(t)
	spec.Resources = spec.Resources[:2]
	input, err = ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ = input.Target("xray-default")
	if _, _, err := mustCompiler(t).Compile(context.Background(), input, target); !hasCode(err, ir.ReferenceMissing) {
		t.Fatal("missing frozen preset resolved externally")
	}
}

func checkNativeGolden(t *testing.T, name string, content []byte) {
	t.Helper()
	path := filepath.Join("..", "..", "fixtures", "compiler", "native-p0", name)
	if os.Getenv("PROXYLOOM_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(content, want) {
		t.Fatalf("native golden differs: %s", name)
	}
}

func TestRouteRuleSemanticsAndRejectAreDistinctFromDirect(t *testing.T) {
	input, err := ir.NewFrozenInput(routingSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("singbox-default")
	a, _, err := mustCompiler(t).Compile(context.Background(), input, target)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Route struct {
			Rules []map[string]any `json:"rules"`
			Final string           `json:"final"`
		} `json:"route"`
	}
	if err := json.Unmarshal(a.Bytes, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Route.Rules[0]["mode"] != "and" || doc.Route.Rules[len(doc.Route.Rules)-1]["action"] != "reject" || doc.Route.Final != "" {
		t.Fatal("AND or reject-final semantics changed")
	}
	target, _ = input.Target("xray-default")
	a, _, err = mustCompiler(t).Compile(context.Background(), input, target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(a.Bytes), "other.invalid") {
		t.Fatal("Xray ORed exact and suffix fields instead of intersecting")
	}
}
