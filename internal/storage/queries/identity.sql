-- name: LockIdentityState :one
SELECT setup_consumed_at FROM public.identity_state WHERE singleton = true FOR UPDATE;

-- name: IdentityNow :one
SELECT clock_timestamp()::timestamptz AS now;

-- name: ConsumeIdentitySetup :exec
UPDATE public.identity_state SET setup_consumed_at = clock_timestamp() WHERE singleton = true;

-- name: CountIdentityUsers :one
SELECT count(*) FROM public.users;

-- name: CreateIdentityUser :one
INSERT INTO public.users (id, scope_id, login, password_hash) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetIdentityUser :one
SELECT * FROM public.users WHERE scope_id = $1 AND login = $2 FOR UPDATE;

-- name: ResetIdentityPassword :exec
UPDATE public.users SET password_hash = $2, auth_version = auth_version + 1 WHERE id = $1;

-- name: CreateIdentitySession :one
INSERT INTO public.sessions (id_hash, scope_id, user_id, auth_version, auth_epoch, created_at, expires_at, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $6, $6::timestamptz + INTERVAL '8 hours', $6)
RETURNING *;

-- name: GetIdentitySession :one
SELECT s.*, u.login, u.role, u.disabled, u.password_hash, u.auth_version AS current_auth_version,
    sc.auth_epoch AS current_auth_epoch
FROM public.sessions s
JOIN public.users u ON u.id = s.user_id AND u.scope_id = s.scope_id
JOIN public.scopes sc ON sc.id = s.scope_id
WHERE s.id_hash = $1 AND s.scope_id = $2
FOR UPDATE OF s, u FOR SHARE OF sc;

-- name: GetIdentityScopeEpoch :one
SELECT auth_epoch FROM public.scopes WHERE id = $1 FOR SHARE;

-- name: TouchIdentitySession :exec
UPDATE public.sessions SET last_seen_at = $2 WHERE id_hash = $1;

-- name: ReauthenticateIdentitySession :exec
UPDATE public.sessions SET last_seen_at = $2, reauth_at = $2 WHERE id_hash = $1;

-- name: DeleteIdentitySession :exec
DELETE FROM public.sessions WHERE id_hash = $1;

-- name: DeleteIdentityUserSessions :exec
DELETE FROM public.sessions WHERE user_id = $1;

-- name: DeleteExpiredIdentitySessions :exec
DELETE FROM public.sessions WHERE expires_at <= $1 OR last_seen_at <= $1::timestamptz - INTERVAL '30 minutes';

-- name: PruneIdentityRateLimits :exec
DELETE FROM public.identity_rate_limits WHERE window_start <= $1::timestamptz - INTERVAL '1 minute';

-- name: CountIdentityRateLimits :one
SELECT count(*) FROM public.identity_rate_limits;

-- name: GetIdentityRateLimit :one
SELECT * FROM public.identity_rate_limits WHERE key_hash = $1;

-- name: CreateIdentityRateLimit :exec
INSERT INTO public.identity_rate_limits(key_hash, window_start, attempts) VALUES ($1, $2, 1);

-- name: IncrementIdentityRateLimit :exec
UPDATE public.identity_rate_limits SET attempts = attempts + 1 WHERE key_hash = $1;

-- name: AppendIdentityAudit :exec
INSERT INTO public.identity_audit_events(id, scope_id, actor_id, action, outcome, account_hash, source_hash, request_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);
