-- DNS profiles and reviewed client presets share encrypted immutable history.
ALTER TABLE public.resources DROP CONSTRAINT resources_kind_check;
ALTER TABLE public.resources ADD CONSTRAINT resources_kind_check
    CHECK (kind IN ('node', 'chain', 'source', 'policy_group', 'routing_profile', 'rule_set', 'dns_profile', 'client_preset'));

ALTER TABLE public.resource_audit_events DROP CONSTRAINT resource_audit_events_action_check;
ALTER TABLE public.resource_audit_events ADD CONSTRAINT resource_audit_events_action_check CHECK (action IN (
    'node.create', 'node.update', 'node.delete', 'node.clone',
    'node.add_tags', 'node.remove_tags', 'node.set_enabled', 'node.override', 'node.restore_source',
    'import.commit', 'chain.create', 'chain.update', 'chain.delete',
    'source.create', 'source.update', 'source.delete', 'source.refresh',
    'policy_group.create', 'policy_group.update', 'policy_group.delete',
    'routing_profile.create', 'routing_profile.update', 'routing_profile.delete',
    'rule_set.create', 'rule_set.update', 'rule_set.delete',
    'dns_profile.create', 'dns_profile.update', 'dns_profile.delete'));

-- Initial provisioning advances a preset from NULL to revision one. After
-- that, even internal callers cannot revise, rename, disable or soft-delete it.
CREATE FUNCTION public.proxyloom_preserve_client_preset() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    IF OLD.kind = 'client_preset' AND OLD.head_revision IS NOT NULL THEN
        RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'immutable_client_preset';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER resources_client_preset_immutable BEFORE UPDATE ON public.resources
    FOR EACH ROW EXECUTE FUNCTION public.proxyloom_preserve_client_preset();
