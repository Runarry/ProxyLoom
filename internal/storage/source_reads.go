package storage

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/source"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (c *Catalog) SourceHead(ctx context.Context, scope, id ir.ID) (source.Document, error) {
	if !validIDs(scope, id) {
		return source.Document{}, catalog.ErrInvalidInput
	}
	index, err := c.q.GetResourceIndex(ctx, dbgen.GetResourceIndexParams{ScopeID: dbID(scope), ID: dbID(id)})
	if err != nil {
		return source.Document{}, catalogError(err)
	}
	if index.DeletedAt.Valid || index.Kind != string(ir.KindSource) {
		return source.Document{}, catalog.ErrNotFound
	}
	row, err := c.q.GetResourceHead(ctx, dbgen.GetResourceHeadParams{ScopeID: dbID(scope), ID: dbID(id)})
	if err != nil {
		return source.Document{}, catalogError(err)
	}
	return c.openSource(dbgen.GetResourceRevisionRow(row))
}

func (c *Catalog) ListSources(ctx context.Context, scope ir.ID, options catalog.SourceListOptions) (source.Page, error) {
	limit, err := pageLimit(options.Limit)
	if err != nil || scope.Validate() != nil || !utf8.ValidString(options.Tag) || utf8.RuneCountInString(options.Tag) > 64 || strings.ContainsAny(options.Tag, "\x00\r\n") {
		return source.Page{}, catalog.ErrInvalidInput
	}
	args := dbgen.ListSourceCandidatesParams{ScopeID: dbID(scope), Tag: options.Tag, PageLimit: 200}
	if options.Enabled != nil {
		args.FilterEnabled, args.Enabled = true, *options.Enabled
	}
	if options.After != nil {
		if options.After.ID.Validate() != nil || options.After.CreatedAt.IsZero() {
			return source.Page{}, catalog.ErrInvalidInput
		}
		args.HasAfter, args.AfterID = true, dbID(options.After.ID)
		args.AfterCreatedAt = pgtype.Timestamptz{Time: options.After.CreatedAt, Valid: true}
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return source.Page{}, catalogError(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := dbgen.New(tx)
	page := source.Page{Items: make([]source.Document, 0, limit)}
	var lastMatch source.Cursor
	for {
		rows, err := q.ListSourceCandidates(ctx, args)
		if err != nil {
			return source.Page{}, catalogError(err)
		}
		for _, row := range rows {
			document, err := c.openSource(dbgen.GetResourceRevisionRow{ScopeID: row.ScopeID, ResourceID: row.ResourceID,
				Revision: row.Revision, SchemaVersion: row.SchemaVersion, SecurityEpoch: row.SecurityEpoch,
				Envelope: row.Envelope, ContentHmac: row.ContentHmac, Wrapping: row.Wrapping, WrapVersion: row.WrapVersion})
			if err != nil {
				return source.Page{}, err
			}
			if len(page.Items) == limit {
				page.Next = &lastMatch
				return page, catalogError(tx.Commit(ctx))
			}
			page.Items = append(page.Items, document)
			lastMatch = source.Cursor{CreatedAt: row.CreatedAt.Time, ID: document.Metadata.ResourceID}
		}
		if len(rows) < int(args.PageLimit) {
			return page, catalogError(tx.Commit(ctx))
		}
		last := rows[len(rows)-1]
		args.HasAfter, args.AfterID, args.AfterCreatedAt = true, last.ResourceID, last.CreatedAt
	}
}
