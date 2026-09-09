-- Policy groups share the immutable encrypted resource history and audit log.
ALTER TABLE public.resources DROP CONSTRAINT resources_kind_check;
ALTER TABLE public.resources ADD CONSTRAINT resources_kind_check
    CHECK (kind IN ('node', 'chain', 'source', 'policy_group'));

ALTER TABLE public.resource_audit_events DROP CONSTRAINT resource_audit_events_action_check;
ALTER TABLE public.resource_audit_events ADD CONSTRAINT resource_audit_events_action_check CHECK (action IN (
    'node.create', 'node.update', 'node.delete', 'node.clone',
    'node.add_tags', 'node.remove_tags', 'node.set_enabled', 'node.override', 'node.restore_source',
    'import.commit',
    'chain.create', 'chain.update', 'chain.delete',
    'source.create', 'source.update', 'source.delete', 'source.refresh',
    'policy_group.create', 'policy_group.update', 'policy_group.delete'));
