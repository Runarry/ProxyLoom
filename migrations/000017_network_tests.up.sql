CREATE TABLE public.test_targets (
 id uuid PRIMARY KEY, scope_id uuid NOT NULL REFERENCES public.scopes(id),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0), name text NOT NULL,
 enabled boolean NOT NULL DEFAULT true, config jsonb NOT NULL,
 safety_checked_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(), deleted_at timestamptz,
 UNIQUE(scope_id,id)
);
CREATE TABLE public.test_target_revisions (
 scope_id uuid NOT NULL, target_id uuid NOT NULL, revision bigint NOT NULL,
 config jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(scope_id,target_id,revision),
 FOREIGN KEY(scope_id,target_id) REFERENCES public.test_targets(scope_id,id)
);
CREATE TABLE public.test_operations (
 scope_id uuid NOT NULL REFERENCES public.scopes(id), actor_id uuid NOT NULL,
 route text NOT NULL, key text NOT NULL, request_hmac bytea NOT NULL,
 operation_id uuid NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(scope_id,actor_id,route,key)
);
CREATE TABLE public.test_jobs (
 job_id uuid PRIMARY KEY REFERENCES public.jobs(id), scope_id uuid NOT NULL,
 subject jsonb NOT NULL, dependencies jsonb NOT NULL, test_target_id uuid NOT NULL,
 test_target_revision bigint NOT NULL, core_identity jsonb NOT NULL, effective_limits jsonb NOT NULL,
 FOREIGN KEY(scope_id,job_id) REFERENCES public.jobs(scope_id,id),
 FOREIGN KEY(scope_id,test_target_id,test_target_revision) REFERENCES public.test_target_revisions(scope_id,target_id,revision)
);
CREATE INDEX test_jobs_subject ON public.test_jobs(scope_id,(subject->>'id'));
CREATE TABLE public.system_audit_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
 scope_id uuid NOT NULL REFERENCES public.scopes(id),actor_id uuid NOT NULL,
 object_id uuid, action text NOT NULL, result text NOT NULL DEFAULT 'success',
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
REVOKE ALL ON public.test_targets,public.test_target_revisions,public.test_operations,public.test_jobs,public.system_audit_events FROM PUBLIC,proxyloom;
GRANT SELECT,INSERT ON public.test_targets,public.test_target_revisions,public.test_operations,public.test_jobs,public.system_audit_events TO proxyloom;
GRANT UPDATE(revision,name,enabled,config,safety_checked_at,deleted_at) ON public.test_targets TO proxyloom;
GRANT USAGE ON SEQUENCE public.system_audit_events_id_seq TO proxyloom;
