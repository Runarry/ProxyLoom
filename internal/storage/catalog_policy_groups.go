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

// Called while the scope and transaction mutex are held. Reads and the ensuing
// group revision therefore see the same dependency heads. Deletion and security
// revocation retain historical references and do not require live dependencies.
func (t *catalogTx) checkPolicyMembers(ctx context.Context, resource ir.Resource) error {
	group, ok := resource.Payload.(*ir.PolicyGroup)
	if !ok {
		return nil
	}
	_, err := catalog.ResolvePolicyMembers(t.scope, *group, func(id ir.ID) (ir.Resource, error) {
		index, err := t.q.GetResourceIndex(ctx, dbgen.GetResourceIndexParams{ScopeID: dbID(t.scope), ID: dbID(id)})
		if err != nil || index.DeletedAt.Valid {
			if err != nil {
				return ir.Resource{}, catalogError(err)
			}
			return ir.Resource{}, catalog.ErrNotFound
		}
		if index.Kind != string(ir.KindNode) && index.Kind != string(ir.KindChain) {
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

func (c *Catalog) ListPolicyGroups(ctx context.Context, scope ir.ID, options catalog.PolicyGroupListOptions) (catalog.PolicyGroupPage, error) {
	limit, err := pageLimit(options.Limit)
	if err != nil || scope.Validate() != nil || !utf8.ValidString(options.Tag) || utf8.RuneCountInString(options.Tag) > 64 || strings.ContainsAny(options.Tag, "\x00\r\n") {
		return catalog.PolicyGroupPage{}, catalog.ErrInvalidInput
	}
	args := dbgen.ListPolicyGroupCandidatesParams{ScopeID: dbID(scope), Tag: options.Tag, PageLimit: int32(limit + 1)}
	if options.Enabled != nil {
		args.FilterEnabled, args.Enabled = true, *options.Enabled
	}
	if options.After != nil {
		if options.After.ID.Validate() != nil || options.After.CreatedAt.IsZero() {
			return catalog.PolicyGroupPage{}, catalog.ErrInvalidInput
		}
		args.HasAfter, args.AfterID = true, dbID(options.After.ID)
		args.AfterCreatedAt = pgtype.Timestamptz{Time: options.After.CreatedAt, Valid: true}
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return catalog.PolicyGroupPage{}, catalogError(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	rows, err := dbgen.New(tx).ListPolicyGroupCandidates(ctx, args)
	if err != nil {
		return catalog.PolicyGroupPage{}, catalogError(err)
	}
	page := catalog.PolicyGroupPage{Items: make([]ir.Resource, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		last := rows[limit-1]
		page.Next = &catalog.Position{CreatedAt: last.CreatedAt.Time, ID: irID(last.ResourceID)}
		rows = rows[:limit]
	}
	for _, row := range rows {
		resource, err := c.open(dbgen.GetResourceRevisionRow{ScopeID: row.ScopeID, ResourceID: row.ResourceID,
			Revision: row.Revision, SchemaVersion: row.SchemaVersion, SecurityEpoch: row.SecurityEpoch,
			Envelope: row.Envelope, ContentHmac: row.ContentHmac, Wrapping: row.Wrapping, WrapVersion: row.WrapVersion})
		if err != nil {
			return catalog.PolicyGroupPage{}, err
		}
		if _, ok := resource.Payload.(*ir.PolicyGroup); !ok {
			return catalog.PolicyGroupPage{}, catalog.ErrCrypto
		}
		page.Items = append(page.Items, resource)
	}
	return page, catalogError(tx.Commit(ctx))
}
