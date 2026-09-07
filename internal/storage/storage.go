// Package storage owns PostgreSQL access. It is not imported by the Runner.
package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrDatabaseUnavailable = errors.New("database_unavailable")
	ErrDatabaseConfig      = errors.New("database_configuration_invalid")
	ErrMigrationMismatch   = errors.New("migration_state_mismatch")
	ErrMigrationFailed     = errors.New("migration_failed")
)

// Open creates a lazy, bounded pool. Database loss affects readiness, not liveness.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, ErrDatabaseConfig
	}
	config.MaxConns = 4
	config.MinConns = 0
	config.ConnConfig.ConnectTimeout = 2 * time.Second
	config.ConnConfig.RuntimeParams["application_name"] = "proxyloom-server"
	config.ConnConfig.Tracer = nil
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, ErrDatabaseConfig
	}
	return pool, nil
}

// Ready checks live connectivity and the entire applied migration prefix.
func Ready(ctx context.Context, pool *pgxpool.Pool) error {
	if err := pool.Ping(ctx); err != nil {
		return ErrDatabaseUnavailable
	}
	status, err := MigrationStatus(ctx, pool)
	if err != nil {
		return err
	}
	if !status.Current {
		return ErrMigrationMismatch
	}
	return nil
}
