-- Maintenance is reachable only through bounded, scope-serialized functions.
ALTER TABLE public.system_audit_events ALTER COLUMN actor_id DROP NOT NULL;
ALTER TABLE public.subscription_operations ADD COLUMN created_at timestamptz NOT NULL DEFAULT clock_timestamp();
ALTER TABLE public.core_builds ADD COLUMN disable_reason text CHECK(length(disable_reason) BETWEEN 1 AND 256);
GRANT UPDATE(disable_reason) ON public.core_builds TO proxyloom;
ALTER TABLE public.compile_batches ADD COLUMN dependencies_registered boolean NOT NULL DEFAULT false;
CREATE TABLE public.compile_dependencies (
 scope_id uuid NOT NULL,batch_id uuid NOT NULL,resource_id uuid NOT NULL,revision bigint NOT NULL,
 PRIMARY KEY(batch_id,resource_id),
 FOREIGN KEY(scope_id,batch_id) REFERENCES public.compile_batches(scope_id,id),
 FOREIGN KEY(scope_id,resource_id,revision) REFERENCES public.resource_revisions(scope_id,resource_id,revision)
);
CREATE INDEX compile_dependencies_revision ON public.compile_dependencies(scope_id,resource_id,revision);
GRANT SELECT,INSERT ON public.compile_dependencies TO proxyloom;
GRANT UPDATE(dependencies_registered) ON public.compile_batches TO proxyloom;
INSERT INTO public.compile_dependencies(scope_id,batch_id,resource_id,revision)
 SELECT DISTINCT p.scope_id,p.batch_id,d.resource_id,d.revision FROM public.publications p
 JOIN public.publication_dependencies d ON d.publication_id=p.id;
UPDATE public.compile_batches b SET dependencies_registered=true WHERE EXISTS(SELECT 1 FROM public.publications p WHERE p.batch_id=b.id);

CREATE FUNCTION public.proxyloom_retention_deadline() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
DECLARE settings jsonb;
BEGIN
 SELECT s.settings INTO settings FROM public.system_settings s WHERE s.scope_id=NEW.scope_id;
 IF TG_TABLE_NAME='import_batches' THEN
  NEW.expires_at:=NEW.created_at+make_interval(days=>COALESCE((settings#>>'{retention,import_days}')::integer,7));
 ELSE
  NEW.expires_at:=NEW.created_at+make_interval(hours=>COALESCE((settings#>>'{retention,idempotency_hours}')::integer,24));
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER import_retention BEFORE INSERT ON public.import_batches FOR EACH ROW EXECUTE FUNCTION public.proxyloom_retention_deadline();
CREATE TRIGGER idempotency_retention BEFORE INSERT ON public.idempotency_keys FOR EACH ROW EXECUTE FUNCTION public.proxyloom_retention_deadline();
REVOKE ALL ON FUNCTION public.proxyloom_retention_deadline() FROM PUBLIC;

CREATE OR REPLACE FUNCTION public.proxyloom_reject_history_change() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,public AS $$
BEGIN
 -- A caller may set a custom GUC, but cannot assume the maintenance function's
 -- owner. Normal UPDATE, DELETE and TRUNCATE remain forbidden, including admin
 -- writes outside this function's transaction marker.
 IF TG_OP='DELETE' AND current_setting('proxyloom.maintenance',true)=txid_current()::text
    AND current_user=pg_get_userbyid((SELECT proowner FROM pg_proc WHERE oid='public.proxyloom_cleanup(uuid,integer)'::regprocedure)) THEN
  RETURN OLD;
 END IF;
 RAISE EXCEPTION USING ERRCODE='23514',MESSAGE='immutable_history';
END;
$$;

CREATE FUNCTION public.proxyloom_cleanup(p_scope uuid,p_limit integer) RETURNS jsonb
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE settings jsonb; cutoff timestamptz:=clock_timestamp(); events_before timestamptz; results_before timestamptz;
 audits_before timestamptz; imports_before timestamptz; publications_before timestamptz; receipts_before timestamptz;
 keep_publications integer; ids uuid[]; parent_ids uuid[]; n integer; total integer:=0; rev record;
BEGIN
 IF p_limit<1 OR p_limit>1000 THEN RAISE EXCEPTION 'invalid_cleanup_limit'; END IF;
 PERFORM 1 FROM public.scopes WHERE id=p_scope FOR UPDATE;
 IF NOT FOUND THEN RAISE EXCEPTION 'unknown_cleanup_scope'; END IF;
 SELECT s.settings INTO settings FROM public.system_settings s WHERE s.scope_id=p_scope;
 IF COALESCE((settings->>'cleanup_paused')::boolean,false) THEN RETURN jsonb_build_object('paused',true,'deleted',0); END IF;
 PERFORM set_config('proxyloom.maintenance',txid_current()::text,true);
 events_before:=cutoff-make_interval(days=>COALESCE((settings#>>'{retention,job_event_days}')::integer,7));
 results_before:=cutoff-make_interval(days=>COALESCE((settings#>>'{retention,test_result_days}')::integer,30));
 audits_before:=cutoff-make_interval(days=>COALESCE((settings#>>'{retention,audit_days}')::integer,180));
 imports_before:=cutoff-make_interval(days=>COALESCE((settings#>>'{retention,import_days}')::integer,7));
 publications_before:=cutoff-make_interval(days=>COALESCE((settings#>>'{retention,publication_days}')::integer,90));
 receipts_before:=cutoff-make_interval(hours=>COALESCE((settings#>>'{retention,idempotency_hours}')::integer,24));
 keep_publications:=COALESCE((settings#>>'{retention,publication_count}')::integer,20);

 DELETE FROM public.job_events WHERE (job_id,seq) IN (
  SELECT e.job_id,e.seq FROM public.job_events e JOIN public.jobs j ON j.id=e.job_id
  WHERE j.scope_id=p_scope AND j.finished_at IS NOT NULL AND e.created_at<events_before ORDER BY e.created_at LIMIT p_limit);
 GET DIAGNOSTICS n=ROW_COUNT; total:=total+n;
 DELETE FROM public.identity_audit_events WHERE id IN (SELECT id FROM public.identity_audit_events WHERE (scope_id=p_scope OR scope_id IS NULL) AND created_at<audits_before ORDER BY created_at LIMIT p_limit);
 GET DIAGNOSTICS n=ROW_COUNT; total:=total+n;
 DELETE FROM public.resource_audit_events WHERE id IN (SELECT id FROM public.resource_audit_events WHERE scope_id=p_scope AND created_at<audits_before ORDER BY created_at LIMIT p_limit);
 GET DIAGNOSTICS n=ROW_COUNT; total:=total+n;
 DELETE FROM public.publication_audit_events WHERE id IN (SELECT id FROM public.publication_audit_events WHERE scope_id=p_scope AND created_at<audits_before ORDER BY created_at LIMIT p_limit);
 GET DIAGNOSTICS n=ROW_COUNT; total:=total+n;
 DELETE FROM public.system_audit_events WHERE id IN (SELECT id FROM public.system_audit_events WHERE scope_id=p_scope AND created_at<audits_before ORDER BY created_at LIMIT p_limit);
 GET DIAGNOSTICS n=ROW_COUNT; total:=total+n;

 -- Expired receipts never delete their target, and unfinished operations keep
 -- their replay identity regardless of age.
 DELETE FROM public.idempotency_keys WHERE (principal_id,route_key,key) IN (
  SELECT principal_id,route_key,key FROM public.idempotency_keys r WHERE scope_id=p_scope
  AND expires_at<cutoff AND created_at<receipts_before
  AND NOT EXISTS(SELECT 1 FROM public.jobs j WHERE (j.id=r.operation_id OR j.batch_id=r.operation_id) AND j.finished_at IS NULL) LIMIT p_limit);
 GET DIAGNOSTICS n=ROW_COUNT; total:=total+n;
 DELETE FROM public.test_operations WHERE (scope_id,actor_id,route,key) IN (
  SELECT scope_id,actor_id,route,key FROM public.test_operations r WHERE scope_id=p_scope AND created_at<receipts_before
  AND NOT EXISTS(SELECT 1 FROM public.jobs j WHERE (j.id=r.operation_id OR j.batch_id=r.operation_id) AND j.finished_at IS NULL) LIMIT p_limit);
 DELETE FROM public.subscription_operations WHERE (scope_id,actor_id,route,key) IN (
  SELECT scope_id,actor_id,route,key FROM public.subscription_operations r WHERE scope_id=p_scope AND created_at<receipts_before
  AND NOT EXISTS(SELECT 1 FROM public.jobs j WHERE (j.id=r.operation_id OR j.batch_id=r.operation_id) AND j.finished_at IS NULL) LIMIT p_limit);
 DELETE FROM public.import_request_keys WHERE (actor_id,route,key) IN (
  SELECT r.actor_id,r.route,r.key FROM public.import_request_keys r JOIN public.import_batches b ON b.id=r.batch_id
  JOIN public.jobs j ON j.id=b.job_id WHERE r.scope_id=p_scope AND r.created_at<receipts_before AND j.finished_at IS NOT NULL LIMIT p_limit);

 -- Purge a whole test batch only after every child has ended and aged out.
 SELECT array_agg(id) INTO parent_ids FROM (
  SELECT b.id FROM public.job_batches b WHERE b.scope_id=p_scope
  AND EXISTS(SELECT 1 FROM public.jobs j WHERE j.batch_id=b.id)
  AND NOT EXISTS(SELECT 1 FROM public.jobs j WHERE j.batch_id=b.id AND (j.finished_at IS NULL OR j.finished_at>=results_before))
  AND NOT EXISTS(SELECT 1 FROM public.test_operations r WHERE r.operation_id=b.id)
  ORDER BY b.created_at,b.id LIMIT greatest(1,p_limit/100)) picked;
 SELECT array_agg(id) INTO ids FROM public.jobs WHERE batch_id=ANY(parent_ids);
 DELETE FROM public.test_jobs WHERE job_id=ANY(ids);
 DELETE FROM public.job_results WHERE job_id=ANY(ids);
 DELETE FROM public.job_events WHERE job_id=ANY(ids);
 DELETE FROM public.job_payload_wrappings WHERE job_id=ANY(ids);
 DELETE FROM public.job_payloads WHERE job_id=ANY(ids);
 DELETE FROM public.quota_reservations WHERE job_id=ANY(ids) AND settled_at IS NOT NULL;
 DELETE FROM public.jobs WHERE id=ANY(ids);
 GET DIAGNOSTICS n=ROW_COUNT; total:=total+n;
 DELETE FROM public.job_batches WHERE id=ANY(parent_ids);

 -- Raw previews can expire while keeping the commit's small replay metadata.
 SELECT array_agg(id) INTO ids FROM (
  SELECT b.id FROM public.import_batches b JOIN public.jobs j ON j.id=b.job_id
  WHERE b.scope_id=p_scope AND j.finished_at IS NOT NULL AND b.created_at<imports_before
  AND (b.raw_envelope IS NOT NULL OR EXISTS(SELECT 1 FROM public.import_candidates c WHERE c.batch_id=b.id))
  ORDER BY b.created_at,b.id LIMIT greatest(1,p_limit/100)) picked;
 DELETE FROM public.import_candidates WHERE batch_id=ANY(ids);
 GET DIAGNOSTICS n=ROW_COUNT; total:=total+n;
 UPDATE public.import_batches SET raw_envelope=NULL,raw_wrapping=NULL,revision=revision+1,
  state=CASE WHEN state IN ('queued','parsing','ready') THEN 'expired' ELSE state END WHERE id=ANY(ids);

 -- Keep the newest N OR the last D days, active heads, rollback ancestry, and
 -- previews which still use a publication as their comparison baseline.
 SELECT array_agg(id) INTO ids FROM (
  SELECT p.id FROM public.publications p WHERE p.scope_id=p_scope AND p.created_at<publications_before
  AND (SELECT count(*) FROM public.publications newer WHERE newer.scope_id=p_scope AND newer.profile_id=p.profile_id AND newer.generation>=p.generation)>keep_publications
  AND NOT EXISTS(SELECT 1 FROM public.publication_heads h WHERE h.publication_id=p.id)
  AND NOT EXISTS(SELECT 1 FROM public.publications child WHERE child.source_publication_id=p.id)
  AND NOT EXISTS(SELECT 1 FROM public.compile_batches b WHERE b.base_publication_id=p.id AND b.created_at>=publications_before)
  AND NOT EXISTS(SELECT 1 FROM public.subscription_operations r WHERE r.operation_id=p.id)
  ORDER BY p.created_at,p.id LIMIT p_limit) picked;
 DELETE FROM public.publication_dependencies WHERE publication_id=ANY(ids);
 DELETE FROM public.publications WHERE id=ANY(ids);
 GET DIAGNOSTICS n=ROW_COUNT; total:=total+n;

 SELECT array_agg(id) INTO ids FROM (
  SELECT b.id FROM public.compile_batches b WHERE b.scope_id=p_scope AND b.created_at<publications_before
  AND NOT EXISTS(SELECT 1 FROM public.publications p WHERE p.batch_id=b.id)
  AND NOT EXISTS(SELECT 1 FROM public.jobs j WHERE j.batch_id=b.id AND j.finished_at IS NULL)
  AND NOT EXISTS(SELECT 1 FROM public.subscription_operations r WHERE r.operation_id=b.id)
  ORDER BY b.created_at,b.id LIMIT p_limit) picked;
 DELETE FROM public.compile_preview_views WHERE batch_id=ANY(ids);
 DELETE FROM public.compile_dependencies WHERE batch_id=ANY(ids);
 DELETE FROM public.compile_output_wrappings WHERE artifact_id IN(SELECT artifact_id FROM public.compile_outputs WHERE batch_id=ANY(ids));
 DELETE FROM public.compile_outputs WHERE batch_id=ANY(ids);
 DELETE FROM public.compile_batch_wrappings WHERE batch_id=ANY(ids);
 DELETE FROM public.compile_batches WHERE id=ANY(ids);
 GET DIAGNOSTICS n=ROW_COUNT; total:=total+n;

 -- Old unregistered batches are encrypted. Retain revisions conservatively
 -- until those batches expire, rather than guessing their dependency graph.
 IF NOT EXISTS(SELECT 1 FROM public.compile_batches WHERE scope_id=p_scope AND NOT dependencies_registered) THEN
  FOR rev IN SELECT r.resource_id,r.revision FROM public.resource_revisions r JOIN public.resources h ON h.id=r.resource_id
   WHERE r.scope_id=p_scope AND r.created_at<publications_before AND r.revision<>h.head_revision
   AND NOT EXISTS(SELECT 1 FROM public.resource_refs f WHERE f.target_resource_id=r.resource_id AND f.target_revision=r.revision)
   AND NOT EXISTS(SELECT 1 FROM public.publication_dependencies d WHERE d.resource_id=r.resource_id AND d.revision=r.revision)
   AND NOT EXISTS(SELECT 1 FROM public.compile_dependencies d WHERE d.resource_id=r.resource_id AND d.revision=r.revision)
   AND NOT EXISTS(SELECT 1 FROM public.compile_batches b WHERE b.profile_id=r.resource_id AND b.profile_revision=r.revision)
   AND NOT EXISTS(SELECT 1 FROM public.idempotency_keys k WHERE k.resource_id=r.resource_id AND k.resource_revision=r.revision)
   AND NOT EXISTS(SELECT 1 FROM public.import_commit_items i WHERE i.resource_id=r.resource_id AND i.revision=r.revision)
   AND NOT EXISTS(SELECT 1 FROM public.test_jobs t, jsonb_array_elements(t.dependencies) d WHERE t.scope_id=p_scope AND d->>'id'=r.resource_id::text AND (d->>'revision')::bigint=r.revision)
   ORDER BY r.created_at,r.resource_id,r.revision LIMIT p_limit
  LOOP
   DELETE FROM public.resource_refs WHERE resource_id=rev.resource_id AND revision=rev.revision;
   DELETE FROM public.resource_revision_wrappings WHERE resource_id=rev.resource_id AND revision=rev.revision;
   DELETE FROM public.resource_revisions WHERE resource_id=rev.resource_id AND revision=rev.revision;
   total:=total+1;
  END LOOP;
 END IF;
 DELETE FROM public.test_target_revisions r WHERE (scope_id,target_id,revision) IN (
  SELECT r.scope_id,r.target_id,r.revision FROM public.test_target_revisions r JOIN public.test_targets h ON h.id=r.target_id
  WHERE r.scope_id=p_scope AND r.revision<>h.revision AND r.created_at<results_before
  AND NOT EXISTS(SELECT 1 FROM public.test_jobs j WHERE j.test_target_id=r.target_id AND j.test_target_revision=r.revision) LIMIT p_limit);
 INSERT INTO public.maintenance_status(scope_id,last_cleanup_at) VALUES(p_scope,cutoff)
 ON CONFLICT(scope_id) DO UPDATE SET last_cleanup_at=EXCLUDED.last_cleanup_at;
 IF total>0 THEN INSERT INTO public.system_audit_events(scope_id,action,request_id) VALUES(p_scope,'cleanup','maintenance'); END IF;
 RETURN jsonb_build_object('paused',false,'deleted',total);
END;
$$;
REVOKE ALL ON FUNCTION public.proxyloom_cleanup(uuid,integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.proxyloom_cleanup(uuid,integer) TO proxyloom;
