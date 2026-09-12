package subscriptions

import (
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/depgraph"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"slices"
	"strconv"
)

func Diagnostic(code ir.DiagnosticCode, id ir.ID, path, target string) ir.Diagnostic {
	return ir.Diagnostic{Code: code, ResourceID: id, FieldPath: path, TargetKey: target, Severity: ir.SeverityError, Message: "The frozen publication input does not meet the required constraints."}
}
func Ref(r ir.Resource) ir.FrozenRef {
	m := r.Metadata
	return ir.FrozenRef{ResourceID: m.ResourceID, Kind: m.Kind, Revision: m.Revision, SecurityEpoch: m.SecurityEpoch}
}

// Select freezes exactly the selected roots and their typed closure. Availability
// is supplied from the same transaction as resources; no live reads occur here.
func Select(snapshot ir.ID, profile ir.Resource, resources map[ir.ID]ir.Resource, stale map[ir.ID]bool, catRevision, authEpoch int64, cores *capability.Catalog) (ir.FrozenInput, []Dependency, error) {
	fail := func(code ir.DiagnosticCode, id ir.ID, path string) (ir.FrozenInput, []Dependency, error) {
		return ir.FrozenInput{}, nil, ir.Diagnostics{Diagnostic(code, id, path, "")}
	}
	p, ok := profile.Payload.(*ir.SubscriptionProfile)
	if !ok || !profile.Metadata.Enabled {
		return fail(ir.ResourceDisabled, profile.Metadata.ResourceID, "/subscription")
	}
	if err := p.Validate(); err != nil {
		return ir.FrozenInput{}, nil, err
	}
	selected := map[ir.ID]bool{}
	excluded := map[ir.ID]bool{}
	for _, id := range p.Members.ExcludeIDs {
		excluded[id] = true
	}
	for _, id := range p.Members.IncludeIDs {
		if !excluded[id] {
			selected[id] = true
		}
	}
	for id, r := range resources {
		if (r.Metadata.Kind == ir.KindNode || r.Metadata.Kind == ir.KindChain || r.Metadata.Kind == ir.KindPolicyGroup) && r.Metadata.Enabled && !stale[id] && !excluded[id] && p.Members.Selector.Matches(r.Metadata.Tags) {
			selected[id] = true
		}
	}
	roots := make([]ir.ID, 0, len(selected)+2+len(p.Targets))
	for id := range selected {
		roots = append(roots, id)
	}
	slices.Sort(roots)
	if len(roots) == 0 {
		return fail(ir.InvalidValue, profile.Metadata.ResourceID, "/members")
	}
	spec := ir.FrozenInputSpec{SchemaVersion: 1, SnapshotID: snapshot, ScopeID: profile.Metadata.ScopeID, CatalogRevision: catRevision, SecurityEpoch: authEpoch, Targets: []ir.Target{}, Members: []ir.FrozenRef{}}
	for _, id := range roots {
		r, ok := resources[id]
		if !ok {
			return fail(ir.ReferenceMissing, id, "/members")
		}
		spec.Members = append(spec.Members, Ref(r))
	}
	for _, root := range []struct {
		id   ir.ID
		kind ir.ResourceKind
		path string
	}{{p.RoutingProfileID, ir.KindRoutingProfile, "/routing_profile_id"}, {p.DNSProfileID, ir.KindDNSProfile, "/dns_profile_id"}} {
		r, ok := resources[root.id]
		if !ok {
			return fail(ir.ReferenceMissing, root.id, root.path)
		}
		if r.Metadata.Kind != root.kind {
			return fail(ir.ReferenceKind, root.id, root.path)
		}
		ref := Ref(r)
		if root.kind == ir.KindRoutingProfile {
			spec.RoutingProfile = &ref
		} else {
			spec.DNSProfile = &ref
		}
		roots = append(roots, root.id)
	}
	for i, t := range p.Targets {
		if !t.IsEnabled() {
			continue
		}
		path := "/targets/" + strconv.Itoa(i)
		b, err := cores.Build(t.CoreBuildID)
		if err != nil {
			return fail(ir.CompileUnknownBuild, t.CoreBuildID, path)
		}
		r, ok := resources[t.ClientPresetID]
		if !ok {
			return fail(ir.ReferenceMissing, t.ClientPresetID, path)
		}
		preset, ok := r.Payload.(*ir.ClientPreset)
		if !ok || preset.CoreFamily != b.Family {
			return fail(ir.ReferenceKind, t.ClientPresetID, path)
		}
		if err := t.CheckPreset(*preset); err != nil {
			return ir.FrozenInput{}, nil, err
		}
		target := ir.Target{Key: t.Key, CoreFamily: b.Family, CoreBuildID: b.ID, CoreBuildSHA256: b.BinarySHA256, AdapterVersion: cores.AdapterVersion, ClientPresetID: t.ClientPresetID, ClientPresetRevision: r.Metadata.Revision, Format: t.Format, PolicyOverrides: map[ir.ID]ir.PolicyOverride{}}
		for _, o := range t.PolicyOverrides {
			target.PolicyOverrides[o.PolicyGroupID] = o.Clone()
		}
		spec.Targets = append(spec.Targets, target)
		roots = append(roots, t.ClientPresetID)
	}
	if len(spec.Targets) == 0 {
		return fail(ir.InvalidValue, profile.Metadata.ResourceID, "/targets")
	}
	slices.SortFunc(spec.Targets, func(a, b ir.Target) int {
		if a.Key < b.Key {
			return -1
		}
		if a.Key > b.Key {
			return 1
		}
		return 0
	})
	expanded := depgraph.Expand(resources, roots, depgraph.Options{ScopeID: profile.Metadata.ScopeID, Exclude: excluded})
	if len(expanded.Diagnostics) > 0 {
		return ir.FrozenInput{}, nil, expanded.Diagnostics
	}
	spec.Resources = expanded.Resources
	slices.SortFunc(spec.Resources, func(a, b ir.Resource) int {
		if a.Metadata.ResourceID < b.Metadata.ResourceID {
			return -1
		}
		return 1
	})
	required := map[ir.ID][]ir.ID{}
	for _, r := range spec.Resources {
		refs, err := catalog.ExtractReferences(r)
		if err != nil {
			return ir.FrozenInput{}, nil, err
		}
		for _, ref := range refs {
			if !slices.Contains(required[ref.TargetID], r.Metadata.ResourceID) {
				required[ref.TargetID] = append(required[ref.TargetID], r.Metadata.ResourceID)
			}
		}
	}
	deps := make([]Dependency, 0, len(spec.Resources))
	for _, r := range spec.Resources {
		m := r.Metadata
		if stale[m.ResourceID] {
			return fail(ir.ResourceDisabled, m.ResourceID, "/members")
		}
		by := required[m.ResourceID]
		if by == nil {
			by = []ir.ID{}
		}
		slices.Sort(by)
		d := Dependency{ResourceID: m.ResourceID, Kind: m.Kind, Revision: Revision(m.Revision), SecurityEpoch: Revision(m.SecurityEpoch), Inclusion: "automatic", RequiredBy: by, CredentialCategories: []string{}}
		if selected[m.ResourceID] {
			d.Inclusion = "explicit"
		}
		if n, ok := r.Payload.(*ir.Node); ok {
			switch n.Auth.(type) {
			case *ir.UUIDAuth, *ir.VMessAuth:
				d.CredentialCategories = append(d.CredentialCategories, "uuid")
			case *ir.PasswordAuth, *ir.MethodPasswordAuth:
				d.CredentialCategories = append(d.CredentialCategories, "password")
			case *ir.UsernamePasswordAuth:
				d.CredentialCategories = append(d.CredentialCategories, "username", "password")
			}
			if _, ok := n.Security.(*ir.RealitySecurity); ok {
				d.CredentialCategories = append(d.CredentialCategories, "reality_public_key", "reality_short_id")
			}
		}
		deps = append(deps, d)
	}
	frozen, err := ir.NewFrozenInput(spec)
	return frozen, deps, err
}
