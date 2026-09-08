-- Administrator credentials, opaque sessions, bounded login throttles, and
-- append-only security audit. No plaintext password, cookie, CSRF, source IP,
-- submitted account identifier, or setup token is persisted.
CREATE TABLE public.identity_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    setup_consumed_at timestamptz
);
INSERT INTO public.identity_state(singleton) VALUES (true);

CREATE TABLE public.users (
    id uuid PRIMARY KEY,
    scope_id uuid NOT NULL REFERENCES public.scopes(id),
    login text NOT NULL CHECK (login ~ '^[a-z0-9._-]{1,64}$'),
    password_hash text NOT NULL CHECK (length(password_hash) BETWEEN 64 AND 512),
    role text NOT NULL DEFAULT 'admin' CHECK (role = 'admin'),
    disabled boolean NOT NULL DEFAULT false,
    auth_version bigint NOT NULL DEFAULT 1 CHECK (auth_version > 0),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (scope_id, login),
    UNIQUE (scope_id, id)
);

CREATE TABLE public.sessions (
    id_hash bytea PRIMARY KEY CHECK (octet_length(id_hash) = 32),
    scope_id uuid NOT NULL,
    user_id uuid NOT NULL,
    auth_version bigint NOT NULL CHECK (auth_version > 0),
    auth_epoch bigint NOT NULL CHECK (auth_epoch > 0),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    reauth_at timestamptz,
    FOREIGN KEY (scope_id, user_id) REFERENCES public.users(scope_id, id),
    CHECK (expires_at = created_at + INTERVAL '8 hours'),
    CHECK (last_seen_at >= created_at AND last_seen_at < expires_at),
    CHECK (reauth_at IS NULL OR (reauth_at >= created_at AND reauth_at < expires_at))
);
CREATE INDEX sessions_expiry ON public.sessions(expires_at);
CREATE INDEX sessions_idle ON public.sessions(last_seen_at);
CREATE INDEX sessions_user ON public.sessions(user_id);

CREATE TABLE public.identity_rate_limits (
    key_hash bytea PRIMARY KEY CHECK (octet_length(key_hash) = 32),
    window_start timestamptz NOT NULL,
    attempts integer NOT NULL CHECK (attempts BETWEEN 1 AND 30)
);
CREATE INDEX identity_rate_expiry ON public.identity_rate_limits(window_start);

CREATE TABLE public.identity_audit_events (
    id uuid PRIMARY KEY,
    scope_id uuid REFERENCES public.scopes(id),
    actor_id uuid REFERENCES public.users(id),
    action text NOT NULL CHECK (action IN ('setup', 'login', 'reauthenticate',
        'logout', 'password.reset', 'secret.reveal', 'private.export', 'token.issue', 'key.rotate')),
    outcome text NOT NULL CHECK (outcome IN ('success', 'denied', 'rate_limited')),
    account_hash bytea CHECK (account_hash IS NULL OR octet_length(account_hash) = 32),
    source_hash bytea CHECK (source_hash IS NULL OR octet_length(source_hash) = 32),
    request_id text NOT NULL DEFAULT '' CHECK (length(request_id) <= 128 AND request_id ~ '^[A-Za-z0-9._:-]*$'),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX identity_audit_scope_time ON public.identity_audit_events(scope_id, created_at, id);
CREATE TRIGGER identity_audit_no_update BEFORE UPDATE OR DELETE ON public.identity_audit_events
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER identity_audit_no_truncate BEFORE TRUNCATE ON public.identity_audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION public.proxyloom_reject_history_change();

CREATE FUNCTION public.proxyloom_identity_state_advance() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF OLD.setup_consumed_at IS NOT NULL OR NEW.setup_consumed_at IS NULL
       OR NEW.singleton <> OLD.singleton THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'identity_setup_already_consumed';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER identity_setup_consume BEFORE UPDATE ON public.identity_state
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_identity_state_advance();
CREATE TRIGGER identity_state_no_delete BEFORE DELETE ON public.identity_state
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER identity_state_no_truncate BEFORE TRUNCATE ON public.identity_state
    FOR EACH STATEMENT EXECUTE FUNCTION public.proxyloom_reject_history_change();

CREATE FUNCTION public.proxyloom_identity_user_advance() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.scope_id <> OLD.scope_id OR NEW.login <> OLD.login
       OR NEW.role <> OLD.role OR NEW.created_at <> OLD.created_at
       OR NEW.auth_version <> OLD.auth_version + 1
       OR (NEW.password_hash = OLD.password_hash AND NEW.disabled = OLD.disabled) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'identity_user_version_required';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER identity_user_advance BEFORE UPDATE ON public.users
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_identity_user_advance();
CREATE TRIGGER identity_users_no_delete BEFORE DELETE ON public.users
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();

CREATE FUNCTION public.proxyloom_identity_session_advance() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NEW.id_hash <> OLD.id_hash OR NEW.scope_id <> OLD.scope_id OR NEW.user_id <> OLD.user_id
       OR NEW.auth_version <> OLD.auth_version OR NEW.auth_epoch <> OLD.auth_epoch
       OR NEW.created_at <> OLD.created_at OR NEW.expires_at <> OLD.expires_at
       OR NEW.last_seen_at < OLD.last_seen_at
       OR (OLD.reauth_at IS NOT NULL AND (NEW.reauth_at IS NULL OR NEW.reauth_at < OLD.reauth_at)) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'identity_session_immutable';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER identity_session_advance BEFORE UPDATE ON public.sessions
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_identity_session_advance();

REVOKE ALL ON public.identity_state, public.users, public.sessions,
    public.identity_rate_limits, public.identity_audit_events FROM PUBLIC, proxyloom;
GRANT SELECT ON public.identity_state, public.users, public.sessions,
    public.identity_rate_limits, public.identity_audit_events TO proxyloom;
GRANT UPDATE (setup_consumed_at) ON public.identity_state TO proxyloom;
GRANT INSERT (id, scope_id, login, password_hash) ON public.users TO proxyloom;
GRANT UPDATE (password_hash, auth_version) ON public.users TO proxyloom;
GRANT INSERT (id_hash, scope_id, user_id, auth_version, auth_epoch, created_at,
    expires_at, last_seen_at) ON public.sessions TO proxyloom;
GRANT UPDATE (last_seen_at, reauth_at) ON public.sessions TO proxyloom;
GRANT DELETE ON public.sessions TO proxyloom;
GRANT INSERT (key_hash, window_start, attempts), UPDATE (window_start, attempts), DELETE
    ON public.identity_rate_limits TO proxyloom;
GRANT INSERT (id, scope_id, actor_id, action, outcome, account_hash, source_hash, request_id)
    ON public.identity_audit_events TO proxyloom;
REVOKE ALL ON FUNCTION public.proxyloom_identity_state_advance(),
    public.proxyloom_identity_user_advance(), public.proxyloom_identity_session_advance() FROM PUBLIC;
