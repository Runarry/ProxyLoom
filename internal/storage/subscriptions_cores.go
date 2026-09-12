package storage

import (
	"context"
	"encoding/json"
	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/compiler"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
	"github.com/jackc/pgx/v5"
)

func (s *Subscriptions) EnsureCoreBuilds(ctx context.Context) error {
	for _, b := range s.cores.Builds() {
		manifest, _ := json.Marshal(runnerprotocol.CoreIdentity{CoreBuildID: b.ID, CoreFamily: b.Family, Version: b.Version, BuildSHA256: b.BinarySHA256, Platform: b.OS, Architecture: b.Arch, AdapterVersion: s.cores.AdapterVersion})
		if _, err := s.catalog.pool.Exec(ctx, `INSERT INTO public.core_builds(id,manifest) VALUES($1,$2) ON CONFLICT(id) DO NOTHING`, dbID(b.ID), manifest); err != nil {
			return subError(err)
		}
		var matches bool
		if err := s.catalog.pool.QueryRow(ctx, `SELECT manifest=$2::jsonb FROM public.core_builds WHERE id=$1`, dbID(b.ID), manifest).Scan(&matches); err != nil || !matches {
			return catalog.ErrInvalidInput
		}
	}
	return nil
}
func (s *Subscriptions) core(ctx context.Context, tx subTx, id ir.ID) (subscriptions.Core, error) {
	c := subscriptions.Core{CapabilityStatus: "unverified"}
	var manifest []byte
	var rev int64
	err := tx.QueryRow(ctx, `SELECT manifest,revision,enabled,registered_at,disabled_at FROM public.core_builds WHERE id=$1`, dbID(id)).Scan(&manifest, &rev, &c.Enabled, &c.RegisteredAt, &c.DisabledAt)
	if err != nil {
		return c, err
	}
	if json.Unmarshal(manifest, &c.CoreIdentity) != nil {
		return c, catalog.ErrCrypto
	}
	c.Revision = subscriptions.Revision(rev)
	return c, nil
}
func (s *Subscriptions) Cores(ctx context.Context) ([]subscriptions.Core, error) {
	out := []subscriptions.Core{}
	for _, b := range s.cores.Builds() {
		c, err := s.core(ctx, s.catalog.pool, b.ID)
		if err != nil {
			return nil, subError(err)
		}
		out = append(out, c)
	}
	return out, nil
}
func (s *Subscriptions) DisableCore(ctx context.Context, a subscriptions.Actor, id ir.ID, expected int64) (subscriptions.Core, error) {
	if !validIDs(a.ScopeID, a.ID, id) || expected < 1 {
		return subscriptions.Core{}, catalog.ErrInvalidInput
	}
	tx, err := s.catalog.pool.Begin(ctx)
	if err != nil {
		return subscriptions.Core{}, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = dbgen.New(tx).LockScope(ctx, dbID(a.ScopeID)); err != nil {
		return subscriptions.Core{}, subError(err)
	}
	c, err := s.core(ctx, tx, id)
	if err != nil {
		return c, subError(err)
	}
	if int64(c.Revision) != expected {
		return c, catalog.ErrRevisionConflict
	}
	if c.Enabled {
		_, err = tx.Exec(ctx, `UPDATE public.core_builds SET enabled=false,revision=revision+1,disabled_at=clock_timestamp() WHERE id=$1`, dbID(id))
		if err == nil {
			err = s.audit(ctx, tx, a, id, "core_disable")
		}
	}
	if err == nil {
		c, err = s.core(ctx, tx, id)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	return c, subError(err)
}
func (s *Subscriptions) Export(ctx context.Context, a subscriptions.Actor, id ir.ID, keys []string, format ir.OutputFormat, secrets bool) ([]apicontract.ExportedArtifact, error) {
	if !validIDs(a.ScopeID, a.ID, id) || len(keys) == 0 || len(keys) > 32 {
		return nil, catalog.ErrInvalidInput
	}
	tx, err := s.catalog.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	p, err := s.readPublication(ctx, tx, a.ScopeID, id)
	if err != nil {
		return nil, subError(err)
	}
	if p.State == "blocked" {
		return nil, subscriptions.ErrBlocked
	}
	out := []apicontract.ExportedArtifact{}
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			return nil, catalog.ErrInvalidInput
		}
		seen[key] = true
		found := false
		for _, target := range p.Targets {
			if target.TargetKey != key {
				continue
			}
			found = true
			if target.Format != format {
				return nil, catalog.ErrInvalidInput
			}
			plain, err := s.artifact(ctx, tx, a.ScopeID, target.ArtifactID)
			if err != nil {
				return nil, subError(err)
			}
			content := string(plain)
			if !secrets {
				content, err = compiler.RedactedNative(plain, format)
			}
			clear(plain)
			if err != nil {
				return nil, catalog.ErrCrypto
			}
			ext, media := ".json", "application/json"
			if format == ir.MihomoYAML {
				ext, media = ".yaml", "application/yaml"
			}
			out = append(out, apicontract.ExportedArtifact{Filename: key + ext, MediaType: media, Content: content, ContainsSecrets: secrets})
		}
		if !found {
			return nil, catalog.ErrInvalidInput
		}
	}
	if err = s.audit(ctx, tx, a, id, "export"); err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return nil, subError(err)
	}
	return out, nil
}
