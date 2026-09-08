-- name: EnsureScope :exec
INSERT INTO public.scopes (id, name) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING;

-- name: GetScope :one
SELECT id, name, catalog_revision, auth_epoch FROM public.scopes WHERE id = $1;

-- name: LockScope :one
SELECT id, name, catalog_revision, auth_epoch FROM public.scopes WHERE id = $1 FOR UPDATE;

-- name: AdvanceCatalog :exec
UPDATE public.scopes SET catalog_revision = catalog_revision + 1 WHERE id = $1;

-- name: LockResources :many
SELECT id FROM public.resources WHERE scope_id = $1 AND id = ANY($2::uuid[]) ORDER BY id FOR UPDATE;

-- name: GetResourceIndex :one
SELECT id, scope_id, kind, name, head_revision, enabled, security_epoch, created_at, deleted_at
FROM public.resources WHERE scope_id = $1 AND id = $2;

-- name: GetResourceRevision :one
SELECT r.scope_id, r.resource_id, r.revision, r.schema_version, r.security_epoch,
    r.envelope, r.content_hmac, w.wrapping, w.wrap_version
FROM public.resource_revisions r
JOIN public.resource_revision_wrappings w USING (scope_id, resource_id, revision)
WHERE r.scope_id = $1 AND r.resource_id = $2 AND r.revision = $3;

-- name: GetResourceHead :one
SELECT r.scope_id, r.resource_id, r.revision, r.schema_version, r.security_epoch,
    r.envelope, r.content_hmac, w.wrapping, w.wrap_version
FROM public.resources h
JOIN public.resource_revisions r ON r.scope_id = h.scope_id AND r.resource_id = h.id AND r.revision = h.head_revision
JOIN public.resource_revision_wrappings w ON w.scope_id = r.scope_id AND w.resource_id = r.resource_id AND w.revision = r.revision
WHERE h.scope_id = $1 AND h.id = $2 AND h.deleted_at IS NULL;

-- name: InsertResource :exec
INSERT INTO public.resources (id, scope_id, kind, name, enabled) VALUES ($1, $2, $3, $4, $5);

-- name: InsertResourceRevision :exec
INSERT INTO public.resource_revisions (scope_id, resource_id, revision, schema_version, security_epoch, envelope, content_hmac)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: InsertResourceWrapping :exec
INSERT INTO public.resource_revision_wrappings (scope_id, resource_id, revision, wrapping) VALUES ($1, $2, $3, $4);

-- name: UpdateResourceHead :exec
UPDATE public.resources SET name = $3, head_revision = $4, enabled = $5,
    security_epoch = $6, deleted_at = $7 WHERE scope_id = $1 AND id = $2;

-- name: InsertResourceReference :exec
INSERT INTO public.resource_refs (scope_id, resource_id, revision, ref_path, target_resource_id, target_revision, expected_kind)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: DeleteResourceTags :exec
DELETE FROM public.resource_tags WHERE scope_id = $1 AND resource_id = $2;

-- name: InsertResourceTag :exec
INSERT INTO public.resource_tags (scope_id, resource_id, tag) VALUES ($1, $2, $3);

-- name: ListResourceTags :many
SELECT DISTINCT t.tag FROM public.resource_tags t
JOIN public.resources r ON r.scope_id = t.scope_id AND r.id = t.resource_id
WHERE t.scope_id = $1 AND r.deleted_at IS NULL AND t.tag > $2 ORDER BY t.tag LIMIT $3;

-- name: ListResources :many
SELECT r.id, r.scope_id, r.kind, r.name, r.head_revision, r.enabled, r.security_epoch, r.created_at, r.deleted_at,
    ARRAY(SELECT t.tag FROM public.resource_tags t WHERE t.scope_id = r.scope_id AND t.resource_id = r.id ORDER BY t.tag)::text[] AS tags
FROM public.resources r
WHERE r.scope_id = sqlc.arg(scope_id)
    AND (sqlc.arg(kind)::text = '' OR r.kind = sqlc.arg(kind))
    AND (sqlc.arg(tag)::text = '' OR EXISTS (SELECT 1 FROM public.resource_tags t
        WHERE t.scope_id = r.scope_id AND t.resource_id = r.id AND t.tag = sqlc.arg(tag)))
    AND (sqlc.arg(include_deleted)::boolean OR r.deleted_at IS NULL)
    AND (NOT sqlc.arg(has_after)::boolean OR (r.created_at, r.id) > (sqlc.arg(after_created_at)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY r.created_at, r.id LIMIT sqlc.arg(page_limit);

-- name: ListResourceReferences :many
SELECT f.resource_id, f.revision, f.target_resource_id, f.target_revision, f.expected_kind, f.ref_path,
    (r.head_revision = f.revision AND r.deleted_at IS NULL AND r.enabled) AS current
FROM public.resource_refs f
JOIN public.resources r ON r.scope_id = f.scope_id AND r.id = f.resource_id
WHERE f.scope_id = sqlc.arg(scope_id) AND f.target_resource_id = sqlc.arg(target_resource_id)
    AND (sqlc.arg(include_historical)::boolean OR (r.head_revision = f.revision AND r.deleted_at IS NULL AND r.enabled))
    AND (NOT sqlc.arg(has_after)::boolean OR (f.resource_id, f.revision, f.ref_path) >
        (sqlc.arg(after_resource_id)::uuid, sqlc.arg(after_revision)::bigint, sqlc.arg(after_path)::text))
ORDER BY f.resource_id, f.revision, f.ref_path LIMIT sqlc.arg(page_limit);

-- name: CompareAndSwapWrapping :execrows
UPDATE public.resource_revision_wrappings SET wrapping = $5, wrap_version = wrap_version + 1
WHERE scope_id = $1 AND resource_id = $2 AND revision = $3 AND wrap_version = $4;

-- name: GetWrappingVersion :one
SELECT wrap_version FROM public.resource_revision_wrappings
WHERE scope_id = $1 AND resource_id = $2 AND revision = $3;
