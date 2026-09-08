package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/Runarry/ProxyLoom/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresMigrationFreshPrefixRepeatAndRollback(t *testing.T) {
	for _, prefix := range []bool{false, true} {
		name := "fresh"
		if prefix {
			name = "bootstrap_prefix"
		}
		t.Run(name, func(t *testing.T) {
			e := newPostgres(t, false)
			items, err := migrations.Load()
			if err != nil {
				t.Fatal("cannot load migration manifest")
			}
			if prefix {
				applyBootstrap(t, e)
			}
			status, err := MigrateUp(e.ctx, e.migrator)
			if err != nil || !status.Current || status.Applied != len(items) {
				t.Fatal("migrator could not apply complete migration chain")
			}
			repeated, err := MigrateUp(e.ctx, e.migrator)
			if err != nil || repeated != status {
				t.Fatal("repeated migration changed applied state")
			}
			if err := Ready(e.ctx, e.runtime); err != nil {
				t.Fatal("runtime cannot read complete migration state")
			}
			var superuser bool
			if err := e.migrator.QueryRow(e.ctx, "SELECT rolsuper FROM pg_roles WHERE rolname = current_user").Scan(&superuser); err != nil || superuser {
				t.Fatal("migration did not execute as a restricted migration role")
			}
		})
	}
	t.Run("failed_append_rolls_back_all_new_objects", func(t *testing.T) {
		e := newPostgres(t, false)
		applyBootstrap(t, e)
		// A deliberately conflicting table makes migration 2 fail after scopes
		// was created. Both the new objects and bookkeeping must roll back.
		if _, err := e.migrator.Exec(e.ctx, "CREATE TABLE public.resources (sentinel boolean)"); err != nil {
			t.Fatal("cannot prepare isolated migration failure")
		}
		if _, err := MigrateUp(e.ctx, e.migrator); !errors.Is(err, ErrMigrationFailed) {
			t.Fatal("conflicting migration was not rejected safely")
		}
		var scopesExist bool
		if err := e.migrator.QueryRow(e.ctx, "SELECT to_regclass('public.scopes') IS NOT NULL").Scan(&scopesExist); err != nil || scopesExist {
			t.Fatal("failed migration retained partial business schema")
		}
		status, err := MigrationStatus(e.ctx, e.runtime)
		if err != nil || status.Applied != 1 || status.Current {
			t.Fatal("failed append changed bootstrap migration prefix")
		}
		if _, err := e.migrator.Exec(e.ctx, "DROP TABLE public.resources"); err != nil {
			t.Fatal("cannot remove isolated failure fixture")
		}
		if status, err = MigrateUp(e.ctx, e.migrator); err != nil || !status.Current {
			t.Fatal("migration could not retry after rollback")
		}
	})
	t.Run("runtime_cannot_create_schema", func(t *testing.T) {
		e := newPostgres(t, false)
		if _, err := MigrateUp(e.ctx, e.runtime); !errors.Is(err, ErrMigrationFailed) {
			t.Fatal("runtime unexpectedly applied a schema migration")
		}
		var exists bool
		if err := e.admin.QueryRow(e.ctx, "SELECT to_regclass('public.proxyloom_schema_migrations') IS NOT NULL").Scan(&exists); err != nil || exists {
			t.Fatal("failed runtime migration retained bookkeeping")
		}
	})
}

func applyBootstrap(t *testing.T, e *postgresEnv) {
	t.Helper()
	items, err := migrations.Load()
	if err != nil || len(items) < 3 {
		t.Fatal("business migrations missing")
	}
	tx, err := e.migrator.Begin(e.ctx)
	if err != nil {
		t.Fatal("cannot begin bootstrap transaction")
	}
	defer tx.Rollback(e.ctx)
	if _, err := tx.Exec(e.ctx, items[0].SQL); err != nil {
		t.Fatal("cannot apply bootstrap fixture")
	}
	if _, err := tx.Exec(e.ctx, "INSERT INTO public.proxyloom_schema_migrations (version, name, checksum) VALUES ($1,$2,$3)",
		items[0].Version, items[0].Name, items[0].Checksum); err != nil {
		t.Fatal("cannot record bootstrap fixture")
	}
	if err := tx.Commit(e.ctx); err != nil {
		t.Fatal("cannot commit bootstrap fixture")
	}
}

func TestPostgresCatalogDatabaseConstraintsAndPrivileges(t *testing.T) {
	e := newPostgres(t, true)
	var a, b, chain ir.Resource
	err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		var err error
		a, err = tx.Create(e.ctx, constraintNodeInput("A"))
		if err != nil {
			return err
		}
		b, err = tx.Create(e.ctx, constraintNodeInput("B"))
		if err != nil {
			return err
		}
		chain, err = tx.Create(e.ctx, constraintChainInput(a.Metadata.ResourceID, b.Metadata.ResourceID))
		return err
	})
	if err != nil {
		t.Fatal("cannot prepare resource constraint fixtures")
	}
	for _, test := range []struct {
		name string
		pool *pgxpool.Pool
		sql  string
		args []any
		code string
	}{
		{"head_required_at_commit", e.runtime, "INSERT INTO public.resources (scope_id,id,kind,name,enabled) VALUES ($1,$2,'node','missing head',true)", []any{e.scope, "d0000000-0000-4000-8000-000000000001"}, "23514"},
		{"unknown_kind", e.runtime, "INSERT INTO public.resources (scope_id,id,kind,name,enabled) VALUES ($1,$2,'source','invalid kind',true)", []any{e.scope, "d0000000-0000-4000-8000-000000000002"}, "23514"},
		{"runtime_revision_update", e.runtime, "UPDATE public.resource_revisions SET envelope=$2 WHERE resource_id=$1", []any{a.Metadata.ResourceID, []byte("synthetic")}, "42501"},
		{"owner_revision_update_trigger", e.migrator, "UPDATE public.resource_revisions SET envelope=$2 WHERE resource_id=$1", []any{a.Metadata.ResourceID, []byte("synthetic")}, "23514"},
		{"owner_revision_delete_trigger", e.migrator, "DELETE FROM public.resource_revisions WHERE resource_id=$1", []any{a.Metadata.ResourceID}, "23514"},
		{"runtime_reference_update", e.runtime, "UPDATE public.resource_refs SET target_revision=1 WHERE resource_id=$1", []any{chain.Metadata.ResourceID}, "42501"},
		{"owner_reference_update_trigger", e.migrator, "UPDATE public.resource_refs SET target_revision=1 WHERE resource_id=$1", []any{chain.Metadata.ResourceID}, "23514"},
		{"owner_reference_delete_trigger", e.migrator, "DELETE FROM public.resource_refs WHERE resource_id=$1", []any{chain.Metadata.ResourceID}, "23514"},
		{"historical_reference_append", e.runtime, "INSERT INTO public.resource_refs (scope_id,resource_id,revision,ref_path,target_resource_id,expected_kind) VALUES ($1,$2,1,'/late',$3,'node')", []any{e.scope, chain.Metadata.ResourceID, a.Metadata.ResourceID}, "23514"},
		{"runtime_hard_delete", e.runtime, "DELETE FROM public.resources WHERE id=$1", []any{a.Metadata.ResourceID}, "42501"},
		{"owner_hard_delete_trigger", e.migrator, "DELETE FROM public.resources WHERE id=$1", []any{a.Metadata.ResourceID}, "23514"},
		{"runtime_truncate", e.runtime, "TRUNCATE public.resource_revisions CASCADE", nil, "42501"},
		{"owner_truncate_trigger", e.migrator, "TRUNCATE public.resource_revisions CASCADE", nil, "23514"},
		{"runtime_disable_trigger", e.runtime, "ALTER TABLE public.resource_revisions DISABLE TRIGGER USER", nil, "42501"},
		{"metadata_without_revision", e.runtime, "UPDATE public.resources SET name='out of band' WHERE id=$1", []any{a.Metadata.ResourceID}, "23514"},
		{"tag_without_revision", e.runtime, "INSERT INTO public.resource_tags (scope_id,resource_id,tag) VALUES ($1,$2,'out of band')", []any{e.scope, a.Metadata.ResourceID}, "23514"},
		{"epoch_cannot_decrease", e.runtime, "UPDATE public.resources SET head_revision=2,security_epoch=0 WHERE id=$1", []any{a.Metadata.ResourceID}, "23514"},
		{"wrapping_requires_cas_advance", e.runtime, "UPDATE public.resource_revision_wrappings SET wrapping=$2 WHERE resource_id=$1", []any{a.Metadata.ResourceID, []byte("synthetic")}, "23514"},
		{"runtime_head_not_client_insertable", e.runtime, "INSERT INTO public.resources (scope_id,id,kind,name,enabled,head_revision) VALUES ($1,$2,'node','client head',true,1)", []any{e.scope, "d0000000-0000-4000-8000-000000000003"}, "42501"},
		{"runtime_catalog_not_client_insertable", e.runtime, "INSERT INTO public.scopes (id,name,catalog_revision) VALUES ($1,'client revision',100)", []any{"d0000000-0000-4000-8000-000000000004"}, "42501"},
		{"runtime_idempotency_claim_requires_receipt", e.runtime, "INSERT INTO public.idempotency_keys (scope_id,principal_id,route_key,key,request_hmac) VALUES ($1,$2,'test.pending','pending',$3)", []any{e.scope, a.Metadata.ResourceID, bytes.Repeat([]byte{1}, 32)}, "23514"},
	} {
		t.Run(test.name, func(t *testing.T) { assertCatalogSQLRejected(t, e.ctx, test.pool, test.code, test.sql, test.args...) })
	}
	// A deferred FK must reject a numerically advancing head without the row.
	assertCatalogSQLRejected(t, e.ctx, e.runtime, "23503",
		"UPDATE public.resources SET head_revision=2 WHERE id=$1", a.Metadata.ResourceID)
	if original, err := e.store.Head(e.ctx, e.scope, a.Metadata.ResourceID); err != nil || original.Metadata.Revision != 1 || original.Metadata.Name != "A" {
		t.Fatal("rejected bypass attempts changed the resource")
	}
}

func TestPostgresCatalogReferenceScopeKindAndFixedRevision(t *testing.T) {
	e := newPostgres(t, true)
	var a, b, chain, foreign ir.Resource
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		var err error
		a, err = tx.Create(e.ctx, constraintNodeInput("A"))
		if err != nil {
			return err
		}
		b, err = tx.Create(e.ctx, constraintNodeInput("B"))
		if err != nil {
			return err
		}
		chain, err = tx.Create(e.ctx, constraintChainInput(a.Metadata.ResourceID, b.Metadata.ResourceID))
		return err
	}); err != nil {
		t.Fatal("cannot create reference fixtures")
	}
	otherScope := ir.ID("e0000000-0000-4000-8000-000000000001")
	if err := e.store.EnsureScope(e.ctx, otherScope, "other"); err != nil {
		t.Fatal("cannot create second scope")
	}
	if err := e.store.Transact(e.ctx, otherScope, func(tx catalog.Tx) error {
		var err error
		foreign, err = tx.Create(e.ctx, constraintNodeInput("foreign"))
		return err
	}); err != nil {
		t.Fatal("cannot create foreign resource")
	}
	for _, test := range []struct {
		name     string
		target   ir.ID
		kind     string
		revision any
		valid    bool
	}{
		{"floating_valid", a.Metadata.ResourceID, "node", nil, true},
		{"fixed_valid", a.Metadata.ResourceID, "node", int64(1), true},
		{"fixed_missing_revision", a.Metadata.ResourceID, "node", int64(2), false},
		{"fixed_wrong_scope", foreign.Metadata.ResourceID, "node", int64(1), false},
		{"floating_wrong_scope", foreign.Metadata.ResourceID, "node", nil, false},
		{"floating_wrong_kind", chain.Metadata.ResourceID, "node", nil, false},
		{"fixed_wrong_kind", chain.Metadata.ResourceID, "node", int64(1), false},
		{"floating_missing_target", "e0000000-0000-4000-8000-000000000002", "node", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			tx, err := e.runtime.Begin(e.ctx)
			if err != nil {
				t.Fatal("cannot begin constraint transaction")
			}
			defer tx.Rollback(e.ctx)
			q := dbgen.New(tx)
			if _, err := q.LockScope(e.ctx, dbID(e.scope)); err != nil {
				t.Fatal("cannot lock scope")
			}
			bound := &catalogTx{store: e.store, q: q, scope: e.scope, active: true}
			source, err := bound.Create(e.ctx, constraintChainInput(a.Metadata.ResourceID, b.Metadata.ResourceID))
			if err != nil {
				t.Fatal("cannot prepare fresh reference source")
			}
			_, err = tx.Exec(e.ctx, "INSERT INTO public.resource_refs (scope_id,resource_id,revision,ref_path,target_resource_id,target_revision,expected_kind) VALUES ($1,$2,1,'/database-probe',$3,$4,$5)",
				e.scope, source.Metadata.ResourceID, test.target, test.revision, test.kind)
			if !test.valid {
				assertCatalogSQLState(t, err, "23503")
				return
			}
			if err != nil {
				t.Fatal("valid floating/fixed reference rejected")
			}
			if err := q.AdvanceCatalog(e.ctx, dbID(e.scope)); err != nil {
				t.Fatal("cannot advance fixture catalog")
			}
			if _, err := tx.Exec(e.ctx, "SET CONSTRAINTS ALL IMMEDIATE"); err != nil {
				t.Fatal("valid reference failed deferred constraints")
			}
			// This raw database probe deliberately has an extra reference that is
			// not an IR field; roll it back after proving relational constraints.
		})
	}
}

func constraintNodeInput(name string) catalog.CreateInput {
	return catalog.CreateInput{Name: name, Tags: []string{"fixture"}, Enabled: true, Payload: &ir.Node{
		SchemaVersion: ir.SchemaVersion, Protocol: ir.SOCKS5,
		Endpoint: ir.Endpoint{Host: "node.example", Port: 1080},
		Auth:     &ir.NoAuth{Kind: ir.AuthNone}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP},
		Security: &ir.NoSecurity{Mode: ir.SecurityNone}, Features: ir.Features{}, Extensions: ir.Extensions{},
	}}
}

func constraintChainInput(a, b ir.ID) catalog.CreateInput {
	return catalog.CreateInput{Name: "chain", Tags: []string{}, Enabled: true,
		Payload: &ir.Chain{SchemaVersion: ir.SchemaVersion, Hops: []ir.NodeRef{{NodeID: a}, {NodeID: b}}, FailurePolicy: ir.FailClosed}}
}

func assertCatalogSQLRejected(t *testing.T, ctx context.Context, pool *pgxpool.Pool, code, query string, args ...any) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal("cannot begin database constraint check")
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, query, args...); err == nil {
		err = tx.Commit(ctx)
	}
	assertCatalogSQLState(t, err, code)
}

func assertCatalogSQLState(t *testing.T, err error, code string) {
	t.Helper()
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) {
		if errors.Is(err, pgx.ErrTxClosed) {
			t.Fatal("constraint probe used a closed transaction")
		}
		t.Fatal("database did not return the expected constraint rejection")
	}
	if pgerr.Code != code {
		t.Fatalf("unexpected SQL state: got %s want %s", pgerr.Code, code)
	}
}
