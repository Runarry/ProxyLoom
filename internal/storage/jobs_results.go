package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/jackc/pgx/v5"
)

var jobPhases = map[string]bool{"queued": true, "leased": true, "fetching": true, "parsing": true, "compiling": true, "preparing": true, "validating": true, "starting": true, "running": true, "probing": true, "downloading": true, "collecting": true, "settling": true, "canceling": true, "completed": true}

func validVerdict(v jobs.Verdict) bool {
	return v == jobs.Pass || v == jobs.Fail || v == jobs.Inconclusive
}
func cleanEvent(input jobs.EventInput) (jobs.EventInput, error) {
	if input.EventID.Validate() != nil || !jobPhases[input.Phase] || input.Completed < 0 || input.Total < 0 || input.Completed > input.Total || (input.Verdict != "" && !validVerdict(input.Verdict)) {
		return input, jobs.ErrInvalidInput
	}
	if input.Error != nil {
		if !runnerprotocol.KnownError(input.Error.Code) {
			return input, jobs.ErrInvalidInput
		}
		input.Error = runnerprotocol.Safe(input.Error.Code)
	}
	return input, nil
}
func appendJobEvent(ctx context.Context, tx pgx.Tx, job jobs.Job, input jobs.EventInput) (jobs.EventReceipt, error) {
	input, err := cleanEvent(input)
	if err != nil {
		return jobs.EventReceipt{}, err
	}
	data, err := json.Marshal(input)
	if err != nil {
		return jobs.EventReceipt{}, jobs.ErrInvalidInput
	}
	hash := runnerprotocol.Digest(data)
	var seq int64
	err = tx.QueryRow(ctx, `UPDATE public.jobs SET event_seq=event_seq+1 WHERE id=$1 RETURNING event_seq`, dbID(job.ID)).Scan(&seq)
	if err != nil {
		return jobs.EventReceipt{}, jobError(err)
	}
	event := jobs.Event{JobID: job.ID, Seq: seq, Phase: input.Phase, Completed: input.Completed, Total: input.Total, Verdict: input.Verdict, Error: input.Error}
	encoded, _ := json.Marshal(event)
	_, err = tx.Exec(ctx, `INSERT INTO public.job_events(job_id,seq,attempt,lease_seq,event_id,event_hash,event) VALUES($1,$2,$3,$4,$5,$6,$7)`, dbID(job.ID), seq, job.Attempt, job.LeaseSeq, dbID(input.EventID), hash, encoded)
	return jobs.EventReceipt{JobID: job.ID, Attempt: job.Attempt, LeaseSeq: runnerprotocol.Sequence(job.LeaseSeq), EventID: input.EventID, Seq: runnerprotocol.Sequence(seq)}, jobError(err)
}
func (s *Jobs) Event(ctx context.Context, id jobs.LeaseIdentity, input jobs.EventInput) (jobs.EventReceipt, error) {
	input, err := cleanEvent(input)
	if err != nil {
		return jobs.EventReceipt{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return jobs.EventReceipt{}, jobs.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	job, err := lockLease(ctx, tx, id)
	if err != nil {
		return jobs.EventReceipt{}, err
	}
	if err = validLease(ctx, tx, id, true); err != nil {
		return jobs.EventReceipt{}, err
	}
	// API-only phases can never arrive from a registered Runner.
	if job.Executor == jobs.Runner && (input.Phase == "queued" || input.Phase == "fetching" || input.Phase == "parsing" || input.Phase == "compiling") {
		return jobs.EventReceipt{}, jobs.ErrInvalidInput
	}
	data, _ := json.Marshal(input)
	hash := runnerprotocol.Digest(data)
	var previous string
	var seq int64
	err = tx.QueryRow(ctx, `SELECT event_hash,seq FROM public.job_events WHERE job_id=$1 AND attempt=$2 AND lease_seq=$3 AND event_id=$4`, dbID(id.JobID), id.Attempt, id.LeaseSeq, dbID(input.EventID)).Scan(&previous, &seq)
	if err == nil {
		if previous != hash {
			return jobs.EventReceipt{}, jobs.ErrConflict
		}
		return jobs.EventReceipt{JobID: id.JobID, Attempt: id.Attempt, LeaseSeq: runnerprotocol.Sequence(id.LeaseSeq), EventID: input.EventID, Seq: runnerprotocol.Sequence(seq), Replayed: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return jobs.EventReceipt{}, jobError(err)
	}
	if input.Phase != "leased" {
		_, err = tx.Exec(ctx, `UPDATE public.jobs SET state='running',revision=revision+CASE WHEN state='leased' THEN 1 ELSE 0 END WHERE id=$1`, dbID(id.JobID))
		if err != nil {
			return jobs.EventReceipt{}, jobError(err)
		}
	}
	receipt, err := appendJobEvent(ctx, tx, job, input)
	if err != nil {
		return jobs.EventReceipt{}, err
	}
	if err = validLease(ctx, tx, id, true); err != nil {
		return jobs.EventReceipt{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return jobs.EventReceipt{}, jobError(err)
	}
	return receipt, nil
}
func canonicalResult(input jobs.Result) (jobs.Result, string, error) {
	if !input.State.Terminal() || (input.Verdict != "" && !validVerdict(input.Verdict)) || input.State == jobs.Succeeded && input.Verdict == "" || runnerprotocol.ValidateConfigMetrics(input.Metrics) != nil {
		return input, "", jobs.ErrInvalidInput
	}
	if input.Error != nil {
		if !runnerprotocol.KnownError(input.Error.Code) {
			return input, "", jobs.ErrInvalidInput
		}
		input.Error = runnerprotocol.Safe(input.Error.Code)
	}
	hash, err := runnerprotocol.ResultHash(runnerprotocol.ResultRequest{State: string(input.State), Verdict: string(input.Verdict), Metrics: input.Metrics, Error: input.Error})
	if err != nil {
		return input, "", jobs.ErrInvalidInput
	}
	if input.Hash != "" && input.Hash != hash {
		return input, "", jobs.ErrConflict
	}
	input.Hash = hash
	return input, hash, nil
}
func infrastructureRetry(job jobs.Job, result jobs.Result) bool {
	if job.Attempt >= 2 || result.State != jobs.Failed || result.Error == nil || job.CancelRequested {
		return false
	}
	switch result.Error.Code {
	case "CORE_BINARY_MISMATCH", "RUNNER_RESOURCE_LIMIT", "PORT_BUSY", "SERVICE_UNAVAILABLE":
		return true
	}
	return false
}
func (s *Jobs) Complete(ctx context.Context, id jobs.LeaseIdentity, result jobs.Result) (jobs.ResultReceipt, error) {
	return s.CompleteTx(ctx, id, result, nil)
}

// CompleteTx fences the business callback and attempt outcome in one commit.
// Accepted replay never re-enters callback. A callback error, lease expiry, or
// lost fence rolls back both changes, including any callback writes.
func (s *Jobs) CompleteTx(ctx context.Context, id jobs.LeaseIdentity, input jobs.Result, callback func(context.Context, pgx.Tx) error) (jobs.ResultReceipt, error) {
	input, hash, err := canonicalResult(input)
	if err != nil {
		return jobs.ResultReceipt{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return jobs.ResultReceipt{}, jobs.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	job, err := lockLease(ctx, tx, id)
	if err != nil {
		return jobs.ResultReceipt{}, err
	}
	var receipt jobs.ResultReceipt
	receipt.JobID = id.JobID
	receipt.Attempt = id.Attempt
	receipt.LeaseSeq = runnerprotocol.Sequence(id.LeaseSeq)
	err = tx.QueryRow(ctx, `SELECT result_id::text,result_hash,settled_bytes FROM public.job_results WHERE job_id=$1 AND attempt=$2 AND lease_seq=$3 AND worker_id=$4`, dbID(id.JobID), id.Attempt, id.LeaseSeq, dbID(id.WorkerID)).Scan(&receipt.ResultID, &receipt.ResultHash, &receipt.SettledBytes)
	if err == nil {
		if receipt.ResultHash != hash {
			return jobs.ResultReceipt{}, jobs.ErrConflict
		}
		receipt.Replayed = true
		return receipt, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return jobs.ResultReceipt{}, jobError(err)
	}
	if err = validLease(ctx, tx, id, true); err != nil {
		return jobs.ResultReceipt{}, err
	}
	if job.CancelRequested && input.State != jobs.Canceled {
		return jobs.ResultReceipt{}, jobs.ErrCanceled
	}
	if callback != nil {
		if job.CancelRequested {
			return jobs.ResultReceipt{}, jobs.ErrCanceled
		}
		if err = callback(ctx, tx); err != nil {
			return jobs.ResultReceipt{}, err
		}
	}
	// clock_timestamp, not transaction time, closes the callback-expiry window.
	if err = validLease(ctx, tx, id, true); err != nil {
		return jobs.ResultReceipt{}, err
	}
	receipt.ResultID = newJobID()
	receipt.ResultHash = hash
	encoded, _ := json.Marshal(runnerprotocol.ResultRequest{JobID: id.JobID, Attempt: id.Attempt, LeaseSeq: runnerprotocol.Sequence(id.LeaseSeq), ResultHash: hash, State: string(input.State), Verdict: string(input.Verdict), Metrics: input.Metrics, Error: input.Error})
	_, err = tx.Exec(ctx, `INSERT INTO public.job_results(result_id,job_id,worker_id,attempt,lease_seq,result_hash,result,settled_bytes) VALUES($1,$2,$3,$4,$5,$6,$7,0)`, dbID(receipt.ResultID), dbID(id.JobID), dbID(id.WorkerID), id.Attempt, id.LeaseSeq, hash, encoded)
	if err != nil {
		return jobs.ResultReceipt{}, jobError(err)
	}
	state := input.State
	verdict := input.Verdict
	if infrastructureRetry(job, input) {
		state = jobs.Queued
		verdict = ""
	}
	var safe []byte
	if input.Error != nil {
		safe, _ = json.Marshal(input.Error)
	}
	tag, err := tx.Exec(ctx, `UPDATE public.jobs SET state=$5,revision=revision+1,lease_until=NULL,verdict=NULLIF($6,''),safe_error=$7,finished_at=CASE WHEN $5='queued' THEN NULL ELSE clock_timestamp() END WHERE id=$1 AND worker_id=$2 AND attempt=$3 AND lease_seq=$4 AND state IN ('leased','running') AND lease_until>clock_timestamp()`, dbID(id.JobID), dbID(id.WorkerID), id.Attempt, id.LeaseSeq, string(state), string(verdict), safe)
	if err != nil {
		return jobs.ResultReceipt{}, jobError(err)
	}
	if tag.RowsAffected() != 1 {
		return jobs.ResultReceipt{}, jobs.ErrLeaseLost
	}
	phase := "completed"
	completed := int32(1)
	if state == jobs.Queued {
		phase = "queued"
		completed = 0
	}
	if _, err = appendJobEvent(ctx, tx, job, jobs.EventInput{EventID: newJobID(), Phase: phase, Completed: completed, Total: 1, Verdict: verdict, Error: input.Error}); err != nil {
		return jobs.ResultReceipt{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return jobs.ResultReceipt{}, jobError(err)
	}
	return receipt, nil
}
