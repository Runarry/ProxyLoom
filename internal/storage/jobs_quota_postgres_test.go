package storage

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/jackc/pgx/v5"
)

func networkInput(env *postgresEnv, kind jobs.Type, size int64) jobs.EnqueueInput {
	const core ir.ID = "a1000000-0000-4000-8000-000000000001"
	subject := runnerprotocol.FrozenSubject{Kind: ir.KindNode, ID: "a1000000-0000-4000-8000-000000000002", Revision: 1, SecurityEpoch: 1}
	data := []byte(`{"synthetic":"budget-only fixture, never executed"}`)
	p := runnerprotocol.FrozenPayload{SchemaVersion: 1, Type: string(kind), Core: runnerprotocol.CoreIdentity{CoreBuildID: core, CoreFamily: ir.Xray, Version: "synthetic", BuildSHA256: runnerprotocol.Digest([]byte("synthetic-core")), Platform: "linux", Architecture: "amd64", AdapterVersion: "synthetic"}, Artifact: runnerprotocol.Artifact{ArtifactID: jobs.NewID(), Format: ir.XrayJSON, SHA256: runnerprotocol.Digest(data), ByteLength: int64(len(data)), ContentBase64: base64.StdEncoding.EncodeToString(data)}, Subject: &subject, Dependencies: []runnerprotocol.FrozenSubject{subject}, Limits: runnerprotocol.Limits{DurationMS: 10000, MaxBytes: size}, ExecutionPolicy: runnerprotocol.ExecutionPolicy{Network: "controlled_target_only", TerminationGraceMS: 2000, MemoryLimitBytes: 1 << 30, ProcessLimit: 32}, QuotaReservationID: jobs.NewID(), MinimumSampleBytes: 1,
		TestTarget: &runnerprotocol.FrozenTestTarget{TestTargetID: jobs.NewID(), Revision: 1, URL: "https://fixture.invalid/check", ValidatedIPs: []string{"192.0.2.10"}, ExpectedResponse: runnerprotocol.HTTPExpectation{StatusCodes: []int{200}, MaxResponseBytes: size}, RedirectPolicy: "deny", VerifyCertificate: true, Compression: "disabled"}, ApprovedEndpoints: []runnerprotocol.ApprovedEndpoint{{IP: "192.0.2.11", Port: 443}}}
	encoded, _ := json.Marshal(p)
	return jobs.EnqueueInput{ID: jobs.NewID(), ScopeID: env.scope, Executor: jobs.Runner, Type: kind, Payload: encoded, CoreBuildID: core}
}
func configureTestQuota(t *testing.T, env *postgresEnv, daily int64, concurrency int32) {
	t.Helper()
	q := jobs.DefaultQuota()
	q.DailyDownloadBytes = daily
	q.ConnectivityConcurrency = concurrency
	encoded, _ := json.Marshal(q)
	if _, err := env.runtime.Exec(env.ctx, `INSERT INTO public.quota_settings(scope_id,settings) VALUES($1,$2) ON CONFLICT(scope_id) DO UPDATE SET settings=EXCLUDED.settings`, dbID(env.scope), encoded); err != nil {
		t.Fatal(err)
	}
}
func budgetTotals(t *testing.T, env *postgresEnv, wantReserved, wantSettled int64) {
	t.Helper()
	var reserved, settled int64
	if err := env.runtime.QueryRow(env.ctx, `SELECT COALESCE(sum(reserved_bytes),0),COALESCE(sum(settled_bytes),0) FROM public.quota_buckets WHERE scope_id=$1`, dbID(env.scope)).Scan(&reserved, &settled); err != nil {
		t.Fatal(err)
	}
	if reserved != wantReserved || settled != wantSettled {
		t.Fatalf("budget mismatch: reserved=%d settled=%d", reserved, settled)
	}
}
func claimNetwork(t *testing.T, env *postgresEnv, queue *Jobs, kind jobs.Type, worker ir.ID) *jobs.Lease {
	t.Helper()
	lease, err := queue.Claim(env.ctx, jobs.ClaimInput{Executor: jobs.Runner, WorkerID: worker, Types: []jobs.Type{kind}, CoreBuildIDs: []ir.ID{"a1000000-0000-4000-8000-000000000001"}, AvailableSlots: runnerprotocol.Slots{Connectivity: 4, DownloadThroughput: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if lease != nil {
		t.Cleanup(func() { clear(lease.Payload) })
	}
	return lease
}
func TestPostgresQuotaConcurrentAdmissionAndBatchRollback(t *testing.T) {
	env, queue := postgresJobs(t)
	configureTestQuota(t, env, 256, 4)
	var accepted atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			_, err := queue.Enqueue(env.ctx, networkInput(env, jobs.Connectivity, 128))
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, jobs.ErrBudgetExceeded) {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if accepted.Load() != 2 {
		t.Fatalf("oversold quota: accepted %d", accepted.Load())
	}
	budgetTotals(t, env, 256, 0)
	var jobsBefore int
	env.runtime.QueryRow(env.ctx, `SELECT count(*) FROM public.jobs`).Scan(&jobsBefore)
	_, err := queue.CreateBatch(env.ctx, jobs.BatchInput{ScopeID: env.scope, EffectiveLimits: runnerprotocol.Limits{DurationMS: 10000, MaxBytes: 128}, Children: []jobs.EnqueueInput{networkInput(env, jobs.Connectivity, 128), networkInput(env, jobs.Connectivity, 128)}})
	if !errors.Is(err, jobs.ErrBudgetExceeded) {
		t.Fatal("over-budget batch accepted", err)
	}
	var jobsAfter, batches int
	env.runtime.QueryRow(env.ctx, `SELECT count(*) FROM public.jobs`).Scan(&jobsAfter)
	env.runtime.QueryRow(env.ctx, `SELECT count(*) FROM public.job_batches`).Scan(&batches)
	if jobsAfter != jobsBefore || batches != 0 {
		t.Fatal("failed admission leaked a partial batch")
	}
}
func TestPostgresQuotaCancelRetryFencingAndSettlement(t *testing.T) {
	env, queue := postgresJobs(t)
	queued, err := queue.Enqueue(env.ctx, networkInput(env, jobs.DownloadThroughput, 100))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = queue.Cancel(env.ctx, env.scope, queued.ID, queued.Revision); err != nil {
		t.Fatal(err)
	}
	budgetTotals(t, env, 0, 0)
	_, err = queue.Enqueue(env.ctx, networkInput(env, jobs.Connectivity, 100))
	if err != nil {
		t.Fatal(err)
	}
	first := claimNetwork(t, env, queue, jobs.Connectivity, jobs.NewID())
	if first == nil {
		t.Fatal("no first attempt")
	}
	bytes := int64(25)
	failure := jobs.Result{State: jobs.Failed, Verdict: jobs.Inconclusive, Metrics: runnerprotocol.Metrics{BodyBytes: &bytes}, Error: runnerprotocol.Safe("SERVICE_UNAVAILABLE")}
	r1, err := queue.Complete(env.ctx, first.Identity, failure)
	if err != nil || r1.SettledBytes != 25 {
		t.Fatal("first settlement failed", err)
	}
	r2, err := queue.Complete(env.ctx, first.Identity, failure)
	if err != nil || !r2.Replayed {
		t.Fatal("receipt was not idempotent", err)
	}
	budgetTotals(t, env, 100, 25)
	second := claimNetwork(t, env, queue, jobs.Connectivity, jobs.NewID())
	if second == nil || second.Identity.Attempt != 2 {
		t.Fatal("retry absent")
	}
	var one, two runnerprotocol.FrozenPayload
	one, _ = runnerprotocol.DecodeFrozenPayload(first.Payload)
	two, _ = runnerprotocol.DecodeFrozenPayload(second.Payload)
	if one.QuotaReservationID == two.QuotaReservationID {
		t.Fatal("retry reused first reservation")
	}
	if _, err = queue.Complete(env.ctx, first.Identity, failure); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatal("old lease accepted", err)
	}
	bytes = 40
	if _, err = queue.Complete(env.ctx, second.Identity, jobs.Result{State: jobs.Succeeded, Verdict: jobs.Pass, Metrics: runnerprotocol.Metrics{BodyBytes: &bytes}}); err != nil {
		t.Fatal(err)
	}
	budgetTotals(t, env, 0, 65)
}
func TestPostgresQuotaLostDownloadNeverRetriesAndKeepsOriginalBucket(t *testing.T) {
	env, queue := postgresJobs(t)
	job, err := queue.Enqueue(env.ctx, networkInput(env, jobs.DownloadThroughput, 100))
	if err != nil {
		t.Fatal(err)
	}
	// Move the pending reservation to an earlier UTC bucket as a controlled
	// cross-midnight fixture. Runtime settlement never recomputes its date.
	_, err = env.admin.Exec(env.ctx, `INSERT INTO public.quota_buckets(scope_id,utc_day,reserved_bytes) SELECT scope_id,utc_day-1,reserved_bytes FROM public.quota_buckets;
UPDATE public.quota_reservations SET utc_day=utc_day-1;
DELETE FROM public.quota_buckets WHERE utc_day=(clock_timestamp() AT TIME ZONE 'UTC')::date`)
	if err != nil {
		t.Fatal(err)
	}
	lease := claimNetwork(t, env, queue, jobs.DownloadThroughput, jobs.NewID())
	if lease == nil {
		t.Fatal("no lease")
	}
	expireJob(t, env, job.ID)
	if _, err = queue.ReapExpired(env.ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := queue.Get(env.ctx, env.scope, job.ID); err != nil || got.State != jobs.Failed || got.Attempt != 1 {
		t.Fatal("lost download scheduled again", err)
	}
	budgetTotals(t, env, 0, 100)
	if next := claimNetwork(t, env, queue, jobs.DownloadThroughput, jobs.NewID()); next != nil {
		t.Fatal("download repeated")
	}
	if _, err = queue.ReapExpired(env.ctx); err != nil {
		t.Fatal(err)
	}
	budgetTotals(t, env, 0, 100)
}
func TestPostgresQuotaGlobalSlotsAndCanceledAttempt(t *testing.T) {
	env, queue := postgresJobs(t)
	configureTestQuota(t, env, 1000, 2)
	for range 3 {
		if _, err := queue.Enqueue(env.ctx, networkInput(env, jobs.Connectivity, 100)); err != nil {
			t.Fatal(err)
		}
	}
	first := claimNetwork(t, env, queue, jobs.Connectivity, jobs.NewID())
	second := claimNetwork(t, env, queue, jobs.Connectivity, jobs.NewID())
	if first == nil || second == nil {
		t.Fatal("initial slots unavailable")
	}
	if lease := claimNetwork(t, env, queue, jobs.Connectivity, jobs.NewID()); lease != nil {
		t.Fatal("global slots oversold")
	}
	job, err := queue.Get(env.ctx, env.scope, first.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = queue.Cancel(env.ctx, env.scope, job.ID, job.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err = queue.Complete(env.ctx, first.Identity, jobs.Result{State: jobs.Canceled, Error: runnerprotocol.Safe("CANCELED")}); err != nil {
		t.Fatal(err)
	}
	budgetTotals(t, env, 200, 100)
	if lease := claimNetwork(t, env, queue, jobs.Connectivity, jobs.NewID()); lease == nil {
		t.Fatal("canceled task retained its slot")
	}
}

func TestPostgresQuotaReservationRollback(t *testing.T) {
	env, queue := postgresJobs(t)
	input := networkInput(env, jobs.Connectivity, 100)
	tx, err := env.runtime.BeginTx(env.ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = queue.EnqueueTx(env.ctx, tx, input); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(env.ctx); err != nil {
		t.Fatal(err)
	}
	budgetTotals(t, env, 0, 0)
	if _, err = queue.Get(env.ctx, env.scope, input.ID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatal("transaction rollback leaked a job", err)
	}
}
