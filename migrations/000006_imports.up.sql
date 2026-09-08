-- Local import staging is encrypted independently from immutable catalog nodes.
-- The replay tables contain allowlisted identifiers, counters and states only.
CREATE TABLE public.import_batches (
    id uuid PRIMARY KEY,
    scope_id uuid NOT NULL REFERENCES public.scopes(id),
    actor_id uuid NOT NULL,
    job_id uuid NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','parsing','ready','failed','committed','expired')),
    format text NOT NULL CHECK (format IN ('auto','uri_list','base64_uri_list')),
    raw_envelope jsonb,
    raw_wrapping jsonb,
    candidate_count integer NOT NULL DEFAULT 0 CHECK (candidate_count BETWEEN 0 AND 5000),
    diagnostics jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(diagnostics) = 'array'),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamptz NOT NULL DEFAULT (CURRENT_TIMESTAMP + interval '7 days'),
    UNIQUE(scope_id,id),
    FOREIGN KEY(scope_id,job_id) REFERENCES public.jobs(scope_id,id) DEFERRABLE INITIALLY DEFERRED,
    CHECK ((raw_envelope IS NULL) = (raw_wrapping IS NULL)),
    CHECK (expires_at > created_at)
);
CREATE INDEX import_batches_expiration ON public.import_batches(expires_at,id);

-- Authentication locks users before scopes. A user FK acquired while holding
-- the catalog scope lock would reverse that order. User IDs/scopes are immutable
-- and users cannot be hard deleted by the runtime; validate ownership without
-- taking an actor row lock, as the business-audit migration also does.
CREATE FUNCTION public.proxyloom_import_actor() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM public.users WHERE scope_id=NEW.scope_id AND id=NEW.actor_id) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='invalid_import_actor';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER import_batches_actor BEFORE INSERT ON public.import_batches
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_import_actor();
REVOKE ALL ON FUNCTION public.proxyloom_import_actor() FROM PUBLIC;

CREATE TABLE public.import_candidates (
    id uuid PRIMARY KEY,
    scope_id uuid NOT NULL,
    batch_id uuid NOT NULL,
    ordinal integer NOT NULL CHECK (ordinal BETWEEN 0 AND 4999),
    envelope jsonb NOT NULL,
    wrapping jsonb NOT NULL,
    UNIQUE(scope_id,batch_id,id),
    UNIQUE(scope_id,batch_id,ordinal),
    FOREIGN KEY(scope_id,batch_id) REFERENCES public.import_batches(scope_id,id)
);

CREATE TABLE public.import_request_keys (
    actor_id uuid NOT NULL,
    route text NOT NULL CHECK (route IN ('create','commit')),
    key text NOT NULL CHECK (length(key) BETWEEN 1 AND 128 AND key ~ '^[A-Za-z0-9_-]+$'),
    scope_id uuid NOT NULL,
    request_hmac bytea NOT NULL CHECK (octet_length(request_hmac) = 32),
    batch_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY(actor_id,route,key),
    FOREIGN KEY(scope_id,batch_id) REFERENCES public.import_batches(scope_id,id) DEFERRABLE INITIALLY DEFERRED
);

CREATE TABLE public.import_commits (
    scope_id uuid NOT NULL,
    batch_id uuid PRIMARY KEY,
    actor_id uuid NOT NULL,
    request_hmac bytea NOT NULL CHECK (octet_length(request_hmac) = 32),
    preview_revision bigint NOT NULL CHECK (preview_revision > 0),
    revision bigint NOT NULL CHECK (revision = preview_revision + 1),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(scope_id,batch_id) REFERENCES public.import_batches(scope_id,id)
);
CREATE TABLE public.import_commit_items (
    batch_id uuid NOT NULL REFERENCES public.import_commits(batch_id),
    ordinal integer NOT NULL CHECK (ordinal BETWEEN 0 AND 4999),
    candidate_id uuid NOT NULL,
    status text NOT NULL CHECK (status IN ('created','updated','skipped')),
    resource_id uuid,
    revision bigint,
    PRIMARY KEY(batch_id,ordinal),
    UNIQUE(batch_id,candidate_id),
    CHECK ((status = 'skipped' AND resource_id IS NULL AND revision IS NULL)
        OR (status IN ('created','updated') AND resource_id IS NOT NULL AND revision > 0))
);

CREATE TRIGGER import_commits_immutable BEFORE UPDATE OR DELETE ON public.import_commits
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER import_commit_items_immutable BEFORE UPDATE OR DELETE ON public.import_commit_items
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER import_request_keys_immutable BEFORE UPDATE OR DELETE ON public.import_request_keys
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER import_commits_no_truncate BEFORE TRUNCATE ON public.import_commits
    FOR EACH STATEMENT EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER import_commit_items_no_truncate BEFORE TRUNCATE ON public.import_commit_items
    FOR EACH STATEMENT EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER import_request_keys_no_truncate BEFORE TRUNCATE ON public.import_request_keys
    FOR EACH STATEMENT EXECUTE FUNCTION public.proxyloom_reject_history_change();

REVOKE ALL ON public.import_batches, public.import_candidates, public.import_request_keys,
    public.import_commits, public.import_commit_items FROM PUBLIC, proxyloom;
GRANT SELECT, INSERT ON public.import_batches, public.import_candidates, public.import_request_keys,
    public.import_commits, public.import_commit_items TO proxyloom;
GRANT UPDATE (revision,state,raw_envelope,raw_wrapping,candidate_count,diagnostics) ON public.import_batches TO proxyloom;
GRANT DELETE ON public.import_candidates TO proxyloom;
