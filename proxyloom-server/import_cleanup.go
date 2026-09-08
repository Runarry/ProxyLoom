package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/Runarry/ProxyLoom/internal/storage"
)

// Expiry is a bounded database sweep, independent from request or browser life.
// The repository skips valid leases and submissions while holding row locks.
func expireImports(ctx context.Context, repository *storage.Imports, logger *slog.Logger) error {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := repository.Expire(bounded, 100)
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.Warn("import_expiration_deferred", "error_code", "SERVICE_UNAVAILABLE")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
