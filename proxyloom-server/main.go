package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Runarry/ProxyLoom/internal/config"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/server"
	"github.com/Runarry/ProxyLoom/internal/storage"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, logger, os.Stdout); err != nil {
		// Every error returned by this command boundary is a fixed, safe code.
		logger.Error("command_failed", "error_code", err.Error())
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, lookup config.Lookup, logger *slog.Logger, output io.Writer) error {
	if len(args) == 0 {
		args = []string{"serve"}
	}
	switch {
	case len(args) > 1 && args[0] == "admin":
		return adminCommand(ctx, args[1:], lookup, output)
	case len(args) == 1 && args[0] == "healthcheck":
		addr, err := config.HTTPAddress(lookup, ":8080")
		if err != nil {
			return err
		}
		return healthcheck(ctx, addr)
	case len(args) == 2 && args[0] == "migrate" && (args[1] == "up" || args[1] == "status"):
		return migrate(ctx, args[1], lookup, output)
	case len(args) == 1 && args[0] == "serve":
		return serve(ctx, lookup, logger)
	default:
		return errors.New("invalid_command: expected serve, migrate up, migrate status, or healthcheck")
	}
}

func serve(ctx context.Context, lookup config.Lookup, logger *slog.Logger) error {
	cfg, err := config.LoadAPI(lookup)
	if err != nil {
		return err
	}
	dsn, err := cfg.ReadDatabaseDSN()
	if err != nil {
		return err
	}
	pool, err := storage.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	setupToken, err := cfg.ReadSetupToken()
	if err != nil {
		return err
	}
	defer clear(setupToken)
	keys, err := cfg.ReadKeys()
	if err != nil {
		return err
	}
	defer keys.Clear()
	identities, err := storage.NewIdentity(pool, identity.Options{
		ScopeID: identity.DefaultScopeID, SetupToken: setupToken, TokenPepper: keys.TokenPepper,
	})
	if err != nil {
		return errors.New("identity_configuration_invalid")
	}
	handler, err := server.NewHandler(cfg.WebDir, server.Dependencies{
		Database: func(ctx context.Context) error { return storage.Ready(ctx, pool) },
		Secrets:  cfg.ValidateSecrets,
		Identity: identities, PublicURL: cfg.PublicURL, Development: cfg.Development,
		TrustedProxies: cfg.TrustedProxies(),
	}, logger)
	if err != nil {
		return err
	}
	defer handler.Close()
	if cfg.Development {
		logger.Info("development_mode", "http_loopback_public_url", true, "management_api", true)
	}
	return server.Serve(ctx, cfg.HTTPAddr, handler, logger)
}

func migrate(ctx context.Context, command string, lookup config.Lookup, output io.Writer) error {
	cfg, err := config.LoadMigration(lookup)
	if err != nil {
		return err
	}
	dsn, err := cfg.ReadDSN()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	pool, err := storage.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	var status storage.Status
	if command == "up" {
		status, err = storage.MigrateUp(ctx, pool)
	} else {
		status, err = storage.MigrationStatus(ctx, pool)
	}
	if err != nil {
		return err
	}
	if err := json.NewEncoder(output).Encode(status); err != nil {
		return errors.New("migration_status_output_failed")
	}
	return nil
}
