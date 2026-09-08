-- Immutable resource history and current query indexes. Scope is the first
-- application write lock; IDs, revisions, and epochs are service-owned.
CREATE TABLE public.scopes (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 256),
    catalog_revision bigint NOT NULL DEFAULT 0 CHECK (catalog_revision >= 0),
    auth_epoch bigint NOT NULL DEFAULT 1 CHECK (auth_epoch > 0),
    last_catalog_transaction bigint NOT NULL DEFAULT 0
);

CREATE TABLE public.resources (
    id uuid PRIMARY KEY,
    scope_id uuid NOT NULL REFERENCES public.scopes(id),
    kind text NOT NULL CHECK (kind IN ('node', 'chain')),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 256),
    head_revision bigint CHECK (head_revision > 0),
    enabled boolean NOT NULL,
    security_epoch bigint NOT NULL DEFAULT 1 CHECK (security_epoch > 0),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at timestamptz,
    UNIQUE (scope_id, id),
    UNIQUE (scope_id, id, kind),
    CHECK (deleted_at IS NULL OR NOT enabled)
);
CREATE INDEX resources_scope_page ON public.resources(scope_id, created_at, id);
CREATE INDEX resources_scope_kind ON public.resources(scope_id, kind, created_at, id);

CREATE TABLE public.resource_revisions (
    scope_id uuid NOT NULL,
    resource_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision > 0),
    schema_version integer NOT NULL CHECK (schema_version = 1),
    security_epoch bigint NOT NULL CHECK (security_epoch > 0),
    envelope bytea NOT NULL CHECK (octet_length(envelope) BETWEEN 1 AND 4194304),
    content_hmac bytea NOT NULL CHECK (octet_length(content_hmac) = 32),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_transaction bigint NOT NULL DEFAULT txid_current(),
    PRIMARY KEY (resource_id, revision),
    UNIQUE (scope_id, resource_id, revision),
    FOREIGN KEY (scope_id, resource_id) REFERENCES public.resources(scope_id, id)
);
ALTER TABLE public.resources ADD CONSTRAINT resources_head_fk
    FOREIGN KEY (scope_id, id, head_revision)
    REFERENCES public.resource_revisions(scope_id, resource_id, revision)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE public.resource_revision_wrappings (
    scope_id uuid NOT NULL,
    resource_id uuid NOT NULL,
    revision bigint NOT NULL,
    wrap_version bigint NOT NULL DEFAULT 1 CHECK (wrap_version > 0),
    wrapping bytea NOT NULL CHECK (octet_length(wrapping) BETWEEN 1 AND 4096),
    PRIMARY KEY (resource_id, revision),
    FOREIGN KEY (scope_id, resource_id, revision)
        REFERENCES public.resource_revisions(scope_id, resource_id, revision)
);

CREATE TABLE public.resource_refs (
    scope_id uuid NOT NULL,
    resource_id uuid NOT NULL,
    revision bigint NOT NULL,
    ref_path text NOT NULL CHECK (length(ref_path) BETWEEN 1 AND 128),
    target_resource_id uuid NOT NULL,
    target_revision bigint CHECK (target_revision > 0),
    expected_kind text NOT NULL CHECK (expected_kind IN ('node', 'chain')),
    PRIMARY KEY (resource_id, revision, ref_path),
    FOREIGN KEY (scope_id, resource_id, revision)
        REFERENCES public.resource_revisions(scope_id, resource_id, revision),
    FOREIGN KEY (scope_id, target_resource_id, expected_kind)
        REFERENCES public.resources(scope_id, id, kind),
    FOREIGN KEY (scope_id, target_resource_id, target_revision)
        REFERENCES public.resource_revisions(scope_id, resource_id, revision)
);
CREATE INDEX resource_refs_target ON public.resource_refs
    (scope_id, target_resource_id, resource_id, revision, ref_path);

CREATE TABLE public.resource_tags (
    scope_id uuid NOT NULL,
    resource_id uuid NOT NULL,
    tag text NOT NULL CHECK (length(tag) BETWEEN 1 AND 64),
    PRIMARY KEY (resource_id, tag),
    FOREIGN KEY (scope_id, resource_id) REFERENCES public.resources(scope_id, id)
);
CREATE INDEX resource_tags_scope_tag ON public.resource_tags(scope_id, tag, resource_id);

CREATE FUNCTION public.proxyloom_reject_history_change() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'immutable_history';
END;
$$;
CREATE TRIGGER resource_revisions_immutable BEFORE UPDATE OR DELETE ON public.resource_revisions
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER resource_refs_immutable BEFORE UPDATE OR DELETE ON public.resource_refs
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER resources_no_hard_delete BEFORE DELETE ON public.resources
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER resource_wrappings_no_delete BEFORE DELETE ON public.resource_revision_wrappings
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER resources_no_truncate BEFORE TRUNCATE ON public.resources
    FOR EACH STATEMENT EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER resource_revisions_no_truncate BEFORE TRUNCATE ON public.resource_revisions
    FOR EACH STATEMENT EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER resource_refs_no_truncate BEFORE TRUNCATE ON public.resource_refs
    FOR EACH STATEMENT EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER resource_wrappings_no_truncate BEFORE TRUNCATE ON public.resource_revision_wrappings
    FOR EACH STATEMENT EXECUTE FUNCTION public.proxyloom_reject_history_change();

CREATE FUNCTION public.proxyloom_scope_advance() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.name <> OLD.name OR NEW.auth_epoch <> OLD.auth_epoch
       OR NEW.catalog_revision <> OLD.catalog_revision + 1
       OR OLD.last_catalog_transaction = txid_current() THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'invalid_catalog_advance';
    END IF;
    NEW.last_catalog_transaction := txid_current();
    RETURN NEW;
END;
$$;
CREATE TRIGGER scopes_advance BEFORE UPDATE ON public.scopes
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_scope_advance();

CREATE FUNCTION public.proxyloom_resource_advance() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.head_revision IS NOT NULL OR NEW.security_epoch <> 1 OR NEW.deleted_at IS NOT NULL THEN
            RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'invalid_initial_resource';
        END IF;
    ELSE
        IF NEW.id <> OLD.id OR NEW.scope_id <> OLD.scope_id OR NEW.kind <> OLD.kind
           OR NEW.created_at <> OLD.created_at OR OLD.deleted_at IS NOT NULL
           OR NEW.head_revision IS NULL OR NEW.head_revision <> COALESCE(OLD.head_revision, 0) + 1
           OR NEW.security_epoch < OLD.security_epoch OR NEW.security_epoch > OLD.security_epoch + 1
           OR ((OLD.enabled AND NOT NEW.enabled) OR NEW.deleted_at IS NOT NULL)
              AND NEW.security_epoch <> OLD.security_epoch + 1 THEN
            RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'invalid_resource_advance';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER resources_advance BEFORE INSERT OR UPDATE ON public.resources
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_resource_advance();

CREATE FUNCTION public.proxyloom_revision_insert() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE current_head bigint;
BEGIN
    SELECT head_revision INTO current_head FROM public.resources
        WHERE scope_id = NEW.scope_id AND id = NEW.resource_id;
    IF NOT FOUND OR NEW.revision <> COALESCE(current_head, 0) + 1 THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'invalid_revision_sequence';
    END IF;
    NEW.created_transaction := txid_current();
    RETURN NEW;
END;
$$;
CREATE TRIGGER resource_revisions_sequence BEFORE INSERT ON public.resource_revisions
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_revision_insert();

CREATE FUNCTION public.proxyloom_revision_child_insert() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM public.resource_revisions
        WHERE scope_id = NEW.scope_id AND resource_id = NEW.resource_id
          AND revision = NEW.revision AND created_transaction = txid_current()) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'immutable_history';
    END IF;
    RETURN NEW;
END;
$$;
-- Wrappings use a separate trigger function: a record on resource_refs does
-- not have a wrap_version field, even on an unreachable PL/pgSQL branch.
CREATE FUNCTION public.proxyloom_wrapping_insert() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NEW.wrap_version <> 1 OR NOT EXISTS (SELECT 1 FROM public.resource_revisions
        WHERE scope_id = NEW.scope_id AND resource_id = NEW.resource_id
          AND revision = NEW.revision AND created_transaction = txid_current()) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'invalid_initial_wrapping';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER resource_refs_append BEFORE INSERT ON public.resource_refs
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_revision_child_insert();
CREATE TRIGGER resource_wrappings_initial BEFORE INSERT ON public.resource_revision_wrappings
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_wrapping_insert();

CREATE FUNCTION public.proxyloom_wrapping_advance() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NEW.scope_id <> OLD.scope_id OR NEW.resource_id <> OLD.resource_id
       OR NEW.revision <> OLD.revision OR NEW.wrap_version <> OLD.wrap_version + 1 THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'invalid_wrapping_advance';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER resource_wrappings_advance BEFORE UPDATE ON public.resource_revision_wrappings
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_wrapping_advance();

CREATE FUNCTION public.proxyloom_resource_complete() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE current_resource public.resources%ROWTYPE;
BEGIN
    SELECT * INTO current_resource FROM public.resources WHERE id = NEW.id;
    IF current_resource.head_revision IS NULL OR NOT EXISTS (
        SELECT 1 FROM public.resource_revisions r
        JOIN public.resource_revision_wrappings w USING (scope_id, resource_id, revision)
        WHERE r.scope_id = current_resource.scope_id AND r.resource_id = current_resource.id
          AND r.revision = current_resource.head_revision
          AND r.security_epoch = current_resource.security_epoch
    ) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'resource_head_required';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM public.scopes WHERE id = current_resource.scope_id
        AND last_catalog_transaction = txid_current()) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'catalog_advance_required';
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER resources_complete AFTER INSERT OR UPDATE ON public.resources
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.proxyloom_resource_complete();

CREATE FUNCTION public.proxyloom_revision_complete() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM public.resources WHERE scope_id = NEW.scope_id
        AND id = NEW.resource_id AND head_revision >= NEW.revision)
       OR NOT EXISTS (SELECT 1 FROM public.resource_revision_wrappings
        WHERE scope_id = NEW.scope_id AND resource_id = NEW.resource_id AND revision = NEW.revision) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'revision_head_required';
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER resource_revisions_complete AFTER INSERT ON public.resource_revisions
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION public.proxyloom_revision_complete();

-- The tag table is only a current index. Changing it requires a revision in
-- the same transaction, so a standalone index edit cannot rewrite history.
CREATE FUNCTION public.proxyloom_tags_versioned() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE tag_scope uuid; tag_resource uuid;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'versioned_tag_update_required';
    ELSIF TG_OP = 'DELETE' THEN
        tag_scope := OLD.scope_id; tag_resource := OLD.resource_id;
    ELSE
        tag_scope := NEW.scope_id; tag_resource := NEW.resource_id;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM public.resource_revisions r
        JOIN public.resources h ON h.scope_id = r.scope_id AND h.id = r.resource_id
        WHERE r.scope_id = tag_scope AND r.resource_id = tag_resource
          AND r.created_transaction = txid_current() AND r.revision >= COALESCE(h.head_revision, 0)) THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'versioned_tag_update_required';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER resource_tags_versioned BEFORE INSERT OR UPDATE OR DELETE ON public.resource_tags
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_tags_versioned();

REVOKE ALL ON public.scopes, public.resources, public.resource_revisions,
    public.resource_revision_wrappings, public.resource_refs, public.resource_tags FROM PUBLIC, proxyloom;
GRANT SELECT ON public.scopes, public.resources, public.resource_revisions,
    public.resource_revision_wrappings, public.resource_refs, public.resource_tags TO proxyloom;
GRANT INSERT (id, name) ON public.scopes TO proxyloom;
GRANT UPDATE (catalog_revision) ON public.scopes TO proxyloom;
GRANT INSERT (id, scope_id, kind, name, enabled) ON public.resources TO proxyloom;
GRANT UPDATE (name, head_revision, enabled, security_epoch, deleted_at) ON public.resources TO proxyloom;
GRANT INSERT (scope_id, resource_id, revision, schema_version, security_epoch, envelope, content_hmac)
    ON public.resource_revisions TO proxyloom;
GRANT INSERT (scope_id, resource_id, revision, wrapping) ON public.resource_revision_wrappings TO proxyloom;
GRANT UPDATE (wrap_version, wrapping) ON public.resource_revision_wrappings TO proxyloom;
GRANT INSERT (scope_id, resource_id, revision, ref_path, target_resource_id, target_revision, expected_kind)
    ON public.resource_refs TO proxyloom;
GRANT INSERT, DELETE ON public.resource_tags TO proxyloom;
REVOKE ALL ON FUNCTION public.proxyloom_reject_history_change(), public.proxyloom_scope_advance(),
    public.proxyloom_resource_advance(), public.proxyloom_revision_insert(), public.proxyloom_revision_child_insert(),
    public.proxyloom_wrapping_insert(), public.proxyloom_wrapping_advance(), public.proxyloom_resource_complete(),
    public.proxyloom_revision_complete(), public.proxyloom_tags_versioned() FROM PUBLIC;
