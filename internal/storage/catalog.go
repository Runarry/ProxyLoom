package storage

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Catalog keeps encryption and database capabilities outside the domain and IR.
// Callbacks receive only transaction-scoped methods, never the pool or a commit.
type Catalog struct {
	pool *pgxpool.Pool
	box  *secretbox.Box
	q    *dbgen.Queries
}

var _ catalog.Repository = (*Catalog)(nil)

func NewCatalog(pool *pgxpool.Pool, box *secretbox.Box) (*Catalog, error) {
	if pool == nil || box == nil {
		return nil, catalog.ErrInvalidInput
	}
	return &Catalog{pool: pool, box: box, q: dbgen.New(pool)}, nil
}

// EnsureScope is an internal provisioning operation, not a management API.
// Existing scopes are preserved, including their name and revision counters.
func (c *Catalog) EnsureScope(ctx context.Context, scope ir.ID, name string) error {
	if scope.Validate() != nil || !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 256 {
		return catalog.ErrInvalidInput
	}
	return catalogError(c.q.EnsureScope(ctx, dbgen.EnsureScopeParams{ID: dbID(scope), Name: name}))
}

func (c *Catalog) Scope(ctx context.Context, scope ir.ID) (catalog.Scope, error) {
	if scope.Validate() != nil {
		return catalog.Scope{}, catalog.ErrInvalidInput
	}
	r, err := c.q.GetScope(ctx, dbID(scope))
	if err != nil {
		return catalog.Scope{}, catalogError(err)
	}
	return catalog.Scope{ID: irID(r.ID), Name: r.Name, CatalogRevision: r.CatalogRevision, AuthEpoch: r.AuthEpoch}, nil
}

func (c *Catalog) Head(ctx context.Context, scope, id ir.ID) (ir.Resource, error) {
	if !validIDs(scope, id) {
		return ir.Resource{}, catalog.ErrInvalidInput
	}
	index, err := c.q.GetResourceIndex(ctx, dbgen.GetResourceIndexParams{ScopeID: dbID(scope), ID: dbID(id)})
	if err != nil {
		return ir.Resource{}, catalogError(err)
	}
	if index.DeletedAt.Valid || !typedCatalogKind(ir.ResourceKind(index.Kind)) {
		return ir.Resource{}, catalog.ErrNotFound
	}
	r, err := c.q.GetResourceHead(ctx, dbgen.GetResourceHeadParams{ScopeID: dbID(scope), ID: dbID(id)})
	if err != nil {
		return ir.Resource{}, catalogError(err)
	}
	return c.open(dbgen.GetResourceRevisionRow(r))
}

// Revision returns metadata and payload from the same immutable snapshot,
// including after soft deletion. Authorization belongs to the calling service.
func (c *Catalog) Revision(ctx context.Context, scope, id ir.ID, revision int64) (ir.Resource, error) {
	if !validIDs(scope, id) || revision < 1 {
		return ir.Resource{}, catalog.ErrInvalidInput
	}
	r, err := c.q.GetResourceRevision(ctx, dbgen.GetResourceRevisionParams{ScopeID: dbID(scope), ResourceID: dbID(id), Revision: revision})
	if err != nil {
		return ir.Resource{}, catalogError(err)
	}
	return c.open(r)
}

func (c *Catalog) open(row dbgen.GetResourceRevisionRow) (ir.Resource, error) {
	var payload secretbox.Payload
	var wrapping secretbox.Wrapping
	if json.Unmarshal(row.Envelope, &payload) != nil || json.Unmarshal(row.Wrapping, &wrapping) != nil {
		return ir.Resource{}, catalog.ErrCrypto
	}
	aad := recordContext(row)
	plain, err := c.box.Open(aad, payload, wrapping)
	if err != nil {
		return ir.Resource{}, catalog.ErrCrypto
	}
	defer clear(plain)
	resource, err := ir.DecodeResource(plain)
	if err != nil || resource.Metadata.ScopeID != aad.ScopeID || resource.Metadata.ResourceID != aad.ObjectID ||
		resource.Metadata.Revision != aad.Revision || resource.Metadata.SchemaVersion != aad.SchemaVersion ||
		resource.Metadata.SecurityEpoch != row.SecurityEpoch {
		return ir.Resource{}, catalog.ErrCrypto
	}
	canonical, err := catalog.Canonical(resource)
	if err != nil {
		return ir.Resource{}, catalog.ErrCrypto
	}
	defer clear(canonical)
	digest, err := c.box.Digest(secretbox.PurposeResourceContent, canonical)
	if err != nil || !hmac.Equal(digest, row.ContentHmac) {
		return ir.Resource{}, catalog.ErrCrypto
	}
	return resource, nil
}

func recordContext(r dbgen.GetResourceRevisionRow) secretbox.Context {
	return secretbox.Context{ScopeID: irID(r.ScopeID), Table: secretbox.TableResourceRevisions,
		ObjectID: irID(r.ResourceID), Revision: r.Revision, SchemaVersion: int(r.SchemaVersion)}
}

type catalogTx struct {
	mu     sync.Mutex
	store  *Catalog
	tx     pgx.Tx
	q      *dbgen.Queries
	scope  ir.ID
	active bool
	dirty  bool
	failed error
}

// Transact serializes writes by scope before locking related resource IDs in
// UUID order. Multiple successful mutations advance the catalog exactly once.
// A callback must use its supplied Tx for all writes and return synchronously.
func (c *Catalog) Transact(ctx context.Context, scope ir.ID, fn func(catalog.Tx) error) error {
	if fn == nil {
		return catalog.ErrInvalidInput
	}
	return c.transact(ctx, scope, func(t *catalogTx) error { return fn(t) })
}

func (c *Catalog) transact(ctx context.Context, scope ir.ID, fn func(*catalogTx) error) error {
	return catalogError(c.transactRaw(ctx, scope, fn))
}

// transactRaw is the package-private bridge for atomic domain operations. It
// preserves their safe errors while retaining scope serialization, the failed
// mutation latch, and a single catalog epoch advance.
func (c *Catalog) transactRaw(ctx context.Context, scope ir.ID, fn func(*catalogTx) error) error {
	if scope.Validate() != nil {
		return catalog.ErrInvalidInput
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return catalogError(err)
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	q := dbgen.New(tx)
	if _, err := q.LockScope(ctx, dbID(scope)); err != nil {
		return catalogError(err)
	}
	t := &catalogTx{store: c, tx: tx, q: q, scope: scope, active: true}
	defer t.close()
	err = fn(t)
	t.mu.Lock()
	t.active = false
	failed, dirty := t.failed, t.dirty
	t.mu.Unlock()
	if err != nil {
		return err
	}
	if failed != nil {
		return failed
	}
	if dirty {
		if err := q.AdvanceCatalog(ctx, dbID(scope)); err != nil {
			return catalogError(err)
		}
	}
	return catalogError(tx.Commit(ctx))
}

func (t *catalogTx) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active = false
}

// The latch prevents a callback from swallowing a failed mutation and
// accidentally committing earlier writes. An escaped Tx cannot be reused.
func (t *catalogTx) mutate(fn func() (ir.Resource, error)) (ir.Resource, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return ir.Resource{}, catalog.ErrTransactionClosed
	}
	if t.failed != nil {
		return ir.Resource{}, t.failed
	}
	r, err := fn()
	if err != nil {
		t.failed = catalogError(err)
		return ir.Resource{}, t.failed
	}
	t.dirty = true
	return r, nil
}

func (t *catalogTx) runLocked(fn func() error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return catalog.ErrTransactionClosed
	}
	if t.failed != nil {
		return t.failed
	}
	if err := fn(); err != nil {
		t.failed = catalogError(err)
		return t.failed
	}
	t.dirty = true
	return nil
}

func (t *catalogTx) Create(ctx context.Context, input catalog.CreateInput) (ir.Resource, error) {
	return t.mutate(func() (ir.Resource, error) {
		if _, preset := input.Payload.(*ir.ClientPreset); preset {
			return ir.Resource{}, catalog.ErrInvalidInput
		}
		r, err := catalog.New(t.scope, input)
		if err != nil {
			return ir.Resource{}, err
		}
		if err := t.checkPolicyMembers(ctx, r); err != nil {
			return ir.Resource{}, err
		}
		if err := t.checkRoutingReferences(ctx, r); err != nil {
			return ir.Resource{}, err
		}
		if err := t.checkDNSReferences(ctx, r); err != nil {
			return ir.Resource{}, err
		}
		refs, err := t.resourceReferences(ctx, r)
		if err != nil {
			return ir.Resource{}, err
		}
		if err := t.lockAndCheckRefs(ctx, nil, refs); err != nil {
			return ir.Resource{}, err
		}
		m := r.Metadata
		if err := t.q.InsertResource(ctx, dbgen.InsertResourceParams{ID: dbID(m.ResourceID), ScopeID: dbID(t.scope),
			Kind: string(m.Kind), Name: m.Name, Enabled: m.Enabled}); err != nil {
			return ir.Resource{}, err
		}
		return r, t.persist(ctx, r, refs, false)
	})
}

func (t *catalogTx) Update(ctx context.Context, id ir.ID, expected int64, input catalog.UpdateInput) (ir.Resource, error) {
	return t.change(ctx, id, expected, false, func(old ir.Resource) (ir.Resource, error) {
		next, err := catalog.Apply(old, input)
		if err == nil {
			err = t.checkPolicyMembers(ctx, next)
		}
		if err == nil {
			err = t.checkRoutingReferences(ctx, next)
		}
		if err == nil {
			err = t.checkDNSReferences(ctx, next)
		}
		return next, err
	})
}

func (t *catalogTx) Delete(ctx context.Context, id ir.ID, expected int64) (ir.Resource, error) {
	return t.change(ctx, id, expected, true, catalog.Delete)
}

func (t *catalogTx) Revoke(ctx context.Context, id ir.ID, expected int64) (ir.Resource, error) {
	return t.change(ctx, id, expected, false, catalog.Revoke)
}

func (t *catalogTx) change(ctx context.Context, id ir.ID, expected int64, deleted bool,
	apply func(ir.Resource) (ir.Resource, error)) (ir.Resource, error) {
	return t.mutate(func() (ir.Resource, error) {
		if id.Validate() != nil || expected < 1 {
			return ir.Resource{}, catalog.ErrInvalidInput
		}
		// Scope lock already excludes every supported concurrent business writer.
		// Read first to derive the complete sorted lock set from both snapshots.
		row, err := t.q.GetResourceHead(ctx, dbgen.GetResourceHeadParams{ScopeID: dbID(t.scope), ID: dbID(id)})
		if err != nil {
			return ir.Resource{}, err
		}
		if row.Revision != expected {
			return ir.Resource{}, catalog.ErrRevisionConflict
		}
		old, err := t.store.open(dbgen.GetResourceRevisionRow(row))
		if err != nil {
			return ir.Resource{}, err
		}
		next, err := apply(old)
		if err != nil {
			return ir.Resource{}, err
		}
		refs, err := t.resourceReferences(ctx, next)
		if err != nil {
			return ir.Resource{}, err
		}
		previousRefs, err := t.resourceReferences(ctx, old)
		if err != nil {
			return ir.Resource{}, err
		}
		ids := []ir.ID{id}
		for _, ref := range previousRefs {
			ids = append(ids, ref.TargetID)
		}
		if err := t.lockAndCheckRefs(ctx, ids, refs); err != nil {
			return ir.Resource{}, err
		}
		return next, t.persist(ctx, next, refs, deleted)
	})
}

func (t *catalogTx) lockAndCheckRefs(ctx context.Context, related []ir.ID, refs []catalog.Reference) error {
	ids := append([]ir.ID{}, related...)
	for _, ref := range refs {
		ids = append(ids, ref.TargetID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	unique := make([]pgtype.UUID, 0, len(ids))
	for i, id := range ids {
		if i == 0 || id != ids[i-1] {
			unique = append(unique, dbID(id))
		}
	}
	if len(unique) != 0 {
		if _, err := t.q.LockResources(ctx, dbgen.LockResourcesParams{ScopeID: dbID(t.scope), Column2: unique}); err != nil {
			return err
		}
	}
	for _, ref := range refs {
		r, err := t.q.GetResourceIndex(ctx, dbgen.GetResourceIndexParams{ScopeID: dbID(t.scope), ID: dbID(ref.TargetID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return catalog.ErrInvalidReference
		}
		if err != nil {
			return err
		}
		if r.Kind != string(ref.ExpectedKind) || !r.HeadRevision.Valid {
			return catalog.ErrInvalidReference
		}
		// Soft-deleted and disabled targets retain their references. Publication
		// must separately enforce availability and current security epochs.
		if ref.TargetRevision != nil {
			if _, err := t.q.GetResourceRevision(ctx, dbgen.GetResourceRevisionParams{ScopeID: dbID(t.scope),
				ResourceID: dbID(ref.TargetID), Revision: *ref.TargetRevision}); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return catalog.ErrInvalidReference
				}
				return err
			}
		}
	}
	return nil
}

func (t *catalogTx) persist(ctx context.Context, resource ir.Resource, refs []catalog.Reference, deleted bool) error {
	plain, err := catalog.Canonical(resource)
	if err != nil {
		return err
	}
	defer clear(plain)
	return t.persistPlain(ctx, resource.Metadata, plain, refs, deleted)
}

func (t *catalogTx) persistPlain(ctx context.Context, m ir.Metadata, plain []byte, refs []catalog.Reference, deleted bool) error {
	payload, wrapping, err := t.store.box.Seal(secretbox.Context{ScopeID: t.scope, Table: secretbox.TableResourceRevisions,
		ObjectID: m.ResourceID, Revision: m.Revision, SchemaVersion: m.SchemaVersion}, plain)
	if err != nil {
		return catalog.ErrCrypto
	}
	digest, err := t.store.box.Digest(secretbox.PurposeResourceContent, plain)
	if err != nil {
		return catalog.ErrCrypto
	}
	envelopeJSON, err := json.Marshal(payload)
	if err != nil {
		return catalog.ErrCrypto
	}
	wrappingJSON, err := json.Marshal(wrapping)
	if err != nil {
		return catalog.ErrCrypto
	}
	if err := t.q.InsertResourceRevision(ctx, dbgen.InsertResourceRevisionParams{ScopeID: dbID(t.scope),
		ResourceID: dbID(m.ResourceID), Revision: m.Revision, SchemaVersion: int32(m.SchemaVersion),
		SecurityEpoch: m.SecurityEpoch, Envelope: envelopeJSON, ContentHmac: digest}); err != nil {
		return err
	}
	if err := t.q.InsertResourceWrapping(ctx, dbgen.InsertResourceWrappingParams{ScopeID: dbID(t.scope),
		ResourceID: dbID(m.ResourceID), Revision: m.Revision, Wrapping: wrappingJSON}); err != nil {
		return err
	}
	for _, ref := range refs {
		var revision pgtype.Int8
		if ref.TargetRevision != nil {
			revision = pgtype.Int8{Int64: *ref.TargetRevision, Valid: true}
		}
		if err := t.q.InsertResourceReference(ctx, dbgen.InsertResourceReferenceParams{ScopeID: dbID(t.scope),
			ResourceID: dbID(m.ResourceID), Revision: m.Revision, RefPath: ref.Path,
			TargetResourceID: dbID(ref.TargetID), TargetRevision: revision, ExpectedKind: string(ref.ExpectedKind)}); err != nil {
			return err
		}
	}
	if err := t.q.DeleteResourceTags(ctx, dbgen.DeleteResourceTagsParams{ScopeID: dbID(t.scope), ResourceID: dbID(m.ResourceID)}); err != nil {
		return err
	}
	for _, tag := range m.Tags {
		if err := t.q.InsertResourceTag(ctx, dbgen.InsertResourceTagParams{ScopeID: dbID(t.scope), ResourceID: dbID(m.ResourceID), Tag: tag}); err != nil {
			return err
		}
	}
	var deletedAt pgtype.Timestamptz
	if deleted {
		deletedAt = pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true}
	}
	return t.q.UpdateResourceHead(ctx, dbgen.UpdateResourceHeadParams{ScopeID: dbID(t.scope), ID: dbID(m.ResourceID),
		Name: m.Name, HeadRevision: pgtype.Int8{Int64: m.Revision, Valid: true}, Enabled: m.Enabled,
		SecurityEpoch: m.SecurityEpoch, DeletedAt: deletedAt})
}

// WrappingVersion returns the independent CAS counter for a rotation worker.
// It exposes no key identifiers or ciphertext and grants no rotation authority.
func (c *Catalog) WrappingVersion(ctx context.Context, scope, id ir.ID, revision int64) (int64, error) {
	if !validIDs(scope, id) || revision < 1 {
		return 0, catalog.ErrInvalidInput
	}
	version, err := c.q.GetWrappingVersion(ctx, dbgen.GetWrappingVersionParams{ScopeID: dbID(scope), ResourceID: dbID(id), Revision: revision})
	return version, catalogError(err)
}

// RewrapRevision replaces only the wrapping selected by its CAS counter.
// Ciphertext and resource, catalog, and security revisions remain unchanged.
func (c *Catalog) RewrapRevision(ctx context.Context, scope, id ir.ID, revision, expectedWrapVersion int64) error {
	if !validIDs(scope, id) || revision < 1 || expectedWrapVersion < 1 || expectedWrapVersion == math.MaxInt64 {
		return catalog.ErrInvalidInput
	}
	row, err := c.q.GetResourceRevision(ctx, dbgen.GetResourceRevisionParams{ScopeID: dbID(scope), ResourceID: dbID(id), Revision: revision})
	if err != nil {
		return catalogError(err)
	}
	if row.WrapVersion != expectedWrapVersion {
		return catalog.ErrWrapConflict
	}
	var payload secretbox.Payload
	var wrapping secretbox.Wrapping
	if json.Unmarshal(row.Envelope, &payload) != nil || json.Unmarshal(row.Wrapping, &wrapping) != nil {
		return catalog.ErrCrypto
	}
	next, err := c.box.Rewrap(recordContext(row), payload, wrapping)
	if err != nil {
		return catalog.ErrCrypto
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return catalog.ErrCrypto
	}
	count, err := c.q.CompareAndSwapWrapping(ctx, dbgen.CompareAndSwapWrappingParams{ScopeID: dbID(scope),
		ResourceID: dbID(id), Revision: revision, WrapVersion: expectedWrapVersion, Wrapping: encoded})
	if err != nil {
		return catalogError(err)
	}
	if count != 1 {
		return catalog.ErrWrapConflict
	}
	return nil
}

func validIDs(ids ...ir.ID) bool {
	for _, id := range ids {
		if id.Validate() != nil {
			return false
		}
	}
	return true
}

// dbID is called only after domain validation or on trusted generated IDs.
func dbID(id ir.ID) pgtype.UUID {
	var result pgtype.UUID
	_ = result.Scan(string(id))
	return result
}

func irID(id pgtype.UUID) ir.ID {
	if !id.Valid {
		return ""
	}
	return ir.ID(id.String())
}

func catalogError(err error) error {
	if err == nil {
		return nil
	}
	for _, safe := range []error{catalog.ErrNotFound, catalog.ErrRevisionConflict, catalog.ErrInvalidReference,
		catalog.ErrInvalidInput, catalog.ErrUnavailable, catalog.ErrCrypto, catalog.ErrIdempotencyConflict,
		catalog.ErrWrapConflict, catalog.ErrTransactionClosed, jobs.ErrConflict, jobs.ErrInvalidInput, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return catalog.ErrNotFound
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) {
		switch pgerr.Code {
		case "23503":
			return catalog.ErrInvalidReference
		case "23505", "40001", "40P01":
			return catalog.ErrRevisionConflict
		case "23514", "23502", "22001", "22003", "22P02":
			return catalog.ErrInvalidInput
		}
	}
	return catalog.ErrUnavailable
}
