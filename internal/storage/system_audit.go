package storage

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/operations"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

var _ operations.Repository = (*Operations)(nil)

var auditActions = map[string]bool{"setup": true, "login": true, "logout": true, "reauth": true, "create": true, "update": true, "delete": true, "reveal": true, "source_refresh": true, "import_commit": true, "compile": true, "publish": true, "rollback": true, "token_issue": true, "token_revoke": true, "test_create": true, "job_cancel": true, "core_disable": true, "settings_update": true, "secret_rewrap": true, "export": true, "cleanup": true, "backup": true, "restore": true, "password_reset": true}

func (s *Operations) Audit(ctx context.Context, scope ir.ID, f operations.AuditFilter) ([]json.RawMessage, bool, error) {
	if scope.Validate() != nil || f.Limit < 1 || f.Limit > 200 || f.Action != "" && !auditActions[f.Action] || f.After.ID != "" && f.After.CreatedAt.IsZero() {
		return nil, false, operations.ErrInvalid
	}
	for _, id := range []ir.ID{f.ActorID, f.ResourceID, f.After.ID} {
		if id != "" && id.Validate() != nil {
			return nil, false, operations.ErrInvalid
		}
	}
	var from, until *time.Time
	if f.From != "" {
		v, err := time.Parse(time.RFC3339Nano, f.From)
		if err != nil {
			return nil, false, operations.ErrInvalid
		}
		from = &v
	}
	if f.Until != "" {
		v, err := time.Parse(time.RFC3339Nano, f.Until)
		if err != nil {
			return nil, false, operations.ErrInvalid
		}
		until = &v
	}
	if from != nil && until != nil && !until.After(*from) {
		return nil, false, operations.ErrInvalid
	}
	rows, err := s.catalog.pool.Query(ctx, `WITH events AS (
 SELECT id,created_at,COALESCE(NULLIF(request_id,''),'historical') request_id,actor_id,NULL::uuid object_id,NULL::bigint revision,
 CASE action WHEN 'reauthenticate' THEN 'reauth' WHEN 'password.reset' THEN 'password_reset' WHEN 'secret.reveal' THEN 'reveal' WHEN 'private.export' THEN 'export' WHEN 'token.issue' THEN 'token_issue' WHEN 'key.rotate' THEN 'secret_rewrap' ELSE action END action,
 CASE WHEN outcome='success' THEN 'success' ELSE 'failure' END outcome,'[]'::jsonb changed_fields
 FROM public.identity_audit_events WHERE scope_id=$1 OR scope_id IS NULL
 UNION ALL
 SELECT id,created_at,request_id,actor_id,object_id,revision,
 CASE WHEN action='import.commit' THEN 'import_commit' WHEN action LIKE '%.create' OR action LIKE '%.clone' THEN 'create' WHEN action LIKE '%.delete' THEN 'delete' WHEN action LIKE '%.refresh' THEN 'source_refresh' ELSE 'update' END,
 outcome,'[]'::jsonb FROM public.resource_audit_events WHERE scope_id=$1
 UNION ALL
 SELECT event_id,created_at,request_id,actor_id,object_id,NULL::bigint,action,'success','[]'::jsonb FROM public.publication_audit_events WHERE scope_id=$1
 UNION ALL
 SELECT event_id,created_at,request_id,actor_id,object_id,NULL::bigint,
 CASE action WHEN 'test_target.write' THEN 'update' WHEN 'test_target.delete' THEN 'delete' WHEN 'tests.create' THEN 'test_create' ELSE action END,
 result,changed_fields FROM public.system_audit_events WHERE scope_id=$1
 ) SELECT id::text,created_at,request_id,actor_id::text,object_id::text,revision,action,outcome,changed_fields FROM events
 WHERE ($2::uuid IS NULL OR actor_id=$2) AND ($3::uuid IS NULL OR object_id=$3) AND ($4='' OR action=$4)
 AND ($5::timestamptz IS NULL OR created_at>=$5) AND ($6::timestamptz IS NULL OR created_at<$6)
 AND ($7::uuid IS NULL OR (created_at,id)>($8,$7)) ORDER BY created_at,id LIMIT $9`, dbID(scope), nullableID(f.ActorID), nullableID(f.ResourceID), f.Action, from, until, nullableID(f.After.ID), f.After.CreatedAt, f.Limit+1)
	if err != nil {
		return nil, false, catalog.ErrUnavailable
	}
	defer rows.Close()
	items := []json.RawMessage{}
	for rows.Next() {
		var id ir.ID
		var created time.Time
		var request, action, outcome string
		var actor, object *string
		var revision *int64
		var fields []byte
		if rows.Scan(&id, &created, &request, &actor, &object, &revision, &action, &outcome, &fields) != nil {
			return nil, false, catalog.ErrUnavailable
		}
		actorType := "system"
		if actor != nil {
			actorType = "administrator"
		}
		item := map[string]any{"event_id": id, "created_at": created, "request_id": request, "actor_type": actorType, "action": action, "outcome": outcome, "changed_fields": json.RawMessage(fields)}
		if actor != nil {
			item["actor_id"] = *actor
		}
		if object != nil {
			item["resource_id"] = *object
		}
		if revision != nil {
			item["revision"] = runnerprotocol.Sequence(*revision)
		}
		data, err := json.Marshal(item)
		if err != nil {
			return nil, false, catalog.ErrUnavailable
		}
		items = append(items, data)
	}
	if rows.Err() != nil {
		return nil, false, catalog.ErrUnavailable
	}
	more := len(items) > f.Limit
	if more {
		items = items[:f.Limit]
	}
	return items, more, nil
}
