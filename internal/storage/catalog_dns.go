package storage

import (
	"context"
	"errors"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
)

func (c *Catalog) ListDNSProfiles(ctx context.Context, scope ir.ID, options catalog.DNSListOptions) (catalog.DNSPage, error) {
	return c.listRouting(ctx, scope, ir.KindDNSProfile, options)
}

// Called while the scope lock and transaction mutex are held. Probe cannot be
// used here because it takes the same mutex.
func (t *catalogTx) checkDNSReferences(ctx context.Context, resource ir.Resource) error {
	profile, ok := resource.Payload.(*ir.DNSProfile)
	if !ok {
		return nil
	}
	_, err := catalog.ResolveDNSReferences(t.scope, *profile, func(id ir.ID) (ir.Resource, error) {
		index, err := t.q.GetResourceIndex(ctx, dbgen.GetResourceIndexParams{ScopeID: dbID(t.scope), ID: dbID(id)})
		if err != nil {
			return ir.Resource{}, catalogError(err)
		}
		if index.DeletedAt.Valid {
			return ir.Resource{}, catalog.ErrNotFound
		}
		if !typedCatalogKind(ir.ResourceKind(index.Kind)) {
			return ir.Resource{Metadata: ir.Metadata{ResourceID: id, ScopeID: t.scope, Kind: ir.ResourceKind(index.Kind)}}, nil
		}
		row, err := t.q.GetResourceHead(ctx, dbgen.GetResourceHeadParams{ScopeID: dbID(t.scope), ID: dbID(id)})
		if err != nil {
			return ir.Resource{}, catalogError(err)
		}
		return t.store.open(dbgen.GetResourceRevisionRow(row))
	})
	var diagnostics ir.Diagnostics
	if errors.As(err, &diagnostics) {
		return catalog.ErrInvalidReference
	}
	return err
}
