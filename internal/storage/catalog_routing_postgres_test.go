package storage

import (
	"errors"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestPostgresRoutingStorageScopeLatchAndIdempotency(t *testing.T) {
	e := newPostgres(t, true)
	node := mustCreate(t, e, catalog.CreateInput{Name: "local", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
	setPayload, err := ir.NewRuleSet(ir.RuleSetWrite{SchemaVersion: 1, Format: ir.DomainCIDRText, Entries: []ir.RuleSetEntry{{Kind: ir.RuleSetDomain, Domain: "example.invalid", Match: ir.DomainSuffix}}})
	if err != nil {
		t.Fatal(err)
	}
	set := mustCreate(t, e, catalog.CreateInput{Name: "set", Tags: []string{}, Enabled: true, Payload: &setPayload})
	otherScope := ir.ID("20000000-0000-4000-8000-000000000001")
	if e.store.EnsureScope(e.ctx, otherScope, "other") != nil {
		t.Fatal("cross-scope setup failed")
	}
	var cross ir.Resource
	if err := e.store.Transact(e.ctx, otherScope, func(tx catalog.Tx) error {
		var err error
		cross, err = tx.Create(e.ctx, catalog.CreateInput{Name: "cross", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
		return err
	}); err != nil {
		t.Fatal("cross-scope node creation failed")
	}
	profile := routingPayload(ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: node.Metadata.ResourceID}, set.Metadata.ResourceID)
	input := catalog.CreateInput{Name: "profile", Tags: []string{}, Enabled: true, Payload: &profile}
	request := catalog.IdempotencyRequest{ScopeID: e.scope, PrincipalID: "30000000-0000-4000-8000-000000000001", RouteKey: "routing_profiles.create", Key: "routing-once", CanonicalRequest: []byte(`{"synthetic":"routing"}`)}
	calls := 0
	callback := func(tx catalog.Tx) (catalog.Receipt, error) {
		calls++
		r, err := tx.Create(e.ctx, input)
		return createdReceipt(r), err
	}
	first, err := e.store.ExecuteIdempotent(e.ctx, request, callback)
	if err != nil || first.Replayed {
		t.Fatal("initial idempotent routing creation failed", err)
	}
	second, err := e.store.ExecuteIdempotent(e.ctx, request, callback)
	if err != nil || !second.Replayed || calls != 1 || first.Receipt != second.Receipt {
		t.Fatal("routing idempotency repeated effect")
	}
	before, err := e.store.Scope(e.ctx, e.scope)
	if err != nil {
		t.Fatal(err)
	}
	bad := profile.Clone()
	bad.Final = ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: cross.Metadata.ResourceID}
	err = e.store.Transact(e.ctx, e.scope, func(tx catalog.Tx) error {
		if _, err := tx.Create(e.ctx, input); err != nil {
			return err
		}
		_, _ = tx.Update(e.ctx, first.Receipt.ResourceID, 1, catalog.UpdateInput{Name: "invalid", Tags: []string{}, Enabled: true, Payload: &bad})
		return nil
	})
	if !errors.Is(err, catalog.ErrInvalidReference) {
		t.Fatal("cross-scope reference failure not latched", err)
	}
	after, err := e.store.Scope(e.ctx, e.scope)
	page, pageErr := e.store.ListRoutingProfiles(e.ctx, e.scope, catalog.RoutingListOptions{Limit: 10})
	if err != nil || pageErr != nil || before.CatalogRevision != after.CatalogRevision || len(page.Items) != 1 || page.Items[0].Metadata.Revision != 1 {
		t.Fatal("failed routing transaction partially committed")
	}
	if _, err := e.admin.Exec(e.ctx, "UPDATE public.resource_revisions SET schema_version=schema_version WHERE resource_id=$1", dbID(first.Receipt.ResourceID)); err == nil {
		t.Fatal("routing revision rewritable")
	}
}
