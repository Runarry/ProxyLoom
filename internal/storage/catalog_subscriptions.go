package storage

import (
	"context"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"strconv"
)

func (c *Catalog) ListSubscriptions(ctx context.Context, scope ir.ID, options catalog.RoutingListOptions) (catalog.RoutingPage, error) {
	return c.listRouting(ctx, scope, ir.KindSubscriptionProfile, options)
}

func (t *catalogTx) resourceReferences(ctx context.Context, r ir.Resource) ([]catalog.Reference, error) {
	refs, err := catalog.ExtractReferences(r)
	if err != nil {
		return nil, err
	}
	p, ok := r.Payload.(*ir.SubscriptionProfile)
	if !ok {
		return refs, nil
	}
	for _, group := range []struct {
		name string
		ids  []ir.ID
	}{{"include_ids", p.Members.IncludeIDs}, {"exclude_ids", p.Members.ExcludeIDs}} {
		for i, id := range group.ids {
			row, err := t.q.GetResourceIndex(ctx, dbgen.GetResourceIndexParams{ScopeID: dbID(t.scope), ID: dbID(id)})
			if err != nil {
				return nil, err
			}
			kind := ir.ResourceKind(row.Kind)
			if kind != ir.KindNode && kind != ir.KindChain && kind != ir.KindPolicyGroup {
				return nil, catalog.ErrInvalidReference
			}
			refs = append(refs, catalog.Reference{SourceID: r.Metadata.ResourceID, SourceKind: r.Metadata.Kind, SourceRevision: r.Metadata.Revision, TargetID: id, ExpectedKind: kind, Path: "/payload/members/" + group.name + "/" + strconv.Itoa(i), Current: true})
		}
	}
	cores, err := capability.Load()
	if err != nil {
		return nil, catalog.ErrUnavailable
	}
	for _, target := range p.Targets {
		row, err := t.q.GetResourceHead(ctx, dbgen.GetResourceHeadParams{ScopeID: dbID(t.scope), ID: dbID(target.ClientPresetID)})
		if err != nil {
			return nil, err
		}
		resource, err := t.store.open(dbgen.GetResourceRevisionRow(row))
		if err != nil {
			return nil, err
		}
		preset, ok := resource.Payload.(*ir.ClientPreset)
		if !ok {
			return nil, catalog.ErrInvalidReference
		}
		build, err := cores.Build(target.CoreBuildID)
		if err != nil || build.Family != preset.CoreFamily || target.CheckPreset(*preset) != nil {
			return nil, catalog.ErrInvalidInput
		}
	}
	return refs, nil
}
