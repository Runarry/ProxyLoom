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

var _ catalog.AuditedTx = (*catalogTx)(nil)

func (t *catalogTx) Head(ctx context.Context, id ir.ID) (ir.Resource, error) {
	return t.readHead(ctx, id, true)
}

func (t *catalogTx) Probe(ctx context.Context, id ir.ID) (ir.Resource, error) {
	return t.readHead(ctx, id, false)
}

func (t *catalogTx) readHead(ctx context.Context, id ir.ID, latchMissing bool) (ir.Resource, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return ir.Resource{}, catalog.ErrTransactionClosed
	}
	if t.failed != nil {
		return ir.Resource{}, t.failed
	}
	if id.Validate() != nil {
		t.failed = catalog.ErrInvalidInput
		return ir.Resource{}, t.failed
	}
	index, err := t.q.GetResourceIndex(ctx, dbgen.GetResourceIndexParams{ScopeID: dbID(t.scope), ID: dbID(id)})
	if err != nil {
		mapped := catalogError(err)
		if latchMissing || (mapped != catalog.ErrNotFound && mapped != catalog.ErrInvalidInput) {
			t.failed = mapped
		}
		return ir.Resource{}, mapped
	}
	if index.DeletedAt.Valid || !typedCatalogKind(ir.ResourceKind(index.Kind)) {
		if latchMissing {
			t.failed = catalog.ErrNotFound
		}
		return ir.Resource{}, catalog.ErrNotFound
	}
	row, err := t.q.GetResourceHead(ctx, dbgen.GetResourceHeadParams{ScopeID: dbID(t.scope), ID: dbID(id)})
	var resource ir.Resource
	if err == nil {
		resource, err = t.store.open(dbgen.GetResourceRevisionRow(row))
	}
	if err != nil {
		mapped := catalogError(err)
		if latchMissing || (mapped != catalog.ErrNotFound && mapped != catalog.ErrInvalidInput) {
			t.failed = mapped
		}
		return ir.Resource{}, mapped
	}
	return resource, nil
}

func (t *catalogTx) Audit(ctx context.Context, audit catalog.MutationAudit) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return catalog.ErrTransactionClosed
	}
	if t.failed != nil {
		return t.failed
	}
	if audit.Validate() != nil {
		t.failed = catalog.ErrInvalidInput
		return t.failed
	}
	id, err := identityUUID()
	if err == nil {
		err = t.q.InsertResourceAudit(ctx, dbgen.InsertResourceAuditParams{ID: id, ScopeID: dbID(t.scope),
			ActorID: dbID(audit.PrincipalID), ObjectID: dbID(audit.ObjectID), Revision: audit.Revision,
			Action: string(audit.Action), RequestID: audit.RequestID})
	}
	if err != nil {
		// An audit failure must abort all earlier resource changes, even if a
		// callback accidentally ignores this method's error.
		t.failed = catalog.ErrUnavailable
	}
	return t.failed
}

func validNodeProtocol(protocol ir.Protocol) bool {
	switch protocol {
	case "", ir.Shadowsocks, ir.VMess, ir.VLESS, ir.Trojan, ir.SOCKS5, ir.HTTP:
		return true
	}
	return false
}

// ListNodes filters indexed metadata in SQL and protocol in the authenticated
// encrypted snapshot. Candidate reads use bounded chunks in one repeatable-read
// snapshot, so a protocol filter cannot splice revisions from concurrent edits.
// No plaintext endpoint, authentication value or secondary payload is persisted.
func (c *Catalog) ListNodes(ctx context.Context, scope ir.ID, options catalog.NodeListOptions) (catalog.NodePage, error) {
	limit, err := pageLimit(options.Limit)
	if err != nil || scope.Validate() != nil || !validNodeProtocol(options.Protocol) ||
		!utf8.ValidString(options.Search) || utf8.RuneCountInString(options.Search) > 256 || strings.ContainsAny(options.Search, "\x00\r\n") ||
		!utf8.ValidString(options.Tag) || utf8.RuneCountInString(options.Tag) > 64 || strings.ContainsAny(options.Tag, "\x00\r\n") {
		return catalog.NodePage{}, catalog.ErrInvalidInput
	}
	args := dbgen.ListNodeCandidatesParams{ScopeID: dbID(scope), Search: options.Search, Tag: options.Tag, PageLimit: 200}
	if options.Enabled != nil {
		args.FilterEnabled, args.Enabled = true, *options.Enabled
	}
	if options.After != nil {
		if options.After.ID.Validate() != nil || options.After.CreatedAt.IsZero() {
			return catalog.NodePage{}, catalog.ErrInvalidInput
		}
		args.HasAfter, args.AfterID = true, dbID(options.After.ID)
		args.AfterCreatedAt = pgtype.Timestamptz{Time: options.After.CreatedAt, Valid: true}
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return catalog.NodePage{}, catalogError(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	q := dbgen.New(tx)
	page := catalog.NodePage{Items: make([]ir.Resource, 0, limit)}
	var lastMatch catalog.Position
	for {
		rows, err := q.ListNodeCandidates(ctx, args)
		if err != nil {
			return catalog.NodePage{}, catalogError(err)
		}
		for _, row := range rows {
			resource, err := c.open(dbgen.GetResourceRevisionRow{ScopeID: row.ScopeID, ResourceID: row.ResourceID,
				Revision: row.Revision, SchemaVersion: row.SchemaVersion, SecurityEpoch: row.SecurityEpoch,
				Envelope: row.Envelope, ContentHmac: row.ContentHmac, Wrapping: row.Wrapping, WrapVersion: row.WrapVersion})
			if err != nil {
				return catalog.NodePage{}, err
			}
			node, ok := resource.Payload.(*ir.Node)
			if !ok {
				return catalog.NodePage{}, catalog.ErrCrypto
			}
			if options.Protocol != "" && node.Protocol != options.Protocol {
				continue
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

func (c *Catalog) NodeRevisions(ctx context.Context, scope, id ir.ID, after int64, limit int) (catalog.NodeRevisionPage, error) {
	count, err := pageLimit(limit)
	if err != nil || !validIDs(scope, id) || after < 0 {
		return catalog.NodeRevisionPage{}, catalog.ErrInvalidInput
	}
	if err := c.nodeExists(ctx, scope, id); err != nil {
		return catalog.NodeRevisionPage{}, err
	}
	rows, err := c.q.ListNodeRevisions(ctx, dbgen.ListNodeRevisionsParams{ScopeID: dbID(scope), ResourceID: dbID(id), AfterRevision: after, PageLimit: int32(count + 1)})
	if err != nil {
		return catalog.NodeRevisionPage{}, catalogError(err)
	}
	page := catalog.NodeRevisionPage{Items: make([]ir.Resource, 0, min(count, len(rows)))}
	if len(rows) > count {
		page.Next = rows[count-1].Revision
		rows = rows[:count]
	}
	for _, row := range rows {
		resource, err := c.open(dbgen.GetResourceRevisionRow(row))
		if err != nil {
			return catalog.NodeRevisionPage{}, err
		}
		page.Items = append(page.Items, resource)
	}
	return page, nil
}

func (c *Catalog) nodeExists(ctx context.Context, scope, id ir.ID) error {
	exists, err := c.q.NodeExists(ctx, dbgen.NodeExistsParams{ScopeID: dbID(scope), ID: dbID(id)})
	if err != nil {
		return catalogError(err)
	}
	if !exists {
		return catalog.ErrNotFound
	}
	return nil
}

func (c *Catalog) NodeReferences(ctx context.Context, scope, id ir.ID, options catalog.NodeReferenceOptions) (catalog.ReferencePage, error) {
	limit, err := pageLimit(options.Limit)
	if options.State == "" {
		options.State = "active"
	}
	if err != nil || !validIDs(scope, id) || (options.State != "active" && options.State != "historical" && options.State != "all") {
		return catalog.ReferencePage{}, catalog.ErrInvalidInput
	}
	args := dbgen.ListNodeReferencesParams{ScopeID: dbID(scope), TargetResourceID: dbID(id), ReferenceState: options.State, PageLimit: int32(limit + 1)}
	if options.After != nil {
		if options.After.SourceID.Validate() != nil || options.After.SourceRevision < 1 || options.After.Path == "" || len(options.After.Path) > 128 {
			return catalog.ReferencePage{}, catalog.ErrInvalidInput
		}
		args.HasAfter, args.AfterResourceID = true, dbID(options.After.SourceID)
		args.AfterRevision, args.AfterPath = options.After.SourceRevision, options.After.Path
	}
	if err := c.nodeExists(ctx, scope, id); err != nil {
		return catalog.ReferencePage{}, err
	}
	rows, err := c.q.ListNodeReferences(ctx, args)
	if err != nil {
		return catalog.ReferencePage{}, catalogError(err)
	}
	page := catalog.ReferencePage{Items: make([]catalog.Reference, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		last := rows[limit-1]
		page.Next = &catalog.ReferencePosition{SourceID: irID(last.ResourceID), SourceRevision: last.Revision, Path: last.RefPath}
		rows = rows[:limit]
	}
	for _, row := range rows {
		ref := catalog.Reference{SourceID: irID(row.ResourceID), SourceRevision: row.Revision, SourceKind: ir.ResourceKind(row.SourceKind),
			TargetID: irID(row.TargetResourceID), ExpectedKind: ir.ResourceKind(row.ExpectedKind), Path: row.RefPath, Current: row.Current}
		if row.TargetRevision.Valid {
			value := row.TargetRevision.Int64
			ref.TargetRevision = &value
		}
		page.Items = append(page.Items, ref)
	}
	return page, nil
}
