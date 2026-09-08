-- name: GetJobMetadata :one
SELECT id,scope_id,state,attempt,lease_seq,revision
FROM public.jobs WHERE scope_id=$1 AND id=$2;
