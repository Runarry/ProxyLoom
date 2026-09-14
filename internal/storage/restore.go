package storage

import (
	"context"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ControlAuthorizationEpoch(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var epoch string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(epoch::text,'') FROM public.control_authorization WHERE singleton`).Scan(&epoch); err != nil {
		return "", ErrDatabaseUnavailable
	}
	return epoch, nil
}

// ResetRestoredAuthorization requires the migration owner, on an isolated
// restored database. Every call generates a fresh epoch, even for the same dump.
func ResetRestoredAuthorization(ctx context.Context, pool *pgxpool.Pool) (ir.ID, error) {
	var epoch ir.ID
	if err := pool.QueryRow(ctx, `SELECT public.proxyloom_reset_after_restore()::text`).Scan(&epoch); err != nil {
		return "", ErrDatabaseUnavailable
	}
	return epoch, nil
}
