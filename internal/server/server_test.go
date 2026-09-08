package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
)

func testHandler(t *testing.T, dependencies Dependencies, output io.Writer) (*Handler, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"index.html": "<!doctype html><title>ProxyLoom shell</title>", "assets/app.js": "console.log('shell')", ".env": "EXAMPLE_HIDDEN_FILE"} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := NewHandler(root, dependencies, slog.New(slog.NewJSONHandler(output, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handler.Close() })
	return handler, root
}

func healthyDependencies() Dependencies {
	return Dependencies{Database: func(context.Context) error { return nil }, Secrets: func() error { return nil }}
}

func request(handler http.Handler, method, target, accept string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	r := httptest.NewRequest(method, target, nil)
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	handler.ServeHTTP(response, r)
	return response
}

func TestLivenessAndReadinessTrackCurrentDependencies(t *testing.T) {
	var logs bytes.Buffer
	var dbErr, keyErr error
	dbCalls := 0
	handler, _ := testHandler(t, Dependencies{
		Database: func(ctx context.Context) error {
			dbCalls++
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > ReadinessTimeout {
				t.Error("readiness did not bound the database operation")
			}
			return dbErr
		},
		Secrets: func() error { return keyErr },
	}, &logs)
	for _, scenario := range []struct {
		name string
		db   error
		key  error
		code int
	}{
		{"ready", nil, nil, http.StatusOK},
		{"database_lost", errors.New("EXAMPLE_PRIVATE_DSN"), nil, http.StatusServiceUnavailable},
		{"database_restored", nil, nil, http.StatusOK},
		{"secret_removed", nil, errors.New("EXAMPLE_PRIVATE_KEY"), http.StatusServiceUnavailable},
		{"secret_restored", nil, nil, http.StatusOK},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dbErr, keyErr = scenario.db, scenario.key
			before := dbCalls
			live := request(handler, http.MethodGet, "/healthz", "")
			if live.Code != http.StatusOK || dbCalls != before {
				t.Fatalf("liveness depends on database: %d", live.Code)
			}
			ready := request(handler, http.MethodGet, "/readyz", "")
			if ready.Code != scenario.code {
				t.Fatalf("readiness status = %d, want %d", ready.Code, scenario.code)
			}
			if strings.Contains(ready.Body.String(), "EXAMPLE_PRIVATE") {
				t.Fatal("dependency details leaked to response")
			}
		})
	}
	if strings.Contains(logs.String(), "EXAMPLE_PRIVATE") {
		t.Fatal("dependency details leaked to logs")
	}
}

func TestReadinessTimesOut(t *testing.T) {
	handler, _ := testHandler(t, Dependencies{
		Database: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		Secrets:  func() error { return nil },
	}, io.Discard)
	started := time.Now()
	response := request(handler, http.MethodGet, "/readyz", "")
	if response.Code != http.StatusServiceUnavailable || time.Since(started) > ReadinessTimeout+time.Second {
		t.Fatalf("readiness did not fail in time: status=%d duration=%s", response.Code, time.Since(started))
	}
}

func TestStaticRoutingDoesNotMaskServicePaths(t *testing.T) {
	handler, _ := testHandler(t, healthyDependencies(), io.Discard)
	for _, target := range []string{
		"/api", "/api/unknown", "/API/unknown", "/internal/unknown", "/s/EXAMPLE_TOKEN",
		"/s", "/readyz/unknown", "/healthz/unknown", "/assets/missing.js", "/assets/missing",
		"/missing.js", "/.env", "/assets/../index.html", "/assets/%2e%2e/index.html", "/assets%5c..%5c.env", "//api/unknown",
	} {
		t.Run(target, func(t *testing.T) {
			response := request(handler, http.MethodGet, target, "text/html")
			if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "<title>") {
				t.Fatalf("reserved or unsafe route reached SPA: %d %s", response.Code, response.Body)
			}
		})
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions} {
		if response := request(handler, method, "/", "text/html"); response.Code != http.StatusNotFound {
			t.Fatalf("%s unexpectedly reached SPA: %d", method, response.Code)
		}
	}
	if response := request(handler, http.MethodGet, "/unknown", "application/json"); response.Code != http.StatusNotFound {
		t.Fatalf("JSON caller reached SPA: %d", response.Code)
	}
	for _, target := range []string{"/", "/settings", "/assets/app.js"} {
		response := request(handler, http.MethodGet, target, "text/html")
		if response.Code != http.StatusOK || response.Body.Len() == 0 || response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("static response invalid for %s: %d %s", target, response.Code, response.Body)
		}
	}
	for _, target := range []string{"/", "/settings", "/assets/app.js", "/healthz", "/readyz", "/api/missing"} {
		if response := request(handler, http.MethodHead, target, "text/html"); response.Body.Len() != 0 {
			t.Errorf("HEAD %s returned a body", target)
		}
	}
}

func TestErrorRequestIDsAreServerOwnedAndMatchLogs(t *testing.T) {
	var logs bytes.Buffer
	handler, _ := testHandler(t, healthyDependencies(), &logs)
	seen := map[string]bool{}
	for range 2 {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/nodes", nil)
		req.Header.Set("X-Request-ID", "EXAMPLE_CLIENT_SECRET_ID")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		var body apicontract.ErrorResponse
		if response.Code != http.StatusNotFound || json.Unmarshal(response.Body.Bytes(), &body) != nil {
			t.Fatal("unimplemented route did not return a structured error")
		}
		id := response.Header().Get("X-Request-ID")
		if len(id) != 32 || body.RequestID != id || seen[id] || body.Error.Code != apicontract.ResourceNotFound {
			t.Fatal("request identity or error contract is inconsistent")
		}
		seen[id] = true
		if !strings.Contains(logs.String(), id) || strings.Contains(logs.String()+response.Body.String(), "EXAMPLE_CLIENT_SECRET_ID") {
			t.Fatal("request ID was not correlated safely")
		}
	}
}

func TestStaticRootRejectsSymlinkEscape(t *testing.T) {
	handler, root := testHandler(t, healthyDependencies(), io.Discard)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("EXAMPLE_OUTSIDE_ROOT"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.txt")); err != nil {
		t.Skip("symlink creation unavailable on this host")
	}
	response := request(handler, http.MethodGet, "/escape.txt", "text/html")
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "EXAMPLE_OUTSIDE_ROOT") {
		t.Fatal("static serving escaped the configured root")
	}
}

func TestSafeLogsExcludeRequestAndPanicSecrets(t *testing.T) {
	var logs bytes.Buffer
	handler, _ := testHandler(t, Dependencies{
		Database: func(context.Context) error { panic("EXAMPLE_PANIC_SECRET") },
		Secrets:  func() error { return nil },
	}, &logs)
	r := httptest.NewRequest("EXAMPLE_SECRET_METHOD", "/s/EXAMPLE_PATH_SECRET?token=EXAMPLE_QUERY_SECRET", nil)
	r.Header.Set("Authorization", "Bearer EXAMPLE_HEADER_SECRET")
	r.Header.Set("Cookie", "session=EXAMPLE_COOKIE_SECRET")
	handler.ServeHTTP(httptest.NewRecorder(), r)
	response := request(handler, http.MethodGet, "/readyz", "")
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("panic was not recovered: %d", response.Code)
	}
	head := request(handler, http.MethodHead, "/readyz", "")
	if head.Code != http.StatusInternalServerError || head.Body.Len() != 0 {
		t.Fatal("HEAD panic response violated the response contract")
	}
	if strings.Contains(logs.String(), "EXAMPLE_") || strings.Contains(response.Body.String(), "EXAMPLE_") {
		t.Fatal("request or panic secret leaked")
	}
	if !strings.Contains(logs.String(), "http_panic") || !strings.Contains(logs.String(), "unmatched") {
		t.Fatal("safe diagnostic events missing")
	}
}

func TestMissingWebAssetsFailStartup(t *testing.T) {
	_, err := NewHandler(t.TempDir(), healthyDependencies(), slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if !errors.Is(err, ErrWebAssetsUnavailable) {
		t.Fatalf("missing index accepted: %v", err)
	}
}
