package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCatalogErrorBoundaryDiscardsDatabaseAndCallbackDetails(t *testing.T) {
	const sentinel = "SYNTHETIC_CATALOG_SECRET_697f3a"
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{"missing", pgx.ErrNoRows, catalog.ErrNotFound},
		{"reference", &pgconn.PgError{Code: "23503", Message: sentinel, Detail: sentinel}, catalog.ErrInvalidReference},
		{"conflict", &pgconn.PgError{Code: "23505", Message: sentinel, Detail: sentinel}, catalog.ErrRevisionConflict},
		{"invalid", &pgconn.PgError{Code: "23514", Message: sentinel, Detail: sentinel}, catalog.ErrInvalidInput},
		{"unavailable", &pgconn.PgError{Code: "08006", Message: sentinel, Detail: sentinel}, catalog.ErrUnavailable},
		{"callback", fmt.Errorf("%s: %w", sentinel, catalog.ErrInvalidInput), catalog.ErrInvalidInput},
		{"unknown_callback", errors.New(sentinel), catalog.ErrUnavailable},
		{"cancelled", fmt.Errorf("%s: %w", sentinel, context.Canceled), context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := catalogError(test.err)
			if !errors.Is(got, test.want) || strings.Contains(got.Error(), sentinel) {
				t.Fatal("catalog error classification or redaction failed")
			}
		})
	}
}
