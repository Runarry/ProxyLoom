package compiler

import (
	"slices"
	"strconv"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func prepareOrchestration(graph *Graph, spec ir.FrozenInputSpec, resources map[ir.ID]ir.Resource, policies []ir.Resource, labels map[string][]string) error {
	fail := func(code ir.DiagnosticCode, path string, id ir.ID) error {
		return ir.Diagnostics{compileIssue(code, path, graph.Target.Key, id)}
	}
	slices.SortFunc(policies, func(a, b ir.Resource) int {
		return compareString(string(a.Metadata.ResourceID), string(b.Metadata.ResourceID))
	})
	controlled := false
	if preset, ok := resources[graph.Target.ClientPresetID].Payload.(*ir.ClientPreset); ok {
		controlled = preset.ControlAPI.Enabled
	}
	tags := map[ir.ID]string{}
	normalizedRules := 0
	checkRuleLimit := func(match ir.RouteMatch, path string, id ir.ID) error {
		count := len(match.DomainExact) + len(match.DomainSuffix) + len(match.IPCIDRs) + len(match.DestinationPorts) + len(match.Network)
		for _, setID := range match.RuleSetIDs {
			meta := resources[setID].Metadata
			graph.RuleSets = append(graph.RuleSets, ir.FrozenRef{ResourceID: setID, Kind: ir.KindRuleSet, Revision: meta.Revision, SecurityEpoch: meta.SecurityEpoch})
			if set, ok := resources[setID].Payload.(*ir.RuleSet); ok {
				count += len(set.Entries)
			}
		}
		normalizedRules += max(1, count)
		if normalizedRules > adapter.MaxRules {
			return fail(ir.InputLimitExceeded, path, id)
		}
		return nil
	}
	for _, item := range graph.Independents {
		tags[item.Resource.Metadata.ResourceID] = item.Tag
	}
	for _, item := range graph.Chains {
		tags[item.ResourceID] = item.TagH2
	}
	for _, resource := range policies {
		tags[resource.Metadata.ResourceID] = labels[policyMaterial(resource.Metadata.ResourceID, resource.Metadata.Revision)][0]
	}
	resolve := func(ref ir.TargetRef) string {
		if ref.Type == ir.BuiltinRef {
			if ref.Builtin == ir.Direct {
				return "direct"
			}
			return adapter.BlockTag
		}
		return tags[ref.ResourceID]
	}
	for _, resource := range policies {
		p := resource.Payload.(*ir.PolicyGroup)
		if p.Strategy == ir.PolicyManualSelect && (!controlled || graph.Target.CoreFamily == ir.Xray) {
			d := compileIssue(ir.CapabilityUnsupported, "/payload/strategy", graph.Target.Key, resource.Metadata.ResourceID)
			d.SuggestedAction = "The approved file preset has no runtime selection control API; use an explicit fixed strategy override."
			return ir.Diagnostics{d}
		}
		if p.Strategy == ir.PolicyRoundRobin && graph.Target.CoreFamily == ir.SingBox {
			return fail(ir.CapabilityUnsupported, "/payload/strategy", resource.Metadata.ResourceID)
		}
		if p.Strategy == ir.PolicyRoundRobin && graph.Target.CoreFamily == ir.Mihomo && !p.HealthCheck.Enabled {
			return fail(ir.CompileUnmappedField, "/payload/health_check/enabled", resource.Metadata.ResourceID)
		}
		if (p.Strategy == ir.PolicyFixed || p.Strategy == ir.PolicyManualSelect) && p.HealthCheck.Enabled {
			return fail(ir.CompileUnmappedField, "/payload/health_check/enabled", resource.Metadata.ResourceID)
		}
		if !p.HealthCheck.Enabled && (p.HealthCheck.URL != "" || p.HealthCheck.IntervalMS != nil || p.HealthCheck.TimeoutMS != nil || p.HealthCheck.ToleranceMS != nil) {
			return fail(ir.CompileUnmappedField, "/payload/health_check", resource.Metadata.ResourceID)
		}
		if p.Strategy == ir.PolicyLatencyBest && !p.HealthCheck.Enabled {
			return fail(ir.CompileUnmappedField, "/payload/health_check/enabled", resource.Metadata.ResourceID)
		}
		if p.HealthCheck.Enabled && graph.Target.CoreFamily != ir.Mihomo {
			return fail(ir.CompileUnmappedField, "/payload/health_check/timeout_ms", resource.Metadata.ResourceID)
		}
		item := adapter.PolicyInstance{Tag: tags[resource.Metadata.ResourceID], ResourceID: resource.Metadata.ResourceID, Strategy: p.Strategy, Health: p.HealthCheck.Clone()}
		memberTags := map[ir.ID]string{}
		for index, member := range p.Members {
			prefix := item.Tag + "_m" + strconv.Itoa(index+1)
			r := resources[member.ResourceID]
			switch payload := r.Payload.(type) {
			case *ir.Node:
				memberTags[member.ResourceID] = prefix + "_n"
				graph.Independents = append(graph.Independents, IndependentOutbound{Tag: prefix + "_n", Resource: r})
			case *ir.Chain:
				memberTags[member.ResourceID] = prefix + "_h2"
				graph.Chains = append(graph.Chains, ChainInstance{ResourceID: r.Metadata.ResourceID, Revision: r.Metadata.Revision, TagH1: prefix + "_h1", TagH2: prefix + "_h2", Hop1: resources[payload.Hops[0].NodeID], Hop2: resources[payload.Hops[1].NodeID], FailurePolicy: payload.FailurePolicy})
			}
		}
		item.Default = memberTags[p.DefaultMember.ResourceID]
		// The default is the first candidate at startup, without altering the
		// frozen resource order or turning a runtime strategy into a fixed one.
		item.Members = append(item.Members, item.Default)
		for _, member := range p.Members {
			if tag := memberTags[member.ResourceID]; tag != item.Default {
				item.Members = append(item.Members, tag)
			}
		}
		graph.Policies = append(graph.Policies, item)
	}
	slices.SortFunc(graph.Policies, func(a, b adapter.PolicyInstance) int { return compareString(a.Tag, b.Tag) })
	if spec.RoutingProfile != nil {
		resource := resources[spec.RoutingProfile.ResourceID]
		profile := resource.Payload.(*ir.RoutingProfile)
		graph.FinalTag = resolve(profile.Final)
		graph.Routing = &adapter.RoutingInput{ResourceID: resource.Metadata.ResourceID, Mode: profile.DomainResolutionMode}
		for i, rule := range profile.Rules {
			if !rule.Enabled {
				continue
			}
			path := "/payload/rules/" + strconv.Itoa(i)
			if err := checkRuleLimit(rule.Match, path, resource.Metadata.ResourceID); err != nil {
				return err
			}
			condition, err := routeCondition(rule.Match, resources, false)
			if err != nil {
				return fail(ir.CompileUnmappedField, path+"/match/rule_set_ids", resource.Metadata.ResourceID)
			}
			graph.Routing.Rules = append(graph.Routing.Rules, adapter.RouteRule{Condition: condition, Target: resolve(rule.Action), FieldPath: path})
		}
	}
	if spec.DNSProfile != nil {
		resource := resources[spec.DNSProfile.ResourceID]
		profile := resource.Payload.(*ir.DNSProfile)
		graph.DNS = &adapter.DNSInput{ResourceID: resource.Metadata.ResourceID, Profile: profile.Clone(), OutboundTags: map[string]string{}}
		for i, resolver := range profile.Resolvers {
			if resolver.Outbound != nil {
				tag := resolve(*resolver.Outbound)
				if resolver.Kind == ir.DNSUDP && tag != "direct" {
					return fail(ir.CompileUnmappedField, "/payload/resolvers/"+strconv.Itoa(i)+"/outbound", resource.Metadata.ResourceID)
				}
				graph.DNS.OutboundTags[resolver.ResolverID] = tag
			}
		}
		for i, rule := range profile.Rules {
			if !rule.Enabled {
				continue
			}
			path := "/payload/rules/" + strconv.Itoa(i)
			if err := checkRuleLimit(ir.RouteMatch{DomainExact: rule.Match.DomainExact, DomainSuffix: rule.Match.DomainSuffix, RuleSetIDs: rule.Match.RuleSetIDs}, path, resource.Metadata.ResourceID); err != nil {
				return err
			}
			condition, err := routeCondition(ir.RouteMatch{DomainExact: rule.Match.DomainExact, DomainSuffix: rule.Match.DomainSuffix, RuleSetIDs: rule.Match.RuleSetIDs}, resources, true)
			if err != nil {
				return fail(ir.CompileUnmappedField, path+"/match/rule_set_ids", resource.Metadata.ResourceID)
			}
			graph.DNS.Rules = append(graph.DNS.Rules, adapter.RouteRule{Condition: condition, Target: rule.ResolverID, FieldPath: path})
		}
	}
	if resource, ok := resources[graph.Target.ClientPresetID]; ok {
		preset, ok := resource.Payload.(*ir.ClientPreset)
		if !ok || resource.Metadata.Revision != graph.Target.ClientPresetRevision || preset.CoreFamily != graph.Target.CoreFamily || preset.Format != graph.Target.Format {
			return fail(ir.CompileTargetMismatch, "/client_preset_id", graph.Target.ClientPresetID)
		}
		copy := preset.Clone()
		graph.Preset = &copy
		if graph.DNS == nil {
			return fail(ir.ReferenceMissing, "/dns_profile", resource.Metadata.ResourceID)
		}
		if graph.Routing == nil {
			return fail(ir.ReferenceMissing, "/routing_profile", resource.Metadata.ResourceID)
		}
	} else if graph.Routing != nil || graph.DNS != nil || len(graph.Policies) > 0 {
		return fail(ir.ReferenceMissing, "/client_preset_id", graph.Target.ClientPresetID)
	}
	slices.SortFunc(graph.RuleSets, func(a, b ir.FrozenRef) int { return compareString(string(a.ResourceID), string(b.ResourceID)) })
	graph.RuleSets = slices.CompactFunc(graph.RuleSets, func(a, b ir.FrozenRef) bool { return a.ResourceID == b.ResourceID })
	return nil
}

func routeCondition(match ir.RouteMatch, resources map[ir.ID]ir.Resource, dns bool) (adapter.Condition, error) {
	root := adapter.Condition{Kind: "and"}
	add := func(kind string, values []string) {
		if len(values) > 0 {
			root.Terms = append(root.Terms, adapter.Condition{Kind: kind, Values: slices.Clone(values)})
		}
	}
	add("domain", match.DomainExact)
	add("domain_suffix", match.DomainSuffix)
	add("ip_cidr", match.IPCIDRs)
	var ports, networks []string
	for _, port := range match.DestinationPorts {
		value := strconv.Itoa(port.From)
		if port.To != port.From {
			value += ":" + strconv.Itoa(port.To)
		}
		ports = append(ports, value)
	}
	for _, network := range match.Network {
		networks = append(networks, string(network))
	}
	add("port", ports)
	add("network", networks)
	if len(match.RuleSetIDs) > 0 {
		set := adapter.Condition{Kind: "or"}
		for _, id := range match.RuleSetIDs {
			rules, ok := resources[id].Payload.(*ir.RuleSet)
			if !ok {
				return adapter.Condition{}, ir.Diagnostics{compileIssue(ir.ReferenceMissing, "", "", id)}
			}
			for _, entry := range rules.Entries {
				kind, value := "ip_cidr", entry.CIDR
				if entry.Kind == ir.RuleSetDomain {
					kind, value = "domain", entry.Domain
					if entry.Match == ir.DomainSuffix {
						kind = "domain_suffix"
					}
				} else if dns {
					return adapter.Condition{}, ir.Diagnostics{compileIssue(ir.CompileUnmappedField, "", "", id)}
				}
				set.Terms = append(set.Terms, adapter.Condition{Kind: kind, Values: []string{value}})
			}
		}
		root.Terms = append(root.Terms, set)
	}
	if len(root.Terms) == 1 {
		return root.Terms[0], nil
	}
	return root, nil
}
