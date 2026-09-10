-- Routing profiles and rule sets use immutable encrypted catalog revisions.
ALTER TABLE public.resources DROP CONSTRAINT resources_kind_check;
ALTER TABLE public.resources ADD CONSTRAINT resources_kind_check
    CHECK (kind IN ('node', 'chain', 'source', 'policy_group', 'routing_profile', 'rule_set'));

ALTER TABLE public.resource_refs DROP CONSTRAINT resource_refs_expected_kind_check;
ALTER TABLE public.resource_refs ADD CONSTRAINT resource_refs_expected_kind_check
    CHECK (expected_kind IN ('node', 'chain', 'policy_group', 'rule_set'));

ALTER TABLE public.resource_audit_events DROP CONSTRAINT resource_audit_events_action_check;
ALTER TABLE public.resource_audit_events ADD CONSTRAINT resource_audit_events_action_check CHECK (action IN (
    'node.create', 'node.update', 'node.delete', 'node.clone',
    'node.add_tags', 'node.remove_tags', 'node.set_enabled', 'node.override', 'node.restore_source',
    'import.commit', 'chain.create', 'chain.update', 'chain.delete',
    'source.create', 'source.update', 'source.delete', 'source.refresh',
    'policy_group.create', 'policy_group.update', 'policy_group.delete',
    'routing_profile.create', 'routing_profile.update', 'routing_profile.delete',
    'rule_set.create', 'rule_set.update', 'rule_set.delete'));
