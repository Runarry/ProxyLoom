package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func secretFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sensitive-file-name")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func apiEnvironment(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"PROXYLOOM_PUBLIC_URL":            "https://admin.example.test",
		"PROXYLOOM_DATABASE_DSN_FILE":     secretFile(t, []byte("postgres://synthetic:fixture@db/proxyloom\n")),
		"PROXYLOOM_MASTER_KEY_FILE":       secretFile(t, bytes.Repeat([]byte{1}, 32)),
		"PROXYLOOM_MASTER_KEY_ID":         "test-master-v1",
		"PROXYLOOM_TOKEN_PEPPER_FILE":     secretFile(t, bytes.Repeat([]byte{2}, 32)),
		"PROXYLOOM_CONTENT_HMAC_KEY_FILE": secretFile(t, bytes.Repeat([]byte{3}, 32)),
	}
}

func lookup(env map[string]string) Lookup { return func(name string) string { return env[name] } }

func TestAPIValidAndMissingSecrets(t *testing.T) {
	env := apiEnvironment(t)
	c, err := LoadAPI(lookup(env))
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTPAddr != ":8080" || c.WebDir != "/app/web" || c.Development {
		t.Fatalf("unexpected defaults: %#v", c)
	}
	dsn, err := c.ReadDatabaseDSN()
	if err != nil || dsn != "postgres://synthetic:fixture@db/proxyloom" {
		t.Fatal("DSN not read and trimmed")
	}
	for _, name := range []string{"PROXYLOOM_DATABASE_DSN_FILE", "PROXYLOOM_MASTER_KEY_FILE", "PROXYLOOM_TOKEN_PEPPER_FILE", "PROXYLOOM_CONTENT_HMAC_KEY_FILE"} {
		t.Run(name, func(t *testing.T) {
			copyEnv := apiEnvironment(t)
			delete(copyEnv, name)
			copyEnv[strings.TrimSuffix(name, "_FILE")] = "raw-value-must-not-be-used"
			if got, err := LoadAPI(lookup(copyEnv)); err == nil || got != (API{}) {
				t.Fatal("must fail closed without a file")
			}
		})
	}
}

func TestSecretValidation(t *testing.T) {
	for _, tc := range []struct {
		name, variable                string
		data                          []byte
		directory, duplicate, missing bool
	}{
		{name: "empty_dsn", variable: "PROXYLOOM_DATABASE_DSN_FILE", data: []byte(" \n")},
		{name: "large_dsn", variable: "PROXYLOOM_DATABASE_DSN_FILE", data: bytes.Repeat([]byte{'x'}, 65537)},
		{name: "short_key", variable: "PROXYLOOM_MASTER_KEY_FILE", data: bytes.Repeat([]byte{4}, 31)},
		{name: "long_key", variable: "PROXYLOOM_TOKEN_PEPPER_FILE", data: bytes.Repeat([]byte{4}, 33)},
		{name: "hex_is_not_raw", variable: "PROXYLOOM_CONTENT_HMAC_KEY_FILE", data: bytes.Repeat([]byte{'a'}, 64)},
		{name: "duplicate", variable: "PROXYLOOM_CONTENT_HMAC_KEY_FILE", duplicate: true},
		{name: "directory", variable: "PROXYLOOM_MASTER_KEY_FILE", directory: true},
		{name: "missing", variable: "PROXYLOOM_MASTER_KEY_FILE", missing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := apiEnvironment(t)
			path := secretFile(t, tc.data)
			if tc.directory {
				path = t.TempDir()
			}
			if tc.missing {
				path = filepath.Join(t.TempDir(), "secret-path-never-log")
			}
			if tc.duplicate {
				path = secretFile(t, bytes.Repeat([]byte{1}, 32))
			}
			env[tc.variable] = path
			_, err := LoadAPI(lookup(env))
			if err == nil {
				t.Fatal("invalid secret accepted")
			}
			if strings.Contains(err.Error(), path) || !strings.HasPrefix(err.Error(), tc.variable+": ") {
				t.Fatalf("unsafe or unexpected error: %s", err)
			}
		})
	}
}

func TestPublicURLPolicy(t *testing.T) {
	for _, tc := range []struct {
		url, dev string
		valid    bool
	}{
		{"https://admin.example.test/base", "", true},
		{"http://localhost:8080", "true", true},
		{"http://127.0.0.2:8080", "true", true},
		{"http://[::1]:8080", "true", true},
		{"http://localhost", "", false},
		{"http://example.test", "true", false},
		{"http://192.168.1.2", "true", false},
		{"https://user:secret@example.test", "", false},
		{"https://example.test/?token=secret", "", false},
		{"https://example.test/?", "", false},
		{"https://example.test/#", "", false},
		{"https://example.test:99999", "", false},
		{"https://example.test:", "", false},
		{"https://bad_host", "", false},
		{"https://[example.test]", "", false},
		{"https://::1", "", false},
		{"", "", false},
		{"https://example.test", "yes", false},
	} {
		t.Run(tc.url+tc.dev, func(t *testing.T) {
			env := apiEnvironment(t)
			env["PROXYLOOM_PUBLIC_URL"], env["PROXYLOOM_DEV_MODE"] = tc.url, tc.dev
			_, err := LoadAPI(lookup(env))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "example.test")) {
				t.Fatal("URL leaked")
			}
		})
	}
}

func TestRunnerIsolation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   []string
		valid bool
	}{
		{"empty", nil, true},
		{"os", []string{"PATH=synthetic", "HOME=synthetic"}, true},
		{"ipv6", []string{"PROXYLOOM_HTTP_ADDR=[::1]:9092"}, true},
		{"db", []string{"PROXYLOOM_DATABASE_DSN_FILE=secret"}, false},
		{"key", []string{"PROXYLOOM_MASTER_KEY_FILE=secret"}, false},
		{"empty_api_variable", []string{"PROXYLOOM_PUBLIC_URL="}, false},
		{"unknown", []string{"PROXYLOOM_sensitive_name=secret"}, false},
		{"case", []string{"proxyloom_MASTER_KEY_FILE=secret"}, false},
		{"public", []string{"PROXYLOOM_HTTP_ADDR=0.0.0.0:9092"}, false},
		{"port", []string{"PROXYLOOM_HTTP_ADDR=127.0.0.1:8080"}, false},
		{"localhost", []string{"PROXYLOOM_HTTP_ADDR=localhost:9092"}, false},
		{"duplicate", []string{"PROXYLOOM_HTTP_ADDR=127.0.0.1:9092", "PROXYLOOM_HTTP_ADDR=127.0.0.1:9092"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadRunner(tc.env)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "sensitive_name")) {
				t.Fatal("input leaked")
			}
		})
	}
}

func TestMigrationIndependentAndRechecksFile(t *testing.T) {
	path := secretFile(t, []byte("synthetic-dsn"))
	c, err := LoadMigration(lookup(map[string]string{"PROXYLOOM_MIGRATION_DSN_FILE": path, "PROXYLOOM_PUBLIC_URL": "invalid"}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadDSN(); err == nil {
		t.Fatal("read must revalidate changed file")
	}
	if _, err := LoadMigration(lookup(map[string]string{"PROXYLOOM_DATABASE_DSN_FILE": path})); err == nil {
		t.Fatal("migration must require its own file")
	}
}

func TestHTTPAddressIndependent(t *testing.T) {
	for _, addr := range []string{":8080", "127.0.0.1:1", "[::1]:65535", "localhost:80"} {
		if _, err := HTTPAddress(lookup(map[string]string{"PROXYLOOM_HTTP_ADDR": addr}), ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, addr := range []string{"evil.example:8080", ":0", ":65536", ":+80", "http://localhost:80", "127.0.0.1"} {
		if _, err := HTTPAddress(lookup(map[string]string{"PROXYLOOM_HTTP_ADDR": addr}), ""); err == nil {
			t.Fatalf("accepted %q", addr)
		}
	}
}
