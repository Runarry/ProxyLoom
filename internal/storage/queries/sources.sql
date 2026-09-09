-- name: ListSourceCandidates :many
SELECT r.scope_id, r.resource_id, r.revision, r.schema_version, r.security_epoch,
    r.envelope, r.content_hmac, w.wrapping, w.wrap_version, h.created_at
FROM public.resources h
JOIN public.resource_revisions r ON r.scope_id = h.scope_id AND r.resource_id = h.id AND r.revision = h.head_revision
JOIN public.resource_revision_wrappings w ON w.scope_id = r.scope_id AND w.resource_id = r.resource_id AND w.revision = r.revision
WHERE h.scope_id = sqlc.arg(scope_id) AND h.kind = 'source' AND h.deleted_at IS NULL
    AND (NOT sqlc.arg(filter_enabled)::boolean OR h.enabled = sqlc.arg(enabled)::boolean)
    AND (sqlc.arg(tag)::text = '' OR EXISTS (SELECT 1 FROM public.resource_tags t
        WHERE t.scope_id = h.scope_id AND t.resource_id = h.id AND t.tag = sqlc.arg(tag)))
    AND (NOT sqlc.arg(has_after)::boolean OR (h.created_at, h.id) > (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY h.created_at, h.id LIMIT sqlc.arg(page_limit);

-- name: InsertSourceSnapshot :exec
INSERT INTO public.source_snapshots (id, scope_id, source_id, source_revision, envelope, wrapping, content_hmac, http_status, content_type, decoded_bytes, state)
VALUES (sqlc.arg(id), sqlc.arg(scope_id), sqlc.arg(source_id), sqlc.arg(source_revision), sqlc.arg(envelope), sqlc.arg(wrapping),
    sqlc.arg(content_hmac), sqlc.narg(http_status), sqlc.narg(content_type), sqlc.arg(decoded_bytes), sqlc.arg(state));

-- name: CountSourceSnapshots :one
SELECT COUNT(*) FROM public.source_snapshots WHERE scope_id = sqlc.arg(scope_id) AND source_id = sqlc.arg(source_id) AND state = sqlc.arg(state);

-- name: ListSourceItems :many
SELECT id, source_id, external_key, envelope, wrapping, base_revision, last_seen_at, state
FROM public.source_items WHERE scope_id = sqlc.arg(scope_id) AND source_id = sqlc.arg(source_id) ORDER BY id;

-- name: InsertSourceItem :exec
INSERT INTO public.source_items (id, scope_id, source_id, external_key, envelope, wrapping, base_revision, last_seen_at, state)
VALUES (sqlc.arg(id), sqlc.arg(scope_id), sqlc.arg(source_id), sqlc.narg(external_key), sqlc.arg(envelope), sqlc.arg(wrapping),
    sqlc.arg(base_revision), sqlc.arg(last_seen_at), sqlc.arg(state));

-- name: UpdateSourceItem :exec
UPDATE public.source_items SET envelope = sqlc.arg(envelope), wrapping = sqlc.arg(wrapping), base_revision = sqlc.arg(base_revision),
    last_seen_at = sqlc.arg(last_seen_at), state = sqlc.arg(state)
WHERE scope_id = sqlc.arg(scope_id) AND id = sqlc.arg(id);

-- name: GetNodeBindingByItem :one
SELECT node_id, source_item_id, binding_revision, match_method
FROM public.node_bindings WHERE scope_id = sqlc.arg(scope_id) AND source_item_id = sqlc.arg(source_item_id);

-- name: GetNodeBindingByNode :one
SELECT node_id, source_item_id, binding_revision, match_method
FROM public.node_bindings WHERE scope_id = sqlc.arg(scope_id) AND node_id = sqlc.arg(node_id);

-- name: InsertNodeBinding :exec
INSERT INTO public.node_bindings (node_id, scope_id, source_item_id, binding_revision, match_method)
VALUES (sqlc.arg(node_id), sqlc.arg(scope_id), sqlc.arg(source_item_id), sqlc.arg(binding_revision), sqlc.arg(match_method));

-- name: UpdateNodeBinding :exec
UPDATE public.node_bindings SET binding_revision = sqlc.arg(binding_revision), match_method = sqlc.arg(match_method)
WHERE scope_id = sqlc.arg(scope_id) AND node_id = sqlc.arg(node_id);

-- name: UpsertSourceSchedule :exec
INSERT INTO public.source_schedules (source_id, scope_id, next_run_at, backoff_seconds, last_job_id)
VALUES (sqlc.arg(source_id), sqlc.arg(scope_id), sqlc.arg(next_run_at), sqlc.arg(backoff_seconds), sqlc.narg(last_job_id))
ON CONFLICT (source_id) DO UPDATE SET next_run_at = EXCLUDED.next_run_at, backoff_seconds = EXCLUDED.backoff_seconds, last_job_id = EXCLUDED.last_job_id;

-- name: DeleteSourceSchedule :exec
DELETE FROM public.source_schedules WHERE scope_id = sqlc.arg(scope_id) AND source_id = sqlc.arg(source_id);

-- name: ListDueSourceSchedules :many
SELECT s.source_id, s.scope_id, s.next_run_at, s.backoff_seconds, s.last_job_id, h.head_revision
FROM public.source_schedules s
JOIN public.resources h ON h.scope_id = s.scope_id AND h.id = s.source_id
WHERE s.next_run_at <= clock_timestamp() AND h.deleted_at IS NULL AND h.kind = 'source'
ORDER BY s.next_run_at, s.source_id LIMIT sqlc.arg(page_limit);
