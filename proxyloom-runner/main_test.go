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

func TestHealthcheckSkipsRunnerAndSecretConfiguration(t *testing.T) {
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" || r.Method != http.MethodGet {
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
	err := run(context.Background(), []string{"healthcheck"}, lookup,
		[]string{"PROXYLOOM_MASTER_KEY_FILE=EXAMPLE_FORBIDDEN_FILE"}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunnerServeRejectsAPIEnvironmentBeforeListening(t *testing.T) {
	err := run(context.Background(), []string{"serve"}, func(string) string { return "" },
		[]string{"PROXYLOOM_DATABASE_DSN_FILE=EXAMPLE_FORBIDDEN_FILE"}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err == nil || strings.Contains(err.Error(), "EXAMPLE_FORBIDDEN_FILE") {
		t.Fatalf("unsafe runner environment handling: %v", err)
	}
}
