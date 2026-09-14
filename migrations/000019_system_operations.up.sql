CREATE TABLE public.system_settings (
 scope_id uuid PRIMARY KEY REFERENCES public.scopes(id),revision bigint NOT NULL CHECK(revision>0),
 settings jsonb NOT NULL,updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE public.system_setting_revisions (
 scope_id uuid NOT NULL REFERENCES public.scopes(id),revision bigint NOT NULL CHECK(revision>0),
 settings jsonb NOT NULL,actor_id uuid NOT NULL,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(scope_id,revision)
);
CREATE TABLE public.maintenance_status (
 scope_id uuid PRIMARY KEY REFERENCES public.scopes(id),last_cleanup_at timestamptz,last_backup_at timestamptz
);
ALTER TABLE public.system_audit_events ADD COLUMN event_id uuid NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE public.system_audit_events ADD COLUMN request_id text NOT NULL DEFAULT 'system';
ALTER TABLE public.system_audit_events ADD COLUMN changed_fields jsonb NOT NULL DEFAULT '[]';
ALTER TABLE public.system_audit_events ADD CONSTRAINT system_audit_event_id UNIQUE(event_id);
ALTER TABLE public.system_audit_events ADD CONSTRAINT system_audit_action CHECK(action IN ('test_target.write','test_target.delete','tests.create','settings_update','cleanup','backup','restore','job_cancel'));
ALTER TABLE public.system_audit_events ADD CONSTRAINT system_audit_result CHECK(result IN ('success','failure'));
ALTER TABLE public.system_audit_events ADD CONSTRAINT system_audit_request CHECK(request_id ~ '^[A-Za-z0-9._:-]{1,128}$');
CREATE INDEX system_audit_scope_time ON public.system_audit_events(scope_id,created_at,event_id);
ALTER TABLE public.publication_audit_events ADD COLUMN event_id uuid NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE public.publication_audit_events ADD COLUMN request_id text NOT NULL DEFAULT 'system';
CREATE UNIQUE INDEX publication_audit_event_id ON public.publication_audit_events(event_id);
ALTER TABLE public.import_batches ADD COLUMN catalog_limits jsonb NOT NULL DEFAULT '{}';
REVOKE ALL ON public.system_settings,public.system_setting_revisions,public.maintenance_status FROM PUBLIC,proxyloom;
GRANT SELECT,INSERT,UPDATE ON public.system_settings,public.maintenance_status TO proxyloom;
GRANT SELECT,INSERT ON public.system_setting_revisions TO proxyloom;
