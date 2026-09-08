package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Runarry/ProxyLoom/internal/config"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/runner"
	coreexec "github.com/Runarry/ProxyLoom/internal/runner/exec"
)

func main() {
	if handled, code := coreexec.SandboxEntrypoint(os.Args[1:]); handled {
		os.Exit(code)
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Environ(), logger); err != nil {
		logger.Error("command_failed", "error_code", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, lookup config.Lookup, environ []string, logger *slog.Logger) error {
	if len(args) == 0 {
		args = []string{"serve"}
	}
	if len(args) == 1 && args[0] == "healthcheck" {
		addr, err := config.HTTPAddress(lookup, "127.0.0.1:9092")
		if err != nil {
			return err
		}
		return healthcheck(ctx, addr)
	}
	if len(args) != 1 || args[0] != "serve" {
		return errors.New("invalid_command: expected serve or healthcheck")
	}
	configured := false
	for _, value := range environ {
		name, _, _ := strings.Cut(value, "=")
		if strings.HasPrefix(strings.ToUpper(name), "PROXYLOOM_RUNNER_") {
			configured = true
		}
	}
	if configured {
		cfg, err := config.LoadRunnerTransport(environ)
		if err != nil {
			return err
		}
		tlsConfig, err := cfg.TLS()
		if err != nil {
			return err
		}
		client, err := runner.NewClient(runner.ClientConfig{RunnerID: ir.ID(cfg.RunnerID), APIURL: cfg.APIURL, TLS: tlsConfig, CoreRoot: cfg.CoreRoot})
		if err != nil {
			return err
		}
		return runner.ServeClient(ctx, cfg.HTTPAddr, client, logger)
	}
	cfg, err := config.LoadRunner(environ)
	if err != nil {
		return err
	}
	return runner.Serve(ctx, cfg.HTTPAddr, logger)
}
