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

func ServeClient(ctx context.Context, addr string, client *Client, logger *slog.Logger) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return ErrServerFailed
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || (r.URL.Path != "/healthz" && r.URL.Path != "/readyz") {
			Handler().ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/readyz" && !client.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "{\"status\":\"running\"}\n")
		}
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 4 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	healthDone := make(chan error, 1)
	clientDone := make(chan error, 1)
	go func() { healthDone <- server.Serve(listener) }()
	go func() { clientDone <- client.Run(ctx) }()
	logger.Info("runner_started", "state", "running", "task_execution", true)
	var result error
	clientStopped, healthStopped := false, false
	select {
	case result = <-clientDone:
		clientStopped = true
	case err := <-healthDone:
		healthStopped = true
		if !errors.Is(err, http.ErrServerClosed) {
			result = ErrServerFailed
		}
	case <-ctx.Done():
	}
	cancel()
	// The consumer's bounded process cleanup finishes before the runner exits.
	if !clientStopped {
		result = errors.Join(result, <-clientDone)
	}
	shutdown, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		result = errors.Join(result, ErrServerFailed)
	}
	if !healthStopped {
		<-healthDone
	}
	logger.Info("runner_stopped")
	return result
}
