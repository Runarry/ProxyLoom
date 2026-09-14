package storage

import (
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"testing"
)

func TestPostgresSubscriptionHeadsKeepScopeAndMixedStates(t *testing.T) {
	h := newPublicationTest(t)
	foreign := newPublicationTest(t)
	h.publish(t, h.validate(t, h.compile(t), false), 0)
	foreign.publish(t, foreign.validate(t, foreign.compile(t), false), 0)
	unpublished := mustCreate(t, h.env, catalog.CreateInput{Name: "unpublished", Tags: []string{}, Enabled: true, Payload: h.profile.Payload})
	heads, err := h.store.Heads(h.env.ctx, h.env.scope, []ir.ID{h.profile.Metadata.ResourceID, foreign.profile.Metadata.ResourceID, unpublished.Metadata.ResourceID})
	if err != nil {
		t.Fatal(err)
	}
	if len(heads) != 3 || heads[h.profile.Metadata.ResourceID].State != "active" || heads[unpublished.Metadata.ResourceID].State != "not_ready" || heads[foreign.profile.Metadata.ResourceID].PublicationID != "" {
		t.Fatal("batch heads mixed publication state or exposed another scope")
	}
}
