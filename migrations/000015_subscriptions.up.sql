ALTER TABLE public.resources DROP CONSTRAINT resources_kind_check;
ALTER TABLE public.resources ADD CONSTRAINT resources_kind_check CHECK (kind IN ('node','chain','source','policy_group','routing_profile','rule_set','dns_profile','client_preset','subscription_profile'));
ALTER TABLE public.resource_refs DROP CONSTRAINT resource_refs_expected_kind_check;
ALTER TABLE public.resource_refs ADD CONSTRAINT resource_refs_expected_kind_check CHECK (expected_kind IN ('node','chain','policy_group','rule_set','routing_profile','dns_profile','client_preset'));
ALTER TABLE public.resource_audit_events DROP CONSTRAINT resource_audit_events_action_check;
ALTER TABLE public.resource_audit_events ADD CONSTRAINT resource_audit_events_action_check CHECK (action IN (
 'node.create','node.update','node.delete','node.clone','node.add_tags','node.remove_tags','node.set_enabled','node.override','node.restore_source',
 'import.commit','chain.create','chain.update','chain.delete','source.create','source.update','source.delete','source.refresh',
 'policy_group.create','policy_group.update','policy_group.delete','routing_profile.create','routing_profile.update','routing_profile.delete',
 'rule_set.create','rule_set.update','rule_set.delete','dns_profile.create','dns_profile.update','dns_profile.delete',
 'subscription_profile.create','subscription_profile.update','subscription_profile.delete','subscription_profile.clone'));

ALTER TABLE public.jobs DROP CONSTRAINT jobs_executor_type_check;
ALTER TABLE public.jobs ADD CONSTRAINT jobs_executor_type_check CHECK (
 (executor='api_worker' AND type IN ('import_parse','source_refresh','compile') AND core_build_id IS NULL) OR
 (executor='runner' AND type='config_validate' AND core_build_id IS NOT NULL));

CREATE TABLE public.core_builds (
 id uuid PRIMARY KEY, manifest jsonb NOT NULL, enabled boolean NOT NULL DEFAULT true,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0), registered_at timestamptz NOT NULL DEFAULT clock_timestamp(), disabled_at timestamptz
);
CREATE TABLE public.compile_batches (
 id uuid PRIMARY KEY, scope_id uuid NOT NULL REFERENCES public.scopes(id), profile_id uuid NOT NULL,
 profile_revision bigint NOT NULL, catalog_revision bigint NOT NULL, auth_epoch bigint NOT NULL,
 envelope jsonb NOT NULL, input_hmac bytea NOT NULL CHECK(octet_length(input_hmac)=32),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 state text NOT NULL DEFAULT 'queued' CHECK(state IN ('queued','compiling','validating','ready','failed','obsolete')),
 diagnostics jsonb NOT NULL DEFAULT '[]', preview_hash text, base_publication_id uuid,
 compile_job_id uuid NOT NULL REFERENCES public.jobs(id), created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(scope_id,id), UNIQUE(scope_id,id,profile_id),
 FOREIGN KEY(scope_id,profile_id,profile_revision) REFERENCES public.resource_revisions(scope_id,resource_id,revision)
);
CREATE INDEX compile_batches_pending ON public.compile_batches(created_at,id) WHERE state IN ('queued','compiling','validating','ready');
CREATE TABLE public.compile_batch_wrappings (
 scope_id uuid NOT NULL, batch_id uuid NOT NULL, wrapping jsonb NOT NULL, wrap_version bigint NOT NULL DEFAULT 1,
 PRIMARY KEY(scope_id,batch_id), FOREIGN KEY(scope_id,batch_id) REFERENCES public.compile_batches(scope_id,id)
);
CREATE TABLE public.compile_outputs (
 artifact_id uuid PRIMARY KEY, scope_id uuid NOT NULL, batch_id uuid NOT NULL, target_key text NOT NULL,
 core_build_id uuid NOT NULL REFERENCES public.core_builds(id), descriptor jsonb NOT NULL,
 envelope jsonb NOT NULL, content_hmac bytea NOT NULL CHECK(octet_length(content_hmac)=32),
 validation_job_id uuid NOT NULL UNIQUE REFERENCES public.jobs(id),
 UNIQUE(scope_id,batch_id,target_key), FOREIGN KEY(scope_id,batch_id) REFERENCES public.compile_batches(scope_id,id)
);
CREATE TABLE public.compile_output_wrappings (
 artifact_id uuid PRIMARY KEY REFERENCES public.compile_outputs(artifact_id), wrapping jsonb NOT NULL,
 wrap_version bigint NOT NULL DEFAULT 1
);
CREATE TABLE public.publications (
 id uuid PRIMARY KEY, scope_id uuid NOT NULL, profile_id uuid NOT NULL, generation bigint NOT NULL CHECK(generation>0),
 batch_id uuid NOT NULL, source_publication_id uuid REFERENCES public.publications(id),
 created_by uuid NOT NULL, created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(scope_id,profile_id,generation), UNIQUE(scope_id,profile_id,id),
 FOREIGN KEY(scope_id,batch_id,profile_id) REFERENCES public.compile_batches(scope_id,id,profile_id)
);
CREATE TABLE public.publication_heads (
 scope_id uuid NOT NULL, profile_id uuid NOT NULL, publication_id uuid NOT NULL,
 PRIMARY KEY(scope_id,profile_id), FOREIGN KEY(scope_id,profile_id,publication_id) REFERENCES public.publications(scope_id,profile_id,id)
);
CREATE TABLE public.publication_dependencies (
 publication_id uuid NOT NULL REFERENCES public.publications(id), scope_id uuid NOT NULL,
 resource_id uuid NOT NULL, revision bigint NOT NULL, security_epoch bigint NOT NULL,
 PRIMARY KEY(publication_id,resource_id),
 FOREIGN KEY(scope_id,resource_id,revision) REFERENCES public.resource_revisions(scope_id,resource_id,revision)
);
CREATE INDEX publication_dependencies_resource ON public.publication_dependencies(scope_id,resource_id);
-- A publication references one whole batch. Outputs cannot be assembled from different batches.
CREATE TABLE public.compile_preview_views (
 scope_id uuid NOT NULL, actor_id uuid NOT NULL, batch_id uuid NOT NULL, preview_hash text NOT NULL,
 PRIMARY KEY(scope_id,actor_id,batch_id), FOREIGN KEY(scope_id,batch_id) REFERENCES public.compile_batches(scope_id,id)
);
CREATE TABLE public.subscription_tokens (
 id uuid PRIMARY KEY, scope_id uuid NOT NULL, profile_id uuid NOT NULL, public_id text NOT NULL UNIQUE,
 secret_digest bytea NOT NULL CHECK(octet_length(secret_digest)=32), name text NOT NULL,
 allowed_targets text[] NOT NULL CHECK(cardinality(allowed_targets)>0), auth_epoch bigint NOT NULL,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0), expires_at timestamptz, revoked_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 FOREIGN KEY(scope_id,profile_id) REFERENCES public.resources(scope_id,id)
);
CREATE INDEX subscription_tokens_profile ON public.subscription_tokens(scope_id,profile_id,created_at,id);
CREATE TABLE public.subscription_operations (
 scope_id uuid NOT NULL, actor_id uuid NOT NULL, route text NOT NULL, key text NOT NULL,
 request_hmac bytea NOT NULL CHECK(octet_length(request_hmac)=32), operation_id uuid NOT NULL,
 PRIMARY KEY(scope_id,actor_id,route,key)
);
CREATE TABLE public.publication_audit_events (
 id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, scope_id uuid NOT NULL, actor_id uuid NOT NULL,
 object_id uuid NOT NULL, action text NOT NULL CHECK(action IN ('compile','publish','rollback','token_issue','token_revoke','core_disable','export')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

REVOKE ALL ON public.core_builds,public.compile_batches,public.compile_batch_wrappings,public.compile_outputs,public.compile_output_wrappings,
 public.publications,public.publication_heads,public.publication_dependencies,public.compile_preview_views,public.subscription_tokens,
 public.subscription_operations,public.publication_audit_events FROM PUBLIC,proxyloom;
GRANT SELECT,INSERT ON public.core_builds,public.compile_batches,public.compile_batch_wrappings,public.compile_outputs,public.compile_output_wrappings,
 public.publications,public.publication_heads,public.publication_dependencies,public.compile_preview_views,public.subscription_tokens,
 public.subscription_operations,public.publication_audit_events TO proxyloom;
GRANT UPDATE(enabled,revision,disabled_at) ON public.core_builds TO proxyloom;
GRANT UPDATE(state,revision,diagnostics,preview_hash) ON public.compile_batches TO proxyloom;
GRANT UPDATE(wrapping,wrap_version) ON public.compile_batch_wrappings,public.compile_output_wrappings TO proxyloom;
GRANT UPDATE(publication_id) ON public.publication_heads TO proxyloom;
GRANT UPDATE(preview_hash) ON public.compile_preview_views TO proxyloom;
GRANT UPDATE(revision,revoked_at) ON public.subscription_tokens TO proxyloom;
GRANT USAGE ON SEQUENCE public.publication_audit_events_id_seq TO proxyloom;
