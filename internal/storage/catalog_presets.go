package storage

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// EnsureBuiltinClientPresets is a provisioning operation. A scope lock makes
// concurrent initializations atomic; existing matching presets cause no writes.
// A conflicting stable ID is an error, never an overwrite of stored history.
func (c *Catalog) EnsureBuiltinClientPresets(ctx context.Context, scope ir.ID) error {
	resources, err := catalog.BuiltinClientPresets(scope)
	if err != nil {
		return err
	}
	return c.transact(ctx, scope, func(tx *catalogTx) error {
		for _, resource := range resources {
			row, err := tx.q.GetResourceIndex(ctx, dbgen.GetResourceIndexParams{ScopeID: dbID(scope), ID: dbID(resource.Metadata.ResourceID)})
			if err == nil {
				if row.DeletedAt.Valid || row.Kind != string(ir.KindClientPreset) || !row.HeadRevision.Valid {
					return catalog.ErrInvalidInput
				}
				head, err := tx.q.GetResourceHead(ctx, dbgen.GetResourceHeadParams{ScopeID: dbID(scope), ID: dbID(resource.Metadata.ResourceID)})
				if err != nil {
					return err
				}
				stored, err := c.open(dbgen.GetResourceRevisionRow(head))
				if err != nil {
					return err
				}
				want, err := catalog.Canonical(resource)
				if err != nil {
					return err
				}
				got, err := catalog.Canonical(stored)
				equal := err == nil && bytes.Equal(got, want)
				clear(got)
				clear(want)
				if !equal {
					return catalog.ErrInvalidInput
				}
				continue
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			_, err = tx.mutate(func() (ir.Resource, error) {
				m := resource.Metadata
				if err := tx.q.InsertResource(ctx, dbgen.InsertResourceParams{ID: dbID(m.ResourceID), ScopeID: dbID(scope),
					Kind: string(m.Kind), Name: m.Name, Enabled: m.Enabled}); err != nil {
					return ir.Resource{}, err
				}
				return resource, tx.persist(ctx, resource, nil, false)
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (c *Catalog) ListClientPresets(ctx context.Context, scope ir.ID, options catalog.ClientPresetListOptions) (catalog.ClientPresetPage, error) {
	limit, err := pageLimit(options.Limit)
	if err != nil || scope.Validate() != nil {
		return catalog.ClientPresetPage{}, catalog.ErrInvalidInput
	}
	if options.CoreFamily != "" && options.CoreFamily != ir.Xray && options.CoreFamily != ir.SingBox && options.CoreFamily != ir.Mihomo {
		return catalog.ClientPresetPage{}, catalog.ErrInvalidInput
	}
	switch options.Platform {
	case "", "linux", "windows", "macos", "android", "ios":
	default:
		return catalog.ClientPresetPage{}, catalog.ErrInvalidInput
	}
	// The only writable path provisions the reviewed presets below. Fetch the
	// bounded set before filtering so filtered pagination never drops a match.
	builtins, err := catalog.BuiltinClientPresets(scope)
	if err != nil {
		return catalog.ClientPresetPage{}, err
	}
	args := dbgen.ListRoutingCandidatesParams{ScopeID: dbID(scope), Kind: string(ir.KindClientPreset), PageLimit: int32(len(builtins) + 1)}
	if options.After != nil {
		if options.After.ID.Validate() != nil || options.After.CreatedAt.IsZero() {
			return catalog.ClientPresetPage{}, catalog.ErrInvalidInput
		}
		args.HasAfter, args.AfterID = true, dbID(options.After.ID)
		args.AfterCreatedAt = pgtype.Timestamptz{Time: options.After.CreatedAt, Valid: true}
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return catalog.ClientPresetPage{}, catalogError(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	rows, err := dbgen.New(tx).ListRoutingCandidates(ctx, args)
	if err != nil {
		return catalog.ClientPresetPage{}, catalogError(err)
	}
	if len(rows) > len(builtins) {
		return catalog.ClientPresetPage{}, catalog.ErrInvalidInput
	}
	page := catalog.ClientPresetPage{Items: []ir.Resource{}}
	var last catalog.Position
	for _, row := range rows {
		resource, err := c.open(dbgen.GetResourceRevisionRow{ScopeID: row.ScopeID, ResourceID: row.ResourceID,
			Revision: row.Revision, SchemaVersion: row.SchemaVersion, SecurityEpoch: row.SecurityEpoch,
			Envelope: row.Envelope, ContentHmac: row.ContentHmac, Wrapping: row.Wrapping, WrapVersion: row.WrapVersion})
		if err != nil {
			return catalog.ClientPresetPage{}, err
		}
		preset, ok := resource.Payload.(*ir.ClientPreset)
		if !ok || resource.Metadata.Kind != ir.KindClientPreset {
			return catalog.ClientPresetPage{}, catalog.ErrCrypto
		}
		if (options.CoreFamily != "" && preset.CoreFamily != options.CoreFamily) || (options.Platform != "" && preset.Platform != options.Platform) {
			continue
		}
		if len(page.Items) == limit {
			page.Next = &last
			break
		}
		page.Items = append(page.Items, resource)
		last = catalog.Position{CreatedAt: row.CreatedAt.Time, ID: resource.Metadata.ResourceID}
	}
	return page, catalogError(tx.Commit(ctx))
}
