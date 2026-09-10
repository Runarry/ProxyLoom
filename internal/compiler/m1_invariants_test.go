package compiler

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestM1PlanIncludesFrozenOrchestrationAndDeepCopies(t *testing.T) {
	spec := policySpec(t, ir.PolicyRoundRobin)
	setID := ir.ID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	set, err := ir.NewRuleSet(ir.RuleSetWrite{SchemaVersion: 1, Format: ir.DomainCIDRText, Entries: []ir.RuleSetEntry{{Kind: ir.RuleSetDomain, Match: ir.DomainSuffix, Domain: "fixture.invalid"}}})
	if err != nil {
		t.Fatal(err)
	}
	meta := spec.Resources[0].Metadata
	meta.ResourceID, meta.Kind, meta.Revision, meta.SecurityEpoch = setID, ir.KindRuleSet, 3, 2
	spec.Resources = append(spec.Resources, ir.Resource{Metadata: meta, Payload: &set})
	for _, r := range spec.Resources {
		if p, ok := r.Payload.(*ir.RoutingProfile); ok {
			p.Rules = append(p.Rules, ir.RoutingRule{Enabled: true, Match: ir.RouteMatch{RuleSetIDs: []ir.ID{setID}}, Action: ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Reject}})
		}
	}
	fixed := ir.PolicyFixed
	for i := range spec.Targets {
		if spec.Targets[i].CoreFamily == ir.SingBox {
			spec.Targets[i].PolicyOverrides = map[ir.ID]ir.PolicyOverride{policyID: {PolicyGroupID: policyID, Strategy: &fixed}}
		}
	}
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("singbox-default")
	graph, _, err := mustCompiler(t).Prepare(input, target)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(graph)
	if len(plan.Resources) != len(spec.Resources) || len(plan.RuleSets) != 1 {
		t.Fatal("plan dropped frozen closure pins")
	}
	for _, r := range spec.Resources {
		want := ir.FrozenRef{ResourceID: r.Metadata.ResourceID, Kind: r.Metadata.Kind, Revision: r.Metadata.Revision, SecurityEpoch: r.Metadata.SecurityEpoch}
		if !slices.Contains(plan.Resources, want) {
			t.Fatalf("missing pin for %s", want.Kind)
		}
	}
	for _, key := range []string{"routing.ordered", "rule_set.inline", "dns.profile", "client_preset"} {
		if !slices.Contains(plan.RequiredCapabilities, key) || !slices.Contains(plan.UnverifiedCapabilities, key) {
			t.Fatalf("missing capability %s", key)
		}
	}
	if len(plan.Policies) != 1 || plan.Policies[0].Strategy != ir.PolicyFixed || len(plan.Target.PolicyOverrides) != 1 || plan.Routing == nil || plan.DNS == nil || plan.Preset == nil || plan.FinalTag != plan.Policies[0].Tag {
		t.Fatal("plan omitted orchestration")
	}
	before, err := marshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	checkNativeGolden(t, "plan-orchestration.json", before)
	if bytes.Contains(before, []byte("EXAMPLE_ONLY")) {
		t.Fatal("plan exposed node credentials")
	}
	plan.Resources[0].Revision = 999
	plan.Policies[0].Members[0] = "mutated"
	plan.DNS.Profile.Resolvers[0].ResolverID = "mutated"
	plan.Routing.Rules[0].Condition.Terms[0].Values[0] = "mutated"
	plan.Preset.LocalListener.Port = 1234
	*plan.Target.PolicyOverrides[0].Strategy = ir.PolicyManualSelect
	after, err := marshalPlan(BuildPlan(graph))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("plan leaked mutable frozen state")
	}
}

func TestM1GroupMembersAreIsolatedFinalExits(t *testing.T) {
	spec := policySpec(t, ir.PolicyFixed)
	base := mustFrozen(t, "frozen-chain-a-b.json").Spec()
	for _, r := range base.Resources {
		if r.Metadata.ResourceID != spec.Resources[0].Metadata.ResourceID {
			spec.Resources = append(spec.Resources, r)
		}
	}
	chainID := ir.ID("33333333-3333-4333-8333-333333333333")
	for _, r := range spec.Resources {
		if p, ok := r.Payload.(*ir.PolicyGroup); ok {
			p.Members = append(p.Members, ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindChain, ResourceID: chainID})
		}
	}
	var other ir.Resource
	for _, r := range spec.Resources {
		if p, ok := r.Payload.(*ir.PolicyGroup); ok {
			copy := p.Clone()
			other = ir.Resource{Metadata: r.Metadata, Payload: &copy}
			other.Metadata.ResourceID = "55555555-5555-4555-8555-555555555556"
		}
	}
	spec.Resources = append(spec.Resources, other)
	spec.Members = append(spec.Members, ir.FrozenRef{ResourceID: other.Metadata.ResourceID, Kind: ir.KindPolicyGroup, Revision: 1, SecurityEpoch: 1}, ir.FrozenRef{ResourceID: chainID, Kind: ir.KindChain, Revision: 1, SecurityEpoch: 1})
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("xray-default")
	graph, _, err := mustCompiler(t).Prepare(input, target)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, p := range graph.Policies {
		for _, member := range p.Members {
			if seen[member] || strings.HasSuffix(member, "_h1") || !strings.HasPrefix(member, p.Tag+"_m") {
				t.Fatal("group admitted a shared or nonfinal outbound")
			}
			seen[member] = true
		}
	}
	if len(graph.Chains) != 3 {
		t.Fatal("chain instances were not isolated")
	}
	if base.Resources[0].Payload.(*ir.Node).Endpoint.Host != "a.example.invalid" {
		t.Fatal("source node mutated")
	}
}

func TestM1ExpandedRuleBudgetDoesNotTruncate(t *testing.T) {
	spec := routingSpec(t)
	var routing *ir.RoutingProfile
	for _, r := range spec.Resources {
		if p, ok := r.Payload.(*ir.RoutingProfile); ok {
			routing = p
		}
	}
	routing.Rules = nil
	for i := 0; i < 21; i++ {
		values := make([]string, 1000)
		for j := range values {
			values[j] = fmt.Sprintf("d%d.example.invalid", j)
		}
		routing.Rules = append(routing.Rules, ir.RoutingRule{Enabled: true, Match: ir.RouteMatch{DomainExact: values}, Action: ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Reject}})
	}
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("xray-default")
	a, d, err := mustCompiler(t).Compile(context.Background(), input, target)
	if err == nil || len(a.Bytes) != 0 || len(d) != 1 || d[0].Code != ir.InputLimitExceeded || d[0].ResourceID != spec.RoutingProfile.ResourceID {
		t.Fatal("expanded rules silently truncated or accepted")
	}
}

func TestM1ExpandedOutboundBudgetCountsPolicyInstances(t *testing.T) {
	spec := policySpec(t, ir.PolicyFixed)
	var original ir.PolicyGroup
	for _, r := range spec.Resources {
		if p, ok := r.Payload.(*ir.PolicyGroup); ok {
			original = p.Clone()
		}
	}
	nodeMeta := spec.Resources[0].Metadata
	for i := 1; i < 200; i++ {
		r := spec.Resources[0]
		r.Metadata.ResourceID = ir.ID(fmt.Sprintf("11111111-1111-4111-8111-%012x", i))
		spec.Resources = append(spec.Resources, r)
		original.Members = append(original.Members, ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: r.Metadata.ResourceID})
	}
	for _, r := range spec.Resources {
		if p, ok := r.Payload.(*ir.PolicyGroup); ok {
			*p = original.Clone()
		}
	}
	for i := 0; i < 10; i++ {
		p := original.Clone()
		meta := nodeMeta
		meta.Kind = ir.KindPolicyGroup
		meta.ResourceID = ir.ID(fmt.Sprintf("55555555-5555-4555-8555-%012x", i))
		spec.Resources = append(spec.Resources, ir.Resource{Metadata: meta, Payload: &p})
		spec.Members = append(spec.Members, ir.FrozenRef{ResourceID: meta.ResourceID, Kind: ir.KindPolicyGroup, Revision: 1, SecurityEpoch: 1})
	}
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("xray-default")
	a, d, err := mustCompiler(t).Compile(context.Background(), input, target)
	if err == nil || len(a.Bytes) != 0 || len(d) != 1 || d[0].Code != ir.InputLimitExceeded || d[0].FieldPath != "/outbounds" {
		t.Fatal("policy clone expansion exceeded outbound cap")
	}
}

func TestM1FinalArtifactByteBudget(t *testing.T) {
	spec := policySpec(t, ir.PolicyFixed)
	n := spec.Resources[0].Payload.(*ir.Node)
	n.Auth.(*ir.PasswordAuth).Password = ir.Secret(strings.Repeat("a", adapter.MaxArtifactBytes/100))
	var original ir.Resource
	for _, r := range spec.Resources {
		if _, ok := r.Payload.(*ir.PolicyGroup); ok {
			original = r
		}
	}
	for i := 0; i < 100; i++ {
		p := original.Payload.(*ir.PolicyGroup).Clone()
		r := ir.Resource{Metadata: original.Metadata, Payload: &p}
		r.Metadata.ResourceID = ir.ID(fmt.Sprintf("55555555-5555-4555-8555-%012x", i))
		spec.Resources = append(spec.Resources, r)
		spec.Members = append(spec.Members, ir.FrozenRef{ResourceID: r.Metadata.ResourceID, Kind: ir.KindPolicyGroup, Revision: 1, SecurityEpoch: 1})
	}
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("xray-default")
	a, d, err := mustCompiler(t).Compile(context.Background(), input, target)
	if err == nil || len(a.Bytes) != 0 || len(d) != 1 || d[0].Code != ir.InputLimitExceeded || d[0].FieldPath != "/artifact" {
		t.Fatal("oversize artifact emitted")
	}
}

func TestM1MihomoHealthDisabledIsNotReplacedWithPublicProbe(t *testing.T) {
	input, err := ir.NewFrozenInput(policySpec(t, ir.PolicyRoundRobin))
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("mihomo-default")
	a, d, err := mustCompiler(t).Compile(context.Background(), input, target)
	if err == nil || len(a.Bytes) != 0 || len(d) != 1 || d[0].Code != ir.CompileUnmappedField || d[0].ResourceID != policyID || d[0].TargetKey != target.Key || d[0].FieldPath != "/payload/health_check/enabled" {
		t.Fatal("disabled health became implicit public probe")
	}
}

func TestM1XrayH2BootstrapMustRemainEquivalent(t *testing.T) {
	spec := m1ChainBootstrapSpec(t, "192.0.2.1:8080", "192.0.2.2:8081")
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("xray-default")
	a, _, err := mustCompiler(t).Compile(context.Background(), input, target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(a.Bytes, []byte(`"domainStrategy":"ForceIP"`)) {
		t.Fatal("H2 domain forwarded without bootstrap")
	}
	checkNativeGolden(t, "chain-bootstrap-local-xray.json", a.Bytes)
	for _, r := range spec.Resources {
		if p, ok := r.Payload.(*ir.DNSProfile); ok {
			p.Resolvers[0] = ir.DNSResolver{ResolverID: "business", Kind: ir.DNSUDP, Address: "192.0.2.53", Port: 53, Outbound: &ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Direct}}
		}
	}
	input, err = ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	a, d, err := mustCompiler(t).Compile(context.Background(), input, target)
	if err == nil || len(a.Bytes) != 0 || len(d) != 1 || d[0].Code != ir.CompileUnmappedField || d[0].ResourceID != "22222222-2222-4222-8222-222222222222" || !strings.HasSuffix(d[0].FieldPath, "/h2/endpoint/host") {
		t.Fatal("H2 local bootstrap silently replaced by business DNS")
	}
}

func TestM1NativeOutboundBudgetIncludesSelectorsAndBuiltins(t *testing.T) {
	resource := orchestrationSpec(t).Resources[0]
	for _, family := range []ir.CoreFamily{ir.Xray, ir.SingBox, ir.Mihomo} {
		limit := adapter.MaxOutbounds - 1
		strategy := ir.PolicyManualSelect
		if family == ir.Xray {
			limit--
			strategy = ir.PolicyRoundRobin
		}
		for _, count := range []int{limit, limit + 1} {
			graph := Graph{Target: ir.Target{CoreFamily: family, Key: "budget"}, FinalTag: "p_budget", Policies: []adapter.PolicyInstance{{Tag: "p_budget", ResourceID: policyID, Strategy: strategy, Members: []string{"n_0000"}, Default: "n_0000"}}, Preset: &ir.ClientPreset{LocalListener: ir.LocalListener{Protocol: "socks5", Listen: "127.0.0.1", Port: 1080}, ControlAPI: ir.ControlAPIPreset{Enabled: true, Listen: "127.0.0.1", Port: 17812}}}
			for i := 0; i < count; i++ {
				graph.Independents = append(graph.Independents, IndependentOutbound{Tag: fmt.Sprintf("n_%04d", i), Resource: resource})
			}
			a, d, err := emitNative(graph)
			if count == limit {
				if err != nil || len(a.Bytes) == 0 {
					t.Fatalf("%s exact limit: %v", family, err)
				}
			} else if err == nil || len(a.Bytes) != 0 || len(d) != 1 || d[0].Code != ir.InputLimitExceeded || d[0].FieldPath != "/outbounds" {
				t.Fatalf("%s omitted selector/builtin from native budget", family)
			}
		}
	}
}

func TestM1NativeRuleBudgetIncludesFinalRoute(t *testing.T) {
	resource := orchestrationSpec(t).Resources[0]
	for _, family := range []ir.CoreFamily{ir.Xray, ir.SingBox, ir.Mihomo} {
		for _, count := range []int{adapter.MaxRules - 1, adapter.MaxRules} {
			graph := Graph{Target: ir.Target{CoreFamily: family, Key: "budget"}, FinalTag: "n_budget", Independents: []IndependentOutbound{{Tag: "n_budget", Resource: resource}}, Routing: &adapter.RoutingInput{Mode: ir.PreserveDomain}}
			for i := 0; i < count; i++ {
				graph.Routing.Rules = append(graph.Routing.Rules, adapter.RouteRule{Condition: adapter.Condition{Kind: "domain", Values: []string{"fixture.invalid"}}, Target: "n_budget"})
			}
			a, d, err := emitNative(graph)
			if count == adapter.MaxRules-1 {
				if err != nil || len(a.Bytes) == 0 {
					t.Fatalf("%s exact rule limit: %v", family, err)
				}
			} else if err == nil || len(a.Bytes) != 0 || len(d) != 1 || d[0].Code != ir.InputLimitExceeded || d[0].FieldPath != "/rules" {
				t.Fatalf("%s omitted final rule from native budget", family)
			}
		}
	}
}
