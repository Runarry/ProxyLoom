package depgraph

import (
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestExpandChainIncludesHopsAndRejectsMissingExcludeAndCycles(t *testing.T) {
	a := resource(ir.KindNode, "11111111-1111-4111-8111-111111111111", &ir.Node{SchemaVersion: 1, Protocol: ir.HTTP,
		Endpoint: ir.Endpoint{Host: "a.example.invalid", Port: 80}, Auth: &ir.NoAuth{Kind: ir.AuthNone},
		Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}})
	b := resource(ir.KindNode, "22222222-2222-4222-8222-222222222222", &ir.Node{SchemaVersion: 1, Protocol: ir.HTTP,
		Endpoint: ir.Endpoint{Host: "b.example.invalid", Port: 80}, Auth: &ir.NoAuth{Kind: ir.AuthNone},
		Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}})
	chainID := ir.ID("33333333-3333-4333-8333-333333333333")
	chain := resource(ir.KindChain, chainID, &ir.Chain{SchemaVersion: 1, Hops: []ir.NodeRef{{NodeID: a.Metadata.ResourceID}, {NodeID: b.Metadata.ResourceID}}, FailurePolicy: ir.FailClosed})
	resources := map[ir.ID]ir.Resource{a.Metadata.ResourceID: a, b.Metadata.ResourceID: b, chainID: chain}
	result := Expand(resources, []ir.ID{chainID}, Options{})
	if len(result.Diagnostics) != 0 || len(result.Order) != 3 {
		t.Fatalf("chain expansion failed: %#v", result)
	}

	missing := Expand(resources, []ir.ID{chainID}, Options{Exclude: map[ir.ID]bool{b.Metadata.ResourceID: true}})
	if len(missing.Diagnostics) == 0 || missing.Diagnostics[0].Code != "DEPENDENCY_EXCLUDED" {
		t.Fatal("excluded hop was omitted instead of rejected")
	}

	broken := chain
	broken.Payload = &ir.Chain{SchemaVersion: 1, Hops: []ir.NodeRef{{NodeID: a.Metadata.ResourceID}, {NodeID: "44444444-4444-4444-8444-444444444444"}}, FailurePolicy: ir.FailClosed}
	resources[chainID] = broken
	absent := Expand(resources, []ir.ID{chainID}, Options{})
	if len(absent.Diagnostics) == 0 || absent.Diagnostics[0].Code != ir.ReferenceMissing {
		t.Fatal("missing hop was not located")
	}

	kind := Expand(map[ir.ID]ir.Resource{chainID: chain, a.Metadata.ResourceID: a, b.Metadata.ResourceID: chain}, []ir.ID{chainID}, Options{})
	if !hasCode(kind, ir.ReferenceKind) {
		t.Fatal("wrong hop kind was accepted")
	}

	cyclic := Expand(map[ir.ID]ir.Resource{a.Metadata.ResourceID: a, b.Metadata.ResourceID: b}, []ir.ID{a.Metadata.ResourceID}, Options{Refs: func(resource ir.Resource) ([]catalog.Reference, error) {
		if resource.Metadata.ResourceID == a.Metadata.ResourceID {
			return []catalog.Reference{{TargetID: b.Metadata.ResourceID, ExpectedKind: ir.KindNode, Path: "/cycle"}}, nil
		}
		return []catalog.Reference{{TargetID: a.Metadata.ResourceID, ExpectedKind: ir.KindNode, Path: "/cycle"}}, nil
	}})
	if !hasCode(cyclic, ir.InvalidValue) {
		t.Fatal("cycle was not diagnosed")
	}

	limited := Expand(resources, []ir.ID{chainID}, Options{MaxResources: 1})
	if !hasCode(limited, ir.InvalidValue) {
		t.Fatal("expansion limit was not enforced")
	}
}

func resource(kind ir.ResourceKind, id ir.ID, payload ir.ResourcePayload) ir.Resource {
	return ir.Resource{Metadata: ir.Metadata{ResourceID: id, ScopeID: "10000000-0000-4000-8000-000000000001", Kind: kind, Revision: 1, SchemaVersion: 1, Name: string(kind), Tags: []string{}, Enabled: true, SecurityEpoch: 1}, Payload: payload}
}

func hasCode(result Result, code ir.DiagnosticCode) bool {
	for _, item := range result.Diagnostics {
		if item.Code == code {
			return true
		}
	}
	return false
}
