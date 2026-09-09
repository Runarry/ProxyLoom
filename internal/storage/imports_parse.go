package storage

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Runarry/ProxyLoom/internal/importparse"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/origin"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5"
)

type preparedImportCandidate struct {
	id                 ir.ID
	index              int
	envelope, wrapping []byte
}

// HandleParse performs decoding and encryption before opening the completion
// transaction. Only CompleteTx may invoke its callback, while holding the job's
// current fencing token. Cancelled or replaced workers cannot publish candidates.
func (s *Imports) HandleParse(ctx context.Context, lease jobs.Lease) (jobs.Result, jobs.CommitFunc, error) {
	if lease.Job.Executor != jobs.APIWorker || lease.Job.Type != jobs.ImportParse || !lease.Identity.Valid() || lease.Identity.JobID != lease.Job.ID || !validIDs(lease.Job.ScopeID, lease.Job.BatchID) {
		return jobs.Result{}, nil, jobs.ErrInvalidInput
	}
	var payload struct {
		BatchID ir.ID `json:"batch_id"`
	}
	if json.Unmarshal(lease.Payload, &payload) != nil || payload.BatchID != lease.Job.BatchID {
		return jobs.Result{}, nil, jobs.ErrInvalidInput
	}
	var format, state string
	var envelope, wrapping []byte
	err := s.catalog.pool.QueryRow(ctx, `SELECT format,state,raw_envelope,raw_wrapping FROM public.import_batches WHERE scope_id=$1 AND id=$2 AND job_id=$3`, dbID(lease.Job.ScopeID), dbID(lease.Job.BatchID), dbID(lease.Job.ID)).Scan(&format, &state, &envelope, &wrapping)
	if err != nil {
		return jobs.Result{}, nil, importError(err)
	}
	if state != "queued" && state != "parsing" {
		return jobs.Result{}, nil, imports.ErrStateConflict
	}
	raw, err := s.open(lease.Job.ScopeID, secretbox.TableImportBatches, lease.Job.BatchID, envelope, wrapping)
	if err != nil {
		return jobs.Result{}, nil, err
	}
	defer clear(raw)
	parserFormat := map[string]string{"auto": "auto", "uri_list": "text", "base64_uri_list": "base64"}[format]
	parsed, parseErr := importparse.Parse(ctx, parserFormat, raw)
	if ctx.Err() != nil {
		return jobs.Result{}, nil, ctx.Err()
	}
	diagnostics := ir.Diagnostics{}
	finalState := "ready"
	result := jobs.Result{State: jobs.Succeeded, Verdict: jobs.Pass}
	prepared := make([]preparedImportCandidate, 0, len(parsed.Candidates))
	if parseErr != nil {
		if !errors.As(parseErr, &diagnostics) {
			diagnostics = ir.Diagnostics{{Code: "IMPORT_PARSE_FAILED", Severity: ir.SeverityError, FieldPath: "/text", Message: "The import input could not be parsed."}}
		}
		finalState = "failed"
		result.State = jobs.Failed
		result.Verdict = jobs.Fail
		result.Error = runnerprotocol.Safe("INVALID_CONFIG")
	} else {
		if len(parsed.Candidates) > imports.MaxCandidates {
			return jobs.Result{}, nil, imports.ErrInvalidInput
		}
		records, err := s.importRecords(ctx, lease.Job.ScopeID)
		if err != nil {
			return jobs.Result{}, nil, err
		}
		seen := map[string]bool{}
		for i, c := range parsed.Candidates {
			if err := ctx.Err(); err != nil {
				return jobs.Result{}, nil, err
			}
			id, err := importID()
			if err != nil {
				return jobs.Result{}, nil, err
			}
			name := c.Name
			if name == "" {
				name = fmt.Sprintf("Imported node %d", i+1)
			}
			candidate := importCandidate{BatchID: lease.Job.BatchID, Index: i, Name: name, State: "invalid", Metadata: c.Metadata, Diagnostics: c.Diagnostics}
			if candidate.Diagnostics == nil {
				candidate.Diagnostics = ir.Diagnostics{}
			}
			if c.Status == importparse.StatusValid && c.Node != nil {
				candidate.State = "new"
				candidate.Node, err = json.Marshal(c.Node)
				if err != nil {
					return jobs.Result{}, nil, imports.ErrUnavailable
				}
				fingerprint, err := s.connectionFingerprint(*c.Node)
				if err != nil {
					return jobs.Result{}, nil, err
				}
				s.applyOriginMatch(&candidate, origin.Item{ExternalKey: origin.ExternalKeyFromMetadata(c.Metadata), Fingerprint: fingerprint,
					Name: name, Protocol: c.Node.Protocol, Host: c.Node.Endpoint.Host, Port: c.Node.Endpoint.Port}, records)
				if seen[fingerprint] {
					candidate.State = "conflict"
					candidate.Diagnostics = append(candidate.Diagnostics, ir.Diagnostic{Code: "IMPORT_DUPLICATE_INPUT", Severity: ir.SeverityWarning, FieldPath: "/node", Message: "Another candidate has the same connection. Confirm duplicate creation or skip it."})
				}
				seen[fingerprint] = true
				// Invalid display metadata cannot turn a valid connection into a
				// candidate that fails only after the user selects it.
				r, err := candidate.resource(lease.Job.ScopeID, id)
				if err != nil || r.Validate() != nil {
					candidate.State = "invalid"
					candidate.Node = nil
					candidate.Name = ""
					candidate.Diagnostics = append(candidate.Diagnostics, ir.Diagnostic{Code: "IMPORT_INVALID_NAME", Severity: ir.SeverityError, FieldPath: "/name", Message: "The imported name is not valid."})
				}
			}
			plain, err := json.Marshal(candidate)
			if err != nil {
				return jobs.Result{}, nil, imports.ErrUnavailable
			}
			envelope, wrapping, err := s.seal(lease.Job.ScopeID, secretbox.TableImportCandidates, id, plain)
			clear(plain)
			if err != nil {
				return jobs.Result{}, nil, err
			}
			prepared = append(prepared, preparedImportCandidate{id, i, envelope, wrapping})
		}
	}
	diagnosticJSON, err := json.Marshal(diagnostics)
	if err != nil {
		return jobs.Result{}, nil, imports.ErrUnavailable
	}
	return result, func(ctx context.Context, tx pgx.Tx) error {
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM public.import_batches WHERE scope_id=$1 AND id=$2 AND job_id=$3 FOR UPDATE`, dbID(lease.Job.ScopeID), dbID(lease.Job.BatchID), dbID(lease.Job.ID)).Scan(&state); err != nil {
			return importError(err)
		}
		if state != "queued" && state != "parsing" {
			return imports.ErrStateConflict
		}
		// pgx batches retain bounded memory and avoid one network round trip per
		// candidate. This remains one transaction with the job result.
		batch := &pgx.Batch{}
		for _, c := range prepared {
			batch.Queue(`INSERT INTO public.import_candidates(id,scope_id,batch_id,ordinal,envelope,wrapping) VALUES($1,$2,$3,$4,$5,$6)`, dbID(c.id), dbID(lease.Job.ScopeID), dbID(lease.Job.BatchID), c.index, c.envelope, c.wrapping)
		}
		if len(prepared) > 0 {
			results := tx.SendBatch(ctx, batch)
			if err := results.Close(); err != nil {
				return importError(err)
			}
		}
		_, err := tx.Exec(ctx, `UPDATE public.import_batches SET state=$3,revision=revision+1,candidate_count=$4,diagnostics=$5 WHERE scope_id=$1 AND id=$2`, dbID(lease.Job.ScopeID), dbID(lease.Job.BatchID), finalState, len(prepared), diagnosticJSON)
		return importError(err)
	}, nil
}

func (s *Imports) connectionFingerprint(node ir.Node) (string, error) {
	canonical, err := importparse.CanonicalConnection(node)
	if err != nil {
		return "", imports.ErrInvalidInput
	}
	defer clear(canonical)
	digest, err := s.catalog.box.Digest(secretbox.PurposeImportConnection, canonical)
	if err != nil {
		return "", imports.ErrUnavailable
	}
	return hex.EncodeToString(digest), nil
}

func (s *Imports) applyOriginMatch(candidate *importCandidate, item origin.Item, records []origin.Record) {
	decision, ok := origin.Match(item, records)
	if !ok {
		return
	}
	switch decision.Kind {
	case origin.Identity, origin.Duplicate:
		candidate.MatchMethod = string(decision.Method)
		if decision.Ambiguous {
			candidate.State = "conflict"
			diagnostic := ir.Diagnostic{Code: "IMPORT_DUPLICATE_EXISTING", Severity: ir.SeverityWarning, FieldPath: "/node", Message: "Several existing nodes share this connection; choose a target explicitly."}
			if decision.Kind == origin.Identity {
				diagnostic.Code = importparse.IdentityAmbiguous
				diagnostic.Message = "Several existing nodes could match; choose a target explicitly."
			}
			candidate.Diagnostics = append(candidate.Diagnostics, diagnostic)
			return
		}
		candidate.State = "matched"
		candidate.ExistingID = decision.NodeID
		candidate.ExistingRevision = decision.Revision
	case origin.Suggest:
		candidate.Diagnostics = append(candidate.Diagnostics, ir.Diagnostic{Code: importparse.IdentitySuggestion, Severity: ir.SeverityWarning, FieldPath: "/node", Message: "A similar existing node is a suggestion only and was not merged."})
	}
}

func (s *Imports) importRecords(ctx context.Context, scope ir.ID) ([]origin.Record, error) {
	rows, err := s.catalog.pool.Query(ctx, `SELECT r.scope_id,r.resource_id,r.revision,r.schema_version,r.security_epoch,r.envelope,r.content_hmac,w.wrapping,w.wrap_version
		FROM public.resources h JOIN public.resource_revisions r ON r.scope_id=h.scope_id AND r.resource_id=h.id AND r.revision=h.head_revision
		JOIN public.resource_revision_wrappings w ON w.scope_id=r.scope_id AND w.resource_id=r.resource_id AND w.revision=r.revision
		WHERE h.scope_id=$1 AND h.kind='node' AND h.deleted_at IS NULL ORDER BY h.id`, dbID(scope))
	if err != nil {
		return nil, importError(err)
	}
	defer rows.Close()
	records := []origin.Record{}
	for rows.Next() {
		var row dbgen.GetResourceRevisionRow
		if err := rows.Scan(&row.ScopeID, &row.ResourceID, &row.Revision, &row.SchemaVersion, &row.SecurityEpoch, &row.Envelope, &row.ContentHmac, &row.Wrapping, &row.WrapVersion); err != nil {
			return nil, importError(err)
		}
		r, err := s.catalog.open(row)
		if err != nil {
			return nil, importError(err)
		}
		node, ok := r.Payload.(*ir.Node)
		if !ok {
			return nil, imports.ErrUnavailable
		}
		fingerprint, err := s.connectionFingerprint(*node)
		if err != nil {
			return nil, err
		}
		records = append(records, origin.Record{NodeID: r.Metadata.ResourceID, Revision: r.Metadata.Revision, Fingerprint: fingerprint,
			Name: r.Metadata.Name, Protocol: node.Protocol, Host: node.Endpoint.Host, Port: node.Endpoint.Port})
	}
	return records, importError(rows.Err())
}
