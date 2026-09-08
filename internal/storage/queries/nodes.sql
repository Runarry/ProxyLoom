-- name: ListNodeCandidates :many
SELECT r.scope_id, r.resource_id, r.revision, r.schema_version, r.security_epoch,
    r.envelope, r.content_hmac, w.wrapping, w.wrap_version, h.created_at
FROM public.resources h
JOIN public.resource_revisions r ON r.scope_id = h.scope_id AND r.resource_id = h.id AND r.revision = h.head_revision
JOIN public.resource_revision_wrappings w ON w.scope_id = r.scope_id AND w.resource_id = r.resource_id AND w.revision = r.revision
WHERE h.scope_id = sqlc.arg(scope_id) AND h.kind = 'node' AND h.deleted_at IS NULL
    AND (sqlc.arg(search)::text = '' OR strpos(lower(h.name), lower(sqlc.arg(search))) > 0)
    AND (NOT sqlc.arg(filter_enabled)::boolean OR h.enabled = sqlc.arg(enabled)::boolean)
    AND (sqlc.arg(tag)::text = '' OR EXISTS (SELECT 1 FROM public.resource_tags t
        WHERE t.scope_id = h.scope_id AND t.resource_id = h.id AND t.tag = sqlc.arg(tag)))
    AND (NOT sqlc.arg(has_after)::boolean OR (h.created_at, h.id) > (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY h.created_at, h.id LIMIT sqlc.arg(page_limit);

-- name: NodeExists :one
SELECT EXISTS (SELECT 1 FROM public.resources WHERE scope_id = $1 AND id = $2 AND kind = 'node');

-- name: ListNodeRevisions :many
SELECT r.scope_id, r.resource_id, r.revision, r.schema_version, r.security_epoch,
    r.envelope, r.content_hmac, w.wrapping, w.wrap_version
FROM public.resource_revisions r
JOIN public.resources h ON h.scope_id = r.scope_id AND h.id = r.resource_id
JOIN public.resource_revision_wrappings w ON w.scope_id = r.scope_id AND w.resource_id = r.resource_id AND w.revision = r.revision
WHERE r.scope_id = sqlc.arg(scope_id) AND r.resource_id = sqlc.arg(resource_id) AND h.kind = 'node'
    AND r.revision > sqlc.arg(after_revision)::bigint
ORDER BY r.revision LIMIT sqlc.arg(page_limit);

-- name: ListNodeReferences :many
SELECT f.resource_id, f.revision, h.kind AS source_kind, f.target_resource_id, f.target_revision,
    f.expected_kind, f.ref_path, (h.head_revision = f.revision AND h.deleted_at IS NULL AND h.enabled)::boolean AS current
FROM public.resource_refs f
JOIN public.resources h ON h.scope_id = f.scope_id AND h.id = f.resource_id
WHERE f.scope_id = sqlc.arg(scope_id) AND f.target_resource_id = sqlc.arg(target_resource_id)
    AND (sqlc.arg(reference_state)::text = 'all'
        OR (sqlc.arg(reference_state) = 'active' AND h.head_revision = f.revision AND h.deleted_at IS NULL AND h.enabled)
        OR (sqlc.arg(reference_state) = 'historical' AND NOT (h.head_revision = f.revision AND h.deleted_at IS NULL AND h.enabled)))
    AND (NOT sqlc.arg(has_after)::boolean OR (f.resource_id, f.revision, f.ref_path) >
        (sqlc.arg(after_resource_id)::uuid, sqlc.arg(after_revision)::bigint, sqlc.arg(after_path)::text))
ORDER BY f.resource_id, f.revision, f.ref_path LIMIT sqlc.arg(page_limit);

-- name: InsertResourceAudit :exec
INSERT INTO public.resource_audit_events (id, scope_id, actor_id, object_id, revision, action, request_id)
VALUES ($1, $2, $3, $4, $5, $6, $7);
