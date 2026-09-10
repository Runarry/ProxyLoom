-- name: ListRoutingCandidates :many
SELECT r.scope_id, r.resource_id, r.revision, r.schema_version, r.security_epoch,
    r.envelope, r.content_hmac, w.wrapping, w.wrap_version, h.created_at
FROM public.resources h
JOIN public.resource_revisions r ON r.scope_id = h.scope_id AND r.resource_id = h.id AND r.revision = h.head_revision
JOIN public.resource_revision_wrappings w ON w.scope_id = r.scope_id AND w.resource_id = r.resource_id AND w.revision = r.revision
WHERE h.scope_id = sqlc.arg(scope_id) AND h.kind = sqlc.arg(kind)::text AND h.deleted_at IS NULL
    AND (NOT sqlc.arg(filter_enabled)::boolean OR h.enabled = sqlc.arg(enabled)::boolean)
    AND (sqlc.arg(tag)::text = '' OR EXISTS (SELECT 1 FROM public.resource_tags t
        WHERE t.scope_id = h.scope_id AND t.resource_id = h.id AND t.tag = sqlc.arg(tag)))
    AND (NOT sqlc.arg(has_after)::boolean OR (h.created_at, h.id) > (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY h.created_at, h.id LIMIT sqlc.arg(page_limit);
