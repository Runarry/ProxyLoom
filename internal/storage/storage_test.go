package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInvalidDSNDoesNotEscapeStorageBoundary(t *testing.T) {
	pool, err := Open(context.Background(), "postgres://EXAMPLE_USER:EXAMPLE_PASSWORD@[invalid")
	if pool != nil {
		pool.Close()
		t.Fatal("invalid DSN returned a pool")
	}
	if !errors.Is(err, ErrDatabaseConfig) || strings.Contains(err.Error(), "EXAMPLE_") {
		t.Fatalf("unsafe DSN error: %v", err)
	}
}
