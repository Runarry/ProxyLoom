package storage

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"math"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func validateImportCommit(input imports.CommitInput) error {
	if !validIDs(input.ScopeID, input.PrincipalID, input.BatchID) || input.ExpectedRevision < 1 || input.ExpectedRevision == math.MaxInt64 || input.SourceRevision < 0 || !validImportKey(input.IdempotencyKey) || len(input.Decisions) < 1 || len(input.Decisions) > imports.MaxCandidates {
		return imports.ErrInvalidInput
	}
	if (catalog.MutationAudit{PrincipalID: input.PrincipalID, ObjectID: input.BatchID, Revision: input.ExpectedRevision, RequestID: input.RequestID, Action: catalog.AuditImportCommit}).Validate() != nil {
		return imports.ErrInvalidInput
	}
	seen := map[ir.ID]bool{}
	targets := map[ir.ID]bool{}
	for _, d := range input.Decisions {
		if d.CandidateID.Validate() != nil || seen[d.CandidateID] || d.ExpectedBindingRevision < 0 {
			return imports.ErrInvalidInput
		}
		seen[d.CandidateID] = true
		switch d.Action {
		case "create", "skip":
			if d.ResourceID != "" || d.ExpectedRevision != 0 || d.ExpectedBindingRevision != 0 {
				return imports.ErrInvalidInput
			}
		case "update", "bind":
			if d.ResourceID.Validate() != nil || d.ExpectedRevision < 1 || targets[d.ResourceID] {
				return imports.ErrInvalidInput
			}
			targets[d.ResourceID] = true
		default:
			return imports.ErrInvalidInput
		}
		if d.Override != nil {
			if d.Action == "skip" || apicontract.ValidateDTO("NodePatchRequest", d.Override) != nil {
				return imports.ErrInvalidInput
			}
		}
	}
	return nil
}

func (s *Imports) Commit(ctx context.Context, input imports.CommitInput) (imports.Commit, error) {
	if err := validateImportCommit(input); err != nil {
		return imports.Commit{}, err
	}
	canonical, err := json.Marshal(struct {
		Scope, Actor, Batch ir.ID
		Revision            int64
		SourceRevision      int64 `json:",omitempty"`
		Decisions           []imports.Decision
	}{input.ScopeID, input.PrincipalID, input.BatchID, input.ExpectedRevision, input.SourceRevision, input.Decisions})
	if err != nil || len(canonical) > secretbox.MaxDigestBytes {
		return imports.Commit{}, imports.ErrInvalidInput
	}
	defer clear(canonical)
	digest, err := s.catalog.box.Digest(secretbox.PurposeImportCommit, canonical)
	if err != nil {
		return imports.Commit{}, imports.ErrUnavailable
	}
	if err := s.expireBatch(ctx, input.ScopeID, input.BatchID); err != nil {
		return imports.Commit{}, err
	}
	var receipt imports.Commit
	err = s.catalog.transactRaw(ctx, input.ScopeID, func(t *catalogTx) error {
		var revision int64
		var state string
		var expired bool
		var sourceID pgtype.UUID
		var sourceRevision pgtype.Int8
		err := t.tx.QueryRow(ctx, `SELECT revision,state,expires_at<=clock_timestamp(),source_id,source_revision FROM public.import_batches WHERE scope_id=$1 AND id=$2 FOR UPDATE`, dbID(input.ScopeID), dbID(input.BatchID)).Scan(&revision, &state, &expired, &sourceID, &sourceRevision)
		if err != nil {
			return err
		}
		if input.IdempotencyKey != "" {
			_, _, err := s.key(ctx, t.tx, input.ScopeID, input.PrincipalID, "commit", input.IdempotencyKey, digest, input.BatchID)
			if err != nil {
				return err
			}
		}
		if state == "committed" {
			var oldDigest []byte
			var actor ir.ID
			if err := t.tx.QueryRow(ctx, "SELECT actor_id,request_hmac FROM public.import_commits WHERE scope_id=$1 AND batch_id=$2", dbID(input.ScopeID), dbID(input.BatchID)).Scan(&actor, &oldDigest); err != nil {
				return err
			}
			if actor != input.PrincipalID || !hmac.Equal(oldDigest, digest) {
				return imports.ErrIdempotencyConflict
			}
			receipt, err = s.readImportReceipt(ctx, t.tx, input.ScopeID, input.BatchID)
			receipt.Replayed = true
			return err
		}
		if state == "expired" || expired {
			return imports.ErrExpired
		}
		if state == "superseded" {
			return imports.ErrStateConflict
		}
		if revision != input.ExpectedRevision {
			return imports.ErrRevisionConflict
		}
		if state != "ready" {
			return imports.ErrStateConflict
		}
		if sourceID.Valid {
			if err := s.validateSourcePreview(ctx, t, input, irID(sourceID), sourceRevision.Int64); err != nil {
				return err
			}
		} else {
			if input.SourceRevision != 0 {
				return imports.ErrUnsupported
			}
			for _, decision := range input.Decisions {
				if decision.Action == "bind" || decision.ExpectedBindingRevision != 0 {
					return imports.ErrUnsupported
				}
			}
		}
		ids := make([]pgtype.UUID, 0, len(input.Decisions))
		for _, d := range input.Decisions {
			ids = append(ids, dbID(d.CandidateID))
		}
		rows, err := t.tx.Query(ctx, `SELECT id,ordinal,envelope,wrapping FROM public.import_candidates WHERE scope_id=$1 AND batch_id=$2 AND id=ANY($3::uuid[])`, dbID(input.ScopeID), dbID(input.BatchID), ids)
		if err != nil {
			return err
		}
		candidates := make(map[ir.ID]importCandidate, len(ids))
		for rows.Next() {
			var id ir.ID
			var index int
			var envelope, wrapping []byte
			if err := rows.Scan(&id, &index, &envelope, &wrapping); err != nil {
				rows.Close()
				return err
			}
			c, err := s.openCandidate(input.ScopeID, input.BatchID, id, index, envelope, wrapping)
			if err != nil {
				rows.Close()
				return err
			}
			candidates[id] = c
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(candidates) != len(input.Decisions) {
			return imports.ErrInvalidInput
		}
		if sourceID.Valid {
			for _, decision := range input.Decisions {
				if err := validateSourceDecision(ctx, t, irID(sourceID), candidates[decision.CandidateID], decision); err != nil {
					return err
				}
			}
		}
		receipt = imports.Commit{BatchID: input.BatchID, Revision: apicontract.Revision(revision + 1), Items: make([]imports.CommitItem, 0, len(input.Decisions))}
		// Every selection is resolved by ID across the entire batch; pagination
		// neither limits membership nor changes the transaction's atomicity.
		for _, decision := range input.Decisions {
			if sourceID.Valid {
				item, err := s.commitSourceDecision(ctx, t, irID(sourceID), candidates[decision.CandidateID], decision)
				if err != nil {
					return err
				}
				receipt.Items = append(receipt.Items, item)
				continue
			}
			item := imports.CommitItem{CandidateID: decision.CandidateID, Status: "skipped"}
			if decision.Action != "skip" {
				candidate := candidates[decision.CandidateID]
				base, err := candidate.resource(input.ScopeID, decision.CandidateID)
				if err != nil {
					return err
				}
				if decision.Action == "update" {
					old, err := t.Head(ctx, decision.ResourceID)
					if err != nil {
						return err
					}
					if old.Metadata.Revision != int64(decision.ExpectedRevision) {
						return imports.ErrRevisionConflict
					}
					if old.Metadata.Kind != ir.KindNode {
						return imports.ErrInvalidInput
					}
					base.Metadata = old.Metadata
					base.Metadata.Name = candidate.Name
				}
				update := catalog.UpdateInput{Name: base.Metadata.Name, Tags: base.Metadata.Tags, Enabled: base.Metadata.Enabled, Payload: base.Payload}
				if decision.Override != nil {
					update, err = decision.Override.Merge(base)
					if err != nil {
						return err
					}
				}
				var written ir.Resource
				if decision.Action == "create" {
					written, err = t.Create(ctx, catalog.CreateInput{Name: update.Name, Tags: update.Tags, Enabled: update.Enabled, Payload: update.Payload})
					item.Status = "created"
				} else {
					written, err = t.Update(ctx, decision.ResourceID, int64(decision.ExpectedRevision), update)
					item.Status = "updated"
				}
				if err != nil {
					return err
				}
				item.ResourceID = written.Metadata.ResourceID
				item.Revision = apicontract.Revision(written.Metadata.Revision)
			}
			receipt.Items = append(receipt.Items, item)
		}
		if err := t.Audit(ctx, catalog.MutationAudit{PrincipalID: input.PrincipalID, ObjectID: input.BatchID, Revision: int64(receipt.Revision), RequestID: input.RequestID, Action: catalog.AuditImportCommit}); err != nil {
			return err
		}
		if _, err := t.tx.Exec(ctx, `INSERT INTO public.import_commits(scope_id,batch_id,actor_id,request_hmac,preview_revision,revision) VALUES($1,$2,$3,$4,$5,$6)`, dbID(input.ScopeID), dbID(input.BatchID), dbID(input.PrincipalID), digest, revision, int64(receipt.Revision)); err != nil {
			return err
		}
		batch := &pgx.Batch{}
		for i, item := range receipt.Items {
			var resource any
			var rev any
			if item.ResourceID != "" {
				resource = dbID(item.ResourceID)
				rev = int64(item.Revision)
			}
			batch.Queue(`INSERT INTO public.import_commit_items(batch_id,ordinal,candidate_id,status,resource_id,revision) VALUES($1,$2,$3,$4,$5,$6)`, dbID(input.BatchID), i, dbID(item.CandidateID), item.Status, resource, rev)
		}
		if err := t.tx.SendBatch(ctx, batch).Close(); err != nil {
			return err
		}
		_, err = t.tx.Exec(ctx, `UPDATE public.import_batches SET state='committed',revision=revision+1,raw_envelope=NULL,raw_wrapping=NULL WHERE scope_id=$1 AND id=$2`, dbID(input.ScopeID), dbID(input.BatchID))
		return err
	})
	if err != nil {
		return imports.Commit{}, importError(err)
	}
	return receipt, nil
}

func (s *Imports) readImportReceipt(ctx context.Context, tx pgx.Tx, scope, batch ir.ID) (imports.Commit, error) {
	r := imports.Commit{BatchID: batch, Items: []imports.CommitItem{}}
	if err := tx.QueryRow(ctx, "SELECT revision FROM public.import_commits WHERE scope_id=$1 AND batch_id=$2", dbID(scope), dbID(batch)).Scan(&r.Revision); err != nil {
		return imports.Commit{}, err
	}
	rows, err := tx.Query(ctx, "SELECT candidate_id,status,resource_id,revision FROM public.import_commit_items WHERE batch_id=$1 ORDER BY ordinal", dbID(batch))
	if err != nil {
		return imports.Commit{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item imports.CommitItem
		var id pgtype.UUID
		var rev pgtype.Int8
		if err := rows.Scan(&item.CandidateID, &item.Status, &id, &rev); err != nil {
			return imports.Commit{}, err
		}
		item.ResourceID = irID(id)
		item.Revision = apicontract.Revision(rev.Int64)
		r.Items = append(r.Items, item)
	}
	if err := rows.Err(); err != nil {
		return imports.Commit{}, err
	}
	if len(r.Items) == 0 {
		return imports.Commit{}, errors.New("import receipt unavailable")
	}
	return r, nil
}
