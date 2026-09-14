package storage

import (
	"context"
	"encoding/json"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/networktest"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"time"
)

var _ networktest.Repository = (*NetworkTests)(nil)

func (s *NetworkTests) Results(ctx context.Context, scope ir.ID, f networktest.ResultFilter) ([]json.RawMessage, bool, error) {
	if scope.Validate() != nil || f.Limit < 1 || f.Limit > 200 || f.SubjectRevision < 0 || f.After.ID != "" && f.After.CreatedAt.IsZero() {
		return nil, false, jobs.ErrInvalidInput
	}
	for _, id := range []ir.ID{f.SubjectID, f.CoreBuildID, f.After.ID} {
		if id != "" && id.Validate() != nil {
			return nil, false, jobs.ErrInvalidInput
		}
	}
	if f.Type != "" && f.Type != "config_validate" && !jobs.Type(f.Type).Network() {
		return nil, false, jobs.ErrInvalidInput
	}
	var from, until *time.Time
	if f.From != "" {
		v, e := time.Parse(time.RFC3339Nano, f.From)
		if e != nil {
			return nil, false, jobs.ErrInvalidInput
		}
		from = &v
	}
	if f.Until != "" {
		v, e := time.Parse(time.RFC3339Nano, f.Until)
		if e != nil {
			return nil, false, jobs.ErrInvalidInput
		}
		until = &v
	}
	if from != nil && until != nil && !until.After(*from) {
		return nil, false, jobs.ErrInvalidInput
	}
	rows, err := s.catalog.pool.Query(ctx, `SELECT COALESCE(r.result_id,j.id)::text,j.id::text,j.batch_id::text,j.worker_id::text,j.attempt,j.type,j.state,j.verdict,COALESCE(r.result,'{}'::jsonb),j.finished_at,t.subject,t.core_identity,t.effective_limits,t.test_target_id::text,t.test_target_revision,t.dependencies,j.safe_error,
 EXISTS(SELECT 1 FROM jsonb_array_elements(t.dependencies) d LEFT JOIN public.resources h ON h.scope_id=t.scope_id AND h.id=(d->>'id')::uuid WHERE h.id IS NULL OR h.deleted_at IS NOT NULL OR NOT h.enabled OR h.head_revision<>(d->>'revision')::bigint OR h.security_epoch<>(d->>'security_epoch')::bigint),
 (SELECT head_revision FROM public.resources WHERE scope_id=t.scope_id AND id=(t.subject->>'id')::uuid)
 FROM public.test_jobs t JOIN public.jobs j ON j.id=t.job_id LEFT JOIN public.job_results r ON r.job_id=j.id AND r.attempt=j.attempt
 WHERE t.scope_id=$1 AND j.attempt>0 AND j.state IN ('succeeded','failed','canceled','timed_out') AND ($2::uuid IS NULL OR (t.subject->>'id')::uuid=$2) AND ($3::uuid IS NULL OR j.core_build_id=$3) AND ($4='' OR j.type=$4) AND ($5::uuid IS NULL OR (j.finished_at,COALESCE(r.result_id,j.id))>($10,$5)) AND ($6::timestamptz IS NULL OR j.finished_at>=$6) AND ($7::timestamptz IS NULL OR j.finished_at<$7) AND ($8='' OR r.result->'observation'->>'location'=$8) AND ($11::bigint=0 OR (t.subject->>'revision')::bigint=$11) ORDER BY j.finished_at,COALESCE(r.result_id,j.id) LIMIT $9`, dbID(scope), nullableID(f.SubjectID), nullableID(f.CoreBuildID), f.Type, nullableID(f.After.ID), from, until, f.Location, f.Limit+1, f.After.CreatedAt, f.SubjectRevision)
	if err != nil {
		return nil, false, testError(err)
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var resultID, jobID, batchID, worker ir.ID
		var target *string
		var attempt int32
		var kind, state string
		var verdict *string
		var raw, subject, core, limits, deps, safe []byte
		var date time.Time
		var targetRev *int64
		var stale bool
		var current *int64
		if err = rows.Scan(&resultID, &jobID, &batchID, &worker, &attempt, &kind, &state, &verdict, &raw, &date, &subject, &core, &limits, &target, &targetRev, &deps, &safe, &stale, &current); err != nil {
			return nil, false, testError(err)
		}
		var result runnerprotocol.ResultRequest
		var identity runnerprotocol.CoreIdentity
		if json.Unmarshal(raw, &result) != nil || json.Unmarshal(core, &identity) != nil {
			return nil, false, jobs.ErrUnavailable
		}
		location, truncated := "unknown", "none"
		if result.Observation != nil {
			location = result.Observation.Location
			truncated = result.Observation.TruncatedBy
		}
		item := map[string]any{"result_id": resultID, "job_id": jobID, "batch_id": batchID, "subject": json.RawMessage(subject), "dependencies": json.RawMessage(deps), "core_build_id": identity.CoreBuildID, "core_build_sha256": identity.BuildSHA256, "runner_id": worker, "location": location, "attempt": attempt, "type": kind, "state": state, "metrics": result.Metrics, "effective_limits": json.RawMessage(limits), "truncated_by": truncated, "stale": stale, "completed_at": date}
		if target != nil {
			item["test_target_id"] = *target
			item["test_target_revision"] = runnerprotocol.Sequence(*targetRev)
		}
		if result.Observation != nil {
			item["execution_sha256"] = result.Observation.ExecutionSHA256
			if result.Observation.CPUThrottled != nil {
				item["cpu_throttled"] = *result.Observation.CPUThrottled
			}
		}
		if verdict != nil {
			item["verdict"] = *verdict
		} else {
			item["verdict"] = "inconclusive"
		}
		if len(safe) > 0 {
			item["error"] = json.RawMessage(safe)
		} else if result.Error != nil {
			item["error"] = result.Error
		}
		if current != nil {
			item["current_subject_revision"] = runnerprotocol.Sequence(*current)
		}
		encoded, e := json.Marshal(item)
		if e != nil {
			return nil, false, jobs.ErrUnavailable
		}
		out = append(out, encoded)
	}
	more := len(out) > f.Limit
	if more {
		out = out[:f.Limit]
	}
	return out, more, testError(rows.Err())
}
