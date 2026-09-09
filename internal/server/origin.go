package server

import (
	"context"
	"errors"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/override"
)

type originMutator interface {
	catalog.AuditedTx
	LoadOrigin(context.Context, ir.ID) (override.Binding, error)
	StoreOrigin(context.Context, override.Binding) error
	LoadSourceBaseline(context.Context, ir.ID) (ir.Node, string, override.Item, error)
	LoadSourceCandidate(context.Context, ir.ID) (ir.Node, string, override.Item, error)
	ApplySourceBaseline(context.Context, ir.ID) error
}

func originDTO(binding override.Binding) apicontract.NodeOriginBinding {
	fields := binding.Patch.Fields()
	if fields == nil {
		fields = []string{}
	}
	return apicontract.NodeOriginBinding{
		BindingRevision:  apicontract.Revision(binding.Revision),
		OriginState:      string(binding.State),
		OverriddenFields: fields,
		SourceItemID:     binding.SourceItemID,
		SourceResourceID: binding.SourceResourceID,
		MatchMethod:      binding.Method,
	}
}

func sourceItemDTO(item override.Item) apicontract.SourceItem {
	return apicontract.SourceItem{
		ID: item.ID, State: item.State, Name: item.Name, ExternalKey: item.ExternalKey,
		NodeID: item.NodeID, SuggestedNodeID: item.SuggestedNodeID,
	}
}

func applyBoundPatch(ctx context.Context, tx originMutator, old ir.Resource, request apicontract.NodePatchRequest) (ir.Resource, catalog.MutationAction, error) {
	binding, err := tx.LoadOrigin(ctx, old.Metadata.ResourceID)
	if err != nil {
		return ir.Resource{}, "", err
	}
	overlay := request.Node != nil || request.Name != nil || len(request.RestoreFields) > 0 || request.OriginAction != ""
	if overlay {
		if request.BindingRevision == nil {
			return ir.Resource{}, "", apicontract.NewError(apicontract.PreconditionRequired, apicontract.Detail{FieldPath: "/binding_revision"})
		}
		if int64(*request.BindingRevision) != binding.Revision {
			return ir.Resource{}, "", apicontract.NewError(apicontract.StateConflict, apicontract.Detail{FieldPath: "/binding_revision"})
		}
	}
	action := catalog.AuditNodeUpdate
	if request.OriginAction == "bind" {
		itemID := request.SourceItemID
		if itemID == "" {
			itemID = binding.SourceItemID
		}
		base, baseName, item, err := tx.LoadSourceCandidate(ctx, itemID)
		if err != nil {
			return ir.Resource{}, "", err
		}
		if item.State != string(override.Conflict) && binding.State != override.Conflict {
			return ir.Resource{}, "", apicontract.NewError(apicontract.StateConflict, apicontract.Detail{FieldPath: "/origin_action"})
		}
		binding.SourceItemID = itemID
		binding.SourceResourceID = item.SourceID
		binding.Method = ir.ManualBinding
		binding.State = override.Active
		node, ok := old.Payload.(*ir.Node)
		if !ok {
			return ir.Resource{}, "", catalog.ErrInvalidInput
		}
		next := *node
		next.Origin = &ir.Origin{SourceResourceID: item.SourceID, SourceItemID: itemID, MatchMethod: ir.ManualBinding}
		merged, name, _, err := override.Merge(base, baseName, binding.Patch)
		if err != nil {
			return ir.Resource{}, "", apicontract.NewError(apicontract.ValidationFailed)
		}
		merged.Origin = next.Origin
		input := catalog.UpdateInput{Name: name, Tags: old.Metadata.Tags, Enabled: old.Metadata.Enabled, Payload: &merged}
		if request.Tags != nil {
			input.Tags = append([]string{}, *request.Tags...)
		}
		if request.Enabled != nil {
			input.Enabled = *request.Enabled
		}
		updated, err := tx.Update(ctx, old.Metadata.ResourceID, old.Metadata.Revision, input)
		if err != nil {
			return ir.Resource{}, "", err
		}
		binding.NodeID = updated.Metadata.ResourceID
		binding.Revision++
		if err := tx.StoreOrigin(ctx, binding); err != nil {
			return ir.Resource{}, "", err
		}
		if err := tx.ApplySourceBaseline(ctx, itemID); err != nil {
			return ir.Resource{}, "", err
		}
		return updated, catalog.AuditNodeOverride, nil
	}
	if request.OriginAction == "skip" {
		if binding.State != override.Conflict {
			return ir.Resource{}, "", apicontract.NewError(apicontract.StateConflict, apicontract.Detail{FieldPath: "/origin_action"})
		}
		binding.State = override.Active
		binding.Revision++
		if err := tx.StoreOrigin(ctx, binding); err != nil {
			return ir.Resource{}, "", err
		}
		return old, catalog.AuditNodeOverride, nil
	}
	patch, err := override.Combine(binding.Patch, request.Name, request.Node, request.RestoreFields)
	if err != nil {
		if errors.Is(err, override.ErrProtocol) {
			return ir.Resource{}, "", apicontract.NewError(apicontract.ValidationFailed, apicontract.Detail{FieldPath: "/node/protocol"})
		}
		if errors.Is(err, override.ErrRestore) {
			return ir.Resource{}, "", apicontract.NewError(apicontract.ValidationFailed, apicontract.Detail{FieldPath: "/restore_fields"})
		}
		return ir.Resource{}, "", apicontract.NewError(apicontract.ValidationFailed)
	}
	base, baseName, _, err := tx.LoadSourceBaseline(ctx, binding.SourceItemID)
	if err != nil {
		return ir.Resource{}, "", err
	}
	effective, name, _, err := override.Merge(base, baseName, patch)
	if err != nil {
		return ir.Resource{}, "", apicontract.NewError(apicontract.ValidationFailed)
	}
	node, ok := old.Payload.(*ir.Node)
	if !ok {
		return ir.Resource{}, "", catalog.ErrInvalidInput
	}
	effective.Origin = node.Origin
	input := catalog.UpdateInput{Name: name, Tags: old.Metadata.Tags, Enabled: old.Metadata.Enabled, Payload: &effective}
	if request.Tags != nil {
		input.Tags = append([]string{}, *request.Tags...)
	}
	if request.Enabled != nil {
		input.Enabled = *request.Enabled
	}
	if len(request.RestoreFields) > 0 {
		action = catalog.AuditNodeRestoreSource
	} else if request.Name != nil || request.Node != nil {
		action = catalog.AuditNodeOverride
	}
	updated, err := tx.Update(ctx, old.Metadata.ResourceID, old.Metadata.Revision, input)
	if err != nil {
		return ir.Resource{}, "", err
	}
	if overlay {
		binding.Patch = patch
		binding.Revision++
		if err := tx.StoreOrigin(ctx, binding); err != nil {
			return ir.Resource{}, "", err
		}
	}
	return updated, action, nil
}
