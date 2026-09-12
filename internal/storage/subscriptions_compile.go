package storage

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/compiler"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
	"github.com/jackc/pgx/v5"
)

func (s *Subscriptions) HandleCompile(ctx context.Context, lease jobs.Lease) (jobs.Result, jobs.CommitFunc, error) {
	var request struct {
		BatchID ir.ID `json:"batch_id"`
	}
	if json.Unmarshal(lease.Payload, &request) != nil || request.BatchID != lease.Job.BatchID {
		return jobs.Result{}, nil, jobs.ErrInvalidInput
	}
	b, err := s.readBatch(ctx, s.catalog.pool, lease.Job.ScopeID, request.BatchID)
	if err != nil {
		return jobs.Result{}, nil, err
	}
	type output struct {
		target  ir.Target
		bytes   []byte
		preview string
		diags   []ir.Diagnostic
	}
	outputs := []output{}
	diagnostics := []ir.Diagnostic{}
	for _, target := range b.Frozen.Input.Spec().Targets {
		if err := ctx.Err(); err != nil {
			return jobs.Result{}, nil, err
		}
		artifact, diags, err := s.compiler.Compile(ctx, b.Frozen.Input, target)
		if err == nil {
			err = s.compiler.PublicationEvidence(b.Frozen.Input, target)
		}
		if err == nil {
			for _, preset := range b.Frozen.Input.Spec().Resources {
				if preset.Metadata.ResourceID == target.ClientPresetID {
					err = compiler.CheckPublication(artifact.Bytes, target, *preset.Payload.(*ir.ClientPreset))
					break
				}
			}
		}
		var preview string
		if err == nil {
			preview, err = compiler.RedactedNative(artifact.Bytes, target.Format)
		}
		if err != nil {
			clear(artifact.Bytes)
			var d ir.Diagnostics
			if errors.As(err, &d) {
				diagnostics = append(diagnostics, d...)
			} else {
				diagnostics = append(diagnostics, subscriptions.Diagnostic(ir.InvalidValue, b.Batch.SubscriptionID, "/targets", target.Key))
			}
			continue
		}
		outputs = append(outputs, output{target: target, bytes: artifact.Bytes, preview: preview, diags: diags})
	}
	result := jobs.Result{State: jobs.Succeeded, Verdict: jobs.Verdict("pass")}
	if len(diagnostics) > 0 {
		result.Verdict = jobs.Verdict("fail")
	}
	return result, func(ctx context.Context, tx pgx.Tx) error {
		defer func() {
			for _, o := range outputs {
				clear(o.bytes)
			}
		}()
		scope, err := dbgen.New(tx).LockScope(ctx, dbID(b.Scope))
		if err != nil {
			return err
		}
		state := "validating"
		if scope.CatalogRevision != int64(b.Batch.CatalogRevision) || scope.AuthEpoch != b.AuthEpoch {
			state = "obsolete"
		} else if len(diagnostics) > 0 {
			state = "failed"
		}
		if state == "validating" {
			for _, o := range outputs {
				artifactID, jobID := jobs.NewID(), jobs.NewID()
				build, _ := s.cores.Build(o.target.CoreBuildID)
				frozen := runnerprotocol.FrozenPayload{SchemaVersion: 1, Type: "config_validate", Core: runnerprotocol.CoreIdentity{CoreBuildID: build.ID, CoreFamily: build.Family, Version: build.Version, BuildSHA256: build.BinarySHA256, Platform: build.OS, Architecture: build.Arch, AdapterVersion: o.target.AdapterVersion}, Artifact: runnerprotocol.Artifact{ArtifactID: artifactID, Format: o.target.Format, SHA256: runnerprotocol.Digest(o.bytes), ByteLength: int64(len(o.bytes)), ContentBase64: base64.StdEncoding.EncodeToString(o.bytes)}, Limits: runnerprotocol.Limits{DurationMS: 15000}, ExecutionPolicy: runnerprotocol.ExecutionPolicy{Network: "none", TerminationGraceMS: 2000, MemoryLimitBytes: 1 << 30, ProcessLimit: 32}}
				payload, err := json.Marshal(frozen)
				if err != nil {
					return err
				}
				_, err = s.jobs.EnqueueTx(ctx, tx, jobs.EnqueueInput{ID: jobID, ScopeID: b.Scope, BatchID: b.Batch.BatchID, Executor: jobs.Runner, Type: jobs.ConfigValidate, CoreBuildID: build.ID, Payload: payload})
				clear(payload)
				if err != nil {
					return err
				}
				envelope, wrapping, err := s.seal(b.Scope, artifactID, secretbox.TableCompileOutputs, o.bytes)
				if err != nil {
					return err
				}
				digest, err := s.catalog.box.Digest(secretbox.PurposeCompileOutput, o.bytes)
				if err != nil {
					return err
				}
				summary := outputSummary(o.target)
				summary.ArtifactID = artifactID
				summary.ValidationJobID = jobID
				summary.State = "validating"
				summary.Preview, summary.PreviewTruncated = subscriptions.Preview(o.preview)
				summary.Diagnostics = subscriptions.Diagnostics(o.diags, 50)
				encoded, _ := json.Marshal(summary)
				_, err = tx.Exec(ctx, `INSERT INTO public.compile_outputs(artifact_id,scope_id,batch_id,target_key,core_build_id,descriptor,envelope,content_hmac,validation_job_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, dbID(artifactID), dbID(b.Scope), dbID(b.Batch.BatchID), o.target.Key, dbID(build.ID), encoded, envelope, digest, dbID(jobID))
				if err != nil {
					return err
				}
				_, err = tx.Exec(ctx, `INSERT INTO public.compile_output_wrappings(artifact_id,wrapping) VALUES($1,$2)`, dbID(artifactID), wrapping)
				if err != nil {
					return err
				}
			}
		}
		diags, _ := json.Marshal(diagnostics)
		_, err = tx.Exec(ctx, `UPDATE public.compile_batches SET state=$3,diagnostics=$4,revision=revision+1 WHERE scope_id=$1 AND id=$2 AND state IN ('queued','compiling')`, dbID(b.Scope), dbID(b.Batch.BatchID), state, diags)
		return err
	}, nil
}
func outputSummary(t ir.Target) subscriptions.Output {
	return subscriptions.Output{TargetKey: t.Key, CoreBuildID: t.CoreBuildID, CoreBuildSHA256: t.CoreBuildSHA256, AdapterVersion: t.AdapterVersion, ClientPresetID: t.ClientPresetID, ClientPresetRevision: subscriptions.Revision(t.ClientPresetRevision), Format: t.Format, State: "queued", Diagnostics: []ir.Diagnostic{}}
}
func (s *Subscriptions) outputRows(ctx context.Context, tx subTx, scope, batch ir.ID) ([]subscriptions.Output, [][]byte, error) {
	rows, err := tx.Query(ctx, `SELECT o.descriptor,o.content_hmac,j.state,COALESCE(j.verdict,''),j.safe_error FROM public.compile_outputs o JOIN public.jobs j ON j.id=o.validation_job_id WHERE o.scope_id=$1 AND o.batch_id=$2 ORDER BY o.target_key`, dbID(scope), dbID(batch))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	outputs := []subscriptions.Output{}
	digests := [][]byte{}
	for rows.Next() {
		var data, digest, safeError []byte
		var state, verdict string
		if err := rows.Scan(&data, &digest, &state, &verdict, &safeError); err != nil {
			return nil, nil, err
		}
		var o subscriptions.Output
		if json.Unmarshal(data, &o) != nil {
			return nil, nil, catalog.ErrCrypto
		}
		o.ValidationVerdict = verdict
		o.State = "validating"
		if state == "succeeded" && verdict == "pass" {
			o.State = "ready"
		} else if state == "succeeded" || state == "failed" || state == "canceled" || state == "timed_out" {
			o.State = "failed"
			code := "CORE_CONFIG_INVALID"
			var failure runnerprotocol.SafeError
			if len(safeError) > 0 && json.Unmarshal(safeError, &failure) == nil && runnerprotocol.KnownError(failure.Code) {
				code = failure.Code
			}
			o.Diagnostics = append(o.Diagnostics, subscriptions.Diagnostic(ir.DiagnosticCode(code), o.ClientPresetID, "/artifact", o.TargetKey))
		}
		outputs = append(outputs, o)
		digests = append(digests, digest)
	}
	return outputs, digests, rows.Err()
}
func (s *Subscriptions) aggregate(ctx context.Context, tx pgx.Tx, b *storedBatch) error {
	outputs, digests, err := s.outputRows(ctx, tx, b.Scope, b.Batch.BatchID)
	if err != nil {
		return err
	}
	prior := b.Batch.State
	var cat, auth int64
	if err := tx.QueryRow(ctx, `SELECT catalog_revision,auth_epoch FROM public.scopes WHERE id=$1`, dbID(b.Scope)).Scan(&cat, &auth); err != nil {
		return err
	}
	var published bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.publications WHERE scope_id=$1 AND batch_id=$2)`, dbID(b.Scope), dbID(b.Batch.BatchID)).Scan(&published); err != nil {
		return err
	}
	if !published && (cat != int64(b.Batch.CatalogRevision) || auth != b.AuthEpoch) {
		b.Batch.State = "obsolete"
	}
	if b.Batch.State != "failed" && b.Batch.State != "obsolete" {
		if len(outputs) == len(b.Frozen.Input.Spec().Targets) {
			b.Batch.State = "ready"
			for _, o := range outputs {
				if o.State == "failed" {
					b.Batch.State = "failed"
					break
				}
				if o.State != "ready" {
					b.Batch.State = "validating"
				}
			}
		} else {
			var state string
			err := tx.QueryRow(ctx, `SELECT state FROM public.jobs WHERE id=$1`, dbID(b.Batch.JobIDs[0])).Scan(&state)
			if err != nil {
				return err
			}
			switch state {
			case "failed", "canceled", "timed_out":
				b.Batch.State = "failed"
			case "leased", "running":
				b.Batch.State = "compiling"
			}
		}
	}
	if b.Batch.State == "ready" && b.Batch.EffectivePreviewHash == "" {
		preimage, _ := json.Marshal(struct {
			Batch, Base ir.ID
			Input       []byte
			Outputs     [][]byte
		}{b.Batch.BatchID, b.BasePublicationID, b.InputHMAC, digests})
		hash, err := s.catalog.box.Digest(secretbox.PurposePreview, preimage)
		if err != nil {
			return err
		}
		b.Batch.EffectivePreviewHash = hex.EncodeToString(hash)
	}
	if prior != b.Batch.State || b.Batch.EffectivePreviewHash != "" {
		tag, err := tx.Exec(ctx, `UPDATE public.compile_batches SET state=$3,preview_hash=NULLIF($4,''),revision=revision+1 WHERE scope_id=$1 AND id=$2 AND (state<>$3 OR COALESCE(preview_hash,'')<>$4)`, dbID(b.Scope), dbID(b.Batch.BatchID), b.Batch.State, b.Batch.EffectivePreviewHash)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			b.Batch.Revision++
		}
	}
	if len(outputs) == 0 {
		for _, t := range b.Frozen.Input.Spec().Targets {
			o := outputSummary(t)
			if b.Batch.State == "failed" {
				o.State = "failed"
			}
			outputs = append(outputs, o)
		}
	}
	for _, o := range outputs {
		if o.ValidationJobID != "" {
			b.Batch.JobIDs = append(b.Batch.JobIDs, o.ValidationJobID)
		}
	}
	if b.Batch.State == "validating" {
		b.Batch.BlockingReasons = append(b.Batch.BlockingReasons, subscriptions.Diagnostic(ir.DiagnosticCode("RUNNER_VALIDATION_PENDING"), b.Batch.SubscriptionID, "/outputs", ""))
	}
	for _, o := range outputs {
		if o.State == "failed" {
			for _, d := range o.Diagnostics {
				if d.Severity == ir.SeverityError {
					b.Batch.Diagnostics = append(b.Batch.Diagnostics, d)
				}
			}
		}
	}
	b.Batch.Outputs = outputs
	return nil
}
func (s *Subscriptions) GetBatch(ctx context.Context, a subscriptions.Actor, id ir.ID) (subscriptions.Batch, error) {
	if !validIDs(a.ScopeID, a.ID, id) {
		return subscriptions.Batch{}, catalog.ErrInvalidInput
	}
	tx, err := s.catalog.pool.Begin(ctx)
	if err != nil {
		return subscriptions.Batch{}, catalog.ErrUnavailable
	}
	defer tx.Rollback(ctx)
	if _, err = dbgen.New(tx).LockScope(ctx, dbID(a.ScopeID)); err != nil {
		return subscriptions.Batch{}, subError(err)
	}
	b, err := s.readBatch(ctx, tx, a.ScopeID, id)
	if err == nil {
		err = s.aggregate(ctx, tx, &b)
	}
	if err != nil {
		return subscriptions.Batch{}, subError(err)
	}
	if b.Batch.State == "ready" {
		profile, safetyErr := s.profile(ctx, tx, a.ScopeID, b.Batch.SubscriptionID)
		if safetyErr == nil {
			safetyErr = s.safeBatch(ctx, tx, b, profile)
		}
		if errors.Is(safetyErr, subscriptions.ErrBlocked) || errors.Is(safetyErr, pgx.ErrNoRows) || errors.Is(safetyErr, catalog.ErrNotFound) {
			b.Batch.BlockingReasons = append(b.Batch.BlockingReasons, subscriptions.Diagnostic(ir.DiagnosticCode("PUBLICATION_BLOCKED"), b.Batch.SubscriptionID, "/publication", ""))
		} else if safetyErr != nil {
			return subscriptions.Batch{}, subError(safetyErr)
		}
	}
	if b.BasePublicationID != "" {
		var previousBatch ir.ID
		if err = tx.QueryRow(ctx, `SELECT batch_id::text FROM public.publications WHERE scope_id=$1 AND id=$2`, dbID(a.ScopeID), dbID(b.BasePublicationID)).Scan(&previousBatch); err != nil {
			return subscriptions.Batch{}, subError(err)
		}
		previous, previousDigests, err := s.outputRows(ctx, tx, a.ScopeID, previousBatch)
		if err != nil {
			return subscriptions.Batch{}, subError(err)
		}
		current, currentDigests, err := s.outputRows(ctx, tx, a.ScopeID, b.Batch.BatchID)
		if err != nil {
			return subscriptions.Batch{}, subError(err)
		}
		for i := range b.Batch.Outputs {
			b.Batch.Outputs[i].Changed = b.Batch.Outputs[i].ArtifactID != ""
			for pi, p := range previous {
				if p.TargetKey == b.Batch.Outputs[i].TargetKey {
					b.Batch.Outputs[i].PreviousPreview = p.Preview
					b.Batch.Outputs[i].PreviousPreviewTruncated = p.PreviewTruncated
					for ci, o := range current {
						if o.TargetKey == p.TargetKey {
							b.Batch.Outputs[i].Changed = !hmac.Equal(currentDigests[ci], previousDigests[pi])
						}
					}
				}
			}
		}
	}
	if b.BasePublicationID == "" {
		for i := range b.Batch.Outputs {
			b.Batch.Outputs[i].Changed = b.Batch.Outputs[i].ArtifactID != ""
		}
	}
	if b.Batch.State == "ready" {
		_, err = tx.Exec(ctx, `INSERT INTO public.compile_preview_views(scope_id,actor_id,batch_id,preview_hash) VALUES($1,$2,$3,$4) ON CONFLICT(scope_id,actor_id,batch_id) DO UPDATE SET preview_hash=EXCLUDED.preview_hash`, dbID(a.ScopeID), dbID(a.ID), dbID(id), b.Batch.EffectivePreviewHash)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	b.Batch.Diagnostics = subscriptions.Diagnostics(b.Batch.Diagnostics, 200)
	return b.Batch, subError(err)
}
func (s *Subscriptions) Reconcile(ctx context.Context) error {
	rows, err := s.catalog.pool.Query(ctx, `SELECT scope_id::text,id::text FROM public.compile_batches WHERE (state IN ('queued','compiling','validating') OR (state='ready' AND EXISTS(SELECT 1 FROM public.scopes sc WHERE sc.id=compile_batches.scope_id AND (sc.catalog_revision<>compile_batches.catalog_revision OR sc.auth_epoch<>compile_batches.auth_epoch)))) AND NOT EXISTS(SELECT 1 FROM public.publications p WHERE p.batch_id=compile_batches.id) ORDER BY created_at,id LIMIT 50`)
	if err != nil {
		return subError(err)
	}
	type pair struct{ scope, id ir.ID }
	items := []pair{}
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.scope, &p.id); err != nil {
			rows.Close()
			return subError(err)
		}
		items = append(items, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return subError(err)
	}
	for _, p := range items {
		err := func() error {
			tx, err := s.catalog.pool.Begin(ctx)
			if err != nil {
				return err
			}
			defer tx.Rollback(ctx)
			if _, err = dbgen.New(tx).LockScope(ctx, dbID(p.scope)); err != nil {
				return err
			}
			b, err := s.readBatch(ctx, tx, p.scope, p.id)
			if err == nil {
				err = s.aggregate(ctx, tx, &b)
			}
			if err != nil {
				return err
			}
			return tx.Commit(ctx)
		}()
		if err != nil {
			return subError(err)
		}
	}
	return nil
}
func (s *Subscriptions) artifact(ctx context.Context, tx subTx, scope, id ir.ID) ([]byte, error) {
	var envelope, wrapping, digest []byte
	err := tx.QueryRow(ctx, `SELECT o.envelope,w.wrapping,o.content_hmac FROM public.compile_outputs o JOIN public.compile_output_wrappings w USING(artifact_id) WHERE o.scope_id=$1 AND o.artifact_id=$2`, dbID(scope), dbID(id)).Scan(&envelope, &wrapping, &digest)
	if err != nil {
		return nil, err
	}
	plain, err := s.open(scope, id, secretbox.TableCompileOutputs, envelope, wrapping)
	if err != nil {
		return nil, err
	}
	actual, err := s.catalog.box.Digest(secretbox.PurposeCompileOutput, plain)
	if err != nil || !hmac.Equal(actual, digest) {
		clear(plain)
		return nil, catalog.ErrCrypto
	}
	return plain, nil
}
