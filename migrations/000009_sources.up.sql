-- Remote sources reuse resource revisions. Refresh jobs stay on the API worker.
ALTER TABLE public.resources DROP CONSTRAINT resources_kind_check;
ALTER TABLE public.resources ADD CONSTRAINT resources_kind_check CHECK (kind IN ('node', 'chain', 'source'));

ALTER TABLE public.resource_audit_events DROP CONSTRAINT resource_audit_events_action_check;
ALTER TABLE public.resource_audit_events ADD CONSTRAINT resource_audit_events_action_check CHECK (action IN (
    'node.create', 'node.update', 'node.delete', 'node.clone',
    'node.add_tags', 'node.remove_tags', 'node.set_enabled', 'import.commit',
    'chain.create', 'chain.update', 'chain.delete',
    'source.create', 'source.update', 'source.delete', 'source.refresh'));

DO $$
DECLARE constraint_name text;
BEGIN
    SELECT con.conname INTO constraint_name
    FROM pg_constraint con
    JOIN pg_class rel ON rel.oid = con.conrelid
    JOIN pg_namespace nsp ON nsp.oid = rel.relnamespace
    WHERE nsp.nspname = 'public' AND rel.relname = 'jobs' AND con.contype = 'c'
      AND pg_get_constraintdef(con.oid) LIKE '%import_parse%';
    IF constraint_name IS NULL THEN
        RAISE EXCEPTION 'jobs_executor_type_constraint_missing';
    END IF;
    EXECUTE format('ALTER TABLE public.jobs DROP CONSTRAINT %I', constraint_name);
END $$;
ALTER TABLE public.jobs ADD CONSTRAINT jobs_executor_type_check CHECK (
    (executor = 'api_worker' AND type IN ('import_parse', 'source_refresh') AND core_build_id IS NULL) OR
    (executor = 'runner' AND type = 'config_validate' AND core_build_id IS NOT NULL));

CREATE UNIQUE INDEX jobs_source_refresh_active ON public.jobs (scope_id, batch_id)
    WHERE type = 'source_refresh' AND batch_id IS NOT NULL AND state IN ('queued', 'leased', 'running');

CREATE TABLE public.source_snapshots (
    id uuid PRIMARY KEY,
    scope_id uuid NOT NULL,
    source_id uuid NOT NULL,
    source_revision bigint NOT NULL CHECK (source_revision > 0),
    envelope jsonb NOT NULL,
    wrapping jsonb NOT NULL,
    content_hmac bytea NOT NULL CHECK (octet_length(content_hmac) = 32),
    http_status integer CHECK (http_status BETWEEN 100 AND 599),
    content_type text CHECK (content_type IS NULL OR length(content_type) BETWEEN 1 AND 128),
    decoded_bytes integer NOT NULL CHECK (decoded_bytes >= 0),
    state text NOT NULL CHECK (state IN ('success', 'failed')),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (scope_id, id),
    FOREIGN KEY (scope_id, source_id) REFERENCES public.resources (scope_id, id)
);
CREATE INDEX source_snapshots_source ON public.source_snapshots (source_id, created_at DESC, id);

CREATE TABLE public.source_items (
    id uuid PRIMARY KEY,
    scope_id uuid NOT NULL,
    source_id uuid NOT NULL,
    external_key text CHECK (external_key IS NULL OR (length(external_key) BETWEEN 1 AND 128 AND external_key ~ '^[A-Za-z0-9._:~-]+$')),
    envelope jsonb NOT NULL,
    wrapping jsonb NOT NULL,
    base_revision bigint NOT NULL CHECK (base_revision > 0),
    last_seen_at timestamptz NOT NULL,
    state text NOT NULL CHECK (state IN ('active', 'missing')),
    UNIQUE (scope_id, id),
    UNIQUE (source_id, external_key),
    FOREIGN KEY (scope_id, source_id) REFERENCES public.resources (scope_id, id)
);
CREATE INDEX source_items_source ON public.source_items (source_id, id);

CREATE TABLE public.node_bindings (
    node_id uuid PRIMARY KEY,
    scope_id uuid NOT NULL,
    source_item_id uuid NOT NULL UNIQUE,
    binding_revision bigint NOT NULL CHECK (binding_revision > 0),
    match_method text NOT NULL CHECK (match_method IN ('stable_external_key', 'manual_binding', 'exact_fingerprint')),
    FOREIGN KEY (scope_id, node_id) REFERENCES public.resources (scope_id, id),
    FOREIGN KEY (scope_id, source_item_id) REFERENCES public.source_items (scope_id, id)
);

CREATE TABLE public.source_schedules (
    source_id uuid PRIMARY KEY,
    scope_id uuid NOT NULL,
    next_run_at timestamptz NOT NULL,
    backoff_seconds integer NOT NULL CHECK (backoff_seconds BETWEEN 60 AND 2592000),
    last_job_id uuid,
    FOREIGN KEY (scope_id, source_id) REFERENCES public.resources (scope_id, id)
);
CREATE INDEX source_schedules_due ON public.source_schedules (next_run_at, source_id);

CREATE TRIGGER source_snapshots_immutable BEFORE UPDATE OR DELETE ON public.source_snapshots
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_reject_history_change();
CREATE TRIGGER source_snapshots_no_truncate BEFORE TRUNCATE ON public.source_snapshots
    FOR EACH STATEMENT EXECUTE FUNCTION public.proxyloom_reject_history_change();

REVOKE ALL ON public.source_snapshots, public.source_items, public.node_bindings, public.source_schedules FROM PUBLIC, proxyloom;
GRANT SELECT, INSERT ON public.source_snapshots, public.source_items, public.node_bindings, public.source_schedules TO proxyloom;
GRANT UPDATE (envelope, wrapping, base_revision, last_seen_at, state) ON public.source_items TO proxyloom;
GRANT UPDATE (binding_revision, match_method) ON public.node_bindings TO proxyloom;
GRANT UPDATE (next_run_at, backoff_seconds, last_job_id) ON public.source_schedules TO proxyloom;
GRANT DELETE ON public.source_schedules TO proxyloom;
