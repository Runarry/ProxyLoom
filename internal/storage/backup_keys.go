package storage

import (
	"context"
	"github.com/jackc/pgx/v5/pgxpool"
	"sort"
	"time"
)

// LockBackup prevents a migration from changing the dump's schema after its
// version was read. Ordinary application writes continue under pg_dump MVCC.
func LockBackup(ctx context.Context, pool *pgxpool.Pool) (func(), error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, ErrDatabaseUnavailable
	}
	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock_shared($1)`, migrationLockID); err != nil {
		conn.Release()
		return nil, ErrDatabaseUnavailable
	}
	return func() {
		// Closing this dedicated connection always releases its session lock,
		// even when the caller's context was canceled during the backup.
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Conn().Close(closeCtx)
		conn.Release()
	}, nil
}

func RequiredMasterKeyIDs(ctx context.Context, pool *pgxpool.Pool, version int) ([]string, error) {
	queries := []struct {
		since int
		sql   string
	}{
		{2, `SELECT DISTINCT convert_from(wrapping,'UTF8')::jsonb->>'key_id' FROM public.resource_revision_wrappings`},
		{5, `SELECT DISTINCT wrapping->>'key_id' FROM public.job_payload_wrappings`},
		{6, `SELECT DISTINCT raw_wrapping->>'key_id' FROM public.import_batches WHERE raw_wrapping IS NOT NULL`},
		{6, `SELECT DISTINCT wrapping->>'key_id' FROM public.import_candidates`},
		{9, `SELECT DISTINCT wrapping->>'key_id' FROM public.source_snapshots`},
		{9, `SELECT DISTINCT wrapping->>'key_id' FROM public.source_items`},
		{10, `SELECT DISTINCT wrapping->>'key_id' FROM public.node_bindings WHERE wrapping IS NOT NULL`},
		{12, `SELECT DISTINCT applied_wrapping->>'key_id' FROM public.source_items WHERE applied_wrapping IS NOT NULL`},
		{15, `SELECT DISTINCT wrapping->>'key_id' FROM public.compile_batch_wrappings`},
		{15, `SELECT DISTINCT wrapping->>'key_id' FROM public.compile_output_wrappings`},
	}
	seen := map[string]bool{}
	for _, query := range queries {
		if version < query.since {
			continue
		}
		rows, err := pool.Query(ctx, query.sql)
		if err != nil {
			return nil, ErrDatabaseUnavailable
		}
		for rows.Next() {
			var id string
			if rows.Scan(&id) != nil || id == "" || len(id) > 64 {
				rows.Close()
				return nil, ErrDatabaseUnavailable
			}
			seen[id] = true
		}
		rows.Close()
		if rows.Err() != nil {
			return nil, ErrDatabaseUnavailable
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
