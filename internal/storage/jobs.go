package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Jobs struct {
	pool *pgxpool.Pool
	box  *secretbox.Box
}

var _ jobs.Repository = (*Jobs)(nil)

func NewJobs(pool *pgxpool.Pool, box *secretbox.Box) (*Jobs, error) {
	if pool == nil || box == nil {
		return nil, jobs.ErrInvalidInput
	}
	return &Jobs{pool: pool, box: box}, nil
}
func (s *Jobs) MACCursor(data []byte) ([]byte, error) {
	return s.box.Digest(secretbox.PurposeCursor, data)
}
func newJobID() ir.ID { return jobs.NewID() }
func jobError(err error) error {
	if err == nil {
		return nil
	}
	for _, safe := range []error{jobs.ErrInvalidInput, jobs.ErrUnavailable, jobs.ErrNotFound, jobs.ErrLeaseLost, jobs.ErrConflict, jobs.ErrRevisionConflict, jobs.ErrCanceled, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return jobs.ErrNotFound
	}
	var db *pgconn.PgError
	if errors.As(err, &db) {
		switch db.Code {
		case "23505":
			return jobs.ErrConflict
		case "23503", "23514", "22P02":
			return jobs.ErrInvalidInput
		}
	}
	return jobs.ErrUnavailable
}
func nullableID(id ir.ID) any {
	if id == "" {
		return nil
	}
	return dbID(id)
}
func jobAAD(scope, id ir.ID) secretbox.Context {
	return secretbox.Context{ScopeID: scope, Table: secretbox.TableJobs, ObjectID: id, Revision: 1, SchemaVersion: 1}
}

const jobColumns = `id::text,scope_id::text,COALESCE(batch_id::text,''),revision,executor,type,state,attempt,lease_seq,
    cancel_requested_at IS NOT NULL,created_at,started_at,finished_at,COALESCE(core_build_id::text,''),COALESCE(verdict,''),safe_error`

func scanJob(row pgx.Row) (jobs.Job, error) {
	var job jobs.Job
	var safe []byte
	err := row.Scan(&job.ID, &job.ScopeID, &job.BatchID, &job.Revision, &job.Executor, &job.Type, &job.State, &job.Attempt, &job.LeaseSeq, &job.CancelRequested, &job.CreatedAt, &job.StartedAt, &job.FinishedAt, &job.CoreBuildID, &job.Verdict, &safe)
	if err != nil {
		return jobs.Job{}, jobError(err)
	}
	if len(safe) > 0 && json.Unmarshal(safe, &job.Error) != nil {
		return jobs.Job{}, jobs.ErrUnavailable
	}
	if job.Error != nil {
		job.Error = runnerprotocol.Safe(job.Error.Code)
	}
	return job, nil
}
func (s *Jobs) Enqueue(ctx context.Context, input jobs.EnqueueInput) (jobs.Job, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return jobs.Job{}, jobs.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	job, err := s.EnqueueTx(ctx, tx, input)
	if err != nil {
		return jobs.Job{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return jobs.Job{}, jobError(err)
	}
	return job, nil
}

// EnqueueTx participates in the caller's transaction. It never commits and
// payload bytes are encrypted before any SQL receives them.
func (s *Jobs) EnqueueTx(ctx context.Context, tx pgx.Tx, input jobs.EnqueueInput) (jobs.Job, error) {
	if tx == nil || input.ScopeID.Validate() != nil || !jobs.ValidType(input.Executor, input.Type) || len(input.Payload) == 0 || len(input.Payload) > jobs.MaxPayloadBytes || (input.ID != "" && input.ID.Validate() != nil) || (input.BatchID != "" && input.BatchID.Validate() != nil) {
		return jobs.Job{}, jobs.ErrInvalidInput
	}
	if input.Executor == jobs.Runner {
		frozen, err := runnerprotocol.DecodeFrozenPayload(input.Payload)
		if err != nil || input.CoreBuildID != frozen.Core.CoreBuildID {
			return jobs.Job{}, jobs.ErrInvalidInput
		}
	} else if input.CoreBuildID != "" {
		return jobs.Job{}, jobs.ErrInvalidInput
	}
	if input.ID == "" {
		input.ID = newJobID()
	}
	payload, wrapping, err := s.box.Seal(jobAAD(input.ScopeID, input.ID), input.Payload)
	if err != nil {
		return jobs.Job{}, jobs.ErrUnavailable
	}
	envelope, _ := json.Marshal(payload)
	wrap, _ := json.Marshal(wrapping)
	job, err := scanJob(tx.QueryRow(ctx, `INSERT INTO public.jobs(id,scope_id,batch_id,executor,type,core_build_id) VALUES($1,$2,$3,$4,$5,$6) RETURNING `+jobColumns, dbID(input.ID), dbID(input.ScopeID), nullableID(input.BatchID), string(input.Executor), string(input.Type), nullableID(input.CoreBuildID)))
	if err != nil {
		return jobs.Job{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.job_payloads(scope_id,job_id,envelope) VALUES($1,$2,$3)`, dbID(input.ScopeID), dbID(input.ID), envelope); err != nil {
		return jobs.Job{}, jobError(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.job_payload_wrappings(scope_id,job_id,wrapping) VALUES($1,$2,$3)`, dbID(input.ScopeID), dbID(input.ID), wrap); err != nil {
		return jobs.Job{}, jobError(err)
	}
	_, err = appendJobEvent(ctx, tx, job, jobs.EventInput{EventID: newJobID(), Phase: "queued", Total: 1})
	return job, err
}
func (s *Jobs) Get(ctx context.Context, scope, id ir.ID) (jobs.Job, error) {
	if !validIDs(scope, id) {
		return jobs.Job{}, jobs.ErrInvalidInput
	}
	return scanJob(s.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM public.jobs WHERE scope_id=$1 AND id=$2`, dbID(scope), dbID(id)))
}

func (s *Jobs) Claim(ctx context.Context, input jobs.ClaimInput) (*jobs.Lease, error) {
	if input.WorkerID.Validate() != nil || len(input.Types) < 1 || len(input.Types) > 3 {
		return nil, jobs.ErrInvalidInput
	}
	kinds := make([]string, 0, len(input.Types))
	for _, kind := range input.Types {
		if !jobs.ValidType(input.Executor, kind) {
			return nil, jobs.ErrInvalidInput
		}
		kinds = append(kinds, string(kind))
	}
	cores := make([]string, 0, len(input.CoreBuildIDs))
	for _, id := range input.CoreBuildIDs {
		if id.Validate() != nil {
			return nil, jobs.ErrInvalidInput
		}
		cores = append(cores, string(id))
	}
	if input.Executor == jobs.Runner && (len(cores) == 0 || len(cores) > 100) {
		return nil, jobs.ErrInvalidInput
	}
	if _, err := s.ReapExpired(ctx); err != nil {
		return nil, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, jobs.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if input.Executor == jobs.Runner {
		// Registered M1 Runner capacity is one validation slot. The transaction
		// lock and persisted active leases survive API/Runner process restarts.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,539432))`, string(input.WorkerID)); err != nil {
			return nil, jobError(err)
		}
		var busy bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.jobs WHERE executor='runner' AND worker_id=$1 AND state IN ('leased','running') AND lease_until>clock_timestamp())`, dbID(input.WorkerID)).Scan(&busy); err != nil {
			return nil, jobError(err)
		}
		if busy {
			return nil, nil
		}
	}
	job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM public.jobs WHERE state='queued' AND cancel_requested_at IS NULL AND executor=$1 AND type=ANY($2::text[]) AND ($1='api_worker' OR core_build_id::text=ANY($3::text[])) ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`, string(input.Executor), kinds, cores))
	if errors.Is(err, jobs.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var expiry time.Time
	err = tx.QueryRow(ctx, `UPDATE public.jobs SET state='leased',attempt=attempt+1,lease_seq=lease_seq+1,revision=revision+1,worker_id=$2,lease_until=clock_timestamp()+interval '30 seconds',started_at=COALESCE(started_at,clock_timestamp()),safe_error=NULL WHERE id=$1 AND state='queued' AND cancel_requested_at IS NULL RETURNING attempt,lease_seq,revision,lease_until,started_at`, dbID(job.ID), dbID(input.WorkerID)).Scan(&job.Attempt, &job.LeaseSeq, &job.Revision, &expiry, &job.StartedAt)
	if err != nil {
		return nil, jobError(err)
	}
	job.State = jobs.Leased
	job.Error = nil
	var encrypted, wrapped []byte
	err = tx.QueryRow(ctx, `SELECT p.envelope,w.wrapping FROM public.job_payloads p JOIN public.job_payload_wrappings w USING(scope_id,job_id) WHERE p.scope_id=$1 AND p.job_id=$2`, dbID(job.ScopeID), dbID(job.ID)).Scan(&encrypted, &wrapped)
	if err != nil {
		return nil, jobs.ErrUnavailable
	}
	var payload secretbox.Payload
	var wrapping secretbox.Wrapping
	if json.Unmarshal(encrypted, &payload) != nil || json.Unmarshal(wrapped, &wrapping) != nil {
		return nil, jobs.ErrUnavailable
	}
	plain, err := s.box.Open(jobAAD(job.ScopeID, job.ID), payload, wrapping)
	if err != nil {
		return nil, jobs.ErrUnavailable
	}
	if _, err = appendJobEvent(ctx, tx, job, jobs.EventInput{EventID: newJobID(), Phase: "leased", Total: 1}); err != nil {
		clear(plain)
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		clear(plain)
		return nil, jobError(err)
	}
	return &jobs.Lease{Job: job, Identity: jobs.LeaseIdentity{JobID: job.ID, WorkerID: input.WorkerID, Attempt: job.Attempt, LeaseSeq: job.LeaseSeq}, Payload: plain, ExpiresAt: expiry}, nil
}

// ReapExpired is restart recovery. It never resurrects cancellation or retries
// a configuration failure. One replacement lease is allowed after lost work.
func (s *Jobs) ReapExpired(ctx context.Context) (int, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, jobs.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT `+jobColumns+` FROM public.jobs WHERE state IN ('leased','running') AND lease_until<=clock_timestamp() ORDER BY lease_until,id LIMIT 100 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return 0, jobError(err)
	}
	var expired []jobs.Job
	for rows.Next() {
		job, scanErr := scanJob(rows)
		if scanErr != nil {
			rows.Close()
			return 0, scanErr
		}
		expired = append(expired, job)
	}
	rows.Close()
	if rows.Err() != nil {
		return 0, jobError(rows.Err())
	}
	for _, job := range expired {
		state := jobs.Queued
		failure := runnerprotocol.Safe("LEASE_LOST")
		if job.CancelRequested {
			state = jobs.Canceled
			failure = runnerprotocol.Safe("CANCELED")
		} else if job.Attempt >= 2 {
			state = jobs.Failed
		}
		data, _ := json.Marshal(failure)
		_, err = tx.Exec(ctx, `UPDATE public.jobs SET state=$2,revision=revision+1,lease_until=NULL,safe_error=$3,finished_at=CASE WHEN $2='queued' THEN NULL ELSE clock_timestamp() END WHERE id=$1 AND state IN ('leased','running') AND lease_until<=clock_timestamp()`, dbID(job.ID), string(state), data)
		if err != nil {
			return 0, jobError(err)
		}
		phase := "queued"
		completed := int32(0)
		if state.Terminal() {
			phase = "completed"
			completed = 1
		}
		if _, err = appendJobEvent(ctx, tx, job, jobs.EventInput{EventID: newJobID(), Phase: phase, Completed: completed, Total: 1, Error: failure}); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, jobError(err)
	}
	return len(expired), nil
}

func lockLease(ctx context.Context, tx pgx.Tx, id jobs.LeaseIdentity) (jobs.Job, error) {
	if !id.Valid() {
		return jobs.Job{}, jobs.ErrInvalidInput
	}
	job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM public.jobs WHERE id=$1 AND worker_id=$2 AND attempt=$3 AND lease_seq=$4 FOR UPDATE`, dbID(id.JobID), dbID(id.WorkerID), id.Attempt, id.LeaseSeq))
	if errors.Is(err, jobs.ErrNotFound) {
		return jobs.Job{}, jobs.ErrLeaseLost
	}
	return job, err
}
func validLease(ctx context.Context, tx pgx.Tx, id jobs.LeaseIdentity, allowCancel bool) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT state IN ('leased','running') AND lease_until>clock_timestamp() AND ($5 OR cancel_requested_at IS NULL) FROM public.jobs WHERE id=$1 AND worker_id=$2 AND attempt=$3 AND lease_seq=$4`, dbID(id.JobID), dbID(id.WorkerID), id.Attempt, id.LeaseSeq, allowCancel).Scan(&valid)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !valid {
		return jobs.ErrLeaseLost
	}
	return jobError(err)
}
func (s *Jobs) Heartbeat(ctx context.Context, id jobs.LeaseIdentity) (jobs.Heartbeat, error) {
	if !id.Valid() {
		return jobs.Heartbeat{}, jobs.ErrInvalidInput
	}
	var result jobs.Heartbeat
	err := s.pool.QueryRow(ctx, `UPDATE public.jobs SET lease_until=clock_timestamp()+interval '30 seconds' WHERE id=$1 AND worker_id=$2 AND attempt=$3 AND lease_seq=$4 AND state IN ('leased','running') AND lease_until>clock_timestamp() RETURNING lease_until,cancel_requested_at IS NOT NULL`, dbID(id.JobID), dbID(id.WorkerID), id.Attempt, id.LeaseSeq).Scan(&result.ExpiresAt, &result.CancelRequested)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, jobs.ErrLeaseLost
	}
	return result, jobError(err)
}
