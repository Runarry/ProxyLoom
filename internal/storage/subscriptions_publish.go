package storage

import (
	"context"
	"errors"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
	"github.com/jackc/pgx/v5"
	"slices"
)

func (s *Subscriptions) headID(ctx context.Context, tx subTx, scope, profile ir.ID) (ir.ID, int64, error) {
	var id ir.ID
	var generation int64
	err := tx.QueryRow(ctx, `SELECT p.id::text,p.generation FROM public.publication_heads h JOIN public.publications p ON p.id=h.publication_id WHERE h.scope_id=$1 AND h.profile_id=$2`, dbID(scope), dbID(profile)).Scan(&id, &generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, nil
	}
	return id, generation, err
}

// safeBatch ignores ordinary revision drift for historical publications but
// always checks current enablement, authentication epochs and build status.
func (s *Subscriptions) safeBatch(ctx context.Context, tx subTx, b storedBatch, current ir.Resource) error {
	if !current.Metadata.Enabled {
		return subscriptions.ErrBlocked
	}
	var epoch int64
	if err := tx.QueryRow(ctx, `SELECT auth_epoch FROM public.scopes WHERE id=$1`, dbID(b.Scope)).Scan(&epoch); err != nil {
		return err
	}
	if epoch != b.AuthEpoch {
		return subscriptions.ErrBlocked
	}
	for _, d := range b.Frozen.Dependencies {
		var enabled, deleted bool
		var epoch int64
		err := tx.QueryRow(ctx, `SELECT enabled,deleted_at IS NOT NULL,security_epoch FROM public.resources WHERE scope_id=$1 AND id=$2`, dbID(b.Scope), dbID(d.ResourceID)).Scan(&enabled, &deleted, &epoch)
		if errors.Is(err, pgx.ErrNoRows) {
			return subscriptions.ErrBlocked
		}
		if err != nil {
			return err
		}
		if !enabled || deleted || epoch != int64(d.SecurityEpoch) {
			return subscriptions.ErrBlocked
		}
	}
	p := current.Payload.(*ir.SubscriptionProfile)
	for _, t := range b.Frozen.Input.Spec().Targets {
		var enabled bool
		if err := tx.QueryRow(ctx, `SELECT enabled FROM public.core_builds WHERE id=$1`, dbID(t.CoreBuildID)).Scan(&enabled); err != nil {
			return err
		}
		if !enabled {
			return subscriptions.ErrBlocked
		}
		authorized := false
		for _, live := range p.Targets {
			if live.IsEnabled() && live.Key == t.Key && live.Format == t.Format {
				authorized = true
			}
		}
		if !authorized {
			return subscriptions.ErrBlocked
		}
	}
	return nil
}
func (s *Subscriptions) Publish(ctx context.Context, a subscriptions.Actor, profile ir.ID, expected int64, request subscriptions.PublishRequest) (subscriptions.Publication, error) {
	if !validIDs(a.ScopeID, a.ID, profile, request.BatchID) || expected < 1 || request.ExpectedGeneration < 0 {
		return subscriptions.Publication{}, catalog.ErrInvalidInput
	}
	tx, err := s.catalog.pool.Begin(ctx)
	if err != nil {
		return subscriptions.Publication{}, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	scope, err := dbgen.New(tx).LockScope(ctx, dbID(a.ScopeID))
	if err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	r, err := s.profile(ctx, tx, a.ScopeID, profile)
	if err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	id, replay, err := s.operation(ctx, tx, a, "publish", struct {
		Profile  ir.ID
		Revision int64
		Request  subscriptions.PublishRequest
	}{profile, expected, request}, jobs.NewID())
	if err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	if replay {
		p, err := s.readPublication(ctx, tx, a.ScopeID, id)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return p, subError(err)
	}
	if r.Metadata.Revision != expected {
		return subscriptions.Publication{}, catalog.ErrRevisionConflict
	}
	b, err := s.readBatch(ctx, tx, a.ScopeID, request.BatchID)
	if err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	if b.Batch.SubscriptionID != profile {
		return subscriptions.Publication{}, catalog.ErrNotFound
	}
	if scope.CatalogRevision != int64(b.Batch.CatalogRevision) || scope.AuthEpoch != b.AuthEpoch || expected != int64(b.Batch.SubscriptionRevision) {
		return subscriptions.Publication{}, subscriptions.ErrObsolete
	}
	if err = s.aggregate(ctx, tx, &b); err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	if b.Batch.State != "ready" {
		return subscriptions.Publication{}, subscriptions.ErrBlocked
	}
	if err = s.safeBatch(ctx, tx, b, r); err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	head, generation, err := s.headID(ctx, tx, a.ScopeID, profile)
	if err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	if generation != int64(request.ExpectedGeneration) || head != b.BasePublicationID {
		return subscriptions.Publication{}, subscriptions.ErrObsolete
	}
	var viewed string
	err = tx.QueryRow(ctx, `SELECT preview_hash FROM public.compile_preview_views WHERE scope_id=$1 AND actor_id=$2 AND batch_id=$3`, dbID(a.ScopeID), dbID(a.ID), dbID(request.BatchID)).Scan(&viewed)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return subscriptions.Publication{}, subError(err)
	}
	if !request.Confirmation.Acknowledged || request.EffectivePreviewHash == "" || viewed != request.EffectivePreviewHash || viewed != b.Batch.EffectivePreviewHash {
		return subscriptions.Publication{}, subscriptions.ErrConfirmation
	}
	if err = s.createPublication(ctx, tx, a, id, profile, generation+1, b, ""); err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	p, err := s.readPublication(ctx, tx, a.ScopeID, id)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return p, subError(err)
}
func (s *Subscriptions) createPublication(ctx context.Context, tx pgx.Tx, a subscriptions.Actor, id, profile ir.ID, generation int64, b storedBatch, source ir.ID) error {
	_, err := tx.Exec(ctx, `INSERT INTO public.publications(id,scope_id,profile_id,generation,batch_id,source_publication_id,created_by) VALUES($1,$2,$3,$4,$5,$6,$7)`, dbID(id), dbID(a.ScopeID), dbID(profile), generation, dbID(b.Batch.BatchID), nullableID(source), dbID(a.ID))
	if err != nil {
		return err
	}
	for _, d := range b.Frozen.Dependencies {
		_, err = tx.Exec(ctx, `INSERT INTO public.publication_dependencies(publication_id,scope_id,resource_id,revision,security_epoch) VALUES($1,$2,$3,$4,$5)`, dbID(id), dbID(a.ScopeID), dbID(d.ResourceID), int64(d.Revision), int64(d.SecurityEpoch))
		if err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.publication_heads(scope_id,profile_id,publication_id) VALUES($1,$2,$3) ON CONFLICT(scope_id,profile_id) DO UPDATE SET publication_id=EXCLUDED.publication_id`, dbID(a.ScopeID), dbID(profile), dbID(id))
	if err != nil {
		return err
	}
	action := "publish"
	if source != "" {
		action = "rollback"
	}
	return s.audit(ctx, tx, a, id, action)
}
func (s *Subscriptions) Rollback(ctx context.Context, a subscriptions.Actor, profile ir.ID, expected int64, request subscriptions.RollbackRequest) (subscriptions.Publication, error) {
	if !validIDs(a.ScopeID, a.ID, profile, request.PublicationID) || expected < 1 || request.ExpectedGeneration < 0 {
		return subscriptions.Publication{}, catalog.ErrInvalidInput
	}
	tx, err := s.catalog.pool.Begin(ctx)
	if err != nil {
		return subscriptions.Publication{}, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = dbgen.New(tx).LockScope(ctx, dbID(a.ScopeID)); err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	r, err := s.profile(ctx, tx, a.ScopeID, profile)
	if err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	id, replay, err := s.operation(ctx, tx, a, "rollback", struct {
		Profile  ir.ID
		Revision int64
		Request  subscriptions.RollbackRequest
	}{profile, expected, request}, jobs.NewID())
	if err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	if replay {
		p, err := s.readPublication(ctx, tx, a.ScopeID, id)
		if err == nil {
			err = tx.Commit(ctx)
		}
		return p, subError(err)
	}
	if r.Metadata.Revision != expected {
		return subscriptions.Publication{}, catalog.ErrRevisionConflict
	}
	old, err := s.readPublication(ctx, tx, a.ScopeID, request.PublicationID)
	if err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	if old.SubscriptionID != profile {
		return subscriptions.Publication{}, catalog.ErrNotFound
	}
	b, err := s.readBatch(ctx, tx, a.ScopeID, old.BatchID)
	if err == nil {
		err = s.safeBatch(ctx, tx, b, r)
	}
	if err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	keys := []string{}
	for _, t := range r.Payload.(*ir.SubscriptionProfile).Targets {
		if t.IsEnabled() {
			keys = append(keys, t.Key)
		}
	}
	oldKeys := []string{}
	for _, t := range b.Frozen.Input.Spec().Targets {
		oldKeys = append(oldKeys, t.Key)
	}
	slices.Sort(keys)
	slices.Sort(oldKeys)
	if !slices.Equal(keys, oldKeys) {
		return subscriptions.Publication{}, subscriptions.ErrBlocked
	}
	outputs, _, err := s.outputRows(ctx, tx, a.ScopeID, b.Batch.BatchID)
	if err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	if len(outputs) != len(oldKeys) {
		return subscriptions.Publication{}, subscriptions.ErrBlocked
	}
	for _, o := range outputs {
		if o.State != "ready" {
			return subscriptions.Publication{}, subscriptions.ErrBlocked
		}
	}
	_, generation, err := s.headID(ctx, tx, a.ScopeID, profile)
	if err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	if generation != int64(request.ExpectedGeneration) {
		return subscriptions.Publication{}, subscriptions.ErrObsolete
	}
	if err = s.createPublication(ctx, tx, a, id, profile, generation+1, b, old.PublicationID); err != nil {
		return subscriptions.Publication{}, subError(err)
	}
	p, err := s.readPublication(ctx, tx, a.ScopeID, id)
	if err == nil {
		err = tx.Commit(ctx)
	}
	return p, subError(err)
}
func (s *Subscriptions) readPublication(ctx context.Context, tx subTx, scope, id ir.ID) (subscriptions.Publication, error) {
	p := subscriptions.Publication{PublicationID: id, State: "historical", Targets: []subscriptions.PublishedTarget{}, BlockingReasons: []ir.Diagnostic{}}
	var generation int64
	err := tx.QueryRow(ctx, `SELECT profile_id::text,generation,batch_id::text,COALESCE(source_publication_id::text,''),created_at,created_by::text FROM public.publications WHERE scope_id=$1 AND id=$2`, dbID(scope), dbID(id)).Scan(&p.SubscriptionID, &generation, &p.BatchID, &p.SourcePublicationID, &p.CreatedAt, &p.CreatedBy)
	if err != nil {
		return p, err
	}
	p.Generation = subscriptions.Revision(generation)
	b, err := s.readBatch(ctx, tx, scope, p.BatchID)
	if err != nil {
		return p, err
	}
	p.Dependencies = b.Frozen.Dependencies
	outputs, _, err := s.outputRows(ctx, tx, scope, p.BatchID)
	if err != nil {
		return p, err
	}
	for _, o := range outputs {
		p.Targets = append(p.Targets, subscriptions.PublishedTarget{TargetKey: o.TargetKey, ArtifactID: o.ArtifactID, CoreBuildID: o.CoreBuildID, CoreBuildSHA256: o.CoreBuildSHA256, ClientPresetID: o.ClientPresetID, ClientPresetRevision: o.ClientPresetRevision, Format: o.Format, ValidationJobID: o.ValidationJobID})
	}
	head, _, err := s.headID(ctx, tx, scope, p.SubscriptionID)
	if err != nil {
		return p, err
	}
	if head == id {
		p.State = "active"
	}
	r, err := s.profile(ctx, tx, scope, p.SubscriptionID)
	if err == nil {
		err = s.safeBatch(ctx, tx, b, r)
	}
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, catalog.ErrNotFound) || errors.Is(err, subscriptions.ErrBlocked) {
		p.State = "blocked"
		p.BlockingReasons = append(p.BlockingReasons, subscriptions.Diagnostic(ir.ResourceDisabled, p.SubscriptionID, "/publication", ""))
		err = nil
	}
	return p, err
}
func (s *Subscriptions) Head(ctx context.Context, scope, profile ir.ID) (subscriptions.Head, error) {
	h := subscriptions.Head{State: "not_ready", BlockingReasons: []ir.Diagnostic{}}
	tx, err := s.catalog.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return h, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	id, gen, err := s.headID(ctx, tx, scope, profile)
	if err != nil {
		return h, subError(err)
	}
	if id != "" {
		p, err := s.readPublication(ctx, tx, scope, id)
		if err != nil {
			return h, subError(err)
		}
		h.PublicationID = id
		h.Generation = subscriptions.Counter(gen)
		h.State = p.State
		h.BlockingReasons = p.BlockingReasons
	}
	return h, subError(tx.Commit(ctx))
}
func (s *Subscriptions) Publications(ctx context.Context, scope, profile, after ir.ID, limit int) ([]subscriptions.Publication, error) {
	if !validIDs(scope, profile) || limit < 1 || limit > 201 {
		return nil, catalog.ErrInvalidInput
	}
	tx, err := s.catalog.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = s.profile(ctx, tx, scope, profile); err != nil {
		return nil, subError(err)
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM public.publications WHERE scope_id=$1 AND profile_id=$2 AND ($3::uuid IS NULL OR (created_at,id)>(SELECT created_at,id FROM public.publications WHERE scope_id=$1 AND profile_id=$2 AND id=$3)) ORDER BY created_at,id LIMIT $4`, dbID(scope), dbID(profile), nullableID(after), limit)
	if err != nil {
		return nil, subError(err)
	}
	ids := []ir.ID{}
	for rows.Next() {
		var id ir.ID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, subError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, subError(err)
	}
	out := []subscriptions.Publication{}
	for _, id := range ids {
		p, err := s.readPublication(ctx, tx, scope, id)
		if err != nil {
			return nil, subError(err)
		}
		out = append(out, p)
	}
	return out, subError(tx.Commit(ctx))
}
