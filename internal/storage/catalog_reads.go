package storage

import (
	"context"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5/pgtype"
)

func pageLimit(limit int) (int, error) {
	if limit == 0 {
		return 50, nil
	}
	if limit < 1 || limit > 200 {
		return 0, catalog.ErrInvalidInput
	}
	return limit, nil
}

func (c *Catalog) List(ctx context.Context, scope ir.ID, options catalog.ListOptions) (catalog.Page, error) {
	limit, err := pageLimit(options.Limit)
	if err != nil || scope.Validate() != nil || (options.Kind != "" && options.Kind != ir.KindNode && options.Kind != ir.KindChain && options.Kind != ir.KindPolicyGroup) ||
		!utf8.ValidString(options.Tag) || utf8.RuneCountInString(options.Tag) > 64 {
		return catalog.Page{}, catalog.ErrInvalidInput
	}
	args := dbgen.ListResourcesParams{ScopeID: dbID(scope), Kind: string(options.Kind), Tag: options.Tag,
		IncludeDeleted: options.IncludeDeleted, PageLimit: int32(limit + 1)}
	if options.After != nil {
		if options.After.ID.Validate() != nil || options.After.CreatedAt.IsZero() {
			return catalog.Page{}, catalog.ErrInvalidInput
		}
		args.HasAfter, args.AfterID = true, dbID(options.After.ID)
		args.AfterCreatedAt = pgtype.Timestamptz{Time: options.After.CreatedAt, Valid: true}
	}
	rows, err := c.q.ListResources(ctx, args)
	if err != nil {
		return catalog.Page{}, catalogError(err)
	}
	page := catalog.Page{Items: make([]catalog.Summary, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		last := rows[limit-1]
		page.Next = &catalog.Position{CreatedAt: last.CreatedAt.Time, ID: irID(last.ID)}
		rows = rows[:limit]
	}
	for _, row := range rows {
		item := catalog.Summary{Metadata: ir.Metadata{ResourceID: irID(row.ID), ScopeID: irID(row.ScopeID),
			Kind: ir.ResourceKind(row.Kind), Name: row.Name, Revision: row.HeadRevision.Int64,
			SchemaVersion: ir.SchemaVersion, Tags: row.Tags, Enabled: row.Enabled, SecurityEpoch: row.SecurityEpoch},
			CreatedAt: row.CreatedAt.Time}
		if row.DeletedAt.Valid {
			when := row.DeletedAt.Time
			item.DeletedAt = &when
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}

func (c *Catalog) Tags(ctx context.Context, scope ir.ID, limit int) ([]string, error) {
	page, err := c.ListTags(ctx, scope, catalog.TagOptions{Limit: limit})
	return page.Items, err
}

func (c *Catalog) ListTags(ctx context.Context, scope ir.ID, options catalog.TagOptions) (catalog.TagPage, error) {
	count, err := pageLimit(options.Limit)
	if err != nil || scope.Validate() != nil || !utf8.ValidString(options.After) || utf8.RuneCountInString(options.After) > 64 {
		return catalog.TagPage{}, catalog.ErrInvalidInput
	}
	tags, err := c.q.ListResourceTags(ctx, dbgen.ListResourceTagsParams{ScopeID: dbID(scope), Tag: options.After, Limit: int32(count + 1)})
	if err != nil {
		return catalog.TagPage{}, catalogError(err)
	}
	page := catalog.TagPage{Items: tags}
	if len(tags) > count {
		page.Items = tags[:count]
		page.Next = tags[count-1]
	}
	return page, nil
}

func (c *Catalog) References(ctx context.Context, scope, target ir.ID, options catalog.ReferenceOptions) (catalog.ReferencePage, error) {
	limit, err := pageLimit(options.Limit)
	if err != nil || !validIDs(scope, target) {
		return catalog.ReferencePage{}, catalog.ErrInvalidInput
	}
	args := dbgen.ListResourceReferencesParams{ScopeID: dbID(scope), TargetResourceID: dbID(target),
		IncludeHistorical: options.IncludeHistorical, PageLimit: int32(limit + 1)}
	if options.After != nil {
		if options.After.SourceID.Validate() != nil || options.After.SourceRevision < 1 || options.After.Path == "" || len(options.After.Path) > 128 {
			return catalog.ReferencePage{}, catalog.ErrInvalidInput
		}
		args.HasAfter, args.AfterResourceID = true, dbID(options.After.SourceID)
		args.AfterRevision, args.AfterPath = options.After.SourceRevision, options.After.Path
	}
	rows, err := c.q.ListResourceReferences(ctx, args)
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
		ref := catalog.Reference{SourceID: irID(row.ResourceID), SourceKind: ir.ResourceKind(row.SourceKind), SourceRevision: row.Revision,
			TargetID: irID(row.TargetResourceID), ExpectedKind: ir.ResourceKind(row.ExpectedKind), Path: row.RefPath, Current: row.Current.Bool}
		if row.TargetRevision.Valid {
			value := row.TargetRevision.Int64
			ref.TargetRevision = &value
		}
		page.Items = append(page.Items, ref)
	}
	return page, nil
}
