package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/origin"
	"github.com/Runarry/ProxyLoom/internal/override"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	"github.com/Runarry/ProxyLoom/internal/source"
	"github.com/jackc/pgx/v5"
)

func (s *Sources) createSourcePreview(ctx context.Context, t *catalogTx, document source.Document, payload refreshPayload, jobID, snapshotID ir.ID, parsed []parsedCandidate, now time.Time) (ir.ID, error) {
	batchID, err := importID()
	if err != nil {
		return "", err
	}
	existing, err := s.loadItems(ctx, t, document.Metadata.ResourceID)
	if err != nil {
		return "", err
	}
	records, err := s.nodeRecords(ctx, t, document.Metadata.ResourceID, existing)
	if err != nil {
		return "", err
	}
	store := &Imports{catalog: s.catalog, jobs: s.jobs}
	preview := []importCandidate{}
	seen := map[ir.ID]bool{}
	inputKeys := map[string]int{}
	for _, candidate := range parsed {
		inputKeys[sourceCandidateKey(candidate)]++
	}
	processed := map[string]bool{}
	for _, candidate := range parsed {
		key := sourceCandidateKey(candidate)
		if processed[key] {
			continue
		}
		processed[key] = true
		old, found := matchStoredItem(existing, candidate)
		item := old
		if !found {
			item.ID, err = newResourceID()
			if err != nil {
				return "", err
			}
			if err := s.insertItem(ctx, t, document, item.ID, candidate, now); err != nil {
				return "", err
			}
		} else if err := s.updateItem(ctx, t, document, item.ID, candidate, now); err != nil {
			return "", err
		}
		seen[item.ID] = true
		item.Name, item.ExternalKey, item.Fingerprint, item.Node, item.State = candidate.Name, candidate.ExternalKey, candidate.Fingerprint, cloneNode(candidate.Node), "active"
		entry := importCandidate{BatchID: batchID, Index: len(preview), SourceItemID: item.ID, Name: candidate.Name,
			State: "new", ChangeKind: "new", Diagnostics: append(ir.Diagnostics{}, candidate.Diagnostics...)}
		entry.Node, err = json.Marshal(candidate.Node)
		if err != nil {
			return "", catalog.ErrUnavailable
		}
		entry.UpstreamChanges = sourceFieldChanges(old.Name, old.Node, candidate.Name, &candidate.Node)
		entry.EffectiveChanges = sourceFieldChanges("", nil, candidate.Name, &candidate.Node)
		matched, ok := origin.Match(origin.Item{SourceItemID: item.ID, ExternalKey: candidate.ExternalKey, Fingerprint: candidate.Fingerprint,
			Name: candidate.Name, Protocol: candidate.Node.Protocol, Host: candidate.Node.Endpoint.Host, Port: candidate.Node.Endpoint.Port}, records)
		conflict := inputKeys[key] > 1
		if ok {
			entry.ExistingID, entry.ExistingRevision, entry.MatchMethod = matched.NodeID, matched.Revision, string(matched.Method)
			conflict = conflict || matched.Kind != origin.Identity || matched.Ambiguous
			if matched.NodeID != "" {
				oldNode, err := t.Probe(ctx, matched.NodeID)
				if err != nil {
					return "", err
				}
				binding, err := t.loadBindingByNode(ctx, matched.NodeID)
				patch := override.Document{}
				if err == nil {
					entry.BindingRevision, entry.BoundItemID, entry.BoundSourceID = binding.Revision, binding.SourceItemID, binding.SourceResourceID
					if binding.SourceResourceID == document.Metadata.ResourceID {
						patch = binding.Patch
					}
				} else if !errors.Is(err, catalog.ErrNotFound) {
					return "", err
				}
				next, name, _, err := override.Merge(candidate.Node, candidate.Name, patch)
				if err != nil {
					return "", err
				}
				entry.EffectiveChanges = sourceFieldChanges(oldNode.Metadata.Name, oldNode.Payload.(*ir.Node), name, &next)
				entry.State, entry.ChangeKind = "matched", "modified"
				if len(entry.UpstreamChanges) == 0 && len(entry.EffectiveChanges) == 0 && binding.State == override.Active && !conflict {
					entry.ChangeKind = "unchanged"
				}
			}
		}
		if conflict {
			entry.State, entry.ChangeKind = "conflict", "conflict"
			entry.Diagnostics = append(entry.Diagnostics, ir.Diagnostic{Code: "SOURCE_CONFIRMATION_REQUIRED", Severity: ir.SeverityWarning,
				FieldPath: "/node", Message: "The upstream item has a duplicate or suggested identity. Confirm a target, create a separate node, or skip it."})
			if inputKeys[key] > 1 {
				entry.Diagnostics = append(entry.Diagnostics, ir.Diagnostic{Code: "SOURCE_DUPLICATE_IDENTITY", Severity: ir.SeverityWarning,
					FieldPath: "/source_item_id", Message: "Several upstream entries share this identity. The first entry is shown for explicit confirmation."})
			}
			if err := s.markConflict(ctx, t, document, item, matched, now); err != nil {
				return "", err
			}
		} else if document.Source.RefreshPolicy.CommitMode == source.SafeUpdates && entry.ChangeKind != "unchanged" {
			if err := s.commitNode(ctx, t, document, payload, candidate, item, records, now); err != nil {
				return "", err
			}
			entry.AutoApplied = true
			if err := applySourceItemBaseline(ctx, t, item.ID); err != nil {
				return "", err
			}
		}
		// Revisions in the preview describe its transaction's final state, even
		// when safe updates or an origin-state transition were automatic.
		if err := refreshPreviewBinding(ctx, t, &entry); err != nil {
			return "", err
		}
		preview = append(preview, entry)
	}
	for _, item := range existing {
		if seen[item.ID] {
			continue
		}
		entry := importCandidate{BatchID: batchID, Index: len(preview), SourceItemID: item.ID, Name: item.Name,
			State: "matched", ChangeKind: "missing", Diagnostics: ir.Diagnostics{},
			MissingDisable:  document.Source.RefreshPolicy.MissingPolicy == source.Disable,
			UpstreamChanges: sourceFieldChanges(item.Name, item.Node, "", nil)}
		entry.Node, err = json.Marshal(item.Node)
		if err != nil {
			return "", catalog.ErrUnavailable
		}
		binding, bindErr := t.loadBindingByItem(ctx, item.ID)
		if bindErr == nil {
			node, err := t.Probe(ctx, binding.NodeID)
			if err != nil {
				return "", err
			}
			if entry.MissingDisable && node.Metadata.Enabled {
				entry.EffectiveChanges = []imports.SourceFieldChange{{FieldPath: "/enabled", Before: json.RawMessage("true"), After: json.RawMessage("false")}}
			}
		} else if !errors.Is(bindErr, catalog.ErrNotFound) {
			return "", bindErr
		}
		if err := s.markMissing(ctx, t, document, payload, item, now); err != nil {
			return "", err
		}
		entry.AutoApplied = document.Source.RefreshPolicy.CommitMode == source.SafeUpdates
		if err := refreshPreviewBinding(ctx, t, &entry); err != nil {
			return "", err
		}
		preview = append(preview, entry)
	}
	if len(preview) > imports.MaxCandidates {
		return "", imports.ErrInvalidInput
	}
	for i := range preview {
		if err := refreshPreviewBinding(ctx, t, &preview[i]); err != nil {
			return "", err
		}
	}
	if err := supersedeSourcePreviews(ctx, t, document.Metadata.ResourceID); err != nil {
		return "", err
	}
	var actor any
	if payload.ActorID != "" {
		actor = dbID(payload.ActorID)
	}
	_, err = t.tx.Exec(ctx, `INSERT INTO public.import_batches(id,scope_id,actor_id,job_id,format,state,source_id,snapshot_id,source_revision,candidate_count)
		VALUES($1,$2,$3,$4,$5,'ready',$6,$7,$8,$9)`, dbID(batchID), dbID(t.scope), actor, dbID(jobID), string(document.Source.Format),
		dbID(document.Metadata.ResourceID), dbID(snapshotID), document.Metadata.Revision+1, len(preview))
	if err != nil {
		return "", err
	}
	batch := &pgx.Batch{}
	for _, entry := range preview {
		id, err := importID()
		if err != nil {
			return "", err
		}
		plain, err := json.Marshal(entry)
		if err != nil {
			return "", catalog.ErrUnavailable
		}
		envelope, wrapping, err := store.seal(t.scope, secretbox.TableImportCandidates, id, plain)
		clear(plain)
		if err != nil {
			return "", err
		}
		batch.Queue(`INSERT INTO public.import_candidates(id,scope_id,batch_id,ordinal,envelope,wrapping) VALUES($1,$2,$3,$4,$5,$6)`,
			dbID(id), dbID(t.scope), dbID(batchID), entry.Index, envelope, wrapping)
	}
	if err := t.tx.SendBatch(ctx, batch).Close(); err != nil {
		return "", err
	}
	return batchID, nil
}

func sourceCandidateKey(candidate parsedCandidate) string {
	if candidate.ExternalKey != "" {
		return "key:" + candidate.ExternalKey
	}
	return "fingerprint:" + candidate.Fingerprint
}

func sourcePreviewCount(existing []storedSourceItem, parsed []parsedCandidate) int {
	keys := map[string]bool{}
	seen := map[ir.ID]bool{}
	for _, candidate := range parsed {
		keys[sourceCandidateKey(candidate)] = true
		if item, found := matchStoredItem(existing, candidate); found {
			seen[item.ID] = true
		}
	}
	return len(keys) + len(existing) - len(seen)
}

func refreshPreviewBinding(ctx context.Context, t *catalogTx, entry *importCandidate) error {
	binding, err := t.loadBindingByItem(ctx, entry.SourceItemID)
	if errors.Is(err, catalog.ErrNotFound) {
		if entry.ExistingID == "" {
			return nil
		}
		binding, err = t.loadBindingByNode(ctx, entry.ExistingID)
		if errors.Is(err, catalog.ErrNotFound) {
			node, probeErr := t.Probe(ctx, entry.ExistingID)
			if probeErr != nil {
				return probeErr
			}
			entry.ExistingRevision = node.Metadata.Revision
			return nil
		}
	}
	if err != nil {
		return err
	}
	node, err := t.Probe(ctx, binding.NodeID)
	if err != nil {
		return err
	}
	entry.ExistingID, entry.ExistingRevision = binding.NodeID, node.Metadata.Revision
	entry.BindingRevision, entry.BoundItemID, entry.BoundSourceID = binding.Revision, binding.SourceItemID, binding.SourceResourceID
	if binding.SourceItemID == entry.SourceItemID {
		entry.MatchMethod = string(binding.Method)
	}
	return nil
}

func applySourceItemBaseline(ctx context.Context, t *catalogTx, itemID ir.ID) error {
	_, err := t.tx.Exec(ctx, `UPDATE public.source_items SET applied_envelope=envelope,applied_wrapping=wrapping,applied_revision=base_revision,state='active' WHERE scope_id=$1 AND id=$2`, dbID(t.scope), dbID(itemID))
	return err
}

func supersedeSourcePreviews(ctx context.Context, t *catalogTx, sourceID ir.ID) error {
	_, err := t.tx.Exec(ctx, `UPDATE public.import_batches SET state='superseded',revision=revision+1 WHERE scope_id=$1 AND source_id=$2 AND state='ready'`, dbID(t.scope), dbID(sourceID))
	return err
}
