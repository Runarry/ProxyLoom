package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const postgresTestSecret = "PROXYLOOM_TEST_SYNTHETIC_SECRET_7e2c3b"

type postgresTestContextKey struct{}

type postgresEnv struct {
	runtime  *pgxpool.Pool
	migrator *pgxpool.Pool
	admin    *pgxpool.Pool
	store    *Catalog
	box      *secretbox.Box
	scope    ir.ID
	ctx      context.Context
}

func newPostgres(t *testing.T, migrate bool) *postgresEnv {
	t.Helper()
	paths := []string{
		os.Getenv("PROXYLOOM_TEST_DATABASE_DSN_FILE"),
		os.Getenv("PROXYLOOM_TEST_MIGRATION_DSN_FILE"),
		os.Getenv("PROXYLOOM_TEST_ADMIN_DSN_FILE"),
	}
	missing := false
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			missing = true
		}
	}
	if missing {
		if os.Getenv("PROXYLOOM_REQUIRE_POSTGRES_TESTS") == "true" {
			t.Fatal("PostgreSQL acceptance environment is incomplete")
		}
		t.Skip("PostgreSQL acceptance skipped: test DSN files are not configured")
	}

	dsns := make([]string, len(paths))
	for i, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil || strings.TrimSpace(string(data)) == "" {
			if os.Getenv("PROXYLOOM_REQUIRE_POSTGRES_TESTS") == "true" {
				t.Fatal("PostgreSQL acceptance environment is unreadable")
			}
			t.Skip("PostgreSQL acceptance skipped: a test DSN file is unreadable")
		}
		dsns[i] = strings.TrimSpace(string(data))
		clear(data)
	}

	var suffix [10]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal("could not generate isolated PostgreSQL database name")
	}
	database := "proxyloom_test_" + hex.EncodeToString(suffix[:])
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), postgresTestContextKey{}, database), 45*time.Second)
	t.Cleanup(cancel)

	control := openTestPool(t, ctx, dsns[2], "")
	if _, err := control.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()+" OWNER proxyloom_migrator"); err != nil {
		control.Close()
		t.Fatal("could not create isolated PostgreSQL acceptance database")
	}

	env := &postgresEnv{ctx: ctx, scope: "10000000-0000-4000-8000-000000000001"}
	t.Cleanup(func() {
		if env.runtime != nil {
			env.runtime.Close()
		}
		if env.migrator != nil {
			env.migrator.Close()
		}
		if env.admin != nil {
			env.admin.Close()
		}
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dropCancel()
		if !strings.HasPrefix(database, "proxyloom_test_") || len(database) != len("proxyloom_test_")+20 {
			t.Errorf("refused to clean an invalid PostgreSQL acceptance database name")
			control.Close()
			return
		}
		if _, err := control.Exec(dropCtx, "DROP DATABASE "+pgx.Identifier{database}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("could not clean isolated PostgreSQL acceptance database")
		}
		control.Close()
	})

	env.admin = openTestPool(t, ctx, dsns[2], database)
	provision := []string{
		"GRANT CONNECT ON DATABASE " + pgx.Identifier{database}.Sanitize() + " TO proxyloom, proxyloom_migrator",
		"GRANT USAGE ON SCHEMA public TO proxyloom",
		"GRANT CREATE ON SCHEMA public TO proxyloom_migrator",
		"ALTER DEFAULT PRIVILEGES FOR ROLE proxyloom_migrator IN SCHEMA public GRANT SELECT ON TABLES TO proxyloom",
	}
	for _, statement := range provision {
		if _, err := env.admin.Exec(ctx, statement); err != nil {
			t.Fatal("could not provision isolated PostgreSQL acceptance database")
		}
	}
	env.migrator = openTestPool(t, ctx, dsns[1], database)
	env.runtime = openTestPool(t, ctx, dsns[0], database)

	master := make([]byte, secretbox.KeySize)
	content := make([]byte, secretbox.KeySize)
	for i := range master {
		master[i], content[i] = 0x11, 0x33
	}
	box, err := secretbox.New("old", map[string][]byte{"old": master}, content)
	clear(master)
	clear(content)
	if err != nil {
		t.Fatal("could not initialize PostgreSQL acceptance encryption")
	}
	env.box = box
	env.store, err = NewCatalog(env.runtime, env.box)
	if err != nil {
		t.Fatal("could not initialize PostgreSQL acceptance catalog")
	}
	if migrate {
		status, err := MigrateUp(ctx, env.migrator)
		if err != nil || !status.Current {
			t.Fatal("could not migrate isolated PostgreSQL acceptance database")
		}
		if err := env.store.EnsureScope(ctx, env.scope, "PostgreSQL acceptance"); err != nil {
			t.Fatal("could not provision PostgreSQL acceptance scope")
		}
	}
	return env
}

func openTestPool(t *testing.T, ctx context.Context, dsn, database string) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid PostgreSQL acceptance connection configuration")
	}
	if database != "" {
		config.ConnConfig.Database = database
	}
	config.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("could not initialize PostgreSQL acceptance connection")
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatal("could not connect to PostgreSQL acceptance environment")
	}
	return pool
}

func syntheticNode() *ir.Node {
	verify := true
	udp := false
	return &ir.Node{
		SchemaVersion: ir.SchemaVersion,
		Protocol:      ir.Trojan,
		Endpoint:      ir.Endpoint{Host: "synthetic.example.invalid", Port: 443},
		Auth:          &ir.PasswordAuth{Kind: ir.AuthPassword, Password: ir.Secret(postgresTestSecret)},
		Transport:     &ir.NativeTCPTransport{Kind: ir.NativeTCP},
		Security: &ir.TLSSecurity{Mode: ir.TLS, ServerName: "synthetic.example.invalid",
			VerifyCertificate: &verify, ALPN: []string{"h2", "http/1.1"}},
		Features: ir.Features{UDP: &udp}, Extensions: ir.Extensions{},
	}
}

func mustCreate(t *testing.T, env *postgresEnv, input catalog.CreateInput) ir.Resource {
	t.Helper()
	var created ir.Resource
	if err := env.store.Transact(env.ctx, env.scope, func(tx catalog.Tx) error {
		var err error
		created, err = tx.Create(env.ctx, input)
		return err
	}); err != nil {
		t.Fatal("could not create PostgreSQL acceptance resource")
	}
	return created
}
