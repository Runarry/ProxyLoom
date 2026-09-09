-- Typed source overlays and origin states. Existing bindings stay active with no patch.
ALTER TABLE public.resource_audit_events DROP CONSTRAINT resource_audit_events_action_check;
ALTER TABLE public.resource_audit_events ADD CONSTRAINT resource_audit_events_action_check CHECK (action IN (
    'node.create', 'node.update', 'node.delete', 'node.clone',
    'node.add_tags', 'node.remove_tags', 'node.set_enabled', 'node.override', 'node.restore_source',
    'import.commit',
    'chain.create', 'chain.update', 'chain.delete',
    'source.create', 'source.update', 'source.delete', 'source.refresh'));

ALTER TABLE public.source_items DROP CONSTRAINT source_items_state_check;
ALTER TABLE public.source_items ADD CONSTRAINT source_items_state_check CHECK (state IN ('active', 'missing', 'conflict'));

ALTER TABLE public.node_bindings
    ADD COLUMN override_envelope jsonb,
    ADD COLUMN wrapping jsonb,
    ADD COLUMN state text NOT NULL DEFAULT 'active';
ALTER TABLE public.node_bindings ADD CONSTRAINT node_bindings_state_check CHECK (state IN ('active', 'stale', 'conflict'));
ALTER TABLE public.node_bindings ADD CONSTRAINT node_bindings_override_present_check CHECK ((override_envelope IS NULL) = (wrapping IS NULL));

REVOKE ALL ON public.node_bindings FROM PUBLIC, proxyloom;
GRANT SELECT, INSERT ON public.node_bindings TO proxyloom;
GRANT UPDATE (binding_revision, match_method, override_envelope, wrapping, state) ON public.node_bindings TO proxyloom;
