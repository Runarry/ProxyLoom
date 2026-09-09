package storage

import (
	"errors"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestPostgresPolicyGroupStorageRejectsUnavailableClosureAndLatchesFailure(t *testing.T) {
	e := newPostgres(t, true)
	a := mustCreate(t, e, catalog.CreateInput{Name: "a", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
	b := mustCreate(t, e, catalog.CreateInput{Name: "b", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
	disabled := mustCreate(t, e, catalog.CreateInput{Name: "disabled", Tags: []string{}, Enabled: false, Payload: syntheticNode()})
	chain := mustCreate(t, e, catalog.CreateInput{Name: "chain", Tags: []string{}, Enabled: true,
		Payload: &ir.Chain{SchemaVersion: 1, Hops: []ir.NodeRef{{NodeID: a.Metadata.ResourceID}, {NodeID: b.Metadata.ResourceID}}, FailurePolicy: ir.FailClosed}})
	otherScope := ir.ID("20000000-0000-4000-8000-000000000001")
	if e.store.EnsureScope(e.ctx, otherScope, "other") != nil {
		t.Fatal("cross-scope policy fixture failed")
	}
	var cross ir.Resource
	if err := e.store.Transact(e.ctx, otherScope, func(tx catalog.Tx) error {
		var err error
		cross, err = tx.Create(e.ctx, catalog.CreateInput{Name: "cross", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
		return err
	}); err != nil {
		t.Fatal("cross-scope node could not be created")
	}
	group := policyGroupPayload(a.Metadata.ResourceID, chain.Metadata.ResourceID, ir.PolicyFixed)
	for _, target := range []ir.ID{disabled.Metadata.ResourceID, cross.Metadata.ResourceID, "99999999-9999-4999-8999-999999999999"} {
		bad := group.Clone()
		bad.Members[0].ResourceID = target
		err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
			_, err := tx.Create(e.ctx, catalog.CreateInput{Name: "invalid", Tags: []string{}, Enabled: true, Payload: &bad})
			return err
		})
		if !errors.Is(err, catalog.ErrInvalidReference) {
			t.Fatalf("storage did not reject a disabled, missing or cross-scope policy member: %v", err)
		}
	}
	badKind := group.Clone()
	badKind.Members[0].Kind = ir.KindChain
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		_, err := tx.Create(e.ctx, catalog.CreateInput{Name: "wrong-kind", Tags: []string{}, Enabled: true, Payload: &badKind})
		return err
	}); !errors.Is(err, catalog.ErrInvalidReference) {
		t.Fatal("storage accepted a policy member with mismatched kind")
	}
	created := mustCreate(t, e, catalog.CreateInput{Name: "good", Tags: []string{}, Enabled: true, Payload: &group})
	before, err := e.store.Scope(e.ctx, e.scope)
	if err != nil {
		t.Fatal("policy scope baseline read failed")
	}
	bad := group.Clone()
	bad.Members[0].ResourceID = disabled.Metadata.ResourceID
	err = e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		if _, err := tx.Create(e.ctx, catalog.CreateInput{Name: "must-roll-back", Tags: []string{}, Enabled: true, Payload: &group}); err != nil {
			return err
		}
		_, _ = tx.Update(e.ctx, created.Metadata.ResourceID, 1, catalog.UpdateInput{Name: "invalid", Tags: []string{}, Enabled: true, Payload: &bad})
		return nil
	})
	if !errors.Is(err, catalog.ErrInvalidReference) {
		t.Fatal("ignored policy validation failure did not abort the transaction")
	}
	after, err := e.store.Scope(e.ctx, e.scope)
	page, pageErr := e.store.ListPolicyGroups(e.ctx, e.scope, catalog.PolicyGroupListOptions{Limit: 10})
	if err != nil || pageErr != nil || before.CatalogRevision != after.CatalogRevision || len(page.Items) != 1 || page.Items[0].Metadata.Revision != 1 {
		t.Fatal("failed policy transaction committed a group, revision or catalog epoch")
	}
	// A live chain becomes unavailable when one of its underlying nodes is
	// disabled. Every member must remain usable, including non-default members.
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		_, err := tx.Update(e.ctx, b.Metadata.ResourceID, 1, catalog.UpdateInput{Name: "b", Tags: []string{}, Enabled: false, Payload: b.Payload})
		return err
	}); err != nil {
		t.Fatal("could not disable the policy chain hop")
	}
	group.DefaultMember = group.Members[0]
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		_, err := tx.Create(e.ctx, catalog.CreateInput{Name: "broken-chain", Tags: []string{}, Enabled: true, Payload: &group})
		return err
	}); !errors.Is(err, catalog.ErrInvalidReference) {
		t.Fatal("storage accepted a policy whose non-default chain hop was disabled")
	}
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		_, err := tx.Delete(e.ctx, chain.Metadata.ResourceID, 1)
		return err
	}); err != nil {
		t.Fatal("could not delete the referenced chain")
	}
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		_, err := tx.Create(e.ctx, catalog.CreateInput{Name: "deleted-chain", Tags: []string{}, Enabled: true, Payload: &group})
		return err
	}); !errors.Is(err, catalog.ErrInvalidReference) {
		t.Fatal("storage accepted a deleted policy member")
	}
	if err := e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		_, err := tx.Delete(e.ctx, created.Metadata.ResourceID, 1)
		return err
	}); err != nil {
		t.Fatal("unavailable dependencies prevented deleting a policy group")
	}
	old, err := e.store.Revision(e.ctx, e.scope, created.Metadata.ResourceID, 1)
	if err != nil || !old.Metadata.Enabled || old.Payload.(*ir.PolicyGroup).Strategy != ir.PolicyFixed {
		t.Fatal("policy cleanup changed immutable history")
	}
	if _, err := e.admin.Exec(e.ctx, "UPDATE public.resource_revisions SET schema_version=schema_version WHERE resource_id=$1", dbID(created.Metadata.ResourceID)); err == nil {
		t.Fatal("database permitted rewriting policy revision history")
	}
}
