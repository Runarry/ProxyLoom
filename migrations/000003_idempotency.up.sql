-- Only typed, validated replay metadata is stored. Secret responses are never
-- accepted here. A deferred constraint forbids committed pending claims.
CREATE TABLE public.idempotency_keys (
    principal_id uuid NOT NULL,
    route_key text NOT NULL CHECK (length(route_key) BETWEEN 1 AND 128),
    key text NOT NULL CHECK (length(key) BETWEEN 1 AND 128),
    scope_id uuid NOT NULL REFERENCES public.scopes(id),
    request_hmac bytea NOT NULL CHECK (octet_length(request_hmac) = 32),
    http_status integer CHECK (http_status BETWEEN 200 AND 299),
    resource_id uuid,
    resource_revision bigint CHECK (resource_revision > 0),
    operation_id uuid,
    status text CHECK (status IN ('created', 'updated', 'deleted', 'revoked', 'accepted')),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP + INTERVAL '24 hours',
    PRIMARY KEY (principal_id, route_key, key),
    CHECK ((resource_id IS NULL) = (resource_revision IS NULL)),
    CHECK ((status IS NULL) = (http_status IS NULL)),
    CHECK (status IS NULL OR resource_id IS NOT NULL OR operation_id IS NOT NULL),
    CHECK (status IS NULL OR (status = 'created' AND http_status = 201 AND resource_id IS NOT NULL)
        OR (status IN ('updated', 'deleted', 'revoked') AND http_status IN (200, 204) AND resource_id IS NOT NULL)
        OR (status = 'accepted' AND http_status = 202 AND operation_id IS NOT NULL)),
    FOREIGN KEY (scope_id, resource_id, resource_revision)
        REFERENCES public.resource_revisions(scope_id, resource_id, revision)
);

CREATE FUNCTION public.proxyloom_idempotency_finalize() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF OLD.status IS NOT NULL OR NEW.principal_id <> OLD.principal_id OR NEW.route_key <> OLD.route_key
       OR NEW.key <> OLD.key OR NEW.scope_id <> OLD.scope_id OR NEW.request_hmac <> OLD.request_hmac
       OR NEW.created_at <> OLD.created_at OR NEW.expires_at <> OLD.expires_at OR NEW.status IS NULL THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'immutable_idempotency_receipt';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER idempotency_finalize BEFORE UPDATE ON public.idempotency_keys
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_idempotency_finalize();
CREATE TRIGGER idempotency_no_delete BEFORE DELETE ON public.idempotency_keys
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER idempotency_no_truncate BEFORE TRUNCATE ON public.idempotency_keys
    FOR EACH STATEMENT EXECUTE FUNCTION public.proxyloom_reject_history_change();

CREATE FUNCTION public.proxyloom_idempotency_complete() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM public.idempotency_keys WHERE principal_id = NEW.principal_id
        AND route_key = NEW.route_key AND key = NEW.key AND status IS NOT NULL) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'idempotency_receipt_required';
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER idempotency_complete AFTER INSERT ON public.idempotency_keys
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.proxyloom_idempotency_complete();

REVOKE ALL ON public.idempotency_keys FROM PUBLIC, proxyloom;
GRANT SELECT ON public.idempotency_keys TO proxyloom;
GRANT INSERT (principal_id, route_key, key, scope_id, request_hmac) ON public.idempotency_keys TO proxyloom;
GRANT UPDATE (http_status, resource_id, resource_revision, operation_id, status) ON public.idempotency_keys TO proxyloom;
REVOKE ALL ON FUNCTION public.proxyloom_idempotency_finalize(), public.proxyloom_idempotency_complete() FROM PUBLIC;
