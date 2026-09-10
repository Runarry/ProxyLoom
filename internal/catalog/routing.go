package catalog

import (
	"errors"
	"strconv"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

type RoutingListOptions struct {
	Tag     string
	Enabled *bool
	After   *Position
	Limit   int
}

type RoutingPage struct {
	Items []ir.Resource
	Next  *Position
}

// ResolveRoutingReferences checks each explicit target and its full P0 closure
// in the caller's transaction or frozen snapshot. Disabled rules retain their
// editing references and must also refer to valid resources.
func ResolveRoutingReferences(scope ir.ID, profile ir.RoutingProfile, resolve func(ir.ID) (ir.Resource, error)) ([]ir.Resource, error) {
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	refs := []Reference{}
	for i, rule := range profile.Rules {
		path := "/rules/" + strconv.Itoa(i)
		if rule.Action.Type == ir.ResourceRef {
			refs = append(refs, Reference{TargetID: rule.Action.ResourceID, ExpectedKind: rule.Action.Kind, Path: path + "/action/resource_id"})
		}
		for j, id := range rule.Match.RuleSetIDs {
			refs = append(refs, Reference{TargetID: id, ExpectedKind: ir.KindRuleSet, Path: path + "/match/rule_set_ids/" + strconv.Itoa(j)})
		}
	}
	if profile.Final.Type == ir.ResourceRef {
		refs = append(refs, Reference{TargetID: profile.Final.ResourceID, ExpectedKind: profile.Final.Kind, Path: "/final/resource_id"})
	}
	return ResolveTargetReferences(scope, refs, resolve)
}

// ResolveTargetReferences validates explicit editing references and expands
// node/chain/policy targets once each, bounded by the frozen resource budget.
func ResolveTargetReferences(scope ir.ID, refs []Reference, resolve func(ir.ID) (ir.Resource, error)) ([]ir.Resource, error) {
	if scope.Validate() != nil || resolve == nil {
		return nil, ErrInvalidInput
	}
	var diagnostics ir.Diagnostics
	resources := []ir.Resource{}
	seen := map[ir.ID]ir.Resource{}
	type lookup struct {
		resource ir.Resource
		err      error
	}
	lookups := map[ir.ID]lookup{}
	add := func(code ir.DiagnosticCode, path string, id ir.ID) {
		if len(diagnostics) < 16 {
			diagnostics = append(diagnostics, ir.Diagnostic{Code: code, Severity: ir.SeverityError, FieldPath: path, ResourceID: id, Message: code.Message()})
		}
	}
	var check func(ir.ID, ir.ResourceKind, string) error
	check = func(id ir.ID, kind ir.ResourceKind, path string) error {
		if id.Validate() != nil {
			add(ir.InvalidValue, path, id)
			return nil
		}
		r, exists := seen[id]
		if !exists {
			cached, found := lookups[id]
			if !found {
				if len(lookups) >= ir.MaxFrozenResources {
					return ErrInvalidInput
				}
				cached.resource, cached.err = resolve(id)
				lookups[id] = cached
			}
			r = cached.resource
			if errors.Is(cached.err, ErrNotFound) {
				add(ir.ReferenceMissing, path, id)
				return nil
			}
			if cached.err != nil {
				return cached.err
			}
		}
		if r.Metadata.ResourceID != id || r.Metadata.Kind != kind {
			add(ir.ReferenceKind, path, id)
			return nil
		}
		if r.Metadata.ScopeID != scope {
			add(ir.ScopeMismatch, path, id)
			return nil
		}
		if !r.Metadata.Enabled {
			add(ir.ResourceDisabled, path, id)
			return nil
		}
		if exists {
			return nil
		}
		if r.Validate() != nil {
			add(ir.InvalidValue, path, id)
			return nil
		}
		seen[id] = r
		resources = append(resources, r)
		switch payload := r.Payload.(type) {
		case *ir.Chain:
			for i, hop := range payload.Hops {
				if err := check(hop.NodeID, ir.KindNode, path+"/hops/"+strconv.Itoa(i)+"/node_id"); err != nil {
					return err
				}
			}
		case *ir.PolicyGroup:
			for i, member := range payload.Members {
				if err := check(member.ResourceID, member.Kind, path+"/members/"+strconv.Itoa(i)+"/resource_id"); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, ref := range refs {
		if err := check(ref.TargetID, ref.ExpectedKind, ref.Path); err != nil {
			return nil, err
		}
	}
	if len(diagnostics) > 0 {
		return nil, diagnostics
	}
	return resources, nil
}
