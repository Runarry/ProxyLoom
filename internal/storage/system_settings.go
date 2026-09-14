package storage

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/operations"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5"
)

type Operations struct {
	catalog *Catalog
	jobs    *Jobs
}

func NewOperations(c *Catalog, j *Jobs) (*Operations, error) {
	if c == nil || j == nil {
		return nil, operations.ErrInvalid
	}
	return &Operations{c, j}, nil
}
func readSystemSettings(ctx context.Context, q quotaQuery, scope ir.ID) (operations.Settings, error) {
	s := operations.Defaults()
	var data []byte
	var revision int64
	err := q.QueryRow(ctx, `SELECT revision,settings FROM public.system_settings WHERE scope_id=$1`, dbID(scope)).Scan(&revision, &data)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return s, catalog.ErrUnavailable
	}
	if err == nil {
		if json.Unmarshal(data, &s) != nil || s.Validate() != nil {
			return s, catalog.ErrUnavailable
		}
		s.Revision = runnerprotocol.Sequence(revision)
	}
	quota, _, err := readQuota(ctx, q, scope)
	if err != nil {
		return s, err
	}
	s.Quota = quota
	return s, nil
}
func (s *Operations) Settings(ctx context.Context, scope ir.ID) (operations.Settings, error) {
	if scope.Validate() != nil {
		return operations.Settings{}, operations.ErrInvalid
	}
	tx, err := s.catalog.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return operations.Settings{}, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	return readSystemSettings(ctx, tx, scope)
}
func (s *Operations) Update(ctx context.Context, a operations.Actor, expected int64, value operations.Settings) (operations.Settings, error) {
	if !validIDs(a.ScopeID, a.ID) || expected < 1 || value.Validate() != nil {
		return value, operations.ErrInvalid
	}
	tx, err := s.catalog.pool.Begin(ctx)
	if err != nil {
		return value, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = dbgen.New(tx).LockScope(ctx, dbID(a.ScopeID)); err != nil {
		return value, catalog.ErrUnavailable
	}
	previous, err := readSystemSettings(ctx, tx, a.ScopeID)
	if err != nil {
		return value, err
	}
	if int64(previous.Revision) != expected {
		return value, catalog.ErrRevisionConflict
	}
	value.Revision = runnerprotocol.Sequence(expected + 1)
	encoded, _ := json.Marshal(value)
	quota, _ := json.Marshal(value.Quota)
	if _, err = tx.Exec(ctx, `INSERT INTO public.system_settings(scope_id,revision,settings) VALUES($1,$2,$3) ON CONFLICT(scope_id) DO UPDATE SET revision=EXCLUDED.revision,settings=EXCLUDED.settings,updated_at=clock_timestamp()`, dbID(a.ScopeID), expected+1, encoded); err != nil {
		return value, catalog.ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.quota_settings(scope_id,revision,settings) VALUES($1,$2,$3) ON CONFLICT(scope_id) DO UPDATE SET revision=EXCLUDED.revision,settings=EXCLUDED.settings`, dbID(a.ScopeID), expected+1, quota); err != nil {
		return value, catalog.ErrUnavailable
	}
	if _, err = tx.Exec(ctx, `INSERT INTO public.system_setting_revisions(scope_id,revision,settings,actor_id) VALUES($1,$2,$3,$4)`, dbID(a.ScopeID), expected+1, encoded, dbID(a.ID)); err != nil {
		return value, catalog.ErrUnavailable
	}
	fields := changedSettingFields(previous, value)
	changed, _ := json.Marshal(fields)
	if _, err = tx.Exec(ctx, `INSERT INTO public.system_audit_events(scope_id,actor_id,action,request_id,changed_fields) VALUES($1,$2,'settings_update',$3,$4)`, dbID(a.ScopeID), dbID(a.ID), a.RequestID, changed); err != nil {
		return value, catalog.ErrUnavailable
	}
	if err = tx.Commit(ctx); err != nil {
		return value, catalog.ErrUnavailable
	}
	return value, nil
}
func changedSettingFields(old, next operations.Settings) []string {
	b1, _ := json.Marshal(old)
	b2, _ := json.Marshal(next)
	var before, after map[string]json.RawMessage
	json.Unmarshal(b1, &before)
	json.Unmarshal(b2, &after)
	fields := []string{}
	for group, value := range after {
		if group == "revision" {
			continue
		}
		if string(value) == string(before[group]) {
			continue
		}
		if group == "cleanup_paused" {
			fields = append(fields, "/cleanup_paused")
			continue
		}
		var a, b map[string]json.RawMessage
		json.Unmarshal(before[group], &a)
		json.Unmarshal(value, &b)
		for key, item := range b {
			if string(a[key]) != string(item) {
				fields = append(fields, "/"+group+"/"+key)
			}
		}
	}
	sort.Strings(fields)
	return fields
}
func (s *Operations) Overview(ctx context.Context, scope ir.ID) (operations.Overview, error) {
	o := operations.Overview{Resources: map[string]int64{}, Jobs: map[string]int64{}}
	if scope.Validate() != nil {
		return o, operations.ErrInvalid
	}
	tx, err := s.catalog.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return o, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	settings, err := readSystemSettings(ctx, tx, scope)
	if err != nil {
		return o, err
	}
	o.CleanupPaused = settings.CleanupPaused
	o.Budget.Limit = settings.Quota.DailyDownloadBytes
	for _, query := range []struct {
		sql string
		out map[string]int64
	}{{`SELECT kind,count(*) FROM public.resources WHERE scope_id=$1 AND deleted_at IS NULL GROUP BY kind`, o.Resources}, {`SELECT state,count(*) FROM public.jobs WHERE scope_id=$1 GROUP BY state`, o.Jobs}} {
		rows, err := tx.Query(ctx, query.sql, dbID(scope))
		if err != nil {
			return o, catalog.ErrUnavailable
		}
		for rows.Next() {
			var key string
			var count int64
			if rows.Scan(&key, &count) != nil {
				rows.Close()
				return o, catalog.ErrUnavailable
			}
			query.out[key] = count
		}
		rows.Close()
		if rows.Err() != nil {
			return o, catalog.ErrUnavailable
		}
	}
	var day time.Time
	err = tx.QueryRow(ctx, `WITH current_day AS MATERIALIZED (SELECT (clock_timestamp() AT TIME ZONE 'UTC')::date AS day) SELECT d.day,COALESCE(q.reserved_bytes,0),COALESCE(q.settled_bytes,0) FROM current_day d LEFT JOIN public.quota_buckets q ON q.scope_id=$1 AND q.utc_day=d.day`, dbID(scope)).Scan(&day, &o.Budget.Reserved, &o.Budget.Settled)
	if err != nil {
		return o, catalog.ErrUnavailable
	}
	o.Budget.Day = day.Format("2006-01-02")
	err = tx.QueryRow(ctx, `SELECT last_cleanup_at,last_backup_at FROM public.maintenance_status WHERE scope_id=$1`, dbID(scope)).Scan(&o.LastCleanup, &o.LastBackup)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return o, catalog.ErrUnavailable
	}
	return o, nil
}
