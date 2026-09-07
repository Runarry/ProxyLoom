package runner

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestIdleRunnerExposesOnlyHealth(t *testing.T) {
	handler := Handler()
	for _, test := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/healthz", http.StatusOK},
		{http.MethodGet, "/readyz", http.StatusOK},
		{http.MethodHead, "/healthz", http.StatusOK},
		{http.MethodPost, "/healthz", http.StatusNotFound},
		{http.MethodPost, "/internal/jobs", http.StatusNotFound},
		{http.MethodGet, "/s/EXAMPLE_TOKEN", http.StatusNotFound},
		{http.MethodGet, "/", http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status {
			t.Fatalf("%s %s status = %d", test.method, test.path, response.Code)
		}
		if test.method == http.MethodHead && response.Body.Len() != 0 {
			t.Fatal("HEAD returned body")
		}
		if test.method == http.MethodGet && test.status == http.StatusOK && !strings.Contains(response.Body.String(), `"status":"idle"`) {
			t.Fatal("runner claimed task readiness")
		}
		if strings.Contains(response.Body.String(), "EXAMPLE_TOKEN") {
			t.Fatal("unknown route echoed token")
		}
	}
}

func TestIdleRunnerStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var logs bytes.Buffer
	finished := make(chan error, 1)
	go func() { finished <- Serve(ctx, "127.0.0.1:0", slog.New(slog.NewJSONHandler(&logs, nil))) }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("graceful stop failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not stop after cancellation")
	}
	if !strings.Contains(logs.String(), "runner_stopped") || !strings.Contains(logs.String(), `"task_execution":false`) {
		t.Fatal("runner lifecycle logs missing")
	}
}
