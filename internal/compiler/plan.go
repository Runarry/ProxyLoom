package compiler

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const (
	PlanContentType   = "application/vnd.proxyloom.compile-plan+json"
	PlanSchemaVersion = 1
	KindIndependent   = "independent"
	KindChainH1       = "chain_h1"
	KindChainH2       = "chain_h2"
)

type Plan struct {
	SchemaVersion          int                      `json:"schema_version"`
	SnapshotID             ir.ID                    `json:"snapshot_id"`
	ScopeID                ir.ID                    `json:"scope_id"`
	CatalogRevision        int64                    `json:"catalog_revision"`
	SecurityEpoch          int64                    `json:"security_epoch"`
	Target                 PlanTarget               `json:"target"`
	CapabilityState        capability.State         `json:"capability_state"`
	RequiredCapabilities   []string                 `json:"required_capabilities"`
	UnverifiedCapabilities []string                 `json:"unverified_capabilities"`
	Outbounds              []PlanOutbound           `json:"outbounds"`
	Chains                 []PlanChain              `json:"chains"`
	Policies               []adapter.PolicyInstance `json:"policies,omitempty"`
	FinalTag               string                   `json:"final_tag,omitempty"`
	Routing                *adapter.RoutingInput    `json:"routing,omitempty"`
	DNS                    *adapter.DNSInput        `json:"dns,omitempty"`
	Preset                 *ir.ClientPreset         `json:"preset,omitempty"`
	RuleSets               []ir.FrozenRef           `json:"rule_sets,omitempty"`
	Resources              []ir.FrozenRef           `json:"resources"`
}

type PlanTarget struct {
	Key                  string              `json:"key"`
	CoreFamily           ir.CoreFamily       `json:"core_family"`
	CoreBuildID          ir.ID               `json:"core_build_id"`
	CoreBuildSHA256      string              `json:"core_build_sha256"`
	AdapterVersion       string              `json:"adapter_version"`
	ClientPresetID       ir.ID               `json:"client_preset_id"`
	ClientPresetRevision int64               `json:"client_preset_revision"`
	Format               ir.OutputFormat     `json:"format"`
	PolicyOverrides      []ir.PolicyOverride `json:"policy_overrides,omitempty"`
}

type PlanOutbound struct {
	Tag          string      `json:"tag"`
	Kind         string      `json:"kind"`
	ResourceID   ir.ID       `json:"resource_id"`
	Revision     int64       `json:"revision"`
	Protocol     ir.Protocol `json:"protocol"`
	SourceNodeID ir.ID       `json:"source_node_id,omitempty"`
	DialerTag    string      `json:"dialer_tag,omitempty"`
}

type PlanChain struct {
	ResourceID    ir.ID            `json:"resource_id"`
	Revision      int64            `json:"revision"`
	TagH1         string           `json:"tag_h1"`
	TagH2         string           `json:"tag_h2"`
	ExitTag       string           `json:"exit_tag"`
	Hop1NodeID    ir.ID            `json:"hop1_node_id"`
	Hop2NodeID    ir.ID            `json:"hop2_node_id"`
	FailurePolicy ir.FailurePolicy `json:"failure_policy"`
}

func BuildPlan(graph Graph) Plan {
	plan := Plan{
		SchemaVersion:          PlanSchemaVersion,
		SnapshotID:             graph.SnapshotID,
		ScopeID:                graph.ScopeID,
		CatalogRevision:        graph.CatalogRevision,
		SecurityEpoch:          graph.SecurityEpoch,
		CapabilityState:        graph.CapabilityState,
		RequiredCapabilities:   slices.Clone(graph.RequiredKeys),
		UnverifiedCapabilities: slices.Clone(graph.UnverifiedKeys),
		Target: PlanTarget{
			Key:                  graph.Target.Key,
			CoreFamily:           graph.Target.CoreFamily,
			CoreBuildID:          graph.Target.CoreBuildID,
			CoreBuildSHA256:      graph.Target.CoreBuildSHA256,
			AdapterVersion:       graph.Target.AdapterVersion,
			ClientPresetID:       graph.Target.ClientPresetID,
			ClientPresetRevision: graph.Target.ClientPresetRevision,
			Format:               graph.Target.Format,
		},
	}
	for _, id := range slices.Sorted(maps.Keys(graph.Target.PolicyOverrides)) {
		plan.Target.PolicyOverrides = append(plan.Target.PolicyOverrides, graph.Target.PolicyOverrides[id].Clone())
	}
	for _, policy := range graph.Policies {
		policy.Members = slices.Clone(policy.Members)
		policy.Health = policy.Health.Clone()
		plan.Policies = append(plan.Policies, policy)
	}
	plan.FinalTag = graph.FinalTag
	plan.RuleSets = slices.Clone(graph.RuleSets)
	plan.Resources = slices.Clone(graph.Resources)
	if graph.Routing != nil {
		copy := *graph.Routing
		copy.Rules = clonePlanRules(copy.Rules)
		plan.Routing = &copy
	}
	if graph.DNS != nil {
		copy := *graph.DNS
		copy.Profile = copy.Profile.Clone()
		copy.OutboundTags = maps.Clone(copy.OutboundTags)
		copy.Rules = clonePlanRules(copy.Rules)
		plan.DNS = &copy
	}
	if graph.Preset != nil {
		copy := graph.Preset.Clone()
		plan.Preset = &copy
	}
	if plan.RequiredCapabilities == nil {
		plan.RequiredCapabilities = []string{}
	}
	if plan.UnverifiedCapabilities == nil {
		plan.UnverifiedCapabilities = []string{}
	}
	for _, outbound := range graph.Independents {
		node := outbound.Resource.Payload.(*ir.Node)
		plan.Outbounds = append(plan.Outbounds, PlanOutbound{
			Tag:        outbound.Tag,
			Kind:       KindIndependent,
			ResourceID: outbound.Resource.Metadata.ResourceID,
			Revision:   outbound.Resource.Metadata.Revision,
			Protocol:   node.Protocol,
		})
	}
	for _, chain := range graph.Chains {
		hop1 := chain.Hop1.Payload.(*ir.Node)
		hop2 := chain.Hop2.Payload.(*ir.Node)
		plan.Outbounds = append(plan.Outbounds,
			PlanOutbound{
				Tag:          chain.TagH1,
				Kind:         KindChainH1,
				ResourceID:   chain.ResourceID,
				Revision:     chain.Revision,
				Protocol:     hop1.Protocol,
				SourceNodeID: chain.Hop1.Metadata.ResourceID,
			},
			PlanOutbound{
				Tag:          chain.TagH2,
				Kind:         KindChainH2,
				ResourceID:   chain.ResourceID,
				Revision:     chain.Revision,
				Protocol:     hop2.Protocol,
				SourceNodeID: chain.Hop2.Metadata.ResourceID,
				DialerTag:    chain.TagH1,
			},
		)
		plan.Chains = append(plan.Chains, PlanChain{
			ResourceID:    chain.ResourceID,
			Revision:      chain.Revision,
			TagH1:         chain.TagH1,
			TagH2:         chain.TagH2,
			ExitTag:       chain.TagH2,
			Hop1NodeID:    chain.Hop1.Metadata.ResourceID,
			Hop2NodeID:    chain.Hop2.Metadata.ResourceID,
			FailurePolicy: chain.FailurePolicy,
		})
	}
	if plan.Outbounds == nil {
		plan.Outbounds = []PlanOutbound{}
	}
	if plan.Chains == nil {
		plan.Chains = []PlanChain{}
	}
	slices.SortFunc(plan.Outbounds, func(a, b PlanOutbound) int { return compareString(a.Tag, b.Tag) })
	slices.SortFunc(plan.Chains, func(a, b PlanChain) int { return compareString(a.TagH1, b.TagH1) })
	return plan
}

func clonePlanRules(rules []adapter.RouteRule) []adapter.RouteRule {
	copy := slices.Clone(rules)
	var cloneCondition func(adapter.Condition) adapter.Condition
	cloneCondition = func(c adapter.Condition) adapter.Condition {
		c.Values = slices.Clone(c.Values)
		c.Terms = slices.Clone(c.Terms)
		for i := range c.Terms {
			c.Terms[i] = cloneCondition(c.Terms[i])
		}
		return c
	}
	for i := range copy {
		copy[i].Condition = cloneCondition(copy[i].Condition)
	}
	return copy
}

func marshalPlan(plan Plan) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(plan); err != nil {
		return nil, err
	}
	// Encoder adds a trailing newline; keep it so repeats stay identical.
	return buf.Bytes(), nil
}
