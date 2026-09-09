-- Refresh previews reuse encrypted import staging and its seven-day retention.
ALTER TABLE public.import_batches
    ADD COLUMN source_id uuid,
    ADD COLUMN snapshot_id uuid,
    ADD COLUMN source_revision bigint,
    ALTER COLUMN actor_id DROP NOT NULL;
ALTER TABLE public.import_batches ADD CONSTRAINT import_batches_source_check CHECK (
    (source_id IS NULL AND snapshot_id IS NULL AND source_revision IS NULL AND actor_id IS NOT NULL)
    OR (source_id IS NOT NULL AND snapshot_id IS NOT NULL AND source_revision IS NOT NULL AND source_revision > 0));
ALTER TABLE public.import_batches ADD CONSTRAINT import_batches_source_fk
    FOREIGN KEY (scope_id, source_id) REFERENCES public.resources(scope_id,id);
ALTER TABLE public.import_batches ADD CONSTRAINT import_batches_snapshot_fk
    FOREIGN KEY (scope_id, snapshot_id) REFERENCES public.source_snapshots(scope_id,id);
ALTER TABLE public.import_batches DROP CONSTRAINT import_batches_state_check;
ALTER TABLE public.import_batches ADD CONSTRAINT import_batches_state_check
    CHECK (state IN ('queued','parsing','ready','failed','committed','expired','superseded'));
CREATE UNIQUE INDEX import_batches_source_snapshot ON public.import_batches(snapshot_id) WHERE snapshot_id IS NOT NULL;
CREATE INDEX import_batches_pending_source ON public.import_batches(scope_id,source_id) WHERE state='ready';

CREATE OR REPLACE FUNCTION public.proxyloom_import_actor() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
    IF NEW.actor_id IS NULL AND NEW.source_id IS NOT NULL THEN
        RETURN NEW;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM public.users WHERE scope_id=NEW.scope_id AND id=NEW.actor_id) THEN
        RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='invalid_import_actor';
    END IF;
    RETURN NEW;
END;
$$;

-- Latest upstream input and the baseline approved for local overlays differ
-- while a manual preview is pending. Both use the source-item encryption AAD.
ALTER TABLE public.source_items
    ADD COLUMN applied_envelope jsonb,
    ADD COLUMN applied_wrapping jsonb,
    ADD COLUMN applied_revision bigint;
ALTER TABLE public.source_items ADD CONSTRAINT source_items_applied_check CHECK (
    (applied_envelope IS NULL AND applied_wrapping IS NULL AND applied_revision IS NULL)
    OR (applied_envelope IS NOT NULL AND applied_wrapping IS NOT NULL AND applied_revision IS NOT NULL AND applied_revision > 0));
GRANT UPDATE (applied_envelope,applied_wrapping,applied_revision) ON public.source_items TO proxyloom;
GRANT UPDATE (source_item_id) ON public.node_bindings TO proxyloom;
