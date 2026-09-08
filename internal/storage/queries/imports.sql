-- name: ReadImportBatchMetadata :one
SELECT id,scope_id,job_id,revision,state,candidate_count,created_at,expires_at,diagnostics
FROM public.import_batches WHERE scope_id=$1 AND id=$2;

-- name: ReadImportCandidatePage :many
SELECT id,ordinal,envelope,wrapping FROM public.import_candidates
WHERE scope_id=$1 AND batch_id=$2 AND ordinal>$3 ORDER BY ordinal LIMIT $4;

-- name: ReadImportCommitMetadata :one
SELECT scope_id,batch_id,actor_id,request_hmac,preview_revision,revision,created_at
FROM public.import_commits WHERE scope_id=$1 AND batch_id=$2;

-- name: ReadImportCommitItems :many
SELECT candidate_id,status,resource_id,revision FROM public.import_commit_items
WHERE batch_id=$1 ORDER BY ordinal;
