-- Durable execution metadata is separate from encrypted immutable payloads.
CREATE TABLE public.job_batches (
    id uuid PRIMARY KEY,
    scope_id uuid NOT NULL REFERENCES public.scopes(id),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    effective_limits jsonb NOT NULL,
    cancel_requested_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (scope_id, id)
);

CREATE TABLE public.jobs (
    id uuid PRIMARY KEY,
    scope_id uuid NOT NULL REFERENCES public.scopes(id),
    -- May identify a test parent or a domain-owned import/compile batch.
    batch_id uuid,
    executor text NOT NULL CHECK (executor IN ('api_worker','runner')),
    type text NOT NULL,
    state text NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','leased','running','succeeded','failed','canceled','timed_out')),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    attempt integer NOT NULL DEFAULT 0 CHECK (attempt BETWEEN 0 AND 2),
    lease_seq bigint NOT NULL DEFAULT 0 CHECK (lease_seq >= 0),
    worker_id uuid,
    lease_until timestamptz,
    core_build_id uuid,
    cancel_requested_at timestamptz,
    verdict text CHECK (verdict IN ('pass','fail','inconclusive')),
    safe_error jsonb,
    event_seq bigint NOT NULL DEFAULT 0 CHECK (event_seq >= 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz,
    finished_at timestamptz,
    UNIQUE (scope_id, id),
    CHECK ((executor='api_worker' AND type='import_parse' AND core_build_id IS NULL) OR
           (executor='runner' AND type='config_validate' AND core_build_id IS NOT NULL)),
    CHECK ((state IN ('leased','running')) = (lease_until IS NOT NULL)),
    CHECK (state NOT IN ('leased','running') OR (attempt > 0 AND lease_seq > 0 AND worker_id IS NOT NULL)),
    CHECK ((state IN ('succeeded','failed','canceled','timed_out')) = (finished_at IS NOT NULL)),
    CHECK (state <> 'succeeded' OR verdict IS NOT NULL)
);
CREATE INDEX jobs_claim ON public.jobs (executor, type, created_at, id) WHERE state='queued' AND cancel_requested_at IS NULL;
CREATE INDEX jobs_expired ON public.jobs (lease_until, id) WHERE state IN ('leased','running');
CREATE INDEX jobs_scope_list ON public.jobs (scope_id, created_at, id);
CREATE INDEX jobs_batch ON public.jobs (scope_id, batch_id, id) WHERE batch_id IS NOT NULL;

CREATE TABLE public.job_payloads (
    scope_id uuid NOT NULL,
    job_id uuid NOT NULL,
    envelope jsonb NOT NULL,
    PRIMARY KEY (scope_id, job_id),
    FOREIGN KEY (scope_id, job_id) REFERENCES public.jobs(scope_id,id)
);
CREATE TABLE public.job_payload_wrappings (
    scope_id uuid NOT NULL,
    job_id uuid NOT NULL,
    wrapping jsonb NOT NULL,
    wrap_version bigint NOT NULL DEFAULT 1 CHECK (wrap_version > 0),
    PRIMARY KEY (scope_id, job_id),
    FOREIGN KEY (scope_id, job_id) REFERENCES public.job_payloads(scope_id,job_id)
);
CREATE TABLE public.job_events (
    job_id uuid NOT NULL REFERENCES public.jobs(id),
    seq bigint NOT NULL CHECK (seq > 0),
    attempt integer NOT NULL CHECK (attempt BETWEEN 0 AND 2),
    lease_seq bigint NOT NULL CHECK (lease_seq >= 0),
    event_id uuid NOT NULL,
    event_hash text NOT NULL CHECK (event_hash ~ '^[0-9a-f]{64}$'),
    event jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (job_id,seq),
    UNIQUE (job_id,attempt,lease_seq,event_id)
);
CREATE TABLE public.job_results (
    result_id uuid PRIMARY KEY,
    job_id uuid NOT NULL REFERENCES public.jobs(id),
    worker_id uuid NOT NULL,
    attempt integer NOT NULL CHECK (attempt BETWEEN 1 AND 2),
    lease_seq bigint NOT NULL CHECK (lease_seq > 0),
    result_hash text NOT NULL CHECK (result_hash ~ '^[0-9a-f]{64}$'),
    result jsonb NOT NULL,
    settled_bytes bigint NOT NULL DEFAULT 0 CHECK (settled_bytes = 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (job_id,attempt,lease_seq)
);

-- Immutable payload/receipt tables have no UPDATE grant. Mutable metadata is
-- restricted to queue-controlled columns; the API never runs schema changes.
REVOKE ALL ON public.job_batches, public.jobs, public.job_payloads, public.job_payload_wrappings,
    public.job_events, public.job_results FROM PUBLIC, proxyloom;
GRANT SELECT ON public.job_batches, public.jobs, public.job_payloads, public.job_payload_wrappings,
    public.job_events, public.job_results TO proxyloom;
GRANT INSERT (id,scope_id,effective_limits) ON public.job_batches TO proxyloom;
GRANT UPDATE (revision,cancel_requested_at) ON public.job_batches TO proxyloom;
GRANT INSERT (id,scope_id,batch_id,executor,type,core_build_id) ON public.jobs TO proxyloom;
GRANT UPDATE (state,revision,attempt,lease_seq,worker_id,lease_until,cancel_requested_at,
    verdict,safe_error,event_seq,started_at,finished_at) ON public.jobs TO proxyloom;
GRANT INSERT (scope_id,job_id,envelope) ON public.job_payloads TO proxyloom;
GRANT INSERT (scope_id,job_id,wrapping) ON public.job_payload_wrappings TO proxyloom;
GRANT UPDATE (wrapping,wrap_version) ON public.job_payload_wrappings TO proxyloom;
GRANT INSERT (job_id,seq,attempt,lease_seq,event_id,event_hash,event) ON public.job_events TO proxyloom;
GRANT INSERT (result_id,job_id,worker_id,attempt,lease_seq,result_hash,result,settled_bytes) ON public.job_results TO proxyloom;
