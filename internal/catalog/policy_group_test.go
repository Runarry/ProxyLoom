package catalog_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestPolicyCatalogRevisionReferencesAndClosure(t *testing.T) {
	a, b := resource(t, node(t, "trojan-a")), resource(t, node(t, "trojan-a"))
	chain, err := catalog.New(scope, catalog.CreateInput{Name: "chain", Tags: []string{}, Enabled: true,
		Payload: &ir.Chain{SchemaVersion: 1, Hops: []ir.NodeRef{{NodeID: a.Metadata.ResourceID}, {NodeID: b.Metadata.ResourceID}}, FailurePolicy: ir.FailClosed}})
	if err != nil {
		t.Fatal(err)
	}
	members := []ir.TargetRef{{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: a.Metadata.ResourceID},
		{Type: ir.ResourceRef, Kind: ir.KindChain, ResourceID: chain.Metadata.ResourceID}}
	group := ir.PolicyGroup{SchemaVersion: 1, Strategy: ir.PolicyManualSelect, Members: members,
		DefaultMember: members[1], HealthCheck: ir.PolicyHealthCheck{Enabled: false}, OnUnavailable: ir.FailClosed}
	created, err := catalog.New(scope, catalog.CreateInput{Name: "group", Tags: []string{}, Enabled: true, Payload: &group})
	if err != nil || created.Metadata.Kind != ir.KindPolicyGroup || created.Metadata.Revision != 1 {
		t.Fatalf("catalog did not create typed policy: %v", err)
	}
	before, _ := catalog.Canonical(created)
	refs, err := catalog.ExtractReferences(created)
	if err != nil || len(refs) != 3 || refs[0].ExpectedKind != ir.KindNode || refs[1].ExpectedKind != ir.KindChain ||
		refs[1].Path != "/payload/members/1/resource_id" || refs[2].Path != "/payload/default_member/resource_id" {
		t.Fatal("policy references omitted member kind or field identity")
	}
	group.Strategy = ir.PolicyRoundRobin
	next, err := catalog.Apply(created, catalog.UpdateInput{Name: "renamed", Tags: []string{}, Enabled: false, Payload: &group})
	after, _ := catalog.Canonical(created)
	if err != nil || !bytes.Equal(before, after) || next.Metadata.Revision != 2 || next.Metadata.SecurityEpoch != 2 || next.Payload.(*ir.PolicyGroup).Strategy != ir.PolicyRoundRobin {
		t.Fatal("policy edit did not preserve history or disable epoch semantics")
	}
	resources := map[ir.ID]ir.Resource{a.Metadata.ResourceID: a, b.Metadata.ResourceID: b, chain.Metadata.ResourceID: chain}
	resolve := func(id ir.ID) (ir.Resource, error) {
		if resource, ok := resources[id]; ok {
			return resource, nil
		}
		return ir.Resource{}, catalog.ErrNotFound
	}
	closure, err := catalog.ResolvePolicyMembers(scope, group, resolve)
	if err != nil || len(closure) != 3 {
		t.Fatalf("policy closure did not include both chain hops exactly once: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func()
	}{
		{"disabled_hop", func() { disabled := b; disabled.Metadata.Enabled = false; resources[b.Metadata.ResourceID] = disabled }},
		{"cross_scope_hop", func() {
			cross := b
			cross.Metadata.ScopeID = "20000000-0000-4000-8000-000000000001"
			resources[b.Metadata.ResourceID] = cross
		}},
		{"missing_hop", func() { delete(resources, b.Metadata.ResourceID) }},
		{"wrong_kind_hop", func() { resources[b.Metadata.ResourceID] = chain }},
	} {
		t.Run(test.name, func(t *testing.T) {
			resources[b.Metadata.ResourceID] = b
			test.edit()
			_, err := catalog.ResolvePolicyMembers(scope, group, resolve)
			var diagnostics ir.Diagnostics
			if !errors.As(err, &diagnostics) || len(diagnostics) != 1 || diagnostics[0].FieldPath != "/members/1/hops/1/node_id" {
				t.Fatal("bad chain hop was accepted or not located")
			}
		})
	}
	if _, err := catalog.ResolvePolicyMembers(scope, group, func(ir.ID) (ir.Resource, error) { return ir.Resource{}, catalog.ErrUnavailable }); !errors.Is(err, catalog.ErrUnavailable) {
		t.Fatal("dependency database failure was hidden as a missing member")
	}
}
