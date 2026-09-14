// Command proxyloom-operations runs explicit offline operator jobs. It has no
// HTTP listener and is never part of the API or Runner dependency graph.
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Runarry/ProxyLoom/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Stdout); err != nil {
		slog.New(slog.NewJSONHandler(os.Stderr, nil)).Error("command_failed", "error_code", err.Error())
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, lookup config.Lookup, output io.Writer) error {
	if len(args) < 2 || args[0] != "admin" {
		return errors.New("operations_invalid_command")
	}
	switch args[1] {
	case "backup", "verify-backup", "restore-backup", "create-backup-key":
		return adminBackup(ctx, args[1:], lookup, output)
	default:
		return errors.New("operations_invalid_command")
	}
}
func readAdminFile(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("admin_secret_file_invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("admin_secret_file_invalid")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("admin_secret_file_invalid")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		clear(data)
		return nil, errors.New("admin_secret_file_invalid")
	}
	return data, nil
}
