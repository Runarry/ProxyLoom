package storage

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (c *Catalog) ListChains(ctx context.Context, scope ir.ID, options catalog.ChainListOptions) (catalog.ChainPage, error) {
	limit, err := pageLimit(options.Limit)
	if err != nil || scope.Validate() != nil || !utf8.ValidString(options.Tag) || utf8.RuneCountInString(options.Tag) > 64 || strings.ContainsAny(options.Tag, "\x00\r\n") {
		return catalog.ChainPage{}, catalog.ErrInvalidInput
	}
	args := dbgen.ListChainCandidatesParams{ScopeID: dbID(scope), Tag: options.Tag, PageLimit: 200}
	if options.Enabled != nil {
		args.FilterEnabled, args.Enabled = true, *options.Enabled
	}
	if options.After != nil {
		if options.After.ID.Validate() != nil || options.After.CreatedAt.IsZero() {
			return catalog.ChainPage{}, catalog.ErrInvalidInput
		}
		args.HasAfter, args.AfterID = true, dbID(options.After.ID)
		args.AfterCreatedAt = pgtype.Timestamptz{Time: options.After.CreatedAt, Valid: true}
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return catalog.ChainPage{}, catalogError(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := dbgen.New(tx)
	page := catalog.ChainPage{Items: make([]ir.Resource, 0, limit)}
	var lastMatch catalog.Position
	for {
		rows, err := q.ListChainCandidates(ctx, args)
		if err != nil {
			return catalog.ChainPage{}, catalogError(err)
		}
		for _, row := range rows {
			resource, err := c.open(dbgen.GetResourceRevisionRow{ScopeID: row.ScopeID, ResourceID: row.ResourceID,
				Revision: row.Revision, SchemaVersion: row.SchemaVersion, SecurityEpoch: row.SecurityEpoch,
				Envelope: row.Envelope, ContentHmac: row.ContentHmac, Wrapping: row.Wrapping, WrapVersion: row.WrapVersion})
			if err != nil {
				return catalog.ChainPage{}, err
			}
			if _, ok := resource.Payload.(*ir.Chain); !ok {
				return catalog.ChainPage{}, catalog.ErrCrypto
			}
			if len(page.Items) == limit {
				page.Next = &lastMatch
				return page, catalogError(tx.Commit(ctx))
			}
			page.Items = append(page.Items, resource)
			lastMatch = catalog.Position{CreatedAt: row.CreatedAt.Time, ID: resource.Metadata.ResourceID}
		}
		if len(rows) < int(args.PageLimit) {
			return page, catalogError(tx.Commit(ctx))
		}
		last := rows[len(rows)-1]
		args.HasAfter, args.AfterID, args.AfterCreatedAt = true, last.ResourceID, last.CreatedAt
	}
}
