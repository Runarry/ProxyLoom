-- name: ClaimIdempotencyKey :execrows
INSERT INTO public.idempotency_keys (principal_id, route_key, key, scope_id, request_hmac)
VALUES ($1, $2, $3, $4, $5) ON CONFLICT (principal_id, route_key, key) DO NOTHING;

-- name: GetIdempotencyReceipt :one
SELECT scope_id, request_hmac, http_status, resource_id, resource_revision, operation_id, status
FROM public.idempotency_keys WHERE principal_id = $1 AND route_key = $2 AND key = $3;

-- name: FinalizeIdempotencyReceipt :exec
UPDATE public.idempotency_keys SET http_status = $4, resource_id = $5, resource_revision = $6,
    operation_id = $7, status = $8 WHERE principal_id = $1 AND route_key = $2 AND key = $3;
