package storage

import (
	"net/http"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/override"
)

func TestPostgresSourceStableCredentialRotationPreservesNameAndRevokesEpoch(t *testing.T) {
	f := newSourcePreviewFixture(t, "safe_updates")
	first := f.refresh(t, sourcePreviewURI("stable-ac04", "Upstream old", "old-ac04.example.invalid", "SYNTHETIC_AC04_OLD"))
	if len(first.Candidates) != 1 || !first.Candidates[0].AutoApplied {
		t.Fatal("initial stable source item was not applied automatically")
	}

	nodes := requireNodeList(t, f.h.do(http.MethodGet, "/api/v1/nodes", "", "", nil))
	if len(nodes.Data) != 1 || nodes.Data[0].Binding == nil {
		t.Fatal("initial stable source item did not create one bound node")
	}
	initial := nodes.Data[0]
	itemID := initial.Binding.SourceItemID
	customName := "Local AC-04 name"
	local := requireNode(t, f.h.do(http.MethodPatch, "/api/v1/nodes/"+string(initial.Metadata.ResourceID),
		`{"name":"`+customName+`","binding_revision":"`+revisionDecimal(int64(initial.Binding.BindingRevision))+`"}`,
		revisionTag(int64(initial.Metadata.Revision)), nil), http.StatusOK)
	if local.Metadata.Name != customName || local.Binding == nil || local.Binding.OriginState != string(override.Active) {
		t.Fatal("local name override was not active before source rotation")
	}

	rotated := f.refresh(t, sourcePreviewURI("stable-ac04", "Upstream renamed", "new-ac04.example.invalid", "SYNTHETIC_AC04_NEW"))
	if len(rotated.Candidates) != 1 || !rotated.Candidates[0].AutoApplied || rotated.Candidates[0].ChangeKind != "modified" {
		t.Fatal("stable credential rotation was not classified as an automatically applied modification")
	}

	nodes = requireNodeList(t, f.h.do(http.MethodGet, "/api/v1/nodes", "", "", nil))
	if len(nodes.Data) != 1 || nodes.Data[0].Metadata.ResourceID != initial.Metadata.ResourceID {
		t.Fatal("stable credential rotation changed node identity or created a duplicate")
	}
	current := requireNode(t, f.h.do(http.MethodGet, "/api/v1/nodes/"+string(initial.Metadata.ResourceID), "", "", nil), http.StatusOK)
	if current.Metadata.Name != customName || current.Node.Endpoint.Host != "new-ac04.example.invalid" {
		t.Fatal("credential rotation lost the local name override or the new upstream endpoint")
	}
	if current.Metadata.SecurityEpoch != local.Metadata.SecurityEpoch+1 {
		t.Fatal("credential rotation did not increment the node security epoch exactly once")
	}
	if current.Binding == nil || current.Binding.SourceItemID != itemID || current.Binding.MatchMethod != ir.StableExternalKey || current.Binding.OriginState != string(override.Active) {
		t.Fatal("credential rotation lost the stable active source binding")
	}

	stored, err := f.h.env.store.Head(f.h.env.ctx, f.h.env.scope, initial.Metadata.ResourceID)
	if err != nil {
		t.Fatal("rotated node could not be read from storage")
	}
	password, ok := stored.Payload.(*ir.Node).Auth.(*ir.PasswordAuth)
	if !ok || password.Password != "SYNTHETIC_AC04_NEW" {
		t.Fatal("stable source rotation did not persist the new credential")
	}
}
