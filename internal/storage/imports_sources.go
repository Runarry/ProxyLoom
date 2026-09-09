package storage

import (
	"bytes"
	"context"
	"errors"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/imports"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/override"
	"github.com/Runarry/ProxyLoom/internal/source"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
)

func (s *Imports) validateSourcePreview(ctx context.Context, t *catalogTx, input imports.CommitInput, sourceID ir.ID, revision int64) error {
	if input.SourceRevision != revision {
		return imports.ErrRevisionConflict
	}
	current, err := t.sourceHead(ctx, sourceID, false)
	if err != nil {
		return err
	}
	if current.Source.LatestPreviewBatchID != input.BatchID {
		return imports.ErrStateConflict
	}
	row, err := t.q.GetResourceRevision(ctx, dbgen.GetResourceRevisionParams{ScopeID: dbID(t.scope), ResourceID: dbID(sourceID), Revision: revision})
	if err != nil {
		return err
	}
	frozen, err := s.catalog.openSource(row)
	if err != nil {
		return err
	}
	// Failed attempts change operational status, not the successful frozen
	// input. Edits and successful refreshes invalidate pending batches; compare
	// the remaining source document as well before any node can be changed.
	current.Metadata.Revision = frozen.Metadata.Revision
	current.Source.LastJobID = frozen.Source.LastJobID
	current.Source.LastError = frozen.Source.LastError
	current.Source.LastSuccessAt = frozen.Source.LastSuccessAt
	a, err := source.Canonical(current)
	if err != nil {
		return err
	}
	defer clear(a)
	b, err := source.Canonical(frozen)
	if err != nil {
		return err
	}
	defer clear(b)
	if !bytes.Equal(a, b) {
		return imports.ErrRevisionConflict
	}
	return nil
}

func validateSourceDecision(ctx context.Context, t *catalogTx, sourceID ir.ID, candidate importCandidate, decision imports.Decision) error {
	if candidate.SourceItemID.Validate() != nil {
		return imports.ErrInvalidInput
	}
	if candidate.AutoApplied || candidate.ChangeKind == "unchanged" {
		if decision.Action == "skip" {
			return nil
		}
		return imports.ErrStateConflict
	}
	item, err := t.q.GetSourceItem(ctx, dbgen.GetSourceItemParams{ScopeID: dbID(t.scope), ID: dbID(candidate.SourceItemID)})
	if err != nil {
		return err
	}
	if irID(item.SourceID) != sourceID {
		return imports.ErrStateConflict
	}
	itemBinding, err := t.loadBindingByItem(ctx, candidate.SourceItemID)
	if err != nil && !errors.Is(err, catalog.ErrNotFound) {
		return err
	}
	if err == nil && (candidate.BoundItemID != candidate.SourceItemID || itemBinding.NodeID != candidate.ExistingID) {
		return imports.ErrStateConflict
	}
	if candidate.ExistingID != "" {
		node, err := t.Probe(ctx, candidate.ExistingID)
		if err != nil {
			return err
		}
		if node.Metadata.Kind != ir.KindNode || node.Metadata.Revision != candidate.ExistingRevision {
			return imports.ErrRevisionConflict
		}
		binding, err := t.loadBindingByNode(ctx, candidate.ExistingID)
		if errors.Is(err, catalog.ErrNotFound) {
			if candidate.BindingRevision != 0 {
				return imports.ErrStateConflict
			}
		} else if err != nil {
			return err
		} else if binding.Revision != candidate.BindingRevision || binding.SourceItemID != candidate.BoundItemID || binding.SourceResourceID != candidate.BoundSourceID {
			return imports.ErrStateConflict
		}
	}
	if decision.Action == "skip" {
		return nil
	}
	if decision.Override != nil && (decision.Override.OriginAction != "" || decision.Override.SourceItemID != "" || decision.Override.BindingRevision != nil) {
		return imports.ErrInvalidInput
	}
	if decision.Action == "create" {
		if candidate.ChangeKind == "missing" || candidate.BoundItemID == candidate.SourceItemID {
			return imports.ErrStateConflict
		}
		return nil
	}
	if decision.ResourceID != candidate.ExistingID || int64(decision.ExpectedRevision) != candidate.ExistingRevision {
		return imports.ErrRevisionConflict
	}
	if int64(decision.ExpectedBindingRevision) != candidate.BindingRevision {
		return imports.ErrStateConflict
	}
	if decision.Action == "update" && candidate.BoundItemID != candidate.SourceItemID {
		return imports.ErrInvalidInput
	}
	if candidate.BoundSourceID != "" && candidate.BoundSourceID != sourceID {
		return imports.ErrStateConflict
	}
	if decision.Action == "bind" && (candidate.ChangeKind != "conflict" || candidate.ExistingID == "") {
		return imports.ErrInvalidInput
	}
	if candidate.ChangeKind == "missing" && decision.Override != nil && (decision.Override.Name != nil || decision.Override.Node != nil || len(decision.Override.RestoreFields) != 0) {
		return imports.ErrInvalidInput
	}
	return nil
}

func (s *Imports) commitSourceDecision(ctx context.Context, t *catalogTx, sourceID ir.ID, candidate importCandidate, decision imports.Decision) (imports.CommitItem, error) {
	item := imports.CommitItem{CandidateID: decision.CandidateID, Status: "skipped"}
	if decision.Action == "skip" {
		return item, nil
	}
	base, err := candidate.resource(t.scope, decision.CandidateID)
	if err != nil {
		return item, err
	}
	binding := override.Binding{SourceResourceID: sourceID, SourceItemID: candidate.SourceItemID, Method: ir.ManualBinding, State: override.Active}
	var old ir.Resource
	if decision.Action != "create" {
		old, err = t.Head(ctx, decision.ResourceID)
		if err != nil {
			return item, err
		}
		base.Metadata = old.Metadata
		base.Metadata.Name = candidate.Name
		stored, err := t.loadBindingByNode(ctx, decision.ResourceID)
		if err == nil {
			binding.Patch, binding.Revision = stored.Patch, stored.Revision
		} else if !errors.Is(err, catalog.ErrNotFound) {
			return item, err
		}
	}
	if candidate.ChangeKind == "missing" {
		update := catalog.UpdateInput{Name: old.Metadata.Name, Tags: old.Metadata.Tags, Enabled: old.Metadata.Enabled && !candidate.MissingDisable, Payload: old.Payload}
		if decision.Override != nil {
			if decision.Override.Tags != nil {
				update.Tags = append([]string{}, (*decision.Override.Tags)...)
			}
			if decision.Override.Enabled != nil {
				update.Enabled = *decision.Override.Enabled
			}
		}
		written := old
		if update.Enabled != old.Metadata.Enabled || decision.Override != nil {
			written, err = t.Update(ctx, decision.ResourceID, int64(decision.ExpectedRevision), update)
			if err != nil {
				return item, err
			}
		}
		item.Status, item.ResourceID, item.Revision = "updated", written.Metadata.ResourceID, apicontract.Revision(written.Metadata.Revision)
		return item, nil
	}
	if decision.Override != nil {
		binding.Patch, err = override.Combine(binding.Patch, decision.Override.Name, decision.Override.Node, decision.Override.RestoreFields)
		if err != nil {
			return item, imports.ErrInvalidInput
		}
	}
	next, name, _, err := override.Merge(*base.Payload.(*ir.Node), candidate.Name, binding.Patch)
	if err != nil {
		return item, imports.ErrInvalidInput
	}
	next.Origin = &ir.Origin{SourceResourceID: sourceID, SourceItemID: candidate.SourceItemID, MatchMethod: ir.ManualBinding}
	update := catalog.UpdateInput{Name: name, Tags: base.Metadata.Tags, Enabled: base.Metadata.Enabled, Payload: &next}
	if decision.Override != nil {
		if decision.Override.Tags != nil {
			update.Tags = append([]string{}, (*decision.Override.Tags)...)
		}
		if decision.Override.Enabled != nil {
			update.Enabled = *decision.Override.Enabled
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
		return item, err
	}
	binding.NodeID, binding.Revision = written.Metadata.ResourceID, binding.Revision+1
	if err := t.saveBinding(ctx, binding); err != nil {
		return item, err
	}
	if err := applySourceItemBaseline(ctx, t, candidate.SourceItemID); err != nil {
		return item, err
	}
	item.ResourceID, item.Revision = written.Metadata.ResourceID, apicontract.Revision(written.Metadata.Revision)
	return item, nil
}
