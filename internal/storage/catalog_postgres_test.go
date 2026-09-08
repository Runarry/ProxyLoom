package storage

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestPostgresCatalogLifecycle(t *testing.T) {
	env := newPostgres(t, true)
	initial, err := env.store.Scope(env.ctx, env.scope)
	if err != nil || initial.CatalogRevision != 0 || initial.AuthEpoch != 1 {
		t.Fatal("unexpected initial scope state")
	}

	a := mustCreate(t, env, catalog.CreateInput{Name: "node-a", Tags: []string{"blue", "shared"}, Enabled: true, Payload: syntheticNode()})
	bNode := syntheticNode()
	bNode.Endpoint.Host = "second.example.invalid"
	bNode.Security.(*ir.TLSSecurity).ServerName = "second.example.invalid"
	b := mustCreate(t, env, catalog.CreateInput{Name: "node-b", Tags: []string{"green", "shared"}, Enabled: true, Payload: bNode})
	chain := mustCreate(t, env, catalog.CreateInput{Name: "chain-before", Tags: []string{"old", "shared"}, Enabled: true,
		Payload: &ir.Chain{SchemaVersion: ir.SchemaVersion, Hops: []ir.NodeRef{{NodeID: a.Metadata.ResourceID}, {NodeID: b.Metadata.ResourceID}}, FailurePolicy: ir.FailClosed}})

	var renamed ir.Resource
	err = env.store.Transact(env.ctx, env.scope, func(tx catalog.Tx) error {
		renamed, err = tx.Update(env.ctx, chain.Metadata.ResourceID, 1, catalog.UpdateInput{Name: "chain-after", Tags: []string{"new", "shared"}, Enabled: true, Payload: chain.Payload})
		return err
	})
	if err != nil {
		t.Fatal("could not update PostgreSQL acceptance chain")
	}
	oldChain, err := env.store.Revision(env.ctx, env.scope, chain.Metadata.ResourceID, 1)
	if err != nil || oldChain.Metadata.Name != "chain-before" || !reflect.DeepEqual(oldChain.Metadata.Tags, []string{"old", "shared"}) {
		t.Fatal("historical chain metadata was not isolated")
	}
	if renamed.Metadata.Revision != 2 || renamed.Metadata.SecurityEpoch != 1 || renamed.Metadata.Name != "chain-after" || !reflect.DeepEqual(renamed.Metadata.Tags, []string{"new", "shared"}) {
		t.Fatal("chain metadata update was incorrect")
	}

	currentRefs, err := env.store.References(env.ctx, env.scope, a.Metadata.ResourceID, catalog.ReferenceOptions{Limit: 10})
	if err != nil || len(currentRefs.Items) != 1 || currentRefs.Items[0].SourceRevision != 2 || !currentRefs.Items[0].Current {
		t.Fatal("current reverse references were incorrect")
	}
	historyRefs, err := env.store.References(env.ctx, env.scope, a.Metadata.ResourceID, catalog.ReferenceOptions{IncludeHistorical: true, Limit: 10})
	if err != nil || len(historyRefs.Items) != 2 || historyRefs.Items[0].SourceRevision != 1 || historyRefs.Items[0].Current || historyRefs.Items[1].SourceRevision != 2 || !historyRefs.Items[1].Current {
		t.Fatal("historical reverse references were incorrect")
	}

	var authChanged, disabled, enabled, revoked, deleted ir.Resource
	err = env.store.Transact(env.ctx, env.scope, func(tx catalog.Tx) error {
		n := syntheticNode()
		n.Auth.(*ir.PasswordAuth).Password = "PROXYLOOM_TEST_ROTATED_SECRET_81d6"
		authChanged, err = tx.Update(env.ctx, a.Metadata.ResourceID, 1, catalog.UpdateInput{Name: "node-a", Tags: []string{"blue", "shared"}, Enabled: true, Payload: n})
		return err
	})
	if err == nil {
		err = env.store.Transact(env.ctx, env.scope, func(tx catalog.Tx) error {
			disabled, err = tx.Update(env.ctx, a.Metadata.ResourceID, 2, catalog.UpdateInput{Name: "node-a", Tags: []string{"blue", "shared"}, Enabled: false, Payload: authChanged.Payload})
			return err
		})
	}
	if err == nil {
		err = env.store.Transact(env.ctx, env.scope, func(tx catalog.Tx) error {
			enabled, err = tx.Update(env.ctx, a.Metadata.ResourceID, 3, catalog.UpdateInput{Name: "node-a", Tags: []string{"blue", "shared"}, Enabled: true, Payload: disabled.Payload})
			return err
		})
	}
	if err == nil {
		err = env.store.Transact(env.ctx, env.scope, func(tx catalog.Tx) error {
			revoked, err = tx.Revoke(env.ctx, a.Metadata.ResourceID, 4)
			return err
		})
	}
	if err == nil {
		err = env.store.Transact(env.ctx, env.scope, func(tx catalog.Tx) error {
			deleted, err = tx.Delete(env.ctx, a.Metadata.ResourceID, 5)
			return err
		})
	}
	if err != nil {
		t.Fatal("could not complete PostgreSQL security lifecycle")
	}
	if authChanged.Metadata.Revision != 2 || authChanged.Metadata.SecurityEpoch != 2 ||
		disabled.Metadata.Revision != 3 || disabled.Metadata.SecurityEpoch != 3 || disabled.Metadata.Enabled ||
		enabled.Metadata.Revision != 4 || enabled.Metadata.SecurityEpoch != 3 || !enabled.Metadata.Enabled ||
		revoked.Metadata.Revision != 5 || revoked.Metadata.SecurityEpoch != 4 ||
		deleted.Metadata.Revision != 6 || deleted.Metadata.SecurityEpoch != 5 || deleted.Metadata.Enabled {
		t.Fatal("security revision or epoch transition was incorrect")
	}
	if _, err := env.store.Head(env.ctx, env.scope, a.Metadata.ResourceID); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("soft-deleted resource remained available as a head")
	}
	if historical, err := env.store.Revision(env.ctx, env.scope, a.Metadata.ResourceID, 1); err != nil || historical.Metadata.Enabled != true || historical.Metadata.SecurityEpoch != 1 || historical.Payload.(*ir.Node).Auth.(*ir.PasswordAuth).Password != ir.Secret(postgresTestSecret) {
		t.Fatal("soft deletion changed immutable resource history")
	}
	after, err := env.store.Scope(env.ctx, env.scope)
	if err != nil || after.CatalogRevision != 9 || after.AuthEpoch != 1 {
		t.Fatal("catalog revision did not advance once per business transaction")
	}

	visible, err := env.store.List(env.ctx, env.scope, catalog.ListOptions{Limit: 10})
	if err != nil || len(visible.Items) != 2 {
		t.Fatal("default list did not exclude the soft-deleted resource")
	}
	all, err := env.store.List(env.ctx, env.scope, catalog.ListOptions{IncludeDeleted: true, Limit: 10})
	if err != nil || len(all.Items) != 3 {
		t.Fatal("deleted-resource list did not preserve the soft-deleted resource")
	}
	refsAfterDelete, err := env.store.References(env.ctx, env.scope, a.Metadata.ResourceID, catalog.ReferenceOptions{Limit: 10})
	if err != nil || len(refsAfterDelete.Items) != 1 || !refsAfterDelete.Items[0].Current {
		t.Fatal("soft deletion incorrectly removed reverse references")
	}
}
