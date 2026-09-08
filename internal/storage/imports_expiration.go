package storage

import (
	"context"
	"errors"

	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/jackc/pgx/v5"
)

// Expire removes retained raw input and candidates in bounded batches. Active
// jobs and a locked (submitting) batch are skipped. Commit receipts survive.
func (s *Imports) Expire(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		return 0, imports.ErrInvalidInput
	}
	rows, err := s.catalog.pool.Query(ctx, `SELECT b.scope_id,b.id FROM public.import_batches b
		WHERE b.expires_at<=clock_timestamp()
		AND (b.raw_envelope IS NOT NULL OR EXISTS(SELECT 1 FROM public.import_candidates c WHERE c.batch_id=b.id))
		AND NOT EXISTS(SELECT 1 FROM public.jobs j WHERE j.scope_id=b.scope_id AND j.id=b.job_id
			AND j.cancel_requested_at IS NULL AND (j.state='queued' OR (j.state IN ('leased','running') AND j.lease_until>clock_timestamp())))
		ORDER BY b.expires_at,b.id LIMIT $1`, limit)
	if err != nil {
		return 0, importError(err)
	}
	type expired struct{ scope, batch ir.ID }
	var ids []expired
	for rows.Next() {
		var id expired
		if err := rows.Scan(&id.scope, &id.batch); err != nil {
			rows.Close()
			return 0, importError(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, importError(err)
	}
	count := 0
	for _, id := range ids {
		changed, err := s.expireOne(ctx, id.scope, id.batch)
		if err != nil {
			return count, err
		}
		if changed {
			count++
		}
	}
	return count, nil
}

func (s *Imports) expireBatch(ctx context.Context, scope, batch ir.ID) error {
	_, err := s.expireOne(ctx, scope, batch)
	return err
}

func (s *Imports) expireOne(ctx context.Context, scope, batch ir.ID) (bool, error) {
	tx, err := s.catalog.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, imports.ErrUnavailable
	}
	defer rollbackImport(tx)
	var job ir.ID
	err = tx.QueryRow(ctx, `SELECT job_id FROM public.import_batches WHERE scope_id=$1 AND id=$2 AND expires_at<=CURRENT_TIMESTAMP AND state<>'expired'`, dbID(scope), dbID(batch)).Scan(&job)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, importError(err)
	}
	// Lock the job before the batch, exactly like CompleteTx. SKIP LOCKED avoids
	// delaying a worker; it also fences a simultaneous claim of an expired lease.
	var active bool
	err = tx.QueryRow(ctx, `SELECT cancel_requested_at IS NULL AND (state='queued' OR (state IN ('leased','running') AND lease_until>CURRENT_TIMESTAMP)) FROM public.jobs WHERE scope_id=$1 AND id=$2 FOR UPDATE SKIP LOCKED`, dbID(scope), dbID(job)).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, importError(err)
	}
	if active {
		return false, nil
	}
	var state string
	err = tx.QueryRow(ctx, `SELECT state FROM public.import_batches WHERE scope_id=$1 AND id=$2 AND expires_at<=CURRENT_TIMESTAMP AND state<>'expired' FOR UPDATE SKIP LOCKED`, dbID(scope), dbID(batch)).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, importError(err)
	}
	if _, err = tx.Exec(ctx, `DELETE FROM public.import_candidates WHERE scope_id=$1 AND batch_id=$2`, dbID(scope), dbID(batch)); err != nil {
		return false, importError(err)
	}
	_, err = tx.Exec(ctx, `UPDATE public.import_batches SET raw_envelope=NULL,raw_wrapping=NULL,state=CASE WHEN state='committed' THEN state ELSE 'expired' END,revision=revision+CASE WHEN state='committed' THEN 0 ELSE 1 END WHERE scope_id=$1 AND id=$2`, dbID(scope), dbID(batch))
	if err != nil {
		return false, importError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, importError(err)
	}
	return true, nil
}
