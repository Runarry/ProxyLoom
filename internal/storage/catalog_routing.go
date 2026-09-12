package storage

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func typedCatalogKind(kind ir.ResourceKind) bool {
	switch kind {
	case ir.KindNode, ir.KindChain, ir.KindPolicyGroup, ir.KindRoutingProfile, ir.KindRuleSet, ir.KindDNSProfile, ir.KindClientPreset, ir.KindSubscriptionProfile:
		return true
	}
	return false
}

// Called under both the scope lock and transaction mutex, before persisting a
// create or update. Deletes retain historical references without live checks.
func (t *catalogTx) checkRoutingReferences(ctx context.Context, resource ir.Resource) error {
	profile, ok := resource.Payload.(*ir.RoutingProfile)
	if !ok {
		return nil
	}
	_, err := catalog.ResolveRoutingReferences(t.scope, *profile, func(id ir.ID) (ir.Resource, error) {
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

func (c *Catalog) ListRoutingProfiles(ctx context.Context, scope ir.ID, options catalog.RoutingListOptions) (catalog.RoutingPage, error) {
	return c.listRouting(ctx, scope, ir.KindRoutingProfile, options)
}

func (c *Catalog) ListRuleSets(ctx context.Context, scope ir.ID, options catalog.RoutingListOptions) (catalog.RoutingPage, error) {
	return c.listRouting(ctx, scope, ir.KindRuleSet, options)
}

func (c *Catalog) listRouting(ctx context.Context, scope ir.ID, kind ir.ResourceKind, options catalog.RoutingListOptions) (catalog.RoutingPage, error) {
	limit, err := pageLimit(options.Limit)
	if err != nil || scope.Validate() != nil || !utf8.ValidString(options.Tag) || utf8.RuneCountInString(options.Tag) > 64 || strings.ContainsAny(options.Tag, "\x00\r\n") {
		return catalog.RoutingPage{}, catalog.ErrInvalidInput
	}
	args := dbgen.ListRoutingCandidatesParams{ScopeID: dbID(scope), Kind: string(kind), Tag: options.Tag, PageLimit: int32(limit + 1)}
	if options.Enabled != nil {
		args.FilterEnabled, args.Enabled = true, *options.Enabled
	}
	if options.After != nil {
		if options.After.ID.Validate() != nil || options.After.CreatedAt.IsZero() {
			return catalog.RoutingPage{}, catalog.ErrInvalidInput
		}
		args.HasAfter, args.AfterID = true, dbID(options.After.ID)
		args.AfterCreatedAt = pgtype.Timestamptz{Time: options.After.CreatedAt, Valid: true}
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return catalog.RoutingPage{}, catalogError(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	rows, err := dbgen.New(tx).ListRoutingCandidates(ctx, args)
	if err != nil {
		return catalog.RoutingPage{}, catalogError(err)
	}
	page := catalog.RoutingPage{Items: make([]ir.Resource, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		last := rows[limit-1]
		page.Next = &catalog.Position{CreatedAt: last.CreatedAt.Time, ID: irID(last.ResourceID)}
		rows = rows[:limit]
	}
	for _, row := range rows {
		r, err := c.open(dbgen.GetResourceRevisionRow{ScopeID: row.ScopeID, ResourceID: row.ResourceID,
			Revision: row.Revision, SchemaVersion: row.SchemaVersion, SecurityEpoch: row.SecurityEpoch,
			Envelope: row.Envelope, ContentHmac: row.ContentHmac, Wrapping: row.Wrapping, WrapVersion: row.WrapVersion})
		if err != nil {
			return catalog.RoutingPage{}, err
		}
		if r.Metadata.Kind != kind {
			return catalog.RoutingPage{}, catalog.ErrCrypto
		}
		page.Items = append(page.Items, r)
	}
	return page, catalogError(tx.Commit(ctx))
}
