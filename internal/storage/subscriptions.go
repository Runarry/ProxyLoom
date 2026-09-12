package storage

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/compiler"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
	"github.com/jackc/pgx/v5"
	"slices"
	"time"
)

type Subscriptions struct {
	catalog  *Catalog
	jobs     *Jobs
	compiler *compiler.Compiler
	cores    *capability.Catalog
	pepper   []byte
}

var _ subscriptions.Repository = (*Subscriptions)(nil)

func NewSubscriptions(c *Catalog, j *Jobs, pepper []byte) (*Subscriptions, error) {
	if c == nil || j == nil || len(pepper) != 32 {
		return nil, catalog.ErrInvalidInput
	}
	cores, err := capability.Load()
	if err != nil {
		return nil, catalog.ErrUnavailable
	}
	return &Subscriptions{catalog: c, jobs: j, compiler: compiler.New(cores), cores: cores, pepper: slices.Clone(pepper)}, nil
}
func (s *Subscriptions) Close() { clear(s.pepper) }
func subError(err error) error {
	if err == nil {
		return nil
	}
	for _, e := range []error{subscriptions.ErrObsolete, subscriptions.ErrBlocked, subscriptions.ErrConfirmation, subscriptions.ErrToken, subscriptions.ErrNotReady, catalog.ErrInvalidInput, catalog.ErrInvalidReference, catalog.ErrUnavailable, catalog.ErrCrypto, catalog.ErrNotFound, catalog.ErrRevisionConflict, catalog.ErrIdempotencyConflict, jobs.ErrLeaseLost, jobs.ErrCanceled, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, e) {
			return e
		}
	}
	var d ir.Diagnostics
	if errors.As(err, &d) {
		return d
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return catalog.ErrNotFound
	}
	return catalog.ErrUnavailable
}

type subTx = dbgen.DBTX
type frozenPublication struct {
	Input        ir.FrozenInput             `json:"input"`
	Dependencies []subscriptions.Dependency `json:"dependencies"`
}
type storedBatch struct {
	Scope             ir.ID
	Batch             subscriptions.Batch
	AuthEpoch         int64
	Frozen            frozenPublication
	InputHMAC         []byte
	BasePublicationID ir.ID
}

func subAAD(scope, id ir.ID, table string) secretbox.Context {
	return secretbox.Context{ScopeID: scope, ObjectID: id, Table: table, Revision: 1, SchemaVersion: 1}
}
func (s *Subscriptions) seal(scope, id ir.ID, table string, data []byte) ([]byte, []byte, error) {
	p, w, err := s.catalog.box.Seal(subAAD(scope, id, table), data)
	if err != nil {
		return nil, nil, catalog.ErrCrypto
	}
	pb, _ := json.Marshal(p)
	wb, _ := json.Marshal(w)
	return pb, wb, nil
}
func (s *Subscriptions) open(scope, id ir.ID, table string, payload, wrapping []byte) ([]byte, error) {
	var p secretbox.Payload
	var w secretbox.Wrapping
	if json.Unmarshal(payload, &p) != nil || json.Unmarshal(wrapping, &w) != nil {
		return nil, catalog.ErrCrypto
	}
	data, err := s.catalog.box.Open(subAAD(scope, id, table), p, w)
	if err != nil {
		return nil, catalog.ErrCrypto
	}
	return data, nil
}
func (s *Subscriptions) audit(ctx context.Context, tx pgx.Tx, a subscriptions.Actor, id ir.ID, action string) error {
	_, err := tx.Exec(ctx, `INSERT INTO public.publication_audit_events(scope_id,actor_id,object_id,action) VALUES($1,$2,$3,$4)`, dbID(a.ScopeID), dbID(a.ID), dbID(id), action)
	return err
}
func (s *Subscriptions) operation(ctx context.Context, tx pgx.Tx, a subscriptions.Actor, route string, request any, id ir.ID) (ir.ID, bool, error) {
	if a.ScopeID.Validate() != nil || a.ID.Validate() != nil || (a.Key != "" && !safeIdempotencyToken(a.Key)) {
		return "", false, catalog.ErrInvalidInput
	}
	if a.Key == "" {
		return id, false, nil
	}
	data, err := json.Marshal(struct {
		Scope, Actor ir.ID
		Route        string
		Request      any
	}{a.ScopeID, a.ID, route, request})
	if err != nil {
		return "", false, catalog.ErrInvalidInput
	}
	defer clear(data)
	digest, err := s.catalog.box.Digest(secretbox.PurposeIdempotency, data)
	if err != nil {
		return "", false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.subscription_operations(scope_id,actor_id,route,key,request_hmac,operation_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, dbID(a.ScopeID), dbID(a.ID), route, a.Key, digest, dbID(id))
	if err != nil {
		return "", false, err
	}
	var oldID ir.ID
	var previous []byte
	err = tx.QueryRow(ctx, `SELECT operation_id::text,request_hmac FROM public.subscription_operations WHERE scope_id=$1 AND actor_id=$2 AND route=$3 AND key=$4`, dbID(a.ScopeID), dbID(a.ID), route, a.Key).Scan(&oldID, &previous)
	if err != nil {
		return "", false, err
	}
	if !hmac.Equal(digest, previous) {
		return "", false, catalog.ErrIdempotencyConflict
	}
	return oldID, oldID != id, nil
}
func (s *Subscriptions) profile(ctx context.Context, tx subTx, scope, id ir.ID) (ir.Resource, error) {
	row, err := dbgen.New(tx).GetResourceHead(ctx, dbgen.GetResourceHeadParams{ScopeID: dbID(scope), ID: dbID(id)})
	if err != nil {
		return ir.Resource{}, err
	}
	r, err := s.catalog.open(dbgen.GetResourceRevisionRow(row))
	if err != nil {
		return ir.Resource{}, err
	}
	if r.Metadata.Kind != ir.KindSubscriptionProfile {
		return ir.Resource{}, catalog.ErrNotFound
	}
	return r, nil
}
func (s *Subscriptions) Compile(ctx context.Context, a subscriptions.Actor, id ir.ID, expected int64, request subscriptions.CompileRequest) (subscriptions.Batch, error) {
	if !validIDs(a.ScopeID, a.ID, id) || expected < 1 {
		return subscriptions.Batch{}, catalog.ErrInvalidInput
	}
	tx, err := s.catalog.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return subscriptions.Batch{}, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	scope, err := dbgen.New(tx).LockScope(ctx, dbID(a.ScopeID))
	if err != nil {
		return subscriptions.Batch{}, subError(err)
	}
	r, err := s.profile(ctx, tx, a.ScopeID, id)
	if err != nil {
		return subscriptions.Batch{}, subError(err)
	}
	if !r.Metadata.Enabled {
		return subscriptions.Batch{}, subscriptions.ErrBlocked
	}
	keys := slices.Clone(request.TargetKeys)
	slices.Sort(keys)
	wanted := []string{}
	for _, target := range r.Payload.(*ir.SubscriptionProfile).Targets {
		if target.IsEnabled() {
			wanted = append(wanted, target.Key)
		}
	}
	slices.Sort(wanted)
	if !slices.Equal(keys, wanted) || len(wanted) == 0 {
		return subscriptions.Batch{}, catalog.ErrInvalidInput
	}
	batchID := jobs.NewID()
	op, replay, err := s.operation(ctx, tx, a, "compile", struct {
		Profile  ir.ID
		Revision int64
		Keys     []string
	}{id, expected, request.TargetKeys}, batchID)
	if err != nil {
		return subscriptions.Batch{}, subError(err)
	}
	if replay {
		if err = tx.Commit(ctx); err != nil {
			return subscriptions.Batch{}, subError(err)
		}
		return s.GetBatch(ctx, a, op)
	}
	if r.Metadata.Revision != expected {
		return subscriptions.Batch{}, catalog.ErrRevisionConflict
	}
	resources, stale, err := s.resources(ctx, tx, a.ScopeID)
	if err != nil {
		return subscriptions.Batch{}, subError(err)
	}
	frozen, deps, err := subscriptions.Select(batchID, r, resources, stale, scope.CatalogRevision, scope.AuthEpoch, s.cores)
	if err != nil {
		return subscriptions.Batch{}, subError(err)
	}
	for _, target := range frozen.Spec().Targets {
		var enabled bool
		if err := tx.QueryRow(ctx, `SELECT enabled FROM public.core_builds WHERE id=$1`, dbID(target.CoreBuildID)).Scan(&enabled); err != nil {
			return subscriptions.Batch{}, subError(err)
		}
		if !enabled {
			return subscriptions.Batch{}, subscriptions.ErrBlocked
		}
	}
	plain, err := json.Marshal(frozenPublication{Input: frozen, Dependencies: deps})
	if err != nil {
		return subscriptions.Batch{}, catalog.ErrInvalidInput
	}
	defer clear(plain)
	envelope, wrapping, err := s.seal(a.ScopeID, batchID, secretbox.TableCompileBatches, plain)
	if err != nil {
		return subscriptions.Batch{}, err
	}
	digest, err := s.catalog.box.Digest(secretbox.PurposeCompileInput, plain)
	if err != nil {
		return subscriptions.Batch{}, catalog.ErrCrypto
	}
	payload, _ := json.Marshal(struct {
		BatchID ir.ID `json:"batch_id"`
	}{batchID})
	job, err := s.jobs.EnqueueTx(ctx, tx, jobs.EnqueueInput{ScopeID: a.ScopeID, BatchID: batchID, Executor: jobs.APIWorker, Type: jobs.Compile, Payload: payload})
	if err != nil {
		return subscriptions.Batch{}, subError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.compile_batches(id,scope_id,profile_id,profile_revision,catalog_revision,auth_epoch,envelope,input_hmac,compile_job_id,base_publication_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,(SELECT publication_id FROM public.publication_heads WHERE scope_id=$2 AND profile_id=$3))`, dbID(batchID), dbID(a.ScopeID), dbID(id), expected, scope.CatalogRevision, scope.AuthEpoch, envelope, digest, dbID(job.ID))
	if err != nil {
		return subscriptions.Batch{}, subError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.compile_batch_wrappings(scope_id,batch_id,wrapping) VALUES($1,$2,$3)`, dbID(a.ScopeID), dbID(batchID), wrapping)
	if err == nil {
		err = s.audit(ctx, tx, a, batchID, "compile")
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return subscriptions.Batch{}, subError(err)
	}
	return s.GetBatch(ctx, a, batchID)
}
func (s *Subscriptions) resources(ctx context.Context, tx pgx.Tx, scope ir.ID) (map[ir.ID]ir.Resource, map[ir.ID]bool, error) {
	rows, err := tx.Query(ctx, `SELECT r.scope_id,r.resource_id,r.revision,r.schema_version,r.security_epoch,r.envelope,r.content_hmac,w.wrapping,w.wrap_version,COALESCE(b.state='stale',false) FROM public.resources h JOIN public.resource_revisions r ON r.scope_id=h.scope_id AND r.resource_id=h.id AND r.revision=h.head_revision JOIN public.resource_revision_wrappings w ON w.scope_id=r.scope_id AND w.resource_id=r.resource_id AND w.revision=r.revision LEFT JOIN public.node_bindings b ON b.scope_id=h.scope_id AND b.node_id=h.id WHERE h.scope_id=$1 AND h.deleted_at IS NULL AND h.kind IN ('node','chain','policy_group','routing_profile','dns_profile','rule_set','client_preset') ORDER BY h.id`, dbID(scope))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	out := map[ir.ID]ir.Resource{}
	stale := map[ir.ID]bool{}
	for rows.Next() {
		var row dbgen.GetResourceRevisionRow
		var isStale bool
		if err := rows.Scan(&row.ScopeID, &row.ResourceID, &row.Revision, &row.SchemaVersion, &row.SecurityEpoch, &row.Envelope, &row.ContentHmac, &row.Wrapping, &row.WrapVersion, &isStale); err != nil {
			return nil, nil, err
		}
		r, err := s.catalog.open(row)
		if err != nil {
			return nil, nil, err
		}
		out[r.Metadata.ResourceID] = r
		stale[r.Metadata.ResourceID] = isStale
	}
	return out, stale, rows.Err()
}
func (s *Subscriptions) readBatch(ctx context.Context, tx subTx, scope, id ir.ID) (storedBatch, error) {
	b := storedBatch{Scope: scope}
	b.Batch.BatchID = id
	var envelope, wrapping, diags []byte
	var cat, rev, profileRev int64
	var job ir.ID
	err := tx.QueryRow(ctx, `SELECT b.profile_id::text,b.profile_revision,b.catalog_revision,b.auth_epoch,b.revision,b.state,b.envelope,w.wrapping,b.input_hmac,b.diagnostics,COALESCE(b.preview_hash,''),b.created_at,b.compile_job_id::text,COALESCE(b.base_publication_id::text,'') FROM public.compile_batches b JOIN public.compile_batch_wrappings w ON w.scope_id=b.scope_id AND w.batch_id=b.id WHERE b.scope_id=$1 AND b.id=$2`, dbID(scope), dbID(id)).Scan(&b.Batch.SubscriptionID, &profileRev, &cat, &b.AuthEpoch, &rev, &b.Batch.State, &envelope, &wrapping, &b.InputHMAC, &diags, &b.Batch.EffectivePreviewHash, &b.Batch.CreatedAt, &job, &b.BasePublicationID)
	if err != nil {
		return b, err
	}
	b.Batch.Revision = subscriptions.Revision(rev)
	b.Batch.CatalogRevision = subscriptions.Revision(cat)
	b.Batch.SubscriptionRevision = subscriptions.Revision(profileRev)
	b.Batch.JobIDs = []ir.ID{job}
	b.Batch.Outputs = []subscriptions.Output{}
	b.Batch.BlockingReasons = []ir.Diagnostic{}
	plain, err := s.open(scope, id, secretbox.TableCompileBatches, envelope, wrapping)
	if err != nil {
		return b, err
	}
	defer clear(plain)
	digest, err := s.catalog.box.Digest(secretbox.PurposeCompileInput, plain)
	if err != nil || !hmac.Equal(digest, b.InputHMAC) || json.Unmarshal(plain, &b.Frozen) != nil || json.Unmarshal(diags, &b.Batch.Diagnostics) != nil {
		return b, catalog.ErrCrypto
	}
	b.Batch.Dependencies = b.Frozen.Dependencies
	return b, nil
}

// Reconcile uses persisted results. A restart never depends on an in-memory
// completion callback and no response handler creates validation work.
func (s *Subscriptions) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.Reconcile(ctx); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
