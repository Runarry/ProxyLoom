package compiler

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strconv"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const ChainTwoHop = "chain.two_hop.tcp"

type IndependentOutbound = adapter.IndependentOutbound
type ChainInstance = adapter.ChainInstance

// Graph is the deterministic compile expansion. It may contain credentials via
// node resources and must not be logged.
type Graph struct {
	SnapshotID      ir.ID
	ScopeID         ir.ID
	CatalogRevision int64
	SecurityEpoch   int64
	Target          ir.Target
	Build           capability.Build
	CapabilityState capability.State
	Independents    []IndependentOutbound
	Chains          []ChainInstance
	Policies        []adapter.PolicyInstance
	FinalTag        string
	Routing         *adapter.RoutingInput
	DNS             *adapter.DNSInput
	Preset          *ir.ClientPreset
	RuleSets        []ir.FrozenRef
	Resources       []ir.FrozenRef
	RequiredKeys    []string
	UnverifiedKeys  []string
}

func (Graph) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "Graph{[REDACTED]}") }
func (Graph) LogValue() slog.Value           { return slog.StringValue("Graph{[REDACTED]}") }

func (c *Compiler) Prepare(input ir.FrozenInput, target ir.Target) (Graph, []ir.Diagnostic, error) {
	if err := input.Validate(); err != nil {
		return Graph{}, asDiagnostics(err), err
	}
	pinned, ok := input.Target(target.Key)
	if !ok || !pinned.Equal(target) {
		d := compileIssue(ir.CompileTargetMismatch, "", target.Key, "")
		return Graph{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
	format, formatOK := expectedFormat(target.CoreFamily)
	if !formatOK || target.Format != format {
		d := compileIssue(ir.CompileFormatMismatch, "/format", target.Key, "")
		return Graph{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
	if target.AdapterVersion != c.catalog.AdapterVersion {
		d := compileIssue(ir.CompileAdapterVersion, "/adapter_version", target.Key, "")
		return Graph{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
	build, err := c.catalog.Build(target.CoreBuildID)
	if err != nil {
		d := compileIssue(ir.CompileUnknownBuild, "/core_build_id", target.Key, "")
		return Graph{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
	if build.Family != target.CoreFamily {
		d := compileIssue(ir.CompileUnknownBuild, "/core_family", target.Key, "")
		return Graph{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
	if authErr := c.catalog.Authenticate(target.CoreBuildID, target.CoreBuildSHA256, "", ""); authErr != nil {
		code := ir.CompileDigestMismatch
		path := "/core_build_sha256"
		if authErr == capability.ErrUnknownBuild {
			code = ir.CompileUnknownBuild
			path = "/core_build_id"
		}
		d := compileIssue(code, path, target.Key, "")
		return Graph{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}

	spec := input.Spec()
	resources := make(map[ir.ID]ir.Resource, len(spec.Resources))
	for _, resource := range spec.Resources {
		if override, ok := target.PolicyOverrides[resource.Metadata.ResourceID]; ok {
			merged, err := ir.MergePolicyOverride(resource, override)
			if err != nil {
				return Graph{}, asDiagnostics(err), err
			}
			resource.Payload = &merged
		}
		resources[resource.Metadata.ResourceID] = resource
	}

	var items []labelItem
	var independents []ir.Resource
	var chains []ir.Resource
	var policies []ir.Resource
	seenNode := map[ir.ID]struct{}{}
	seenChain := map[ir.ID]struct{}{}
	seenPolicy := map[ir.ID]struct{}{}
	var addResource func(ir.ID) error
	addResource = func(id ir.ID) error {
		resource := resources[id]
		switch resource.Payload.(type) {
		case *ir.Node:
			if _, exists := seenNode[id]; exists {
				return nil
			}
			seenNode[id] = struct{}{}
			independents = append(independents, resource)
			items = append(items, nodeLabelItem(resource.Metadata.ResourceID, resource.Metadata.Revision))
		case *ir.Chain:
			if _, exists := seenChain[id]; exists {
				return nil
			}
			seenChain[id] = struct{}{}
			chains = append(chains, resource)
			items = append(items, chainLabelItem(resource.Metadata.ResourceID, resource.Metadata.Revision))
		case *ir.PolicyGroup:
			if _, exists := seenPolicy[id]; exists {
				return nil
			}
			seenPolicy[id] = struct{}{}
			policies = append(policies, resource)
			items = append(items, policyLabelItem(id, resource.Metadata.Revision))
		default:
			d := compileIssue(ir.InvalidUnion, "/members", target.Key, id)
			return ir.Diagnostics{d}
		}
		return nil
	}
	for _, member := range spec.Members {
		if err := addResource(member.ResourceID); err != nil {
			return Graph{}, asDiagnostics(err), err
		}
	}
	for _, original := range spec.Resources {
		resource := resources[original.Metadata.ResourceID]
		var refs []ir.TargetRef
		switch payload := resource.Payload.(type) {
		case *ir.RoutingProfile:
			refs = append(refs, payload.Final)
			for _, rule := range payload.Rules {
				refs = append(refs, rule.Action)
			}
		case *ir.DNSProfile:
			for _, resolver := range payload.Resolvers {
				if resolver.Outbound != nil {
					refs = append(refs, *resolver.Outbound)
				}
			}
		}
		for _, ref := range refs {
			if ref.Type == ir.ResourceRef {
				if err := addResource(ref.ResourceID); err != nil {
					return Graph{}, asDiagnostics(err), err
				}
			}
		}
	}
	labels, err := c.labels.assign(items)
	if err != nil {
		return Graph{}, asDiagnostics(err), err
	}
	count := len(independents) + 2*len(chains)
	for _, r := range policies {
		for _, m := range r.Payload.(*ir.PolicyGroup).Members {
			count++
			if m.Kind == ir.KindChain {
				count++
			}
		}
	}
	if count > adapter.MaxOutbounds {
		d := compileIssue(ir.InputLimitExceeded, "/outbounds", target.Key, "")
		return Graph{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}

	graph := Graph{
		SnapshotID:      spec.SnapshotID,
		ScopeID:         spec.ScopeID,
		CatalogRevision: spec.CatalogRevision,
		SecurityEpoch:   spec.SecurityEpoch,
		Target:          target,
		Build:           build,
		CapabilityState: capability.Unverified,
	}
	for _, resource := range spec.Resources {
		m := resource.Metadata
		graph.Resources = append(graph.Resources, ir.FrozenRef{ResourceID: m.ResourceID, Kind: m.Kind, Revision: m.Revision, SecurityEpoch: m.SecurityEpoch})
	}
	slices.SortFunc(graph.Resources, func(a, b ir.FrozenRef) int { return compareString(string(a.ResourceID), string(b.ResourceID)) })
	for _, resource := range independents {
		tags := labels[nodeMaterial(resource.Metadata.ResourceID, resource.Metadata.Revision)]
		graph.Independents = append(graph.Independents, IndependentOutbound{Tag: tags[0], Resource: resource})
	}
	for _, resource := range chains {
		chain := resource.Payload.(*ir.Chain)
		tags := labels[chainMaterial(resource.Metadata.ResourceID, resource.Metadata.Revision)]
		hop1 := resources[chain.Hops[0].NodeID]
		hop2 := resources[chain.Hops[1].NodeID]
		graph.Chains = append(graph.Chains, ChainInstance{
			ResourceID:    resource.Metadata.ResourceID,
			Revision:      resource.Metadata.Revision,
			TagH1:         tags[0],
			TagH2:         tags[1],
			Hop1:          hop1,
			Hop2:          hop2,
			FailurePolicy: chain.FailurePolicy,
		})
	}
	slices.SortFunc(graph.Independents, func(a, b IndependentOutbound) int {
		return compareString(a.Tag, b.Tag)
	})
	slices.SortFunc(graph.Chains, func(a, b ChainInstance) int {
		return compareString(string(a.ResourceID), string(b.ResourceID))
	})
	if err := prepareOrchestration(&graph, spec, resources, policies, labels); err != nil {
		return Graph{}, asDiagnostics(err), err
	}
	slices.SortFunc(graph.Independents, func(a, b IndependentOutbound) int { return compareString(a.Tag, b.Tag) })
	slices.SortFunc(graph.Chains, func(a, b ChainInstance) int { return compareString(a.TagH1, b.TagH1) })

	keys, unverified, diags, err := c.collectCapabilities(graph)
	if err != nil {
		return Graph{}, diags, err
	}
	graph.RequiredKeys = keys
	graph.UnverifiedKeys = unverified
	if len(keys) > 0 {
		graph.CapabilityState = capability.Unverified
	}
	return graph, diags, nil
}

func (c *Compiler) collectCapabilities(graph Graph) ([]string, []string, []ir.Diagnostic, error) {
	type req struct {
		key        string
		path       string
		resourceID ir.ID
	}
	var required []req
	addNode := func(resource ir.Resource, path string) error {
		node, ok := resource.Payload.(*ir.Node)
		if !ok || node == nil {
			d := compileIssue(ir.InvalidUnion, path, graph.Target.Key, resource.Metadata.ResourceID)
			return ir.Diagnostics{d}
		}
		key, ok := nodeCapabilityKey(*node, c.catalog.Combinations)
		if !ok {
			d := compileIssue(ir.CompileUnknownCapability, path+"/protocol", graph.Target.Key, resource.Metadata.ResourceID)
			return ir.Diagnostics{d}
		}
		required = append(required, req{key: key, path: path, resourceID: resource.Metadata.ResourceID})
		return nil
	}
	for i, outbound := range graph.Independents {
		if err := addNode(outbound.Resource, "/independents/"+strconv.Itoa(i)); err != nil {
			return nil, nil, asDiagnostics(err), err
		}
	}
	for i, chain := range graph.Chains {
		base := "/chains/" + strconv.Itoa(i)
		if err := addNode(chain.Hop1, base+"/h1"); err != nil {
			return nil, nil, asDiagnostics(err), err
		}
		if err := addNode(chain.Hop2, base+"/h2"); err != nil {
			return nil, nil, asDiagnostics(err), err
		}
		required = append(required, req{key: ChainTwoHop, path: base, resourceID: chain.ResourceID})
	}
	for _, policy := range graph.Policies {
		required = append(required, req{key: "policy." + string(policy.Strategy), path: "/payload/strategy", resourceID: policy.ResourceID})
	}
	if graph.Routing != nil {
		required = append(required, req{key: "routing.ordered", path: "/payload/rules", resourceID: graph.Routing.ResourceID})
	}
	if graph.DNS != nil {
		required = append(required, req{key: "dns.profile", path: "/payload/resolvers", resourceID: graph.DNS.ResourceID})
	}
	if graph.Preset != nil {
		required = append(required, req{key: "client_preset", path: "/payload", resourceID: graph.Target.ClientPresetID})
	}
	for _, set := range graph.RuleSets {
		required = append(required, req{key: "rule_set.inline", path: "/payload/entries", resourceID: set.ResourceID})
	}

	keys := make([]string, 0, len(required))
	unverified := make([]string, 0, len(required))
	seen := map[string]struct{}{}
	seenUnverified := map[string]struct{}{}
	var diags ir.Diagnostics
	for _, item := range required {
		if _, ok := seen[item.key]; !ok {
			keys = append(keys, item.key)
			seen[item.key] = struct{}{}
		}
		record, err := c.catalog.Capability(graph.Build.ID, item.key)
		if err != nil {
			d := compileIssue(ir.CompileUnknownCapability, item.path, graph.Target.Key, item.resourceID)
			return nil, nil, []ir.Diagnostic{d}, ir.Diagnostics{d}
		}
		switch record.State {
		case capability.Unsupported:
			d := compileIssue(ir.CapabilityUnsupported, item.path, graph.Target.Key, item.resourceID)
			return nil, nil, []ir.Diagnostic{d}, ir.Diagnostics{d}
		case capability.Verified:
		default:
			if _, ok := seenUnverified[item.key]; !ok {
				unverified = append(unverified, item.key)
				seenUnverified[item.key] = struct{}{}
			}
			d := compileIssue(ir.CapabilityUnverified, item.path, graph.Target.Key, item.resourceID)
			d.Severity = ir.SeverityInfo
			diags = append(diags, d)
		}
	}
	sort.Strings(keys)
	sort.Strings(unverified)
	return keys, unverified, stableCompileDiagnostics(diags), nil
}

func compareString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func asDiagnostics(err error) []ir.Diagnostic {
	var diags ir.Diagnostics
	if err == nil {
		return nil
	}
	if errors.As(err, &diags) {
		return slices.Clone(diags)
	}
	return []ir.Diagnostic{compileIssue(ir.InvalidSnapshot, "", "", "")}
}

func stableCompileDiagnostics(d ir.Diagnostics) ir.Diagnostics {
	if len(d) == 0 {
		return nil
	}
	sort.SliceStable(d, func(i, j int) bool {
		if d[i].FieldPath != d[j].FieldPath {
			return d[i].FieldPath < d[j].FieldPath
		}
		if d[i].Code != d[j].Code {
			return d[i].Code < d[j].Code
		}
		return string(d[i].ResourceID) < string(d[j].ResourceID)
	})
	return d
}
