package storage

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/operations"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
)

func cleanupFixture(t *testing.T, e *postgresEnv) CleanupResult {
	t.Helper()
	var raw []byte
	if err := e.runtime.QueryRow(e.ctx, `SELECT public.proxyloom_cleanup($1,1000)`, dbID(e.scope)).Scan(&raw); err != nil {
		t.Fatal("cleanup SQL failed", err)
	}
	var result CleanupResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestPostgresOperationsCleanupProtectsPendingBatchesAndQuota(t *testing.T) {
	e, q := postgresJobs(t)
	op, err := NewOperations(e.store, q)
	if err != nil {
		t.Fatal(err)
	}
	actor := operations.Actor{ScopeID: e.scope, ID: jobs.NewID(), RequestID: "cleanup-settings"}
	settings := operations.Defaults()
	settings.CleanupPaused = true
	if _, err = op.Update(e.ctx, actor, 1, settings); err != nil {
		t.Fatal(err)
	}
	finished, err := q.CreateBatch(e.ctx, jobs.BatchInput{ScopeID: e.scope, EffectiveLimits: runnerprotocol.Limits{DurationMS: 10000, MaxBytes: 1000}, Children: []jobs.EnqueueInput{networkInput(e, jobs.Connectivity, 1000)}})
	if err != nil {
		t.Fatal(err)
	}
	lease := claimNetwork(t, e, q, jobs.Connectivity, jobs.NewID())
	if lease == nil {
		t.Fatal("missing lease")
	}
	used := int64(100)
	if _, err = q.Complete(e.ctx, lease.Identity, jobs.Result{State: jobs.Succeeded, Verdict: jobs.Pass, Metrics: runnerprotocol.Metrics{BodyBytes: &used}}); err != nil {
		t.Fatal(err)
	}
	pending, err := q.CreateBatch(e.ctx, jobs.BatchInput{ScopeID: e.scope, EffectiveLimits: runnerprotocol.Limits{DurationMS: 10000, MaxBytes: 1000}, Children: []jobs.EnqueueInput{networkInput(e, jobs.Connectivity, 1000)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.admin.Exec(e.ctx, `UPDATE public.jobs SET finished_at=clock_timestamp()-interval '40 days' WHERE batch_id=$1`, dbID(finished.ID)); err != nil {
		t.Fatal(err)
	}
	if r := cleanupFixture(t, e); !r.Paused || r.Deleted != 0 {
		t.Fatal("cleanup pause ignored")
	}
	if _, err = q.Snapshot(e.ctx, e.scope, finished.ID); err != nil {
		t.Fatal("paused cleanup removed result")
	}
	settings.CleanupPaused = false
	if _, err = op.Update(e.ctx, actor, 2, settings); err != nil {
		t.Fatal(err)
	}
	if r := cleanupFixture(t, e); r.Paused || r.Deleted < 1 {
		t.Fatal("no expired test removed")
	}
	if _, err = q.Snapshot(e.ctx, e.scope, finished.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatal("expired test retained", err)
	}
	if _, err = q.Snapshot(e.ctx, e.scope, pending.ID); err != nil {
		t.Fatal("pending test removed", err)
	}
	budgetTotals(t, e, 1000, 100)
	if count := importCount(t, e, `SELECT count(*) FROM public.quota_reservations WHERE job_id=$1`, dbID(lease.Job.ID)); count != 0 {
		t.Fatal("old reservation not collected")
	}
	if r := cleanupFixture(t, e); r.Deleted != 0 {
		t.Fatal("cleanup is not idempotent")
	}
	tx, err := e.runtime.Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(e.ctx)
	if _, err = tx.Exec(e.ctx, `SELECT set_config('proxyloom.maintenance',txid_current()::text,true)`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(e.ctx, `DELETE FROM public.system_setting_revisions`); err == nil {
		t.Fatal("runtime bypassed immutable settings history")
	}
}

func TestPostgresOperationsCleanupKeepsPublicationAndReferencedRevision(t *testing.T) {
	h := newPublicationTest(t)
	e := h.env
	b := h.validate(t, h.compile(t), false)
	p := h.publish(t, b, 0)
	unused := mustCreate(t, e, catalog.CreateInput{Name: "unused history", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
	tombstone := mustCreate(t, e, catalog.CreateInput{Name: "deleted unused node", Tags: []string{"old"}, Enabled: true, Payload: syntheticNode()})
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error { _, err := tx.Delete(e.ctx, tombstone.Metadata.ResourceID, 1); return err }); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		_, err := tx.Update(e.ctx, unused.Metadata.ResourceID, 1, catalog.UpdateInput{Name: "unused current", Tags: []string{}, Enabled: true, Payload: unused.Payload})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		_, err := tx.Update(e.ctx, h.node.Metadata.ResourceID, 1, catalog.UpdateInput{Name: "current node", Tags: []string{}, Enabled: true, Payload: h.node.Payload})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Only fixture timestamps are aged by the owner. Runtime permissions and
	// history triggers are restored before calling the production cleaner.
	if _, err := e.admin.Exec(e.ctx, `ALTER TABLE public.resource_revisions DISABLE TRIGGER resource_revisions_immutable;
UPDATE public.resource_revisions SET created_at=clock_timestamp()-interval '100 days';
ALTER TABLE public.resource_revisions ENABLE TRIGGER resource_revisions_immutable;
ALTER TABLE public.resources DISABLE TRIGGER USER;
UPDATE public.resources SET deleted_at=clock_timestamp()-interval '100 days' WHERE deleted_at IS NOT NULL;
ALTER TABLE public.resources ENABLE TRIGGER USER;
UPDATE public.publications SET created_at=clock_timestamp()-interval '100 days';
UPDATE public.compile_batches SET created_at=clock_timestamp()-interval '100 days';`); err != nil {
		t.Fatal(err)
	}
	cleanupFixture(t, e)
	if count := importCount(t, e, `SELECT count(*) FROM public.resources WHERE id=$1`, dbID(tombstone.Metadata.ResourceID)); count != 0 {
		t.Fatal("unreferenced tombstone not physically collected")
	}
	if count := importCount(t, e, `SELECT count(*) FROM public.resource_revisions WHERE resource_id=$1`, dbID(unused.Metadata.ResourceID)); count != 1 {
		t.Fatal("unreferenced old revision retained")
	}
	if count := importCount(t, e, `SELECT count(*) FROM public.resource_revisions WHERE resource_id=$1 AND revision=1`, dbID(h.node.Metadata.ResourceID)); count != 1 {
		t.Fatal("published frozen revision removed")
	}
	if count := importCount(t, e, `SELECT count(*) FROM public.publications WHERE id=$1`, dbID(p.PublicationID)); count != 1 {
		t.Fatal("active publication removed")
	}
	if _, err := h.store.GetBatch(e.ctx, h.actor, b.BatchID); err != nil {
		t.Fatal("retained publication evidence unavailable", err)
	}
	// A runtime-controlled GUC cannot grant owner deletion privileges.
	tx, err := e.runtime.Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(e.ctx)
	if _, err = tx.Exec(e.ctx, `SELECT set_config('proxyloom.maintenance',txid_current()::text,true)`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(e.ctx, `DELETE FROM public.resource_revisions WHERE resource_id=$1 AND revision=1`, dbID(h.node.Metadata.ResourceID)); err == nil {
		t.Fatal("runtime bypassed revision guard")
	}
	if _, err := e.admin.Exec(e.ctx, `DELETE FROM public.resource_revisions WHERE resource_id=$1 AND revision=1`, dbID(h.node.Metadata.ResourceID)); err == nil {
		t.Fatal("ordinary history guard weakened")
	}
}

func TestPostgresOperationsCleanupExpiredComparisonKeepsActiveDownload(t *testing.T) {
	h := newPublicationTest(t)
	e := h.env
	first := h.publish(t, h.validate(t, h.compile(t), false), 0)
	secondBatch := h.validate(t, h.compile(t), false)
	second := h.publish(t, secondBatch, 1)
	op, err := NewOperations(e.store, h.store.jobs)
	if err != nil {
		t.Fatal(err)
	}
	settings := operations.Defaults()
	settings.Retention.PublicationCount = 1
	if _, err = op.Update(e.ctx, operations.Actor{ScopeID: e.scope, ID: h.actor.ID, RequestID: "retention-test"}, 1, settings); err != nil {
		t.Fatal(err)
	}
	if _, err = e.admin.Exec(e.ctx, `UPDATE public.publications SET created_at=clock_timestamp()-interval '100 days'; UPDATE public.compile_batches SET created_at=clock_timestamp()-interval '100 days'`); err != nil {
		t.Fatal(err)
	}
	cleanupFixture(t, e)
	if count := importCount(t, e, `SELECT count(*) FROM public.publications WHERE id=$1`, dbID(first.PublicationID)); count != 0 {
		t.Fatal("ordinary old publication was not collected")
	}
	b, err := h.store.GetBatch(e.ctx, h.actor, secondBatch.BatchID)
	if err != nil || len(b.Outputs) == 0 {
		t.Fatal("active batch became unreadable", err)
	}
	for _, output := range b.Outputs {
		if !output.PreviousPreviewExpired {
			t.Fatal("missing comparison claimed as evidence")
		}
	}
	issued, err := h.store.IssueToken(e.ctx, h.actor, h.profile.Metadata.ResourceID, subscriptions.TokenRequest{Name: "retained snapshot", AllowedTargets: h.keys})
	if err != nil {
		t.Fatal(err)
	}
	download, err := h.store.Download(e.ctx, issued.Token, h.keys[0])
	if err != nil || len(download.Bytes) == 0 {
		t.Fatal("active download lost frozen configuration", err)
	}
	clear(download.Bytes)
	if count := importCount(t, e, `SELECT count(*) FROM public.publication_heads WHERE publication_id=$1`, dbID(second.PublicationID)); count != 1 {
		t.Fatal("active pointer changed")
	}
}
