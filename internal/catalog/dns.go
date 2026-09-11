package catalog

import (
	"strconv"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

type DNSListOptions = RoutingListOptions
type DNSPage = RoutingPage

// DNSReferences preserves every declared editing reference, including rules
// currently disabled. Resolver IDs are local keys, not catalog resources.
func DNSReferences(profile ir.DNSProfile) []Reference {
	refs := []Reference{}
	for i, resolver := range profile.Resolvers {
		if resolver.Outbound != nil && resolver.Outbound.Type == ir.ResourceRef {
			refs = append(refs, Reference{TargetID: resolver.Outbound.ResourceID, ExpectedKind: resolver.Outbound.Kind,
				Path: "/resolvers/" + strconv.Itoa(i) + "/outbound/resource_id"})
		}
	}
	for i, rule := range profile.Rules {
		for j, id := range rule.Match.RuleSetIDs {
			refs = append(refs, Reference{TargetID: id, ExpectedKind: ir.KindRuleSet,
				Path: "/rules/" + strconv.Itoa(i) + "/match/rule_set_ids/" + strconv.Itoa(j)})
		}
	}
	return refs
}

// ResolveDNSReferences checks outbound closure and domain-only rule sets using
// the caller's consistent read. Explicit local/IP bootstrap is acyclic by
// construction; the typed profile rejects references into business resolvers.
func ResolveDNSReferences(scope ir.ID, profile ir.DNSProfile, resolve func(ir.ID) (ir.Resource, error)) ([]ir.Resource, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	refs := DNSReferences(profile)
	resources, err := ResolveTargetReferences(scope, refs, resolve)
	if err != nil {
		return nil, err
	}
	invalidSets := map[ir.ID]bool{}
	for _, resource := range resources {
		if set, ok := resource.Payload.(*ir.RuleSet); ok {
			for _, entry := range set.Entries {
				if entry.Kind == ir.RuleSetCIDR {
					invalidSets[resource.Metadata.ResourceID] = true
				}
			}
		}
	}
	var diagnostics ir.Diagnostics
	for _, ref := range refs {
		if invalidSets[ref.TargetID] {
			diagnostics = append(diagnostics, ir.Diagnostic{Code: ir.InvalidValue, Severity: ir.SeverityError,
				FieldPath: ref.Path, ResourceID: ref.TargetID, Message: ir.InvalidValue.Message()})
		}
	}
	if len(diagnostics) != 0 {
		return nil, diagnostics
	}
	return resources, nil
}
