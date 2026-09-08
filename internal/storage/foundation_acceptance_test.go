package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestPostgresFoundationAPITransactionBoundary(t *testing.T) {
	env := newPostgres(t, true)
	const (
		originalUser     = "EXAMPLE_ONLY_BOUNDARY_USER"
		originalPassword = "EXAMPLE_ONLY_BOUNDARY_PASSWORD"
		rotatedPassword  = "EXAMPLE_ONLY_BOUNDARY_ROTATED"
	)
	createJSON := []byte(`{
		"name":"node-before","tags":["z","a"],"enabled":true,
		"node":{"schema_version":1,"protocol":"http",
		"endpoint":{"host":"boundary.example.invalid","port":443},
		"auth":{"kind":"username_password","username":"` + originalUser + `","password":"` + originalPassword + `"},
		"transport":{"kind":"native_tcp"},"unknown_credential_field":"ignored",
		"security":{"mode":"none"},"features":{"udp":false},"extensions":{}}
	}`)
	var rejected apicontract.NodeCreateRequest
	if err := apicontract.Decode(createJSON, "NodeCreateRequest", &rejected); apicontract.AsError(err).Code() != apicontract.UnknownField {
		t.Fatal("unknown field did not produce the expected contract error")
	}
	createJSON = []byte(`{
		"name":"node-before","tags":["z","a"],"enabled":true,
		"node":{"schema_version":1,"protocol":"http",
		"endpoint":{"host":"boundary.example.invalid","port":443},
		"auth":{"kind":"username_password","username":"` + originalUser + `","password":"` + originalPassword + `"},
		"transport":{"kind":"native_tcp"},
		"security":{"mode":"none"},"features":{"udp":false},"extensions":{}}
	}`)
	var create apicontract.NodeCreateRequest
	if err := apicontract.Decode(createJSON, "NodeCreateRequest", &create); err != nil {
		t.Fatal("strict API decoding rejected a valid node create request")
	}
	input, err := create.Input()
	if err != nil {
		t.Fatal("typed API create conversion failed")
	}

	principal := ir.ID("20000000-0000-4000-8000-000000000002")
	header := http.Header{"Idempotency-Key": []string{"boundary-create-1"}}
	metadata, err := apicontract.ReadIdempotency(header, env.scope, principal, "nodes.create")
	if err != nil {
		t.Fatal("valid idempotency metadata was rejected")
	}
	request, err := metadata.Request(createJSON, "NodeCreateRequest")
	if err != nil {
		t.Fatal("valid idempotent API request was rejected")
	}
	var created ir.Resource
	result, err := env.store.ExecuteIdempotent(env.ctx, request, func(tx catalog.Tx) (catalog.Receipt, error) {
		created, err = tx.Create(env.ctx, input)
		if err != nil {
			return catalog.Receipt{}, err
		}
		return catalog.Receipt{HTTPStatus: 201, ResourceID: created.Metadata.ResourceID, Revision: created.Metadata.Revision, Status: catalog.ReceiptCreated}, nil
	})
	if err != nil || result.Replayed || result.Receipt.ResourceID != created.Metadata.ResourceID {
		t.Fatal("idempotent API create transaction failed")
	}
	called := false
	result, err = env.store.ExecuteIdempotent(env.ctx, request, func(catalog.Tx) (catalog.Receipt, error) {
		called = true
		return catalog.Receipt{}, errors.New("replay callback must not execute")
	})
	if err != nil || !result.Replayed || called || result.Receipt.ResourceID != created.Metadata.ResourceID {
		t.Fatal("same authenticated API request did not replay its typed receipt")
	}

	head, err := env.store.Head(env.ctx, env.scope, created.Metadata.ResourceID)
	if err != nil {
		t.Fatal("created resource could not be read")
	}
	assertRedactedNodeResponse(t, head, originalUser, originalPassword, rotatedPassword)
	assertFoundationState(t, env, head, 1, 1, 1)

	patchJSON := []byte(`{"name":"node-after","tags":["updated"],"node":{"auth":{"kind":"username_password","password":"` + rotatedPassword + `"}}}`)
	var patch apicontract.NodePatchRequest
	if err := apicontract.Decode(patchJSON, "NodePatchRequest", &patch); err != nil {
		t.Fatal("strict API decoding rejected a valid secret-preserving patch")
	}
	update, err := patch.Merge(head)
	if err != nil {
		t.Fatal("secret-preserving patch merge failed")
	}
	var updated ir.Resource
	if err = env.store.Transact(env.ctx, env.scope, func(tx catalog.Tx) error {
		updated, err = tx.Update(env.ctx, head.Metadata.ResourceID, head.Metadata.Revision, update)
		return err
	}); err != nil {
		t.Fatal("typed patch transaction failed")
	}
	updatedAuth := updated.Payload.(*ir.Node).Auth.(*ir.UsernamePasswordAuth)
	if string(updatedAuth.Username) != originalUser || string(updatedAuth.Password) != rotatedPassword {
		t.Fatal("absent username was not preserved or password was not replaced")
	}
	assertFoundationState(t, env, updated, 2, 2, 2)
	assertRedactedNodeResponse(t, updated, originalUser, originalPassword, rotatedPassword)

	historical, err := env.store.Revision(env.ctx, env.scope, created.Metadata.ResourceID, 1)
	if err != nil {
		t.Fatal("historical revision could not be read")
	}
	historicalAuth := historical.Payload.(*ir.Node).Auth.(*ir.UsernamePasswordAuth)
	if historical.Metadata.Name != "node-before" || len(historical.Metadata.Tags) != 2 || string(historicalAuth.Username) != originalUser || string(historicalAuth.Password) != originalPassword {
		t.Fatal("historical read mixed current metadata or payload into revision one")
	}

	clearJSON := []byte(`{"node":{"auth":{"kind":"username_password","username":null}}}`)
	var clearPatch apicontract.NodePatchRequest
	if err := apicontract.Decode(clearJSON, "NodePatchRequest", &clearPatch); err != nil {
		t.Fatal("strict API decoding rejected an explicit secret clear")
	}
	if _, err := clearPatch.Merge(updated); apicontract.AsError(err).Code() != apicontract.ValidationFailed {
		t.Fatal("clearing a required authentication field did not fail with 422 semantics")
	}
	current, err := env.store.Head(env.ctx, env.scope, created.Metadata.ResourceID)
	if err != nil {
		t.Fatal("head read after rejected patch failed")
	}
	assertFoundationState(t, env, current, 2, 2, 2)
}

func assertRedactedNodeResponse(t *testing.T, resource ir.Resource, secrets ...string) {
	t.Helper()
	response, err := apicontract.NewNodeReadResponse("boundary-request", resource)
	if err != nil {
		t.Fatal("node response projection failed")
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal("node response encoding failed")
	}
	for _, secret := range secrets {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatal("ordinary node response leaked a credential")
		}
	}
	if !bytes.Contains(encoded, []byte(`"has_username":true`)) || !bytes.Contains(encoded, []byte(`"has_password":true`)) {
		t.Fatal("ordinary node response omitted configured secret state")
	}
}

func assertFoundationState(t *testing.T, env *postgresEnv, resource ir.Resource, revision, epoch, catalogRevision int64) {
	t.Helper()
	scope, err := env.store.Scope(env.ctx, env.scope)
	if err != nil {
		t.Fatal("scope counters could not be read")
	}
	if resource.Metadata.Revision != revision || resource.Metadata.SecurityEpoch != epoch || scope.CatalogRevision != catalogRevision {
		t.Fatalf("unexpected foundation counters: revision=%d epoch=%d catalog=%d", resource.Metadata.Revision, resource.Metadata.SecurityEpoch, scope.CatalogRevision)
	}
}
