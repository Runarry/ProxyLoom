package storage

import (
	"context"

	"github.com/Runarry/ProxyLoom/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The transaction lock serializes migration processes without a persistent lock
// row. PostgreSQL releases it on rollback, disconnect, or process death.
const migrationLockID int64 = 5787775634612703565

type appliedMigration struct {
	Version  int64
	Name     string
	Checksum string
}

// Status contains no credentials or SQL. A valid prefix may still need Up.
type Status struct {
	Applied int   `json:"applied"`
	Pending int   `json:"pending"`
	Latest  int64 `json:"latest"`
	Current bool  `json:"current"`
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readApplied(ctx context.Context, db queryer) ([]appliedMigration, error) {
	var exists bool
	if err := db.QueryRow(ctx, "SELECT to_regclass('public.proxyloom_schema_migrations') IS NOT NULL").Scan(&exists); err != nil {
		return nil, ErrDatabaseUnavailable
	}
	if !exists {
		return nil, nil
	}
	rows, err := db.Query(ctx, "SELECT version, name, checksum FROM public.proxyloom_schema_migrations ORDER BY version")
	if err != nil {
		return nil, ErrDatabaseUnavailable
	}
	defer rows.Close()
	var applied []appliedMigration
	for rows.Next() {
		var item appliedMigration
		if err := rows.Scan(&item.Version, &item.Name, &item.Checksum); err != nil {
			return nil, ErrMigrationMismatch
		}
		applied = append(applied, item)
	}
	if rows.Err() != nil {
		return nil, ErrDatabaseUnavailable
	}
	return applied, nil
}

func compareApplied(expected []migrations.Migration, applied []appliedMigration) (Status, error) {
	if len(expected) == 0 || len(applied) > len(expected) {
		return Status{}, ErrMigrationMismatch
	}
	for i, actual := range applied {
		want := expected[i]
		if actual.Version != want.Version || actual.Name != want.Name || actual.Checksum != want.Checksum {
			return Status{}, ErrMigrationMismatch
		}
	}
	return Status{Applied: len(applied), Pending: len(expected) - len(applied), Latest: expected[len(expected)-1].Version, Current: len(applied) == len(expected)}, nil
}

// MigrationStatus is read-only and works with the restricted runtime account.
func MigrationStatus(ctx context.Context, pool *pgxpool.Pool) (Status, error) {
	expected, err := migrations.Load()
	if err != nil {
		return Status{}, ErrMigrationMismatch
	}
	applied, err := readApplied(ctx, pool)
	if err != nil {
		return Status{}, err
	}
	return compareApplied(expected, applied)
}

// MigrateUp validates old checksums before applying new migrations atomically.
// It requires the separate migration role, never the API runtime role.
func MigrateUp(ctx context.Context, pool *pgxpool.Pool) (Status, error) {
	expected, err := migrations.Load()
	if err != nil {
		return Status{}, ErrMigrationMismatch
	}
	// Waiting for the advisory lock must not freeze a snapshot that predates the
	// previous migrator's commit, even if the database has another default.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite})
	if err != nil {
		return Status{}, ErrDatabaseUnavailable
	}
	defer tx.Rollback(ctx) // A committed transaction returns ErrTxClosed.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockID); err != nil {
		return Status{}, ErrMigrationFailed
	}
	applied, err := readApplied(ctx, tx)
	if err != nil {
		return Status{}, err
	}
	status, err := compareApplied(expected, applied)
	if err != nil {
		return Status{}, err
	}
	for _, migration := range expected[status.Applied:] {
		if _, err := tx.Exec(ctx, migration.SQL); err != nil {
			return Status{}, ErrMigrationFailed
		}
		if _, err := tx.Exec(ctx, "INSERT INTO public.proxyloom_schema_migrations (version, name, checksum) VALUES ($1, $2, $3)", migration.Version, migration.Name, migration.Checksum); err != nil {
			return Status{}, ErrMigrationFailed
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Status{}, ErrMigrationFailed
	}
	return Status{Applied: len(expected), Pending: 0, Latest: expected[len(expected)-1].Version, Current: true}, nil
}
