package catalog

import (
	"errors"
	"strconv"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

type PolicyGroupListOptions struct {
	Tag     string
	Enabled *bool
	After   *Position
	Limit   int
}

type PolicyGroupPage struct {
	Items []ir.Resource
	Next  *Position
}

// ResolvePolicyMembers checks the complete P0 closure in one caller-supplied
// snapshot. The resolver must read within the caller's transaction or frozen
// input. Node and Chain are the only members; chain hops remain concrete nodes.
func ResolvePolicyMembers(scope ir.ID, group ir.PolicyGroup, resolve func(ir.ID) (ir.Resource, error)) ([]ir.Resource, error) {
	if scope.Validate() != nil || resolve == nil {
		return nil, ErrInvalidInput
	}
	if err := group.Validate(); err != nil {
		return nil, err
	}
	var diagnostics ir.Diagnostics
	resources := []ir.Resource{}
	seen := map[ir.ID]ir.Resource{}
	add := func(code ir.DiagnosticCode, path string, id ir.ID) {
		diagnostics = append(diagnostics, ir.Diagnostic{Code: code, Severity: ir.SeverityError,
			FieldPath: path, ResourceID: id, Message: code.Message()})
	}
	var check func(ir.ID, ir.ResourceKind, string) (ir.Resource, bool, error)
	check = func(id ir.ID, kind ir.ResourceKind, path string) (ir.Resource, bool, error) {
		resource, exists := seen[id]
		if !exists {
			var err error
			resource, err = resolve(id)
			if errors.Is(err, ErrNotFound) {
				add(ir.ReferenceMissing, path, id)
				return ir.Resource{}, false, nil
			}
			if err != nil {
				return ir.Resource{}, false, err
			}
		}
		if resource.Metadata.ResourceID != id || resource.Metadata.Kind != kind {
			add(ir.ReferenceKind, path, id)
			return ir.Resource{}, false, nil
		}
		if resource.Metadata.ScopeID != scope {
			add(ir.ScopeMismatch, path, id)
			return ir.Resource{}, false, nil
		}
		if !resource.Metadata.Enabled {
			add(ir.ResourceDisabled, path, id)
			return ir.Resource{}, false, nil
		}
		if resource.Validate() != nil {
			add(ir.InvalidValue, path, id)
			return ir.Resource{}, false, nil
		}
		if !exists {
			seen[id] = resource
			resources = append(resources, resource)
		}
		return resource, true, nil
	}
	for i, member := range group.Members {
		path := "/members/" + strconv.Itoa(i)
		resource, valid, err := check(member.ResourceID, member.Kind, path+"/resource_id")
		if err != nil {
			return nil, err
		}
		if !valid {
			continue
		}
		if chain, ok := resource.Payload.(*ir.Chain); ok {
			for hopIndex, hop := range chain.Hops {
				if _, _, err := check(hop.NodeID, ir.KindNode, path+"/hops/"+strconv.Itoa(hopIndex)+"/node_id"); err != nil {
					return nil, err
				}
			}
		}
	}
	if len(diagnostics) > 0 {
		return nil, diagnostics
	}
	return resources, nil
}
