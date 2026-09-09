package depgraph

import (
	"slices"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestPolicyGraphExpandsChainAndRejectsUnavailableClosure(t *testing.T) {
	node := &ir.Node{SchemaVersion: 1, Protocol: ir.HTTP, Endpoint: ir.Endpoint{Host: "hop.example.invalid", Port: 80},
		Auth: &ir.NoAuth{Kind: ir.AuthNone}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}}
	a := resource(ir.KindNode, "11111111-1111-4111-8111-111111111111", node)
	b := resource(ir.KindNode, "22222222-2222-4222-8222-222222222222", node)
	chain := resource(ir.KindChain, "33333333-3333-4333-8333-333333333333", &ir.Chain{SchemaVersion: 1,
		Hops: []ir.NodeRef{{NodeID: a.Metadata.ResourceID}, {NodeID: b.Metadata.ResourceID}}, FailurePolicy: ir.FailClosed})
	member := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindChain, ResourceID: chain.Metadata.ResourceID}
	group := resource(ir.KindPolicyGroup, "44444444-4444-4444-8444-444444444444", &ir.PolicyGroup{SchemaVersion: 1,
		Strategy: ir.PolicyRoundRobin, Members: []ir.TargetRef{member}, DefaultMember: member,
		HealthCheck: ir.PolicyHealthCheck{Enabled: false}, OnUnavailable: ir.FailClosed})
	resources := map[ir.ID]ir.Resource{a.Metadata.ResourceID: a, b.Metadata.ResourceID: b, chain.Metadata.ResourceID: chain, group.Metadata.ResourceID: group}
	roots := []ir.ID{group.Metadata.ResourceID}
	result := Expand(resources, roots, Options{})
	if len(result.Diagnostics) != 0 || !slices.Equal(result.Order, []ir.ID{a.Metadata.ResourceID, b.Metadata.ResourceID, chain.Metadata.ResourceID, group.Metadata.ResourceID}) {
		t.Fatal("policy did not expand both chain hops before the group")
	}
	if result := Expand(resources, roots, Options{Exclude: map[ir.ID]bool{b.Metadata.ResourceID: true}}); !hasCode(result, "DEPENDENCY_EXCLUDED") {
		t.Fatal("policy omitted an excluded required hop")
	}
	bad := b
	bad.Metadata.Enabled = false
	resources[b.Metadata.ResourceID] = bad
	if result := Expand(resources, roots, Options{}); !hasCode(result, ir.ResourceDisabled) {
		t.Fatal("policy expanded a disabled chain hop")
	}
	bad = b
	bad.Metadata.ScopeID = "99999999-9999-4999-8999-999999999999"
	resources[b.Metadata.ResourceID] = bad
	if result := Expand(resources, roots, Options{}); !hasCode(result, ir.ScopeMismatch) {
		t.Fatal("policy expanded across resource scope")
	}
	delete(resources, b.Metadata.ResourceID)
	if result := Expand(resources, roots, Options{}); !hasCode(result, ir.ReferenceMissing) {
		t.Fatal("policy omitted a missing chain hop")
	}
}
