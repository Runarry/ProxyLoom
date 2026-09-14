package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Runarry/ProxyLoom/internal/config"
	"github.com/Runarry/ProxyLoom/migrations"
)

var ErrDatabase = errors.New("backup_database_command_failed")
var ErrOutputExists = errors.New("backup_output_already_exists")

type FileResult struct {
	BackupID       string    `json:"backup_id"`
	CreatedAt      time.Time `json:"created_at"`
	DatabaseSchema int64     `json:"database_schema"`
	SHA256         string    `json:"sha256"`
}

// pgEnvironment keeps credentials in a private libpq password file. Database
// addresses and ordinary SSL settings may be environment values; passwords,
// master keys and the caller's environment are never forwarded to PostgreSQL.
func pgEnvironment(dsn, directory string) ([]string, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.Hostname() == "" || u.Fragment != "" || u.Opaque != "" {
		return nil, ErrDatabase
	}
	password, ok := u.User.Password()
	if !ok || password == "" {
		return nil, ErrDatabase
	}
	database := strings.TrimPrefix(u.Path, "/")
	user := u.User.Username()
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	for _, value := range []string{database, user, u.Hostname(), password} {
		if value == "" || strings.ContainsAny(value, "\r\n\x00") {
			return nil, ErrDatabase
		}
	}
	fields := []string{u.Hostname(), port, database, user, password}
	for i, value := range fields {
		fields[i] = strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), ":", `\:`)
	}
	passfile := filepath.Join(directory, "pgpass")
	if err = os.WriteFile(passfile, []byte(strings.Join(fields, ":")+"\n"), 0600); err != nil {
		return nil, ErrDatabase
	}
	env := []string{"PGHOST=" + u.Hostname(), "PGPORT=" + port, "PGUSER=" + user, "PGDATABASE=" + database, "PGPASSFILE=" + passfile, "PGCONNECT_TIMEOUT=10", "PGAPPNAME=proxyloom-maintenance", "PGCLIENTENCODING=UTF8", "LANG=C.UTF-8"}
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR", "TMPDIR", "TEMP", "TMP"} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	allowed := map[string]string{"sslmode": "PGSSLMODE", "sslrootcert": "PGSSLROOTCERT", "sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY"}
	for key, values := range u.Query() {
		name, ok := allowed[key]
		if !ok || len(values) != 1 || strings.ContainsAny(values[0], "\r\n\x00") {
			return nil, ErrDatabase
		}
		env = append(env, name+"="+values[0])
	}
	return env, nil
}

func postgresCommand(ctx context.Context, toolsDir, dsn, name string, args ...string) (*exec.Cmd, func(), error) {
	if !filepath.IsAbs(toolsDir) || (name != "pg_dump" && name != "pg_restore" && name != "psql") {
		return nil, nil, ErrDatabase
	}
	directory, err := os.MkdirTemp("", "proxyloom-pg-")
	if err != nil {
		return nil, nil, ErrDatabase
	}
	cleanup := func() { _ = os.Remove(filepath.Join(directory, "pgpass")); _ = os.Remove(directory) }
	env, err := pgEnvironment(dsn, directory)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	namePath := filepath.Join(toolsDir, name)
	if runtime.GOOS == "windows" {
		namePath += ".exe"
	}
	cmd := exec.CommandContext(ctx, namePath, args...)
	cmd.Env = env
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 5 * time.Second
	return cmd, cleanup, nil
}

// Snapshot publishes only a completed, synced encrypted dump. Hard linking the
// temporary file makes destination creation atomic without replacing a backup.
func Snapshot(ctx context.Context, toolsDir, dsn, destination, recipient string, keys config.KeyMaterial, schema int64) (FileResult, error) {
	var result FileResult
	if !filepath.IsAbs(destination) {
		return result, ErrWrite
	}
	if _, err := os.Lstat(destination); err == nil {
		return result, ErrOutputExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, ErrWrite
	}
	file, err := os.CreateTemp(filepath.Dir(destination), ".proxyloom-backup-")
	if err != nil {
		return result, ErrWrite
	}
	defer func() { file.Close(); os.Remove(file.Name()) }()
	hash := sha256.New()
	w, m, err := Encrypt(io.MultiWriter(file, hash), recipient, keys, schema)
	if err != nil {
		return result, err
	}
	cmd, cleanup, err := postgresCommand(ctx, toolsDir, dsn, "pg_dump", "--format=custom", "--schema=public", "--no-password")
	if err != nil {
		return result, err
	}
	defer cleanup()
	cmd.Stdout = w
	if err = cmd.Run(); err != nil {
		return result, ErrDatabase
	}
	if err = w.Close(); err != nil {
		return result, ErrWrite
	}
	if err = file.Sync(); err != nil {
		return result, ErrWrite
	}
	if err = file.Close(); err != nil {
		return result, ErrWrite
	}
	if err = os.Link(file.Name(), destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return result, ErrOutputExists
		}
		return result, ErrWrite
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(destination))
		if err != nil {
			return result, ErrWrite
		}
		err = dir.Sync()
		dir.Close()
		if err != nil {
			return result, ErrWrite
		}
	}
	return FileResult{BackupID: m.BackupID, CreatedAt: m.CreatedAt, DatabaseSchema: m.DatabaseSchema, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

// Restore authenticates the full archive first, then streams pg_restore SQL to
// psql. Empty-database checking, schema upgrades and authorization reset share
// one transaction, so a crash cannot commit old permissions before revocation.
func Restore(ctx context.Context, toolsDir, dsn string, encrypted io.ReadSeeker, identity string, keys config.KeyMaterial) (Manifest, error) {
	if _, err := encrypted.Seek(0, io.SeekStart); err != nil {
		return Manifest{}, ErrArchive
	}
	m, err := Verify(encrypted, identity, keys)
	if err != nil {
		return Manifest{}, err
	}
	finalize, err := restoreFinalization(m.DatabaseSchema)
	if err != nil {
		return Manifest{}, err
	}
	if _, err = encrypted.Seek(0, io.SeekStart); err != nil {
		return Manifest{}, ErrArchive
	}
	plain, _, err := Decrypt(encrypted, identity, keys)
	if err != nil {
		return Manifest{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, cleanup, err := postgresCommand(ctx, toolsDir, dsn, "pg_restore", "--clean", "--if-exists", "--no-owner", "--exit-on-error", "--no-password", "--file=-")
	if err != nil {
		return Manifest{}, err
	}
	defer cleanup()
	psql, cleanPSQL, err := postgresCommand(ctx, toolsDir, dsn, "psql", "--no-psqlrc", "--quiet", "--no-password", "--single-transaction", "--set", "ON_ERROR_STOP=1", "--file=-")
	if err != nil {
		return Manifest{}, err
	}
	defer cleanPSQL()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	psql.Stdin = reader
	psql.Stdout = io.Discard
	if err = psql.Start(); err != nil {
		return Manifest{}, ErrDatabase
	}
	done := make(chan error, 1)
	go func() { err := psql.Wait(); reader.Close(); done <- err }()
	failed := func() (Manifest, error) {
		cancel()
		writer.CloseWithError(ErrDatabase)
		<-done
		return Manifest{}, ErrDatabase
	}
	const prepare = `SELECT pg_advisory_xact_lock(5787775634612703565);
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema')
 OR EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema')
 THEN RAISE EXCEPTION 'restore_requires_empty_database'; END IF;
END; $$;
`
	if _, err = io.WriteString(writer, prepare); err != nil {
		return failed()
	}
	cmd.Stdin = plain
	cmd.Stdout = writer
	if err = cmd.Run(); err != nil {
		return failed()
	}
	if _, err = io.WriteString(writer, finalize); err != nil {
		return failed()
	}
	writer.Close()
	if err = <-done; err != nil {
		return Manifest{}, ErrDatabase
	}
	return m, nil
}

func restoreFinalization(sourceSchema int64) (string, error) {
	all, err := migrations.Load()
	if err != nil || sourceSchema < 1 || sourceSchema > int64(len(all)) {
		return "", ErrArchive
	}
	var sql strings.Builder
	fmt.Fprintf(&sql, "\nDO $$ BEGIN IF (SELECT count(*) FROM public.proxyloom_schema_migrations)<>%d THEN RAISE EXCEPTION 'restore_schema_mismatch'; END IF;\n", sourceSchema)
	for _, m := range all[:sourceSchema] {
		fmt.Fprintf(&sql, "IF NOT EXISTS(SELECT 1 FROM public.proxyloom_schema_migrations WHERE version=%d AND name='%s' AND checksum='%s') THEN RAISE EXCEPTION 'restore_migration_mismatch'; END IF;\n", m.Version, strings.ReplaceAll(m.Name, "'", "''"), m.Checksum)
	}
	sql.WriteString("END; $$;\n")
	for _, m := range all[sourceSchema:] {
		sql.WriteString(m.SQL)
		sql.WriteByte('\n')
		fmt.Fprintf(&sql, "INSERT INTO public.proxyloom_schema_migrations(version,name,checksum) VALUES(%d,'%s','%s');\n", m.Version, strings.ReplaceAll(m.Name, "'", "''"), m.Checksum)
	}
	sql.WriteString("SELECT public.proxyloom_reset_after_restore();\n")
	return sql.String(), nil
}
