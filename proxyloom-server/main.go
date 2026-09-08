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

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/config"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnercontrol"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
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
	box, err := secretbox.New(keys.ActiveKeyID, keys.MasterKeys, keys.ContentHMACKey)
	if err != nil {
		return errors.New("catalog_key_configuration_invalid")
	}
	catalogStore, err := storage.NewCatalog(pool, box)
	if err != nil {
		return errors.New("catalog_configuration_invalid")
	}
	jobStore, err := storage.NewJobs(pool, box)
	if err != nil {
		return errors.New("job_configuration_invalid")
	}
	importStore, err := storage.NewImports(catalogStore, jobStore)
	if err != nil {
		return errors.New("import_configuration_invalid")
	}
	cursor, err := apicontract.NewCursorCodec(jobStore)
	if err != nil {
		return errors.New("cursor_configuration_invalid")
	}
	worker, err := jobs.NewWorker(jobStore, jobs.WorkerConfig{
		WorkerID: jobs.NewID(), Handlers: map[jobs.Type]jobs.Handler{jobs.ImportParse: importStore.HandleParse},
	})
	if err != nil {
		return errors.New("worker_configuration_invalid")
	}
	internalConfig, err := config.LoadRunnerListener(lookup)
	if err != nil {
		return err
	}
	var internalListener *runnercontrol.Server
	if internalConfig.Address != "" {
		registered, err := runnercontrol.ReadRegistry(internalConfig.RegistryFile)
		if err != nil {
			return errors.New("runner_registration_invalid")
		}
		tlsConfig, err := internalConfig.TLS()
		if err != nil {
			return errors.New("runner_tls_invalid")
		}
		internalListener, err = runnercontrol.New(runnercontrol.Config{TLS: tlsConfig, Registrations: registered, Jobs: jobStore})
		if err != nil {
			return errors.New("runner_listener_configuration_invalid")
		}
	}
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
		Nodes:          &server.NodeDependencies{Repository: catalogStore, Cursor: cursor},
		Imports:        importStore, Jobs: jobStore, JobCursor: cursor,
	}, logger)
	if err != nil {
		return err
	}
	defer handler.Close()
	if cfg.Development {
		logger.Info("development_mode", "http_loopback_public_url", true, "management_api", true)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan error, 4)
	count := 3
	go func() { finished <- server.Serve(runCtx, cfg.HTTPAddr, handler, logger) }()
	go func() { finished <- worker.Run(runCtx) }()
	go func() { finished <- expireImports(runCtx, importStore, logger) }()
	if internalListener != nil {
		count++
		go func() { finished <- internalListener.Serve(runCtx, internalConfig.Address, logger) }()
	}
	err = <-finished
	cancel()
	for remaining := count - 1; remaining > 0; remaining-- {
		other := <-finished
		if err == nil || errors.Is(err, context.Canceled) {
			err = other
		}
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return nil
	}
	if err != nil {
		return errors.New("api_background_service_failed")
	}
	return nil
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
