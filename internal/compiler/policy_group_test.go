package compiler

import (
	"context"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestCompilePolicyStrategiesAreExplicitlyUnsupported(t *testing.T) {
	compiler := mustCompiler(t)
	for _, strategy := range []ir.PolicyStrategy{ir.PolicyManualSelect, ir.PolicyRoundRobin} {
		for _, family := range []ir.CoreFamily{ir.Xray, ir.SingBox, ir.Mihomo} {
			if strategy == ir.PolicyRoundRobin && family != ir.SingBox {
				continue
			}
			t.Run(string(strategy)+"/"+string(family), func(t *testing.T) {
				spec := mustFrozen(t, "frozen-chain-a-b.json").Spec()
				var chain ir.Resource
				for _, resource := range spec.Resources {
					if resource.Metadata.Kind == ir.KindChain {
						chain = resource
					}
				}
				member := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindChain, ResourceID: chain.Metadata.ResourceID}
				policy := ir.Resource{Metadata: chain.Metadata, Payload: &ir.PolicyGroup{SchemaVersion: 1, Strategy: strategy,
					Members: []ir.TargetRef{member}, DefaultMember: member, HealthCheck: ir.PolicyHealthCheck{Enabled: false}, OnUnavailable: ir.FailClosed}}
				policy.Metadata.Kind, policy.Metadata.ResourceID = ir.KindPolicyGroup, "55555555-5555-4555-8555-555555555555"
				spec.Resources = append(spec.Resources, policy)
				spec.Members = []ir.FrozenRef{{ResourceID: policy.Metadata.ResourceID, Kind: ir.KindPolicyGroup, Revision: policy.Metadata.Revision, SecurityEpoch: policy.Metadata.SecurityEpoch}}
				input, err := ir.NewFrozenInput(spec)
				if err != nil {
					t.Fatal(err)
				}
				var target ir.Target
				for _, item := range spec.Targets {
					if item.CoreFamily == family {
						target = item
					}
				}
				artifact, diagnostics, err := compiler.Compile(context.Background(), input, target)
				if err == nil || len(artifact.Bytes) != 0 || len(diagnostics) != 1 || diagnostics[0].Code != ir.CapabilityUnsupported ||
					diagnostics[0].ResourceID != policy.Metadata.ResourceID || diagnostics[0].TargetKey != target.Key || diagnostics[0].FieldPath != "/payload/strategy" {
					t.Fatal("policy silently degraded or lost its unsupported strategy diagnostic")
				}
			})
		}
	}
}
