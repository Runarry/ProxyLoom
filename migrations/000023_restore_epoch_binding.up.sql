-- Use an unambiguous local variable for the restored registration epoch.
CREATE OR REPLACE FUNCTION public.proxyloom_reset_after_restore() RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE restored_epoch uuid:=gen_random_uuid();
BEGIN
 -- Called only by the isolated restore command after authenticating the whole
 -- archive. Runtime/Runner identities cannot invoke this function.
 PERFORM set_config('proxyloom.restore',txid_current()::text,true);
 UPDATE public.control_authorization SET epoch=restored_epoch WHERE singleton;
 DELETE FROM public.sessions;
 UPDATE public.subscription_tokens SET revoked_at=clock_timestamp(),revision=revision+1 WHERE revoked_at IS NULL;
 UPDATE public.scopes SET auth_epoch=auth_epoch+1,catalog_revision=catalog_revision+1;
 WITH pending AS (
  UPDATE public.quota_reservations r SET settled_bytes=CASE WHEN r.attempt<=j.attempt THEN r.reserved_bytes ELSE 0 END,settled_at=clock_timestamp()
  FROM public.jobs j WHERE j.id=r.job_id AND r.settled_at IS NULL
  RETURNING r.scope_id,r.utc_day,r.reserved_bytes,r.settled_bytes
 ), totals AS (
  SELECT scope_id,utc_day,sum(reserved_bytes) reserved,sum(settled_bytes) settled FROM pending GROUP BY scope_id,utc_day
 ) UPDATE public.quota_buckets b SET reserved_bytes=b.reserved_bytes-t.reserved,settled_bytes=b.settled_bytes+t.settled
 FROM totals t WHERE t.scope_id=b.scope_id AND t.utc_day=b.utc_day;
 UPDATE public.jobs SET state='canceled',revision=revision+1,lease_seq=lease_seq+1,lease_until=NULL,
  cancel_requested_at=clock_timestamp(),finished_at=clock_timestamp(),verdict='inconclusive',
  safe_error='{"code":"CANCELED","message":"The task was canceled."}'::jsonb
 WHERE finished_at IS NULL;
 UPDATE public.compile_batches SET state='obsolete',revision=revision+1 WHERE state IN ('queued','compiling','validating','ready')
  AND NOT EXISTS(SELECT 1 FROM public.publications p WHERE p.batch_id=compile_batches.id);
 INSERT INTO public.system_audit_events(scope_id,action,request_id) SELECT id,'restore','isolated-restore' FROM public.scopes;
 RETURN restored_epoch;
END;
$$;
REVOKE ALL ON FUNCTION public.proxyloom_reset_after_restore() FROM PUBLIC,proxyloom;
