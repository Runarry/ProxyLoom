package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/storage"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Exercise the actual controlled-host command against its own database. It
// does not reuse the verifier's base database or any development volume.
func TestPostgresIdentityAdminResetCommand(t *testing.T) {
	names := []string{"PROXYLOOM_TEST_ADMIN_DSN_FILE", "PROXYLOOM_TEST_MIGRATION_DSN_FILE", "PROXYLOOM_TEST_DATABASE_DSN_FILE"}
	configs := make([]*pgxpool.Config, len(names))
	for i, name := range names {
		path := os.Getenv(name)
		if path == "" {
			if os.Getenv("PROXYLOOM_REQUIRE_POSTGRES_TESTS") == "true" {
				t.Fatal("required command database fixture missing")
			}
			t.Skip("PostgreSQL command fixture not configured")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("command fixture file unavailable")
		}
		configs[i], err = pgxpool.ParseConfig(strings.TrimSpace(string(data)))
		clear(data)
		if err != nil {
			t.Fatal("command fixture DSN invalid")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	control, err := pgxpool.NewWithConfig(ctx, configs[0])
	if err != nil {
		t.Fatal("command fixture admin unavailable")
	}
	defer control.Close()
	var nonce [10]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal("command fixture random unavailable")
	}
	database := "proxyloom_admin_test_" + hex.EncodeToString(nonce[:])
	if _, err := control.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()+" OWNER proxyloom_migrator"); err != nil {
		t.Fatal("command fixture database creation failed")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		if _, err := control.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{database}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error("command fixture database cleanup failed")
		}
	}()
	for _, cfg := range configs[1:] {
		cfg.ConnConfig.Database = database
	}
	migrator, err := pgxpool.NewWithConfig(ctx, configs[1])
	if err != nil {
		t.Fatal("command fixture migrator unavailable")
	}
	defer migrator.Close()
	if _, err := storage.MigrateUp(ctx, migrator); err != nil {
		t.Fatal("command fixture migration failed")
	}
	runtime, err := pgxpool.NewWithConfig(ctx, configs[2])
	if err != nil {
		t.Fatal("command fixture runtime unavailable")
	}
	defer runtime.Close()
	pepper := make([]byte, 32)
	setupBytes := make([]byte, 32)
	if _, err := rand.Read(pepper); err != nil {
		t.Fatal("command fixture random unavailable")
	}
	if _, err := rand.Read(setupBytes); err != nil {
		t.Fatal("command fixture random unavailable")
	}
	setup := base64.RawURLEncoding.EncodeToString(setupBytes)
	service, err := storage.NewIdentity(runtime, identity.Options{TokenPepper: pepper, SetupToken: []byte(setup)})
	if err != nil {
		t.Fatal("command fixture identity unavailable")
	}
	source := identity.Source{IP: "127.0.0.1", RequestID: "command-acceptance"}
	initial := "EXAMPLE_COMMAND_INITIAL_PASSWORD"
	replacement := "EXAMPLE_COMMAND_NEW_PASSWORD"
	session, err := service.Setup(ctx, setup, "admin", initial, source)
	if err != nil {
		t.Fatal("command fixture setup failed")
	}
	directory := t.TempDir()
	// ConnString retains pgx's original parsed string, even after changing its
	// Database field for NewWithConfig. Serialize this test's database explicitly.
	dsnURL, err := url.Parse(configs[2].ConnString())
	if err != nil || (dsnURL.Scheme != "postgres" && dsnURL.Scheme != "postgresql") {
		t.Fatal("command fixture requires its generated PostgreSQL URL")
	}
	dsnURL.Path = "/" + database
	files := map[string][]byte{"dsn": []byte(dsnURL.String()), "pepper": pepper, "password": []byte(replacement + "\n")}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(directory, name), data, 0600); err != nil {
			t.Fatal("command fixture secret write failed")
		}
	}
	lookup := func(name string) string {
		switch name {
		case "PROXYLOOM_DATABASE_DSN_FILE":
			return filepath.Join(directory, "dsn")
		case "PROXYLOOM_TOKEN_PEPPER_FILE":
			return filepath.Join(directory, "pepper")
		}
		t.Error("reset command read unrelated configuration")
		return ""
	}
	var output bytes.Buffer
	if err := adminCommand(ctx, []string{"reset-password", "--username", "admin", "--password-file", filepath.Join(directory, "password")}, lookup, &output); err != nil {
		t.Fatal("actual reset command failed:", err)
	}
	if _, err := service.Authenticate(ctx, session.ID); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("command did not revoke old session")
	}
	if _, err := service.Login(ctx, "admin", initial, "", source); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("old password survived reset")
	}
	if _, err := service.Login(ctx, "admin", replacement, "", source); err != nil {
		t.Fatal("replacement password did not authenticate")
	}
	if _, err := service.Setup(ctx, setup, "admin", initial, source); !errors.Is(err, identity.ErrSetupComplete) {
		t.Fatal("reset reopened initialization")
	}
	for _, secret := range []string{initial, replacement, setup, session.ID, session.CSRFToken} {
		if strings.Contains(output.String(), secret) {
			t.Fatal("command output exposed a secret")
		}
	}
}
