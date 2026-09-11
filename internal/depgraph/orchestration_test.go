package depgraph

import (
	"slices"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestRoutingAndDNSShareFrozenDependencyClosure(t *testing.T) {
	node := &ir.Node{SchemaVersion: 1, Protocol: ir.HTTP, Endpoint: ir.Endpoint{Host: "proxy.example.invalid", Port: 80},
		Auth: &ir.NoAuth{Kind: ir.AuthNone}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}}
	a := resource(ir.KindNode, "11111111-1111-4111-8111-111111111111", node)
	b := resource(ir.KindNode, "22222222-2222-4222-8222-222222222222", node)
	chain := resource(ir.KindChain, "33333333-3333-4333-8333-333333333333", &ir.Chain{SchemaVersion: 1,
		Hops: []ir.NodeRef{{NodeID: a.Metadata.ResourceID}, {NodeID: b.Metadata.ResourceID}}, FailurePolicy: ir.FailClosed})
	member := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindChain, ResourceID: chain.Metadata.ResourceID}
	group := resource(ir.KindPolicyGroup, "44444444-4444-4444-8444-444444444444", &ir.PolicyGroup{SchemaVersion: 1,
		Strategy: ir.PolicyFixed, Members: []ir.TargetRef{member}, DefaultMember: member,
		HealthCheck: ir.PolicyHealthCheck{Enabled: false}, OnUnavailable: ir.FailClosed})
	set, err := ir.NewRuleSet(ir.RuleSetWrite{SchemaVersion: 1, Format: ir.DomainCIDRText,
		Entries: []ir.RuleSetEntry{{Kind: ir.RuleSetDomain, Domain: "example.invalid", Match: ir.DomainSuffix}}})
	if err != nil {
		t.Fatal(err)
	}
	rules := resource(ir.KindRuleSet, "55555555-5555-4555-8555-555555555555", &set)
	groupRef := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindPolicyGroup, ResourceID: group.Metadata.ResourceID}
	routing := resource(ir.KindRoutingProfile, "66666666-6666-4666-8666-666666666666", &ir.RoutingProfile{SchemaVersion: 1,
		Rules: []ir.RoutingRule{{Match: ir.RouteMatch{RuleSetIDs: []ir.ID{rules.Metadata.ResourceID}}, Action: groupRef, Enabled: true}},
		Final: ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Reject}, DomainResolutionMode: ir.PreserveDomain})
	dns := resource(ir.KindDNSProfile, "77777777-7777-4777-8777-777777777777", &ir.DNSProfile{SchemaVersion: 1,
		Bootstrap: []ir.BootstrapResolver{{ResolverID: "bootstrap", Kind: ir.DNSLocal}},
		Resolvers: []ir.DNSResolver{{ResolverID: "business", Kind: ir.DNSHTTPS, URL: "https://resolver.example.invalid/dns-query", BootstrapResolverID: "bootstrap", Outbound: &groupRef}},
		Rules:     []ir.DNSRule{{Match: ir.DomainMatch{RuleSetIDs: []ir.ID{rules.Metadata.ResourceID}}, ResolverID: "business", Enabled: true}}, FinalResolver: "business"})
	resources := map[ir.ID]ir.Resource{}
	for _, item := range []ir.Resource{a, b, chain, group, rules, routing, dns} {
		resources[item.Metadata.ResourceID] = item
	}
	roots := []ir.ID{routing.Metadata.ResourceID, dns.Metadata.ResourceID}
	result := Expand(resources, roots, Options{ScopeID: a.Metadata.ScopeID})
	if len(result.Diagnostics) != 0 || len(result.Resources) != 7 {
		t.Fatalf("routing/DNS closure failed: %v", result.Diagnostics)
	}
	for _, dependent := range []ir.ID{routing.Metadata.ResourceID, dns.Metadata.ResourceID} {
		for _, dependency := range []ir.ID{a.Metadata.ResourceID, b.Metadata.ResourceID, chain.Metadata.ResourceID, group.Metadata.ResourceID, rules.Metadata.ResourceID} {
			if slices.Index(result.Order, dependency) >= slices.Index(result.Order, dependent) {
				t.Fatal("shared dependency was not expanded before its dependent")
			}
		}
	}
	if excluded := Expand(resources, roots, Options{Exclude: map[ir.ID]bool{a.Metadata.ResourceID: true}}); !hasCode(excluded, "DEPENDENCY_EXCLUDED") {
		t.Fatal("route and DNS closure omitted explicitly excluded chain hop")
	}
	if limited := Expand(resources, roots, Options{MaxResources: 6}); !hasCode(limited, ir.InvalidValue) {
		t.Fatal("combined route/DNS closure exceeded the limit without a diagnostic")
	}
	for _, test := range []struct {
		name string
		code ir.DiagnosticCode
		edit func()
	}{
		{"disabled", ir.ResourceDisabled, func() { item := a; item.Metadata.Enabled = false; resources[a.Metadata.ResourceID] = item }},
		{"cross-scope", ir.ScopeMismatch, func() {
			item := a
			item.Metadata.ScopeID = "88888888-8888-4888-8888-888888888888"
			resources[a.Metadata.ResourceID] = item
		}},
		{"wrong-kind", ir.ReferenceKind, func() {
			item := group
			item.Metadata.ResourceID = a.Metadata.ResourceID
			resources[a.Metadata.ResourceID] = item
		}},
		{"missing", ir.ReferenceMissing, func() { delete(resources, a.Metadata.ResourceID) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			resources[a.Metadata.ResourceID] = a
			test.edit()
			if result := Expand(resources, roots, Options{ScopeID: a.Metadata.ScopeID}); !hasCode(result, test.code) {
				t.Fatalf("expected %s, got %v", test.code, result.Diagnostics)
			}
		})
	}
}
