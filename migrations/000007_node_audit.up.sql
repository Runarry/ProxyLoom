-- Management mutations commit their audit event in the resource transaction.
-- Identifiers and an action allowlist are the entire audit vocabulary; no
-- names, endpoints, request bodies, credentials or configuration are accepted.
CREATE TABLE public.resource_audit_events (
    id uuid PRIMARY KEY,
    scope_id uuid NOT NULL REFERENCES public.scopes(id),
    actor_id uuid NOT NULL,
    object_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    action text NOT NULL CHECK (action IN ('node.create', 'node.update', 'node.delete',
        'node.clone', 'node.add_tags', 'node.remove_tags', 'node.set_enabled', 'import.commit')),
    outcome text NOT NULL DEFAULT 'success' CHECK (outcome = 'success'),
    request_id text NOT NULL CHECK (request_id ~ '^[A-Za-z0-9_-]{1,64}$'),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
-- Catalog mutations lock scope before writing audit. Identity authentication
-- locks the administrator before taking its scope read lock. A user FK here
-- would invert that lock order. User IDs/scopes are immutable and users cannot
-- be deleted, so a non-locking membership check provides the same association
-- without waiting for the identity row's password/session lock.
CREATE FUNCTION public.proxyloom_resource_audit_actor() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM public.users WHERE scope_id = NEW.scope_id AND id = NEW.actor_id) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'invalid_audit_actor';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER resource_audit_actor BEFORE INSERT ON public.resource_audit_events
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_resource_audit_actor();
CREATE INDEX resource_audit_scope_time ON public.resource_audit_events(scope_id, created_at, id);
CREATE INDEX resource_audit_object ON public.resource_audit_events(scope_id, object_id, revision);
CREATE TRIGGER resource_audit_immutable BEFORE UPDATE OR DELETE ON public.resource_audit_events
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER resource_audit_no_truncate BEFORE TRUNCATE ON public.resource_audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION public.proxyloom_reject_history_change();

REVOKE ALL ON public.resource_audit_events FROM PUBLIC, proxyloom;
GRANT SELECT ON public.resource_audit_events TO proxyloom;
GRANT INSERT (id, scope_id, actor_id, object_id, revision, action, request_id)
    ON public.resource_audit_events TO proxyloom;
REVOKE ALL ON FUNCTION public.proxyloom_resource_audit_actor() FROM PUBLIC;
