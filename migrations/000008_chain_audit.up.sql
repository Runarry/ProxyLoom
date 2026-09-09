-- Chain mutations reuse resource_audit_events. Append chain actions without
-- rewriting the original identity or node audit migrations.
ALTER TABLE public.resource_audit_events
    DROP CONSTRAINT resource_audit_events_action_check;
ALTER TABLE public.resource_audit_events
    ADD CONSTRAINT resource_audit_events_action_check CHECK (action IN (
        'node.create', 'node.update', 'node.delete', 'node.clone',
        'node.add_tags', 'node.remove_tags', 'node.set_enabled', 'import.commit',
        'chain.create', 'chain.update', 'chain.delete'));
