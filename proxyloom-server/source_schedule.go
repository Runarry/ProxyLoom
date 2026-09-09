package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/Runarry/ProxyLoom/internal/storage"
)

func scheduleSources(ctx context.Context, repository *storage.Sources, logger *slog.Logger) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		_, err := repository.ScheduleDue(bounded, 50)
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.Warn("source_schedule_deferred", "error_code", "SERVICE_UNAVAILABLE")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
