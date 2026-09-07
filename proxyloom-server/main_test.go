package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHealthcheckDoesNotLoadSecrets(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" || r.Method != http.MethodGet {
			t.Errorf("wrong health endpoint: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer probe.Close()
	lookup := func(name string) string {
		if name != "PROXYLOOM_HTTP_ADDR" {
			t.Fatalf("healthcheck read secret configuration %s", name)
		}
		return strings.TrimPrefix(probe.URL, "http://")
	}
	if err := run(context.Background(), []string{"healthcheck"}, lookup, slog.New(slog.NewJSONHandler(io.Discard, nil)), io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestHealthcheckRejectsUnavailableAndRedirects(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusFound} {
		probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "/healthz")
			w.WriteHeader(status)
		}))
		err := healthcheck(context.Background(), strings.TrimPrefix(probe.URL, "http://"))
		probe.Close()
		if err == nil {
			t.Fatalf("healthcheck accepted status %d", status)
		}
	}
}

func TestInvalidCommandDoesNotEchoArguments(t *testing.T) {
	err := run(context.Background(), []string{"EXAMPLE_SECRET_ARGUMENT"}, func(string) string { return "" }, slog.New(slog.NewJSONHandler(io.Discard, nil)), io.Discard)
	if err == nil || strings.Contains(err.Error(), "EXAMPLE_SECRET_ARGUMENT") {
		t.Fatalf("unsafe invalid-command error: %v", err)
	}
}
