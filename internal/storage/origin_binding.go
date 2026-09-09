package storage

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/override"
	"github.com/Runarry/ProxyLoom/internal/secretbox"
	dbgen "github.com/Runarry/ProxyLoom/internal/storage/generated"
	"github.com/jackc/pgx/v5/pgtype"
)

func (c *Catalog) NodeBinding(ctx context.Context, scope, nodeID ir.ID) (override.Binding, error) {
	if !validIDs(scope, nodeID) {
		return override.Binding{}, catalog.ErrInvalidInput
	}
	row, err := c.q.GetNodeBindingByNode(ctx, dbgen.GetNodeBindingByNodeParams{ScopeID: dbID(scope), NodeID: dbID(nodeID)})
	if err != nil {
		return override.Binding{}, catalogError(err)
	}
	return c.decodeBinding(scope, row.NodeID, row.SourceItemID, row.SourceID, row.BindingRevision, row.MatchMethod, row.State, row.OverrideEnvelope, row.Wrapping)
}

func (c *Catalog) ListNodeBindings(ctx context.Context, scope ir.ID, nodeIDs []ir.ID) (map[ir.ID]override.Binding, error) {
	if scope.Validate() != nil {
		return nil, catalog.ErrInvalidInput
	}
	ids := make([]pgtype.UUID, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		if id.Validate() != nil {
			return nil, catalog.ErrInvalidInput
		}
		ids = append(ids, dbID(id))
	}
	if len(ids) == 0 {
		return map[ir.ID]override.Binding{}, nil
	}
	rows, err := c.q.ListNodeBindingsByNodes(ctx, dbgen.ListNodeBindingsByNodesParams{ScopeID: dbID(scope), NodeIds: ids})
	if err != nil {
		return nil, catalogError(err)
	}
	out := map[ir.ID]override.Binding{}
	for _, row := range rows {
		binding, err := c.decodeBinding(scope, row.NodeID, row.SourceItemID, row.SourceID, row.BindingRevision, row.MatchMethod, row.State, row.OverrideEnvelope, row.Wrapping)
		if err != nil {
			return nil, err
		}
		out[binding.NodeID] = binding
	}
	return out, nil
}

func (c *Catalog) SourceItems(ctx context.Context, scope, sourceID ir.ID) ([]override.Item, error) {
	if !validIDs(scope, sourceID) {
		return nil, catalog.ErrInvalidInput
	}
	rows, err := c.q.ListSourceItems(ctx, dbgen.ListSourceItemsParams{ScopeID: dbID(scope), SourceID: dbID(sourceID)})
	if err != nil {
		return nil, catalogError(err)
	}
	items := make([]override.Item, 0, len(rows))
	for _, row := range rows {
		id := irID(row.ID)
		plain, err := c.openBindingRecord(scope, secretbox.TableSourceItems, id, row.BaseRevision, row.Envelope, row.Wrapping)
		if err != nil {
			return nil, err
		}
		var stored struct {
			Name, ExternalKey string
			SuggestedNodeID   ir.ID `json:"suggested_node_id"`
		}
		if json.Unmarshal(plain, &stored) != nil {
			clear(plain)
			return nil, catalog.ErrCrypto
		}
		clear(plain)
		item := override.Item{ID: id, SourceID: sourceID, Name: stored.Name, State: row.State, SuggestedNodeID: stored.SuggestedNodeID}
		if row.ExternalKey.Valid {
			item.ExternalKey = row.ExternalKey.String
		}
		binding, err := c.q.GetNodeBindingByItem(ctx, dbgen.GetNodeBindingByItemParams{ScopeID: dbID(scope), SourceItemID: dbID(id)})
		if err == nil {
			item.NodeID = irID(binding.NodeID)
		} else if !errors.Is(catalogError(err), catalog.ErrNotFound) {
			return nil, catalogError(err)
		}
		items = append(items, item)
	}
	return items, nil
}

func (c *Catalog) decodeBinding(scope ir.ID, nodeID, itemID, sourceID pgtype.UUID, revision int64, method, state string, envelope, wrapping []byte) (override.Binding, error) {
	binding := override.Binding{
		NodeID: irID(nodeID), SourceItemID: irID(itemID), SourceResourceID: irID(sourceID),
		Revision: revision, Method: ir.MatchMethod(method), State: override.State(state),
	}
	if !binding.State.Valid() {
		return override.Binding{}, catalog.ErrCrypto
	}
	if len(envelope) == 0 {
		return binding, nil
	}
	plain, err := c.openBindingRecord(scope, secretbox.TableNodeBindings, binding.NodeID, revision, envelope, wrapping)
	if err != nil {
		return override.Binding{}, err
	}
	defer clear(plain)
	patch, err := override.Decode(plain)
	if err != nil {
		return override.Binding{}, catalog.ErrCrypto
	}
	binding.Patch = patch
	return binding, nil
}

func (c *Catalog) openBindingRecord(scope ir.ID, table string, id ir.ID, revision int64, envelope, wrapping []byte) ([]byte, error) {
	var payload secretbox.Payload
	var wrap secretbox.Wrapping
	if json.Unmarshal(envelope, &payload) != nil || json.Unmarshal(wrapping, &wrap) != nil {
		return nil, catalog.ErrCrypto
	}
	plain, err := c.box.Open(secretbox.Context{ScopeID: scope, Table: table, ObjectID: id, Revision: revision, SchemaVersion: 1}, payload, wrap)
	if err != nil {
		return nil, catalog.ErrCrypto
	}
	return plain, nil
}

func (c *Catalog) sealBinding(scope ir.ID, id ir.ID, revision int64, plain []byte) ([]byte, []byte, error) {
	payload, wrapping, err := c.box.Seal(secretbox.Context{ScopeID: scope, Table: secretbox.TableNodeBindings, ObjectID: id, Revision: revision, SchemaVersion: 1}, plain)
	if err != nil {
		return nil, nil, catalog.ErrCrypto
	}
	envelope, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, catalog.ErrCrypto
	}
	wrap, err := json.Marshal(wrapping)
	if err != nil {
		return nil, nil, catalog.ErrCrypto
	}
	return envelope, wrap, nil
}

func (t *catalogTx) LoadOrigin(ctx context.Context, nodeID ir.ID) (override.Binding, error) {
	return t.loadBindingByNode(ctx, nodeID)
}

func (t *catalogTx) StoreOrigin(ctx context.Context, binding override.Binding) error {
	return t.saveBinding(ctx, binding)
}

func (t *catalogTx) LoadSourceBaseline(ctx context.Context, itemID ir.ID) (ir.Node, string, override.Item, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return ir.Node{}, "", override.Item{}, catalog.ErrTransactionClosed
	}
	if t.failed != nil {
		return ir.Node{}, "", override.Item{}, t.failed
	}
	row, err := t.q.GetSourceItem(ctx, dbgen.GetSourceItemParams{ScopeID: dbID(t.scope), ID: dbID(itemID)})
	if err != nil {
		return ir.Node{}, "", override.Item{}, catalogError(err)
	}
	id := irID(row.ID)
	plain, err := t.store.openBindingRecord(t.scope, secretbox.TableSourceItems, id, row.BaseRevision, row.Envelope, row.Wrapping)
	if err != nil {
		return ir.Node{}, "", override.Item{}, err
	}
	defer clear(plain)
	var stored struct {
		Name, ExternalKey, Fingerprint string
		SuggestedNodeID                ir.ID `json:"suggested_node_id"`
		Node                           *ir.Node
	}
	if json.Unmarshal(plain, &stored) != nil || stored.Node == nil {
		return ir.Node{}, "", override.Item{}, catalog.ErrCrypto
	}
	item := override.Item{ID: id, SourceID: irID(row.SourceID), Name: stored.Name, State: row.State, SuggestedNodeID: stored.SuggestedNodeID}
	if row.ExternalKey.Valid {
		item.ExternalKey = row.ExternalKey.String
	}
	return *stored.Node, stored.Name, item, nil
}

func (t *catalogTx) loadBindingByNode(ctx context.Context, nodeID ir.ID) (override.Binding, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return override.Binding{}, catalog.ErrTransactionClosed
	}
	if t.failed != nil {
		return override.Binding{}, t.failed
	}
	row, err := t.q.GetNodeBindingByNode(ctx, dbgen.GetNodeBindingByNodeParams{ScopeID: dbID(t.scope), NodeID: dbID(nodeID)})
	if err != nil {
		return override.Binding{}, catalogError(err)
	}
	return t.store.decodeBinding(t.scope, row.NodeID, row.SourceItemID, row.SourceID, row.BindingRevision, row.MatchMethod, row.State, row.OverrideEnvelope, row.Wrapping)
}

func (t *catalogTx) loadBindingByItem(ctx context.Context, itemID ir.ID) (override.Binding, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return override.Binding{}, catalog.ErrTransactionClosed
	}
	if t.failed != nil {
		return override.Binding{}, t.failed
	}
	row, err := t.q.GetNodeBindingByItem(ctx, dbgen.GetNodeBindingByItemParams{ScopeID: dbID(t.scope), SourceItemID: dbID(itemID)})
	if err != nil {
		return override.Binding{}, catalogError(err)
	}
	return t.store.decodeBinding(t.scope, row.NodeID, row.SourceItemID, row.SourceID, row.BindingRevision, row.MatchMethod, row.State, row.OverrideEnvelope, row.Wrapping)
}

func (t *catalogTx) saveBinding(ctx context.Context, binding override.Binding) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.active {
		return catalog.ErrTransactionClosed
	}
	if t.failed != nil {
		return t.failed
	}
	if binding.NodeID.Validate() != nil || binding.SourceItemID.Validate() != nil || binding.Revision < 1 || !binding.State.Valid() {
		return catalog.ErrInvalidInput
	}
	var envelope, wrapping []byte
	plain, err := binding.Patch.Canonical()
	if err != nil {
		return catalog.ErrInvalidInput
	}
	if len(plain) > 0 {
		defer clear(plain)
		envelope, wrapping, err = t.store.sealBinding(t.scope, binding.NodeID, binding.Revision, plain)
		if err != nil {
			return err
		}
	}
	_, err = t.q.GetNodeBindingByNode(ctx, dbgen.GetNodeBindingByNodeParams{ScopeID: dbID(t.scope), NodeID: dbID(binding.NodeID)})
	if errors.Is(catalogError(err), catalog.ErrNotFound) {
		if err := t.q.InsertNodeBinding(ctx, dbgen.InsertNodeBindingParams{
			NodeID: dbID(binding.NodeID), ScopeID: dbID(t.scope), SourceItemID: dbID(binding.SourceItemID),
			BindingRevision: binding.Revision, MatchMethod: string(binding.Method), State: string(binding.State),
			OverrideEnvelope: envelope, Wrapping: wrapping,
		}); err != nil {
			return err
		}
		t.dirty = true
		return nil
	}
	if err != nil {
		return err
	}
	if err := t.q.UpdateNodeBinding(ctx, dbgen.UpdateNodeBindingParams{
		BindingRevision: binding.Revision, MatchMethod: string(binding.Method), State: string(binding.State),
		OverrideEnvelope: envelope, Wrapping: wrapping, ScopeID: dbID(t.scope), NodeID: dbID(binding.NodeID),
	}); err != nil {
		return err
	}
	t.dirty = true
	return nil
}
