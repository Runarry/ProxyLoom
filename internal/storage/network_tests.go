package storage

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"slices"
	"sort"
	"time"

	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/networktest"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5"
)

type NetworkTests struct {
	catalog  *Catalog
	jobs     *Jobs
	cores    *capability.Catalog
	resolver networktest.Resolver
}

func NewNetworkTests(c *Catalog, j *Jobs, resolver networktest.Resolver) (*NetworkTests, error) {
	if c == nil || j == nil {
		return nil, jobs.ErrInvalidInput
	}
	cores, err := capability.Load()
	if err != nil {
		return nil, err
	}
	return &NetworkTests{c, j, cores, resolver}, nil
}
func testError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{networktest.ErrAddress, networktest.ErrTarget, jobs.ErrBudgetExceeded, jobs.ErrInvalidInput, catalog.ErrIdempotencyConflict, catalog.ErrRevisionConflict, catalog.ErrNotFound} {
		if errors.Is(err, known) {
			return known
		}
	}
	var diagnostic ir.Diagnostics
	if errors.As(err, &diagnostic) {
		return diagnostic
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return catalog.ErrNotFound
	}
	return jobs.ErrUnavailable
}

const targetColumns = `id::text,revision,name,enabled,config,safety_checked_at,created_at`

func scanTestTarget(row pgx.Row) (networktest.Target, error) {
	var t networktest.Target
	var config []byte
	var revision int64
	err := row.Scan(&t.ID, &revision, &t.Name, &t.Enabled, &config, &t.CheckedAt, &t.CreatedAt)
	if err != nil {
		return t, testError(err)
	}
	t.Revision = runnerprotocol.Sequence(revision)
	t.SafetyState = "approved"
	t.Diagnostics = []ir.Diagnostic{}
	if json.Unmarshal(config, &t.Config) != nil || t.Config.Validate() != nil {
		return t, jobs.ErrUnavailable
	}
	return t, nil
}
func (s *NetworkTests) Target(ctx context.Context, scope, id ir.ID) (networktest.Target, error) {
	if !validIDs(scope, id) {
		return networktest.Target{}, jobs.ErrInvalidInput
	}
	return scanTestTarget(s.catalog.pool.QueryRow(ctx, `SELECT `+targetColumns+` FROM public.test_targets WHERE scope_id=$1 AND id=$2 AND deleted_at IS NULL`, dbID(scope), dbID(id)))
}
func (s *NetworkTests) Targets(ctx context.Context, scope ir.ID, limit int, after networktest.PagePosition, enabled *bool) ([]networktest.Target, bool, error) {
	if scope.Validate() != nil || limit < 1 || limit > 200 || after.ID != "" && (after.ID.Validate() != nil || after.CreatedAt.IsZero()) {
		return nil, false, jobs.ErrInvalidInput
	}
	rows, err := s.catalog.pool.Query(ctx, `SELECT `+targetColumns+` FROM public.test_targets WHERE scope_id=$1 AND deleted_at IS NULL AND ($2::uuid IS NULL OR (created_at,id)>($3,$2)) AND ($5::boolean IS NULL OR enabled=$5) ORDER BY created_at,id LIMIT $4`, dbID(scope), nullableID(after.ID), after.CreatedAt, limit+1, enabled)
	if err != nil {
		return nil, false, testError(err)
	}
	defer rows.Close()
	items := []networktest.Target{}
	for rows.Next() {
		item, err := scanTestTarget(rows)
		if err != nil {
			return nil, false, err
		}
		items = append(items, item)
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	return items, more, testError(rows.Err())
}
func (s *NetworkTests) operation(ctx context.Context, tx pgx.Tx, a networktest.Actor, route string, request any, id ir.ID) (ir.ID, bool, error) {
	if !validIDs(a.ScopeID, a.ID) || !safeIdempotencyToken(a.Key) {
		return "", false, jobs.ErrInvalidInput
	}
	data, err := json.Marshal(request)
	if err != nil {
		return "", false, err
	}
	defer clear(data)
	digest, err := s.catalog.box.Digest(secretbox.PurposeIdempotency, data)
	if err != nil {
		return "", false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.test_operations(scope_id,actor_id,route,key,request_hmac,operation_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, dbID(a.ScopeID), dbID(a.ID), route, a.Key, digest, dbID(id))
	if err != nil {
		return "", false, err
	}
	var existing ir.ID
	var previous []byte
	err = tx.QueryRow(ctx, `SELECT operation_id::text,request_hmac FROM public.test_operations WHERE scope_id=$1 AND actor_id=$2 AND route=$3 AND key=$4`, dbID(a.ScopeID), dbID(a.ID), route, a.Key).Scan(&existing, &previous)
	if err != nil {
		return "", false, err
	}
	if !hmac.Equal(previous, digest) {
		return "", false, catalog.ErrIdempotencyConflict
	}
	return existing, existing != id, nil
}

// Replay is independent of current DNS, object heads and target availability.
// The insertion path still checks under the scope lock to close concurrent races.
func (s *NetworkTests) replay(ctx context.Context, a networktest.Actor, route string, request any) (ir.ID, error) {
	if !validIDs(a.ScopeID, a.ID) || !safeIdempotencyToken(a.Key) {
		return "", jobs.ErrInvalidInput
	}
	var id ir.ID
	var previous []byte
	err := s.catalog.pool.QueryRow(ctx, `SELECT operation_id::text,request_hmac FROM public.test_operations WHERE scope_id=$1 AND actor_id=$2 AND route=$3 AND key=$4`, dbID(a.ScopeID), dbID(a.ID), route, a.Key).Scan(&id, &previous)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", testError(err)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return "", jobs.ErrInvalidInput
	}
	defer clear(data)
	digest, err := s.catalog.box.Digest(secretbox.PurposeIdempotency, data)
	if err != nil {
		return "", jobs.ErrUnavailable
	}
	if !hmac.Equal(previous, digest) {
		return "", catalog.ErrIdempotencyConflict
	}
	return id, nil
}
func systemAudit(ctx context.Context, tx pgx.Tx, a networktest.Actor, id ir.ID, action string) error {
	_, err := tx.Exec(ctx, `INSERT INTO public.system_audit_events(scope_id,actor_id,object_id,action) VALUES($1,$2,$3,$4)`, dbID(a.ScopeID), dbID(a.ID), nullableID(id), action)
	return err
}
func (s *NetworkTests) WriteTarget(ctx context.Context, a networktest.Actor, id ir.ID, expected int64, request networktest.TargetRequest) (networktest.Target, error) {
	if request.Validate() != nil || !validIDs(a.ScopeID, a.ID) || id != "" && (id.Validate() != nil || expected < 1) {
		return networktest.Target{}, jobs.ErrInvalidInput
	}
	if id == "" {
		existing, err := s.replay(ctx, a, "target.create", request)
		if err != nil {
			return networktest.Target{}, err
		}
		if existing != "" {
			return scanTestTarget(s.catalog.pool.QueryRow(ctx, `SELECT `+targetColumns+` FROM public.test_targets WHERE scope_id=$1 AND id=$2`, dbID(a.ScopeID), dbID(existing)))
		}
	}
	u, _ := url.Parse(request.Config.URL)
	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := s.resolver.Resolve(resolveCtx, u.Hostname()); err != nil {
		return networktest.Target{}, err
	}
	tx, err := s.catalog.pool.Begin(ctx)
	if err != nil {
		return networktest.Target{}, testError(err)
	}
	defer tx.Rollback(ctx)
	if _, err = dbgen.New(tx).LockScope(ctx, dbID(a.ScopeID)); err != nil {
		return networktest.Target{}, testError(err)
	}
	config, _ := json.Marshal(request.Config)
	defer clear(config)
	enabled := request.Enabled == nil || *request.Enabled
	if id == "" {
		id = jobs.NewID()
		var replay bool
		id, replay, err = s.operation(ctx, tx, a, "target.create", request, id)
		if err != nil {
			return networktest.Target{}, testError(err)
		}
		if replay {
			return scanTestTarget(tx.QueryRow(ctx, `SELECT `+targetColumns+` FROM public.test_targets WHERE scope_id=$1 AND id=$2`, dbID(a.ScopeID), dbID(id)))
		}
		_, err = tx.Exec(ctx, `INSERT INTO public.test_targets(id,scope_id,name,enabled,config) VALUES($1,$2,$3,$4,$5)`, dbID(id), dbID(a.ScopeID), request.Name, enabled, config)
	} else {
		result, e := tx.Exec(ctx, `UPDATE public.test_targets SET revision=revision+1,name=$3,enabled=$4,config=$5,safety_checked_at=clock_timestamp() WHERE scope_id=$1 AND id=$2 AND revision=$6 AND deleted_at IS NULL`, dbID(a.ScopeID), dbID(id), request.Name, enabled, config, expected)
		err = e
		if err == nil && result.RowsAffected() != 1 {
			return networktest.Target{}, catalog.ErrRevisionConflict
		}
	}
	if err != nil {
		return networktest.Target{}, testError(err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.test_target_revisions(scope_id,target_id,revision,config) SELECT scope_id,id,revision,config FROM public.test_targets WHERE id=$1`, dbID(id))
	if err != nil {
		return networktest.Target{}, testError(err)
	}
	if err = systemAudit(ctx, tx, a, id, "test_target.write"); err != nil {
		return networktest.Target{}, testError(err)
	}
	result, err := scanTestTarget(tx.QueryRow(ctx, `SELECT `+targetColumns+` FROM public.test_targets WHERE id=$1`, dbID(id)))
	if err == nil {
		err = tx.Commit(ctx)
	}
	return result, testError(err)
}
func (s *NetworkTests) DeleteTarget(ctx context.Context, a networktest.Actor, id ir.ID, expected int64) error {
	if !validIDs(a.ScopeID, a.ID, id) || expected < 1 {
		return jobs.ErrInvalidInput
	}
	tx, err := s.catalog.pool.Begin(ctx)
	if err != nil {
		return testError(err)
	}
	defer tx.Rollback(ctx)
	if _, err = dbgen.New(tx).LockScope(ctx, dbID(a.ScopeID)); err != nil {
		return testError(err)
	}
	tag, err := tx.Exec(ctx, `UPDATE public.test_targets SET deleted_at=clock_timestamp(),enabled=false,revision=revision+1 WHERE scope_id=$1 AND id=$2 AND revision=$3 AND deleted_at IS NULL`, dbID(a.ScopeID), dbID(id), expected)
	if err != nil {
		return testError(err)
	}
	if tag.RowsAffected() != 1 {
		return catalog.ErrRevisionConflict
	}
	if err = systemAudit(ctx, tx, a, id, "test_target.delete"); err == nil {
		err = tx.Commit(ctx)
	}
	return testError(err)
}

func frozenSubject(r ir.Resource) runnerprotocol.FrozenSubject {
	m := r.Metadata
	return runnerprotocol.FrozenSubject{Kind: m.Kind, ID: m.ResourceID, Revision: runnerprotocol.Sequence(m.Revision), SecurityEpoch: runnerprotocol.Sequence(m.SecurityEpoch)}
}
func (s *NetworkTests) Create(ctx context.Context, a networktest.Actor, request networktest.Request) (ir.ID, error) {
	online := jobs.Type(request.Type).Network()
	if !validIDs(a.ScopeID, a.ID, request.CoreBuildID) || !safeIdempotencyToken(a.Key) || len(request.Subjects) < 1 || len(request.Subjects) > 100 || !online && request.Type != "config_validate" || request.Limits.DurationMS < 1 || online && (request.TestTargetID.Validate() != nil || request.Limits.MaxBytes < 1) || !online && (request.TestTargetID != "" || request.Limits.MaxBytes != 0) {
		return "", jobs.ErrInvalidInput
	}
	if existing, err := s.replay(ctx, a, "tests.create", request); err != nil || existing != "" {
		return existing, err
	}
	build, err := s.cores.Build(request.CoreBuildID)
	if err != nil {
		return "", jobs.ErrInvalidInput
	}
	var target networktest.Target
	if online {
		target, err = s.Target(ctx, a.ScopeID, request.TestTargetID)
		if err != nil {
			return "", err
		}
		if !target.Enabled || !slices.Contains(target.Config.AllowedTypes, request.Type) {
			return "", jobs.ErrInvalidInput
		}
	}
	// Collect one consistent catalog view; release the transaction before DNS.
	tx, err := s.catalog.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return "", testError(err)
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	scope, err := q.GetScope(ctx, dbID(a.ScopeID))
	if err != nil {
		return "", testError(err)
	}
	resources := map[ir.ID]ir.Resource{}
	var collect func(ir.ID) error
	collect = func(id ir.ID) error {
		if _, ok := resources[id]; ok {
			return nil
		}
		row, err := q.GetResourceHead(ctx, dbgen.GetResourceHeadParams{ScopeID: dbID(a.ScopeID), ID: dbID(id)})
		if err != nil {
			return err
		}
		r, err := s.catalog.open(dbgen.GetResourceRevisionRow(row))
		if err != nil {
			return err
		}
		if !r.Metadata.Enabled || r.Metadata.Kind != ir.KindNode && r.Metadata.Kind != ir.KindChain {
			return jobs.ErrInvalidInput
		}
		resources[id] = r
		if chain, ok := r.Payload.(*ir.Chain); ok {
			for _, hop := range chain.Hops {
				if err = collect(hop.NodeID); err != nil {
					return err
				}
				if resources[hop.NodeID].Metadata.Kind != ir.KindNode {
					return jobs.ErrInvalidInput
				}
			}
		}
		return nil
	}
	seen := map[ir.ID]bool{}
	for _, subject := range request.Subjects {
		if subject.ID.Validate() != nil || seen[subject.ID] {
			return "", jobs.ErrInvalidInput
		}
		seen[subject.ID] = true
		if err = collect(subject.ID); err != nil {
			return "", testError(err)
		}
		if resources[subject.ID].Metadata.Kind != subject.Kind {
			return "", jobs.ErrInvalidInput
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return "", testError(err)
	}
	var addresses map[ir.ID]string
	var targetIPs []string
	resolver := s.resolver
	if online {
		settings, err := readSystemSettings(ctx, s.catalog.pool, a.ScopeID)
		if err != nil {
			return "", err
		}
		prefixes, err := settings.PrivateProxyPrefixes()
		if err != nil {
			return "", jobs.ErrUnavailable
		}
		resolver.ProxyAllowNets = prefixes
		resolveCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		addresses = map[ir.ID]string{}
		for id, r := range resources {
			if node, ok := r.Payload.(*ir.Node); ok {
				ips, err := resolver.ResolveEndpoint(resolveCtx, node.Endpoint.Host)
				if err != nil {
					return "", err
				}
				addresses[id] = ips[0]
			}
		}
		u, _ := url.Parse(target.Config.URL)
		targetIPs, err = resolver.Resolve(resolveCtx, u.Hostname())
		if err != nil {
			return "", err
		}
	}
	quota, _, err := readQuota(ctx, s.catalog.pool, a.ScopeID)
	if err != nil {
		return "", err
	}
	limits := runnerprotocol.Limits{DurationMS: min(request.Limits.DurationMS, quota.MaxTestDurationMS, 60000)}
	if online {
		limits.DurationMS = min(limits.DurationMS, target.Config.Limits.DurationMS)
		limits.MaxBytes = min(request.Limits.MaxBytes, target.Config.Limits.MaxBytes, quota.MaxTestBytes)
		if request.Type == "connectivity" {
			limits.MaxBytes = min(limits.MaxBytes, 3*min(target.Config.ExpectedResponse.MaxResponseBytes, 64<<10))
		} else {
			limits.MaxBytes = min(limits.MaxBytes, target.Config.ExpectedResponse.MaxResponseBytes)
		}
	}
	batchID := jobs.NewID()
	children := []jobs.EnqueueInput{}
	payloads := []runnerprotocol.FrozenPayload{}
	for _, subject := range request.Subjects {
		r := resources[subject.ID]
		artifact, endpoints, err := networktest.Compile(build.Family, jobs.NewID(), r, resources, addresses)
		if err != nil {
			return "", err
		}
		if online {
			for i := range endpoints {
				ip, parseErr := netip.ParseAddr(endpoints[i].IP)
				if parseErr != nil {
					return "", jobs.ErrUnavailable
				}
				endpoints[i].PrivateAuthorized = resolver.PrivateEndpointAuthorized(ip)
			}
		}
		deps := []runnerprotocol.FrozenSubject{frozenSubject(r)}
		if chain, ok := r.Payload.(*ir.Chain); ok {
			for _, hop := range chain.Hops {
				deps = append(deps, frozenSubject(resources[hop.NodeID]))
			}
		}
		sort.Slice(deps, func(i, j int) bool { return deps[i].ID < deps[j].ID })
		frozen := frozenSubject(r)
		p := runnerprotocol.FrozenPayload{SchemaVersion: 1, Type: request.Type, Core: runnerprotocol.CoreIdentity{CoreBuildID: build.ID, CoreFamily: build.Family, Version: build.Version, BuildSHA256: build.BinarySHA256, Platform: build.OS, Architecture: build.Arch, AdapterVersion: capability.AdapterVersion}, Artifact: runnerprotocol.Artifact{ArtifactID: artifact.SnapshotID, Format: map[ir.CoreFamily]ir.OutputFormat{ir.Xray: ir.XrayJSON, ir.SingBox: ir.SingBoxJSON, ir.Mihomo: ir.MihomoYAML}[build.Family], SHA256: runnerprotocol.Digest(artifact.Bytes), ByteLength: int64(len(artifact.Bytes)), ContentBase64: base64.StdEncoding.EncodeToString(artifact.Bytes)}, Subject: &frozen, Dependencies: deps, ApprovedEndpoints: endpoints, MinimumSampleBytes: quota.MinimumThroughputSampleBytes, Limits: limits, QuotaReservationID: jobs.NewID(), ExecutionPolicy: runnerprotocol.ExecutionPolicy{Network: "controlled_target_only", TerminationGraceMS: 2000, MemoryLimitBytes: 1 << 30, ProcessLimit: 32}, TestTarget: &runnerprotocol.FrozenTestTarget{TestTargetID: target.ID, Revision: target.Revision, URL: target.Config.URL, ValidatedIPs: targetIPs, ExpectedResponse: target.Config.ExpectedResponse, RedirectPolicy: "deny", VerifyCertificate: true, Compression: "disabled"}}
		clear(artifact.Bytes)
		if online {
			p.ExecutionPolicy.ProcessLimit = 128
		}
		if !online {
			p.TestTarget = nil
			p.ApprovedEndpoints = nil
			p.Dependencies = nil
			p.MinimumSampleBytes = 0
			p.QuotaReservationID = ""
			p.ExecutionPolicy.Network = "none"
		}
		if runnerprotocol.ValidatePayload(p) != nil {
			return "", jobs.ErrInvalidInput
		}
		data, _ := json.Marshal(p)
		defer clear(data)
		children = append(children, jobs.EnqueueInput{ID: jobs.NewID(), ScopeID: a.ScopeID, Executor: jobs.Runner, Type: jobs.Type(request.Type), CoreBuildID: build.ID, Payload: data})
		// History keeps complete dependencies even for offline wire payloads.
		p.Dependencies = deps
		payloads = append(payloads, p)
	}
	tx, err = s.catalog.pool.Begin(ctx)
	if err != nil {
		return "", testError(err)
	}
	defer tx.Rollback(ctx)
	current, err := dbgen.New(tx).LockScope(ctx, dbID(a.ScopeID))
	if err != nil {
		return "", testError(err)
	}
	op, replay, err := s.operation(ctx, tx, a, "tests.create", request, batchID)
	if err != nil {
		return "", testError(err)
	}
	if replay {
		return op, nil
	}
	if current.CatalogRevision != scope.CatalogRevision || current.AuthEpoch != scope.AuthEpoch {
		return "", catalog.ErrRevisionConflict
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT ($1::uuid IS NULL OR EXISTS(SELECT 1 FROM public.test_targets WHERE id=$1 AND scope_id=$2 AND revision=$3 AND enabled AND deleted_at IS NULL)) AND EXISTS(SELECT 1 FROM public.core_builds WHERE id=$4 AND enabled)`, nullableID(target.ID), dbID(a.ScopeID), int64(target.Revision), dbID(build.ID)).Scan(&valid); err != nil {
		return "", testError(err)
	}
	if !valid {
		return "", catalog.ErrRevisionConflict
	}
	if _, err = s.jobs.CreateBatchTx(ctx, tx, jobs.BatchInput{ID: batchID, ScopeID: a.ScopeID, EffectiveLimits: limits, Children: children}); err != nil {
		return "", testError(err)
	}
	for i, child := range children {
		p := payloads[i]
		subject, _ := json.Marshal(p.Subject)
		deps, _ := json.Marshal(p.Dependencies)
		core, _ := json.Marshal(p.Core)
		effective, _ := json.Marshal(p.Limits)
		var targetRevision *int64
		if online {
			revision := int64(target.Revision)
			targetRevision = &revision
		}
		if _, err = tx.Exec(ctx, `INSERT INTO public.test_jobs(job_id,scope_id,subject,dependencies,test_target_id,test_target_revision,core_identity,effective_limits) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, dbID(child.ID), dbID(a.ScopeID), subject, deps, nullableID(target.ID), targetRevision, core, effective); err != nil {
			return "", testError(err)
		}
	}
	if err = systemAudit(ctx, tx, a, batchID, "tests.create"); err == nil {
		err = tx.Commit(ctx)
	}
	return batchID, testError(err)
}
