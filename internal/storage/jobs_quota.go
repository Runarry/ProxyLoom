package storage

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/jackc/pgx/v5"
)

type quotaQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readQuota(ctx context.Context, query quotaQuery, scope ir.ID) (jobs.Quota, int64, error) {
	q := jobs.DefaultQuota()
	var encoded []byte
	var revision int64
	err := query.QueryRow(ctx, `SELECT settings,revision FROM public.quota_settings WHERE scope_id=$1`, dbID(scope)).Scan(&encoded, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return q, 1, nil
	}
	if err != nil {
		return q, 0, jobError(err)
	}
	if json.Unmarshal(encoded, &q) != nil || !q.Valid() {
		return q, 0, jobs.ErrUnavailable
	}
	return q, revision, nil
}

func reserveRetry(ctx context.Context, tx pgx.Tx, job jobs.Job) error {
	var maximum int64
	if err := tx.QueryRow(ctx, `SELECT reserved_bytes FROM public.quota_reservations WHERE job_id=$1 AND attempt=$2`, dbID(job.ID), job.Attempt).Scan(&maximum); err != nil {
		return jobError(err)
	}
	_, err := reserveAttempt(ctx, tx, job, job.Attempt+1, maximum)
	return err
}

func claimCandidate(ctx context.Context, tx pgx.Tx, input jobs.ClaimInput, kinds []string, cores []string) (jobs.Job, error) {
	// Select one head per eligible type. A long queue for a full connectivity
	// slot must never hide a download or configuration task behind it.
	var candidates []jobs.Job
	if input.Executor == jobs.APIWorker {
		return scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM public.jobs WHERE state='queued' AND cancel_requested_at IS NULL AND executor=$1 AND type=ANY($2::text[]) ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`, string(input.Executor), kinds))
	}
	for _, kind := range kinds {
		job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM public.jobs WHERE state='queued' AND cancel_requested_at IS NULL AND executor=$1 AND type=$2 AND core_build_id::text=ANY($3::text[]) ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`, string(input.Executor), kind, cores))
		if errors.Is(err, jobs.ErrNotFound) {
			continue
		}
		if err != nil {
			return jobs.Job{}, err
		}
		candidates = append(candidates, job)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].CreatedAt.Equal(candidates[j].CreatedAt) {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].CreatedAt.Before(candidates[j].CreatedAt)
	})
	for _, job := range candidates {
		var revoked bool
		if err := tx.QueryRow(ctx, `SELECT `+testRevokedSQL+` FROM public.jobs WHERE id=$1`, dbID(job.ID)).Scan(&revoked); err != nil {
			return jobs.Job{}, jobError(err)
		}
		if revoked {
			if err := cancelChild(ctx, tx, job); err != nil {
				return jobs.Job{}, err
			}
			continue
		}
		q, _, err := readQuota(ctx, tx, job.ScopeID)
		if err != nil {
			return jobs.Job{}, err
		}
		localLimit, globalLimit := int32(1), int32(2147483647)
		maximum := input.MaximumSlots
		if maximum == (runnerprotocol.Slots{}) {
			maximum = runnerprotocol.Slots{ConfigValidate: 1, Connectivity: 4, DownloadThroughput: 1}
		}
		available := input.AvailableSlots.ConfigValidate
		if input.AvailableSlots == (runnerprotocol.Slots{}) {
			available = 1
		}
		switch job.Type {
		case jobs.Connectivity:
			localLimit = min(q.ConnectivityConcurrency, maximum.Connectivity)
			globalLimit = q.ConnectivityConcurrency
			available = input.AvailableSlots.Connectivity
		case jobs.DownloadThroughput:
			localLimit = min(q.ThroughputConcurrency, maximum.DownloadThroughput)
			globalLimit = q.ThroughputConcurrency
			available = input.AvailableSlots.DownloadThroughput
		}
		if available < 1 {
			continue
		}
		var offline, online int32
		if err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE type='config_validate'),count(*) FILTER(WHERE type IN ('connectivity','download_throughput')) FROM public.jobs WHERE worker_id=$1 AND executor='runner' AND state IN ('leased','running') AND lease_until>clock_timestamp()`, dbID(input.WorkerID)).Scan(&offline, &online); err != nil {
			return jobs.Job{}, jobError(err)
		}
		if job.Type.Network() && offline > 0 || job.Type == jobs.ConfigValidate && online > 0 {
			continue
		}
		var global, local int32
		if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE worker_id=$2) FROM public.jobs WHERE executor='runner' AND type=$1 AND state IN ('leased','running') AND lease_until>clock_timestamp()`, string(job.Type), dbID(input.WorkerID)).Scan(&global, &local); err != nil {
			return jobs.Job{}, jobError(err)
		}
		if global < globalLimit && local < localLimit {
			return job, nil
		}
	}
	return jobs.Job{}, jobs.ErrNotFound
}

// Ordinary edits keep their frozen input. Security invalidation, target
// deletion and build deactivation prevent both new leases and continued work.
const testRevokedSQL = `EXISTS(SELECT 1 FROM public.test_jobs t WHERE t.job_id=public.jobs.id AND (
 NOT EXISTS(SELECT 1 FROM public.core_builds b WHERE b.id=public.jobs.core_build_id AND b.enabled)
 OR (t.test_target_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM public.test_targets target WHERE target.id=t.test_target_id AND target.scope_id=t.scope_id AND target.enabled AND target.deleted_at IS NULL))
 OR EXISTS(SELECT 1 FROM jsonb_array_elements(t.dependencies) d LEFT JOIN public.resources h ON h.scope_id=t.scope_id AND h.id=(d->>'id')::uuid WHERE h.id IS NULL OR h.deleted_at IS NOT NULL OR NOT h.enabled OR h.security_epoch<>(d->>'security_epoch')::bigint)))`

// reserveAttempt runs after the job insert, in the same admission transaction.
// UPDATE's predicate and bucket row lock serialize independent batches. An
// existing reservation is returned without consulting a new day's bucket.
func reserveAttempt(ctx context.Context, tx pgx.Tx, job jobs.Job, attempt int32, maximum int64) (ir.ID, error) {
	if !job.Type.Network() || attempt < 1 || attempt > 2 || maximum < 1 || maximum > 1<<30 {
		return "", jobs.ErrInvalidInput
	}
	var existing ir.ID
	var reserved int64
	err := tx.QueryRow(ctx, `SELECT id::text,reserved_bytes FROM public.quota_reservations WHERE job_id=$1 AND attempt=$2`, dbID(job.ID), attempt).Scan(&existing, &reserved)
	if err == nil {
		if reserved != maximum {
			return "", jobs.ErrConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", jobError(err)
	}
	quota, _, err := readQuota(ctx, tx, job.ScopeID)
	if err != nil {
		return "", err
	}
	if maximum > quota.MaxTestBytes {
		return "", jobs.ErrBudgetExceeded
	}
	var day time.Time
	if err = tx.QueryRow(ctx, `SELECT (clock_timestamp() AT TIME ZONE 'UTC')::date`).Scan(&day); err != nil {
		return "", jobError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.quota_buckets(scope_id,utc_day) VALUES($1,$2) ON CONFLICT DO NOTHING`, dbID(job.ScopeID), day); err != nil {
		return "", jobError(err)
	}
	tag, err := tx.Exec(ctx, `UPDATE public.quota_buckets SET reserved_bytes=reserved_bytes+$3 WHERE scope_id=$1 AND utc_day=$2 AND settled_bytes <= $4 AND reserved_bytes <= $4-settled_bytes AND $3 <= $4-settled_bytes-reserved_bytes`, dbID(job.ScopeID), day, maximum, quota.DailyDownloadBytes)
	if err != nil {
		return "", jobError(err)
	}
	if tag.RowsAffected() != 1 {
		return "", jobs.ErrBudgetExceeded
	}
	id := jobs.NewID()
	_, err = tx.Exec(ctx, `INSERT INTO public.quota_reservations(id,scope_id,job_id,attempt,utc_day,reserved_bytes) VALUES($1,$2,$3,$4,$5,$6)`, dbID(id), dbID(job.ScopeID), dbID(job.ID), attempt, day, maximum)
	return id, jobError(err)
}

// The caller locks the job first. nil usage means unknown and settles the full
// reservation; known-zero cancellation is valid only before execution started.
func settleAttempt(ctx context.Context, tx pgx.Tx, job jobs.Job, attempt int32, usage *int64) (int64, error) {
	if !job.Type.Network() {
		return 0, nil
	}
	var id ir.ID
	var day time.Time
	var reserved int64
	var settled *int64
	err := tx.QueryRow(ctx, `SELECT id::text,utc_day,reserved_bytes,settled_bytes FROM public.quota_reservations WHERE job_id=$1 AND attempt=$2 FOR UPDATE`, dbID(job.ID), attempt).Scan(&id, &day, &reserved, &settled)
	if err != nil {
		return 0, jobError(err)
	}
	if settled != nil {
		return *settled, nil
	}
	actual := reserved
	if usage != nil {
		if *usage < 0 || *usage > reserved {
			return 0, jobs.ErrInvalidInput
		}
		actual = *usage
	}
	_, err = tx.Exec(ctx, `UPDATE public.quota_buckets SET reserved_bytes=reserved_bytes-$3,settled_bytes=settled_bytes+$4 WHERE scope_id=$1 AND utc_day=$2`, dbID(job.ScopeID), day, reserved, actual)
	if err != nil {
		return 0, jobError(err)
	}
	_, err = tx.Exec(ctx, `UPDATE public.quota_reservations SET settled_bytes=$2,settled_at=clock_timestamp() WHERE id=$1 AND settled_at IS NULL`, dbID(id), actual)
	return actual, jobError(err)
}
