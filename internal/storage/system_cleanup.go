package storage

import (
	"context"
	"encoding/json"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type CleanupResult struct {
	Paused  bool `json:"paused"`
	Deleted int  `json:"deleted"`
}

func (s *Operations) Cleanup(ctx context.Context, scope ir.ID, limit int) (CleanupResult, error) {
	var result CleanupResult
	if scope.Validate() != nil || limit < 1 || limit > 1000 {
		return result, catalog.ErrInvalidInput
	}
	var data []byte
	if err := s.catalog.pool.QueryRow(ctx, `SELECT public.proxyloom_cleanup($1,$2)`, dbID(scope), limit).Scan(&data); err != nil {
		return result, catalog.ErrUnavailable
	}
	if json.Unmarshal(data, &result) != nil {
		return result, catalog.ErrUnavailable
	}
	return result, nil
}
