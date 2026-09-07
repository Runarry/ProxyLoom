// Package runner is an idle lifecycle shell. Task transport, mTLS, and core
// execution belong to later tasks and are deliberately not exposed here.
package runner

import (
	"context"
	"errors"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"time"
)

var ErrServerFailed = errors.New("runner_health_server_failed")

func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || (r.URL.Path != "/healthz" && r.URL.Path != "/readyz") {
			w.WriteHeader(http.StatusNotFound)
			if r.Method != http.MethodHead {
				_, _ = io.WriteString(w, "{\"error\":{\"code\":\"NOT_FOUND\",\"message\":\"Route not found\"}}\n")
			}
			return
		}
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "{\"status\":\"idle\"}\n")
		}
	})
}

func Serve(ctx context.Context, addr string, logger *slog.Logger) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return ErrServerFailed
	}
	server := &http.Server{
		Handler: Handler(), ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second,
		IdleTimeout: 10 * time.Second, MaxHeaderBytes: 4 << 10,
		ErrorLog: log.New(io.Discard, "", 0),
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	logger.Info("runner_started", "state", "idle", "task_execution", false)
	select {
	case err := <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			return ErrServerFailed
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return ErrServerFailed
		}
		<-finished
	}
	logger.Info("runner_stopped")
	return nil
}
