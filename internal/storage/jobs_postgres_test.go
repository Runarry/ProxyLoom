package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/jackc/pgx/v5"
)

func postgresJobs(t *testing.T) (*postgresEnv, *Jobs) {
	t.Helper()
	env := newPostgres(t, true)
	queue, err := NewJobs(env.runtime, env.box)
	if err != nil {
		t.Fatal("queue initialization failed")
	}
	return env, queue
}
func enqueueParse(t *testing.T, env *postgresEnv, queue *Jobs) jobs.Job {
	t.Helper()
	job, err := queue.Enqueue(env.ctx, jobs.EnqueueInput{ScopeID: env.scope, Executor: jobs.APIWorker, Type: jobs.ImportParse, Payload: []byte(`{"batch":"synthetic-private-job-input"}`)})
	if err != nil {
		t.Fatal("parse enqueue failed", err)
	}
	return job
}
func claimParse(t *testing.T, env *postgresEnv, queue *Jobs) *jobs.Lease {
	t.Helper()
	lease, err := queue.Claim(env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.ImportParse}})
	if err != nil || lease == nil {
		t.Fatal("parse claim failed", err)
	}
	t.Cleanup(func() { clear(lease.Payload) })
	return lease
}
func succeedJob() jobs.Result { return jobs.Result{State: jobs.Succeeded, Verdict: jobs.Pass} }
func expireJob(t *testing.T, env *postgresEnv, id ir.ID) {
	t.Helper()
	if _, err := env.admin.Exec(env.ctx, `UPDATE public.jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, dbID(id)); err != nil {
		t.Fatal("lease expiry preparation failed")
	}
}
func TestPostgresJobsConcurrentClaimsEncryptedPayloadAndRestart(t *testing.T) {
	env, queue := postgresJobs(t)
	for range 12 {
		enqueueParse(t, env, queue)
	}
	var encrypted string
	if err := env.runtime.QueryRow(env.ctx, `SELECT envelope::text FROM public.job_payloads LIMIT 1`).Scan(&encrypted); err != nil || strings.Contains(encrypted, "synthetic-private-job-input") {
		t.Fatal("job payload was not independently encrypted")
	}
	var wg sync.WaitGroup
	leases := make(chan *jobs.Lease, 12)
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := queue.Claim(env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.ImportParse}})
			if err != nil {
				errs <- err
				return
			}
			leases <- lease
		}()
	}
	wg.Wait()
	close(leases)
	close(errs)
	for err := range errs {
		t.Fatal("concurrent claim failed", err)
	}
	seen := map[ir.ID]bool{}
	var first *jobs.Lease
	for lease := range leases {
		if lease == nil || seen[lease.Job.ID] {
			t.Fatal("a job was issued more than once concurrently")
		}
		seen[lease.Job.ID] = true
		if first == nil {
			first = lease
		}
		clear(lease.Payload)
	}
	if len(seen) != 12 {
		t.Fatal("concurrent consumers lost available jobs")
	}
	if duration := time.Until(first.ExpiresAt); duration < 20*time.Second || duration > 31*time.Second {
		t.Fatal("lease was not bounded to thirty seconds")
	}
	expireJob(t, env, first.Job.ID)
	restarted, err := NewJobs(env.runtime, env.box)
	if err != nil {
		t.Fatal(err)
	}
	replacement := claimParse(t, env, restarted)
	if replacement.Job.ID != first.Job.ID || replacement.Identity.Attempt != 2 || replacement.Identity.LeaseSeq != 2 {
		t.Fatal("restart did not fence the replacement attempt")
	}
	if _, err = queue.Heartbeat(env.ctx, first.Identity); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("stale heartbeat was accepted", err)
	}
	if _, err = queue.Event(env.ctx, first.Identity, jobs.EventInput{EventID: jobs.NewID(), Phase: "parsing", Total: 1}); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("stale event was accepted", err)
	}
	if _, err = queue.Complete(env.ctx, first.Identity, succeedJob()); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("stale result was accepted", err)
	}
	if _, err = restarted.Complete(env.ctx, replacement.Identity, succeedJob()); err != nil {
		t.Fatal("replacement result failed", err)
	}
}
func TestPostgresJobsAtomicCompletionRollbackReplayAndExpiry(t *testing.T) {
	env, queue := postgresJobs(t)
	enqueueParse(t, env, queue)
	lease := claimParse(t, env, queue)
	callbackError := errors.New("synthetic_callback_rollback")
	advance := func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE public.scopes SET catalog_revision=catalog_revision+1 WHERE id=$1`, dbID(env.scope))
		return err
	}
	before, err := env.store.Scope(env.ctx, env.scope)
	if err != nil {
		t.Fatal(err)
	}
	_, err = queue.CompleteTx(env.ctx, lease.Identity, succeedJob(), func(ctx context.Context, tx pgx.Tx) error {
		if err := advance(ctx, tx); err != nil {
			return err
		}
		return callbackError
	})
	if !errors.Is(err, callbackError) {
		t.Fatal("callback failure was hidden", err)
	}
	after, err := env.store.Scope(env.ctx, env.scope)
	if err != nil || after.CatalogRevision != before.CatalogRevision {
		t.Fatal("callback rollback left business changes")
	}
	accepted, err := queue.CompleteTx(env.ctx, lease.Identity, succeedJob(), advance)
	if err != nil || accepted.Replayed {
		t.Fatal("first result failed", err)
	}
	replayed, err := queue.CompleteTx(env.ctx, lease.Identity, succeedJob(), func(context.Context, pgx.Tx) error { t.Error("result replay entered business callback"); return nil })
	if err != nil || !replayed.Replayed || replayed.ResultID != accepted.ResultID {
		t.Fatal("result did not replay stable receipt", err)
	}
	after, err = env.store.Scope(env.ctx, env.scope)
	if err != nil || after.CatalogRevision != before.CatalogRevision+1 {
		t.Fatal("result replay repeated business changes")
	}
	if _, err = queue.Complete(env.ctx, lease.Identity, jobs.Result{State: jobs.Succeeded, Verdict: jobs.Fail}); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("conflicting result hash was accepted", err)
	}
	if _, err = queue.Heartbeat(env.ctx, lease.Identity); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("terminal lease renewed", err)
	}
	if _, err = queue.Event(env.ctx, lease.Identity, jobs.EventInput{EventID: jobs.NewID(), Phase: "completed", Completed: 1, Total: 1}); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("terminal job accepted event", err)
	}
	enqueueParse(t, env, queue)
	slow := claimParse(t, env, queue)
	if _, err = env.admin.Exec(env.ctx, `UPDATE public.jobs SET lease_until=clock_timestamp()+interval '100 milliseconds' WHERE id=$1`, dbID(slow.Job.ID)); err != nil {
		t.Fatal("short lease setup failed")
	}
	_, err = queue.CompleteTx(env.ctx, slow.Identity, succeedJob(), func(ctx context.Context, tx pgx.Tx) error {
		if err := advance(ctx, tx); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT pg_sleep(0.15)`)
		return err
	})
	if !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("callback published after lease expiry", err)
	}
	after, err = env.store.Scope(env.ctx, env.scope)
	if err != nil || after.CatalogRevision != before.CatalogRevision+1 {
		t.Fatal("expired callback changes survived rollback")
	}
}
func TestPostgresJobsCancellationBatchAggregationAndEvents(t *testing.T) {
	env, queue := postgresJobs(t)
	batch, err := queue.CreateBatch(env.ctx, jobs.BatchInput{ScopeID: env.scope, EffectiveLimits: runnerprotocol.Limits{DurationMS: 10000}, Children: []jobs.EnqueueInput{
		{Executor: jobs.APIWorker, Type: jobs.ImportParse, Payload: []byte("first")},
		{Executor: jobs.APIWorker, Type: jobs.ImportParse, Payload: []byte("second")},
		{Executor: jobs.APIWorker, Type: jobs.ImportParse, Payload: []byte("third")},
	}})
	if err != nil {
		t.Fatal("batch creation failed", err)
	}
	first := claimParse(t, env, queue)
	if _, err = queue.Complete(env.ctx, first.Identity, succeedJob()); err != nil {
		t.Fatal(err)
	}
	second := claimParse(t, env, queue)
	input := jobs.EventInput{EventID: jobs.NewID(), Phase: "parsing", Total: 3, Completed: 1, Error: &runnerprotocol.SafeError{Code: "INVALID_CONFIG", Message: "synthetic-private-runner-output", Details: []runnerprotocol.Detail{{FieldPath: "/synthetic-private-path"}}}}
	event, err := queue.Event(env.ctx, second.Identity, input)
	if err != nil {
		t.Fatal("event append failed", err)
	}
	replay, err := queue.Event(env.ctx, second.Identity, input)
	if err != nil || !replay.Replayed || replay.Seq != event.Seq {
		t.Fatal("event replay failed", err)
	}
	input.Completed = 2
	if _, err = queue.Event(env.ctx, second.Identity, input); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("event identity conflict was ignored", err)
	}
	snapshot, err := queue.Snapshot(env.ctx, env.scope, batch.ID)
	if err != nil || snapshot.Batch.Completed != 1 {
		t.Fatal("batch progress was not persisted", err)
	}
	if _, err = queue.Cancel(env.ctx, env.scope, batch.ID, snapshot.Batch.Revision-1); !errors.Is(err, jobs.ErrRevisionConflict) {
		t.Fatal("batch ignored stale revision", err)
	}
	canceled, err := queue.Cancel(env.ctx, env.scope, batch.ID, snapshot.Batch.Revision)
	if err != nil || !canceled.Batch.CancelRequested || canceled.Batch.Completed != 2 {
		t.Fatal("batch cancellation not persisted", err)
	}
	beat, err := queue.Heartbeat(env.ctx, second.Identity)
	if err != nil || !beat.CancelRequested {
		t.Fatal("runner could not observe cancellation", err)
	}
	_, err = queue.CompleteTx(env.ctx, second.Identity, succeedJob(), func(context.Context, pgx.Tx) error { t.Error("canceled attempt entered callback"); return nil })
	if !errors.Is(err, jobs.ErrCanceled) {
		t.Fatal("canceled attempt published success", err)
	}
	if _, err = queue.Complete(env.ctx, second.Identity, jobs.Result{State: jobs.Canceled, Error: runnerprotocol.Safe("CANCELED")}); err != nil {
		t.Fatal("cancel completion failed", err)
	}
	snapshot, err = queue.Snapshot(env.ctx, env.scope, batch.ID)
	if err != nil || snapshot.Batch.State != jobs.Canceled || snapshot.Batch.Completed != 3 {
		t.Fatal("terminal batch aggregate failed", err)
	}
	terminal, err := queue.Get(env.ctx, env.scope, first.Job.ID)
	if err != nil || terminal.State != jobs.Succeeded || terminal.CancelRequested {
		t.Fatal("parent cancel rewrote terminal child", err)
	}
	page, err := queue.Events(env.ctx, env.scope, second.Job.ID, 0, 200)
	if err != nil || !page.Terminal || len(page.Events) < 3 {
		t.Fatal("event replay unavailable", err)
	}
	data, _ := json.Marshal(page)
	if strings.Contains(string(data), "synthetic-private-") {
		t.Fatal("event persisted caller text")
	}
	if _, err = queue.Events(env.ctx, env.scope, second.Job.ID, page.LatestSeq+1, 200); !errors.Is(err, jobs.ErrInvalidInput) {
		t.Fatal("future event cursor was accepted", err)
	}
	if _, err = env.admin.Exec(env.ctx, `UPDATE public.job_events SET created_at=clock_timestamp()-interval '8 days' WHERE job_id=$1`, dbID(second.Job.ID)); err != nil {
		t.Fatal("retention preparation failed")
	}
	page, err = queue.Events(env.ctx, env.scope, second.Job.ID, 0, 200)
	if err != nil || !page.Reset || len(page.Events) != 0 {
		t.Fatal("expired history did not reset snapshot", err)
	}
}
func queueConfigInput(scope ir.ID) jobs.EnqueueInput {
	plain := []byte(`{"outbounds":[{"tag":"blocked","protocol":"blackhole"}]}`)
	payload := runnerprotocol.FrozenPayload{SchemaVersion: 1, Type: "config_validate", Core: runnerprotocol.CoreIdentity{CoreBuildID: "30000000-0000-4000-8000-000000000001", CoreFamily: ir.Xray, Version: "synthetic", BuildSHA256: strings.Repeat("a", 64), Platform: "linux", Architecture: "amd64", AdapterVersion: "synthetic"}, Artifact: runnerprotocol.Artifact{ArtifactID: jobs.NewID(), Format: ir.XrayJSON, SHA256: runnerprotocol.Digest(plain), ByteLength: int64(len(plain)), ContentBase64: base64.StdEncoding.EncodeToString(plain)}, Limits: runnerprotocol.Limits{DurationMS: 10000}, ExecutionPolicy: runnerprotocol.ExecutionPolicy{Network: "none", TerminationGraceMS: 2000, MemoryLimitBytes: 64 << 20, ProcessLimit: 8}}
	data, _ := json.Marshal(payload)
	return jobs.EnqueueInput{ScopeID: scope, Executor: jobs.Runner, Type: jobs.ConfigValidate, CoreBuildID: payload.Core.CoreBuildID, Payload: data}
}
func TestPostgresJobsRunnerCapacityRetryClassificationAndExhaustion(t *testing.T) {
	env, queue := postgresJobs(t)
	input := queueConfigInput(env.scope)
	for range 2 {
		if _, err := queue.Enqueue(env.ctx, input); err != nil {
			t.Fatal("validation enqueue failed", err)
		}
	}
	claim := jobs.ClaimInput{Executor: jobs.Runner, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.ConfigValidate}, CoreBuildIDs: []ir.ID{input.CoreBuildID}}
	var wg sync.WaitGroup
	results := make(chan *jobs.Lease, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := queue.Claim(env.ctx, claim)
			if err != nil {
				errs <- err
			}
			results <- lease
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal("runner claim failed", err)
	}
	var lease *jobs.Lease
	count := 0
	for candidate := range results {
		if candidate != nil {
			lease = candidate
			count++
		}
	}
	if count != 1 {
		t.Fatal("runner obtained more than its registered slot")
	}
	defer clear(lease.Payload)
	restarted, _ := NewJobs(env.runtime, env.box)
	if extra, err := restarted.Claim(env.ctx, claim); err != nil || extra != nil {
		t.Fatal("process restart lost runner capacity reservation", err)
	}
	if _, err := queue.Complete(env.ctx, lease.Identity, jobs.Result{State: jobs.Failed, Error: runnerprotocol.Safe("CORE_CONFIG_INVALID")}); err != nil {
		t.Fatal("invalid config result failed", err)
	}
	failed, err := queue.Get(env.ctx, env.scope, lease.Job.ID)
	if err != nil || failed.State != jobs.Failed || failed.Attempt != 1 {
		t.Fatal("configuration failure was retried", err)
	}
	lease, err = queue.Claim(env.ctx, claim)
	if err != nil || lease == nil {
		t.Fatal("second validation claim failed", err)
	}
	defer clear(lease.Payload)
	if _, err = queue.Complete(env.ctx, lease.Identity, jobs.Result{State: jobs.Failed, Error: runnerprotocol.Safe("RUNNER_RESOURCE_LIMIT")}); err != nil {
		t.Fatal("infrastructure result failed", err)
	}
	retry, err := queue.Claim(env.ctx, claim)
	if err != nil || retry == nil || retry.Job.ID != lease.Job.ID || retry.Identity.Attempt != 2 {
		t.Fatal("infrastructure retry not scheduled exactly once", err)
	}
	defer clear(retry.Payload)
	if _, err = queue.Complete(env.ctx, lease.Identity, jobs.Result{State: jobs.Failed, Error: runnerprotocol.Safe("RUNNER_RESOURCE_LIMIT")}); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("reissued lease accepted earlier result", err)
	}
	expireJob(t, env, retry.Job.ID)
	if _, err = queue.ReapExpired(env.ctx); err != nil {
		t.Fatal("expiry recovery failed", err)
	}
	exhausted, err := queue.Get(env.ctx, env.scope, retry.Job.ID)
	if err != nil || exhausted.State != jobs.Failed || exhausted.Attempt != 2 {
		t.Fatal("lost lease retry exceeded attempt limit", err)
	}
	if extra, err := queue.Claim(env.ctx, claim); err != nil || extra != nil {
		t.Fatal("third validation attempt was issued", err)
	}
}
func TestPostgresJobsEnqueueTransactionScopeAndList(t *testing.T) {
	env, queue := postgresJobs(t)
	tx, err := env.runtime.Begin(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	job, err := queue.EnqueueTx(env.ctx, tx, jobs.EnqueueInput{ScopeID: env.scope, Executor: jobs.APIWorker, Type: jobs.ImportParse, Payload: []byte("transaction-input")})
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(env.ctx)
	if _, err = queue.Get(env.ctx, env.scope, job.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatal("rolled-back enqueue survived", err)
	}
	for range 3 {
		enqueueParse(t, env, queue)
	}
	page, err := queue.List(env.ctx, jobs.ListInput{ScopeID: env.scope, Limit: 2})
	if err != nil || !page.HasMore || len(page.Jobs) != 2 {
		t.Fatal("stable queue list first page failed", err)
	}
	last := page.Jobs[1]
	next, err := queue.List(env.ctx, jobs.ListInput{ScopeID: env.scope, Limit: 2, AfterID: last.ID, AfterCreatedAt: last.CreatedAt})
	if err != nil || next.HasMore || len(next.Jobs) != 1 || next.Jobs[0].ID == last.ID {
		t.Fatal("stable queue list continuation failed", err)
	}
	other := jobs.NewID()
	if err = env.store.EnsureScope(env.ctx, other, "other synthetic scope"); err != nil {
		t.Fatal(err)
	}
	if _, err = queue.Snapshot(env.ctx, other, last.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatal("job snapshot crossed scope", err)
	}
	if _, err = queue.Events(env.ctx, other, last.ID, 0, 20); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatal("job events crossed scope", err)
	}
	if _, err = queue.Cancel(env.ctx, other, last.ID, last.Revision); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatal("job cancellation crossed scope", err)
	}
}
