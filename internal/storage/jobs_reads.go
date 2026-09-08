package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/jackc/pgx/v5"
)

var _ jobs.ManagementRepository = (*Jobs)(nil)

func (s *Jobs) List(ctx context.Context, input jobs.ListInput) (jobs.Page, error) {
	if input.ScopeID.Validate() != nil || input.Limit < 1 || input.Limit > 200 || (input.State != "" && !input.State.Valid()) || (input.Executor != "" && input.Executor != jobs.APIWorker && input.Executor != jobs.Runner) || (input.Type != "" && !input.Type.Valid()) || (input.BatchID != "" && input.BatchID.Validate() != nil) || (input.AfterID != "" && (input.AfterID.Validate() != nil || input.AfterCreatedAt.IsZero())) {
		return jobs.Page{}, jobs.ErrInvalidInput
	}
	rows, err := s.pool.Query(ctx, `SELECT `+jobColumns+` FROM public.jobs WHERE scope_id=$1 AND ($2='' OR state=$2) AND ($3='' OR type=$3) AND ($4='' OR executor=$4) AND ($5::uuid IS NULL OR batch_id=$5) AND ($6::uuid IS NULL OR (created_at,id)>($7,$6)) ORDER BY created_at,id LIMIT $8`, dbID(input.ScopeID), string(input.State), string(input.Type), string(input.Executor), nullableID(input.BatchID), nullableID(input.AfterID), input.AfterCreatedAt, input.Limit+1)
	if err != nil {
		return jobs.Page{}, jobError(err)
	}
	defer rows.Close()
	page := jobs.Page{Jobs: []jobs.Job{}}
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return jobs.Page{}, err
		}
		page.Jobs = append(page.Jobs, job)
	}
	if rows.Err() != nil {
		return jobs.Page{}, jobError(rows.Err())
	}
	if len(page.Jobs) > input.Limit {
		page.HasMore = true
		page.Jobs = page.Jobs[:input.Limit]
	}
	return page, nil
}
func (s *Jobs) CreateBatch(ctx context.Context, input jobs.BatchInput) (jobs.Batch, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return jobs.Batch{}, jobs.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	batch, err := s.CreateBatchTx(ctx, tx, input)
	if err != nil {
		return jobs.Batch{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return jobs.Batch{}, jobError(err)
	}
	return batch, nil
}
func (s *Jobs) CreateBatchTx(ctx context.Context, tx pgx.Tx, input jobs.BatchInput) (jobs.Batch, error) {
	if tx == nil || input.ScopeID.Validate() != nil || len(input.Children) < 1 || len(input.Children) > 200 || (input.ID != "" && input.ID.Validate() != nil) || input.EffectiveLimits.MaxBytes != 0 || input.EffectiveLimits.DurationMS < 1 || input.EffectiveLimits.DurationMS > 300000 {
		return jobs.Batch{}, jobs.ErrInvalidInput
	}
	if input.ID == "" {
		input.ID = newJobID()
	}
	limits, _ := json.Marshal(input.EffectiveLimits)
	_, err := tx.Exec(ctx, `INSERT INTO public.job_batches(id,scope_id,effective_limits) VALUES($1,$2,$3)`, dbID(input.ID), dbID(input.ScopeID), limits)
	if err != nil {
		return jobs.Batch{}, jobError(err)
	}
	for _, child := range input.Children {
		if child.ScopeID != "" && child.ScopeID != input.ScopeID || child.BatchID != "" && child.BatchID != input.ID {
			return jobs.Batch{}, jobs.ErrInvalidInput
		}
		child.ScopeID = input.ScopeID
		child.BatchID = input.ID
		if _, err = s.EnqueueTx(ctx, tx, child); err != nil {
			return jobs.Batch{}, err
		}
	}
	return readBatch(ctx, tx, input.ScopeID, input.ID, false)
}
func aggregateBatch(batch *jobs.Batch) {
	batch.State = jobs.Queued
	allPass := true
	anyFail := false
	failed := false
	timedOut := false
	canceled := false
	for _, job := range batch.Children {
		batch.Revision += job.Revision - 1
		if job.State != jobs.Queued {
			batch.State = jobs.Running
		}
		if job.State.Terminal() {
			batch.Completed++
		}
		failed = failed || job.State == jobs.Failed
		timedOut = timedOut || job.State == jobs.TimedOut
		canceled = canceled || job.State == jobs.Canceled
		allPass = allPass && job.Verdict == jobs.Pass
		anyFail = anyFail || job.Verdict == jobs.Fail
	}
	if int(batch.Completed) != len(batch.Children) {
		return
	}
	batch.State = jobs.Succeeded
	if failed {
		batch.State = jobs.Failed
	} else if timedOut {
		batch.State = jobs.TimedOut
	} else if canceled {
		batch.State = jobs.Canceled
	}
	batch.Verdict = jobs.Inconclusive
	if anyFail {
		batch.Verdict = jobs.Fail
	} else if allPass {
		batch.Verdict = jobs.Pass
	}
}
func readBatch(ctx context.Context, tx pgx.Tx, scope, id ir.ID, lock bool) (jobs.Batch, error) {
	var batch jobs.Batch
	var limits []byte
	suffix := ""
	if lock {
		suffix = " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, `SELECT id::text,scope_id::text,revision,cancel_requested_at IS NOT NULL,created_at,effective_limits FROM public.job_batches WHERE scope_id=$1 AND id=$2`+suffix, dbID(scope), dbID(id)).Scan(&batch.ID, &batch.ScopeID, &batch.Revision, &batch.CancelRequested, &batch.CreatedAt, &limits)
	if err != nil {
		return batch, jobError(err)
	}
	if json.Unmarshal(limits, &batch.EffectiveLimits) != nil {
		return batch, jobs.ErrUnavailable
	}
	rows, err := tx.Query(ctx, `SELECT `+jobColumns+` FROM public.jobs WHERE scope_id=$1 AND batch_id=$2 ORDER BY created_at,id`+suffix, dbID(scope), dbID(id))
	if err != nil {
		return batch, jobError(err)
	}
	defer rows.Close()
	batch.Children = []jobs.Job{}
	for rows.Next() {
		child, err := scanJob(rows)
		if err != nil {
			return batch, err
		}
		batch.Children = append(batch.Children, child)
	}
	if rows.Err() != nil {
		return batch, jobError(rows.Err())
	}
	if len(batch.Children) < 1 || len(batch.Children) > 200 {
		return batch, jobs.ErrUnavailable
	}
	aggregateBatch(&batch)
	return batch, nil
}
func (s *Jobs) Snapshot(ctx context.Context, scope, id ir.ID) (jobs.Snapshot, error) {
	if !validIDs(scope, id) {
		return jobs.Snapshot{}, jobs.ErrInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return jobs.Snapshot{}, jobs.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM public.jobs WHERE scope_id=$1 AND id=$2`, dbID(scope), dbID(id)))
	if err == nil {
		return jobs.Snapshot{Job: &job}, nil
	}
	if !errors.Is(err, jobs.ErrNotFound) {
		return jobs.Snapshot{}, err
	}
	batch, err := readBatch(ctx, tx, scope, id, false)
	if err != nil {
		return jobs.Snapshot{}, err
	}
	return jobs.Snapshot{Batch: &batch}, nil
}
func cancelChild(ctx context.Context, tx pgx.Tx, job jobs.Job) error {
	if job.State.Terminal() || job.CancelRequested {
		return nil
	}
	_, err := tx.Exec(ctx, `UPDATE public.jobs SET cancel_requested_at=clock_timestamp(),revision=revision+1,state=CASE WHEN state='queued' THEN 'canceled' ELSE state END,finished_at=CASE WHEN state='queued' THEN clock_timestamp() ELSE finished_at END WHERE id=$1`, dbID(job.ID))
	if err != nil {
		return jobError(err)
	}
	phase := "canceling"
	completed := int32(0)
	if job.State == jobs.Queued {
		phase = "completed"
		completed = 1
	}
	_, err = appendJobEvent(ctx, tx, job, jobs.EventInput{EventID: newJobID(), Phase: phase, Completed: completed, Total: 1, Error: runnerprotocol.Safe("CANCELED")})
	return err
}
func (s *Jobs) Cancel(ctx context.Context, scope, id ir.ID, expectedRevision int64) (jobs.Snapshot, error) {
	if !validIDs(scope, id) || expectedRevision < 1 {
		return jobs.Snapshot{}, jobs.ErrInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return jobs.Snapshot{}, jobs.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	job, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM public.jobs WHERE scope_id=$1 AND id=$2 FOR UPDATE`, dbID(scope), dbID(id)))
	var snapshot jobs.Snapshot
	if err == nil {
		if job.Revision != expectedRevision {
			return snapshot, jobs.ErrRevisionConflict
		}
		if err = cancelChild(ctx, tx, job); err != nil {
			return snapshot, err
		}
		job, err = scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM public.jobs WHERE scope_id=$1 AND id=$2`, dbID(scope), dbID(id)))
		snapshot.Job = &job
	} else if errors.Is(err, jobs.ErrNotFound) {
		batch, readErr := readBatch(ctx, tx, scope, id, true)
		if readErr != nil {
			return snapshot, readErr
		}
		if batch.Revision != expectedRevision {
			return snapshot, jobs.ErrRevisionConflict
		}
		if !batch.State.Terminal() && !batch.CancelRequested {
			if _, err = tx.Exec(ctx, `UPDATE public.job_batches SET cancel_requested_at=clock_timestamp(),revision=revision+1 WHERE id=$1`, dbID(id)); err != nil {
				return snapshot, jobError(err)
			}
		}
		for _, child := range batch.Children {
			if err = cancelChild(ctx, tx, child); err != nil {
				return snapshot, err
			}
		}
		batch, err = readBatch(ctx, tx, scope, id, false)
		snapshot.Batch = &batch
	} else {
		return snapshot, err
	}
	if err != nil {
		return jobs.Snapshot{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return jobs.Snapshot{}, jobError(err)
	}
	return snapshot, nil
}
func (s *Jobs) Events(ctx context.Context, scope, id ir.ID, after int64, limit int) (jobs.EventPage, error) {
	if !validIDs(scope, id) || after < 0 || limit < 1 || limit > 200 {
		return jobs.EventPage{}, jobs.ErrInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return jobs.EventPage{}, jobs.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	page := jobs.EventPage{Events: []jobs.Event{}}
	var state jobs.State
	err = tx.QueryRow(ctx, `SELECT event_seq,state FROM public.jobs WHERE scope_id=$1 AND id=$2`, dbID(scope), dbID(id)).Scan(&page.LatestSeq, &state)
	if err != nil {
		return page, jobError(err)
	}
	page.Terminal = state.Terminal()
	if after > page.LatestSeq {
		return page, jobs.ErrInvalidInput
	}
	var first int64
	err = tx.QueryRow(ctx, `SELECT COALESCE(min(seq),$2+1) FROM public.job_events WHERE job_id=$1 AND created_at>=clock_timestamp()-interval '7 days'`, dbID(id), page.LatestSeq).Scan(&first)
	if err != nil {
		return page, jobError(err)
	}
	if after < first-1 {
		page.Reset = true
		return page, nil
	}
	rows, err := tx.Query(ctx, `SELECT event FROM public.job_events WHERE job_id=$1 AND seq>$2 AND created_at>=clock_timestamp()-interval '7 days' ORDER BY seq LIMIT $3`, dbID(id), after, limit)
	if err != nil {
		return page, jobError(err)
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		var event jobs.Event
		if rows.Scan(&data) != nil || json.Unmarshal(data, &event) != nil {
			return page, jobs.ErrUnavailable
		}
		if event.Error != nil {
			event.Error = runnerprotocol.Safe(event.Error.Code)
		}
		page.Events = append(page.Events, event)
	}
	return page, jobError(rows.Err())
}
