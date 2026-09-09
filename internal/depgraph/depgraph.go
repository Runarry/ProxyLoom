// Package depgraph expands typed resource references without database or network access.
package depgraph

import (
	"strconv"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const DefaultMaxResources = 2000

type Options struct {
	ScopeID      ir.ID
	Exclude      map[ir.ID]bool
	MaxResources int
	Refs         func(ir.Resource) ([]catalog.Reference, error)
}

type Result struct {
	Order       []ir.ID
	Resources   []ir.Resource
	Diagnostics ir.Diagnostics
}

func Expand(resources map[ir.ID]ir.Resource, roots []ir.ID, options Options) Result {
	if options.MaxResources == 0 {
		options.MaxResources = DefaultMaxResources
	}
	if options.Refs == nil {
		options.Refs = catalog.ExtractReferences
	}
	if options.Exclude == nil {
		options.Exclude = map[ir.ID]bool{}
	}
	out := Result{Order: []ir.ID{}, Resources: []ir.Resource{}, Diagnostics: ir.Diagnostics{}}
	scope := options.ScopeID
	seen := map[ir.ID]int{} // 0 unknown, 1 visiting, 2 done
	var walk func(id ir.ID, path []ir.ID, via string)
	walk = func(id ir.ID, path []ir.ID, via string) {
		if options.Exclude[id] {
			out.Diagnostics = append(out.Diagnostics, ir.Diagnostic{Code: "DEPENDENCY_EXCLUDED", Severity: ir.SeverityError, ResourceID: id, FieldPath: via, Message: "An explicit exclude removed a required dependency."})
			return
		}
		if seen[id] == 1 {
			out.Diagnostics = append(out.Diagnostics, ir.Diagnostic{Code: "DEPENDENCY_CYCLE", Severity: ir.SeverityError, ResourceID: id, FieldPath: via, Message: "The dependency graph contains a cycle."})
			return
		}
		if seen[id] == 2 {
			return
		}
		resource, ok := resources[id]
		if !ok {
			out.Diagnostics = append(out.Diagnostics, ir.Diagnostic{Code: ir.ReferenceMissing, Severity: ir.SeverityError, ResourceID: id, FieldPath: via, Message: "The referenced resource is missing."})
			return
		}
		if scope == "" {
			scope = resource.Metadata.ScopeID
		}
		if resource.Metadata.ScopeID != scope {
			out.Diagnostics = append(out.Diagnostics, ir.Diagnostic{Code: ir.ScopeMismatch, Severity: ir.SeverityError, ResourceID: id, FieldPath: via, Message: ir.ScopeMismatch.Message()})
			return
		}
		if !resource.Metadata.Enabled {
			out.Diagnostics = append(out.Diagnostics, ir.Diagnostic{Code: ir.ResourceDisabled, Severity: ir.SeverityError, ResourceID: id, FieldPath: via, Message: ir.ResourceDisabled.Message()})
			return
		}
		if len(out.Order)+len(path) >= options.MaxResources {
			out.Diagnostics = append(out.Diagnostics, ir.Diagnostic{Code: ir.InvalidValue, Severity: ir.SeverityError, ResourceID: id, FieldPath: via, Message: "Dependency expansion exceeded the resource limit."})
			return
		}
		seen[id] = 1
		refs, err := options.Refs(resource)
		if err != nil {
			out.Diagnostics = append(out.Diagnostics, ir.Diagnostic{Code: ir.InvalidValue, Severity: ir.SeverityError, ResourceID: id, FieldPath: via, Message: "The resource references could not be expanded."})
			seen[id] = 2
			return
		}
		for _, ref := range refs {
			target, exists := resources[ref.TargetID]
			if exists && target.Metadata.Kind != ref.ExpectedKind {
				out.Diagnostics = append(out.Diagnostics, ir.Diagnostic{Code: ir.ReferenceKind, Severity: ir.SeverityError, ResourceID: ref.TargetID, FieldPath: ref.Path, Message: "The referenced resource has the wrong kind."})
				continue
			}
			walk(ref.TargetID, append(append([]ir.ID{}, path...), id), ref.Path)
		}
		seen[id] = 2
		out.Order = append(out.Order, id)
		out.Resources = append(out.Resources, resource)
	}
	for i, root := range roots {
		walk(root, nil, "/roots/"+strconv.Itoa(i))
	}
	return out
}
