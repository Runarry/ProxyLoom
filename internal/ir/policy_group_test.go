package ir_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/schemas"
)

func policyFixture() ir.PolicyGroup {
	member := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: "11111111-1111-4111-8111-111111111111"}
	return ir.PolicyGroup{SchemaVersion: 1, Strategy: ir.PolicyManualSelect, Members: []ir.TargetRef{member},
		DefaultMember: member, HealthCheck: ir.PolicyHealthCheck{Enabled: false}, OnUnavailable: ir.FailClosed}
}

func policyInt(n int) *int { return &n }

func policyHealth() ir.PolicyHealthCheck {
	return ir.PolicyHealthCheck{Enabled: true, URL: "https://health.example.invalid/check", IntervalMS: policyInt(10000), TimeoutMS: policyInt(5000), ToleranceMS: policyInt(0)}
}

func TestPolicyGroupFourStrategiesAndStrictBoundaries(t *testing.T) {
	for _, strategy := range []ir.PolicyStrategy{ir.PolicyFixed, ir.PolicyManualSelect, ir.PolicyLatencyBest, ir.PolicyRoundRobin} {
		t.Run(string(strategy), func(t *testing.T) {
			group := policyFixture()
			group.Strategy, group.HealthCheck = strategy, policyHealth()
			data := mustJSON(t, group)
			if err := schemas.Validate("policy_group", parseValue(t, data)); err != nil {
				t.Fatal("valid policy failed standalone schema")
			}
			decoded, err := ir.DecodePolicyGroup(data)
			if err != nil || !reflect.DeepEqual(decoded, group) {
				t.Fatalf("strategy did not round trip unchanged: %v", err)
			}
		})
	}
	for name, mutate := range map[string]func(*ir.PolicyGroup){
		"empty":     func(g *ir.PolicyGroup) { g.Members = []ir.TargetRef{} },
		"duplicate": func(g *ir.PolicyGroup) { g.Members = append(g.Members, g.Members[0]) },
		"same_id_other_kind": func(g *ir.PolicyGroup) {
			member := g.Members[0]
			member.Kind = ir.KindChain
			g.Members = append(g.Members, member)
		},
		"nested":           func(g *ir.PolicyGroup) { g.Members[0].Kind = ir.KindPolicyGroup },
		"builtin":          func(g *ir.PolicyGroup) { g.Members[0] = ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Direct} },
		"default_absent":   func(g *ir.PolicyGroup) { g.DefaultMember.ResourceID = "22222222-2222-4222-8222-222222222222" },
		"default_kind":     func(g *ir.PolicyGroup) { g.DefaultMember.Kind = ir.KindChain },
		"unknown_strategy": func(g *ir.PolicyGroup) { g.Strategy = "urltest" },
		"direct_fallback":  func(g *ir.PolicyGroup) { g.OnUnavailable = "direct" },
		"version":          func(g *ir.PolicyGroup) { g.SchemaVersion = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			group := policyFixture()
			mutate(&group)
			if group.Validate() == nil {
				t.Fatal("invalid policy construction accepted")
			}
			if _, err := ir.DecodePolicyGroup(mustJSON(t, group)); err == nil {
				t.Fatal("invalid policy JSON accepted")
			}
		})
	}
	group := policyFixture()
	group.Members = nil
	for i := 1; i <= 200; i++ {
		group.Members = append(group.Members, ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode,
			ResourceID: ir.ID(fmt.Sprintf("00000000-0000-4000-8000-%012d", i))})
	}
	group.DefaultMember = group.Members[199]
	if group.Validate() != nil {
		t.Fatal("200 unique members rejected")
	}
	group.Members = append(group.Members, ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: "00000000-0000-4000-8000-000000000201"})
	if group.Validate() == nil {
		t.Fatal("member limit was not enforced")
	}
	valid := string(mustJSON(t, policyFixture()))
	for _, invalid := range []string{
		strings.Replace(valid, `"strategy":"manual_select"`, `"strategy":"manual_select","strategy":"fixed"`, 1),
		strings.Replace(valid, `"strategy":"manual_select"`, `"strategy":null`, 1),
		strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"arbitrary_config":"SYNTHETIC_PRIVATE_VALUE"`, 1),
	} {
		old := policyFixture()
		before := mustJSON(t, old)
		err := json.Unmarshal([]byte(invalid), &old)
		if err == nil || !bytes.Equal(before, mustJSON(t, old)) || strings.Contains(err.Error(), "SYNTHETIC_PRIVATE_VALUE") {
			t.Fatal("strict rejection, diagnostic redaction or receiver isolation failed")
		}
	}
}

func TestPolicyHealthValidation(t *testing.T) {
	for name, mutate := range map[string]func(*ir.PolicyHealthCheck){
		"url_required":       func(h *ir.PolicyHealthCheck) { h.URL = "" },
		"interval_required":  func(h *ir.PolicyHealthCheck) { h.IntervalMS = nil },
		"timeout_required":   func(h *ir.PolicyHealthCheck) { h.TimeoutMS = nil },
		"tolerance_required": func(h *ir.PolicyHealthCheck) { h.ToleranceMS = nil },
		"interval_low":       func(h *ir.PolicyHealthCheck) { h.IntervalMS = policyInt(999) },
		"interval_high":      func(h *ir.PolicyHealthCheck) { h.IntervalMS = policyInt(86400001) },
		"timeout_low":        func(h *ir.PolicyHealthCheck) { h.TimeoutMS = policyInt(99) },
		"timeout_high":       func(h *ir.PolicyHealthCheck) { h.TimeoutMS = policyInt(60001) },
		"tolerance_low":      func(h *ir.PolicyHealthCheck) { h.ToleranceMS = policyInt(-1) },
		"tolerance_high":     func(h *ir.PolicyHealthCheck) { h.ToleranceMS = policyInt(60001) },
		"http":               func(h *ir.PolicyHealthCheck) { h.URL = "http://health.example.invalid/check" },
		"userinfo":           func(h *ir.PolicyHealthCheck) { h.URL = "https://" + "synthetic@health.example.invalid/check" },
		"fragment":           func(h *ir.PolicyHealthCheck) { h.URL += "#fragment" },
		"invalid_port":       func(h *ir.PolicyHealthCheck) { h.URL = "https://health.example.invalid:65536/check" },
		"empty_port":         func(h *ir.PolicyHealthCheck) { h.URL = "https://health.example.invalid:/check" },
		"missing_host":       func(h *ir.PolicyHealthCheck) { h.URL = "https://:443/check" },
	} {
		t.Run(name, func(t *testing.T) {
			health := policyHealth()
			mutate(&health)
			if health.Validate() == nil {
				t.Fatal("invalid client health check accepted")
			}
		})
	}
}

func TestPolicyOverrideMergesOnlyReviewedFieldsWithoutMutation(t *testing.T) {
	group := policyFixture()
	group.HealthCheck = policyHealth()
	base := ir.Resource{Metadata: ir.Metadata{ResourceID: "33333333-3333-4333-8333-333333333333",
		ScopeID: "10000000-0000-4000-8000-000000000001", Kind: ir.KindPolicyGroup, Revision: 3,
		SchemaVersion: 1, Name: "policy", Tags: []string{}, Enabled: true, SecurityEpoch: 1}, Payload: &group}
	before := mustJSON(t, base)
	strategy := ir.PolicyRoundRobin
	health := policyHealth()
	override := ir.PolicyOverride{PolicyGroupID: base.Metadata.ResourceID, Strategy: &strategy, HealthCheck: &health}
	merged, err := ir.MergePolicyOverride(base, override)
	if err != nil || merged.Strategy != ir.PolicyRoundRobin || merged.OnUnavailable != ir.FailClosed || !reflect.DeepEqual(merged.Members, group.Members) {
		t.Fatalf("explicit strategy override failed: %v", err)
	}
	merged.Members[0].ResourceID = "22222222-2222-4222-8222-222222222222"
	*merged.HealthCheck.IntervalMS = 20000
	if !bytes.Equal(before, mustJSON(t, base)) || *health.IntervalMS != 10000 {
		t.Fatal("override mutated a caller-owned member, health check or revision")
	}
	missing := group.DefaultMember
	missing.ResourceID = "22222222-2222-4222-8222-222222222222"
	override.DefaultMember = &missing
	if _, err := ir.MergePolicyOverride(base, override); err == nil {
		t.Fatal("override selected a nonmember")
	}
	override.DefaultMember = nil
	override.PolicyGroupID = missing.ResourceID
	if _, err := ir.MergePolicyOverride(base, override); err == nil {
		t.Fatal("override applied to a different group")
	}
	for _, fields := range []string{``, `,"members":[]`, `,"on_unavailable":"direct"`, `,"strategy":null`, `,"strategy":"urltest"`} {
		data := `{"policy_group_id":"` + string(base.Metadata.ResourceID) + `"` + fields + `}`
		if _, err := ir.DecodePolicyOverride([]byte(data)); err == nil {
			t.Fatal("empty, null or nonwhitelisted target override accepted")
		}
	}
}

func TestFrozenPolicyIncludesChainHopsRegardlessOfResourceOrder(t *testing.T) {
	spec := mustSpec(t)
	var chain ir.Resource
	for _, resource := range spec.Resources {
		if resource.Metadata.Kind == ir.KindChain {
			chain = resource
		}
	}
	member := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindChain, ResourceID: chain.Metadata.ResourceID}
	group := policyFixture()
	group.Members, group.DefaultMember = []ir.TargetRef{member}, member
	policy := ir.Resource{Metadata: chain.Metadata, Payload: &group}
	policy.Metadata.ResourceID, policy.Metadata.Kind = "44444444-4444-4444-8444-444444444444", ir.KindPolicyGroup
	spec.Resources = append(spec.Resources, policy)
	spec.Members = []ir.FrozenRef{{ResourceID: policy.Metadata.ResourceID, Kind: ir.KindPolicyGroup,
		Revision: policy.Metadata.Revision, SecurityEpoch: policy.Metadata.SecurityEpoch}}
	for i := range spec.Resources {
		spec.Resources = append(spec.Resources[1:], spec.Resources[0])
		frozen, err := ir.NewFrozenInput(spec)
		if err != nil || frozen.Validate() != nil {
			t.Fatalf("policy chain closure depended on resource order %d: %v", i, err)
		}
		copy := frozen.Spec()
		for _, resource := range copy.Resources {
			if group, ok := resource.Payload.(*ir.PolicyGroup); ok {
				group.Members[0].ResourceID = "55555555-5555-4555-8555-555555555555"
			}
		}
		if frozen.Validate() != nil {
			t.Fatal("policy snapshot leaked a mutable alias")
		}
	}
	for i, resource := range spec.Resources {
		if resource.Metadata.Kind == ir.KindNode {
			missing := spec
			missing.Resources = append(append([]ir.Resource{}, spec.Resources[:i]...), spec.Resources[i+1:]...)
			if _, err := ir.NewFrozenInput(missing); err == nil {
				t.Fatal("frozen policy accepted a missing chain hop")
			}
			break
		}
	}
}
