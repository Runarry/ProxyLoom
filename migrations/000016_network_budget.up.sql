CREATE TABLE public.quota_settings (
 scope_id uuid PRIMARY KEY REFERENCES public.scopes(id),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision > 0),
 settings jsonb NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE public.quota_buckets (
 scope_id uuid NOT NULL REFERENCES public.scopes(id),
 utc_day date NOT NULL,
 reserved_bytes bigint NOT NULL DEFAULT 0 CHECK(reserved_bytes >= 0),
 settled_bytes bigint NOT NULL DEFAULT 0 CHECK(settled_bytes >= 0),
 PRIMARY KEY(scope_id,utc_day)
);
CREATE TABLE public.quota_reservations (
 id uuid PRIMARY KEY,
 scope_id uuid NOT NULL,
 job_id uuid NOT NULL,
 attempt integer NOT NULL CHECK(attempt BETWEEN 1 AND 2),
 utc_day date NOT NULL,
 reserved_bytes bigint NOT NULL CHECK(reserved_bytes > 0),
 settled_bytes bigint CHECK(settled_bytes BETWEEN 0 AND reserved_bytes),
 started_at timestamptz,
 settled_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(job_id,attempt),
 FOREIGN KEY(scope_id,utc_day) REFERENCES public.quota_buckets(scope_id,utc_day),
 FOREIGN KEY(scope_id,job_id) REFERENCES public.jobs(scope_id,id),
 CHECK((settled_at IS NULL) = (settled_bytes IS NULL))
);
CREATE INDEX quota_reservations_pending ON public.quota_reservations(scope_id,utc_day) WHERE settled_at IS NULL;
REVOKE ALL ON public.quota_settings, public.quota_buckets, public.quota_reservations FROM PUBLIC, proxyloom;
GRANT SELECT, INSERT ON public.quota_settings, public.quota_buckets, public.quota_reservations TO proxyloom;
GRANT UPDATE (revision,settings,updated_at) ON public.quota_settings TO proxyloom;
GRANT UPDATE (reserved_bytes,settled_bytes) ON public.quota_buckets TO proxyloom;
GRANT UPDATE (started_at,settled_bytes,settled_at) ON public.quota_reservations TO proxyloom;

ALTER TABLE public.jobs DROP CONSTRAINT jobs_executor_type_check;
ALTER TABLE public.jobs ADD CONSTRAINT jobs_executor_type_check CHECK (
 (executor='api_worker' AND type IN ('import_parse','source_refresh','compile') AND core_build_id IS NULL) OR
 (executor='runner' AND type IN ('config_validate','connectivity','download_throughput') AND core_build_id IS NOT NULL));
ALTER TABLE public.job_results DROP CONSTRAINT job_results_settled_bytes_check;
ALTER TABLE public.job_results ADD CONSTRAINT job_results_settled_bytes_check CHECK(settled_bytes>=0);
