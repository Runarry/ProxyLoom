package storage

import (
	"bytes"
	"encoding/json"
	"github.com/Runarry/ProxyLoom/api"
	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/subscriptions"
	"net/http"
	"net/http/httptest"
	"testing"
)

func subJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal("synthetic request encoding failed")
	}
	return string(b)
}
func requireSubDTO(t *testing.T, r *httptest.ResponseRecorder, status int, schema string) {
	t.Helper()
	requireNodeStatus(t, r, status)
	var v any
	if json.Unmarshal(r.Body.Bytes(), &v) != nil || api.Validate(schema, v) != nil {
		t.Fatal("subscription response does not match", schema)
	}
	if bytes.Contains(r.Body.Bytes(), []byte(postgresTestSecret)) {
		t.Fatal("management response exposed a credential")
	}
}
func TestPostgresSubscriptionHTTPContractAndAuthorization(t *testing.T) {
	h := newNodeHTTPAcceptance(t, true)
	fixture := publicationTestEnv(t, h.env)
	fixture.actor.ID = h.actor
	path := "/api/v1/subscriptions/" + string(fixture.profile.Metadata.ResourceID)
	read := h.do(http.MethodGet, path, "", "", nil)
	requireSubDTO(t, read, 200, "SubscriptionResponse")
	requireSubDTO(t, h.do(http.MethodGet, "/api/v1/subscriptions?limit=1", "", "", nil), 200, "SubscriptionListResponse")
	requireSubDTO(t, h.do(http.MethodGet, "/api/v1/cores?limit=1", "", "", nil), 200, "CoreListResponse")
	requireNodeStatus(t, h.do(http.MethodPatch, path, `{"name":"changed"}`, revisionTag(2), nil), 412)
	clone := h.do(http.MethodPost, path+"/clone", `{"name":"copy"}`, revisionTag(1), nil)
	requireSubDTO(t, clone, 201, "SubscriptionResponse")
	var copied struct {
		Data struct {
			Metadata apicontract.ResourceMetadata `json:"metadata"`
			Profile  ir.SubscriptionProfile       `json:"subscription"`
		} `json:"data"`
	}
	if json.Unmarshal(clone.Body.Bytes(), &copied) != nil || copied.Data.Metadata.ResourceID == fixture.profile.Metadata.ResourceID || copied.Data.Profile.Members.IncludeIDs[0] != fixture.node.Metadata.ResourceID {
		t.Fatal("clone copied resource identity")
	}
	b := fixture.validate(t, fixture.compile(t), false)
	r := h.do(http.MethodGet, "/api/v1/compile-batches/"+string(b.BatchID), "", "", nil)
	requireSubDTO(t, r, 200, "CompileBatchResponse")
	var rb struct {
		Data subscriptions.Batch `json:"data"`
	}
	if json.Unmarshal(r.Body.Bytes(), &rb) != nil {
		t.Fatal("invalid batch body")
	}
	request := subscriptions.PublishRequest{BatchID: b.BatchID, EffectivePreviewHash: rb.Data.EffectivePreviewHash}
	request.Confirmation.Acknowledged = true
	requireSubDTO(t, h.do(http.MethodPost, path+"/publish", subJSON(t, request), revisionTag(1), nil), 201, "PublicationResponse")
	requireSubDTO(t, h.do(http.MethodGet, path+"/publications?limit=1", "", "", nil), 200, "PublicationListResponse")
	issueBody := subJSON(t, subscriptions.TokenRequest{Name: "http", AllowedTargets: fixture.keys})
	requireNodeStatus(t, h.do(http.MethodPost, path+"/tokens", issueBody, "", nil), 403)
	// Reauthentication is a real session operation, not a forged timestamp.
	reauth := h.do(http.MethodPost, "/api/v1/auth/reauth", `{"password":"`+nodeAcceptancePassword+`"}`, "", nil)
	h.setSession(t, reauth, 200)
	token := h.do(http.MethodPost, path+"/tokens", issueBody, "", func(r *http.Request) { r.Header.Set("Idempotency-Key", "http-token") })
	requireSubDTO(t, token, 201, "TokenIssueResponse")
	var issued struct {
		Data subscriptions.TokenIssue `json:"data"`
	}
	if json.Unmarshal(token.Body.Bytes(), &issued) != nil || issued.Data.Token == "" {
		t.Fatal("missing one-time token")
	}
	publicPath := "/s/" + issued.Data.Token + "/" + fixture.keys[0]
	requireNodeStatus(t, h.do(http.MethodGet, path, "", "", func(r *http.Request) {
		r.Header.Del("Cookie")
		r.Header.Set("Authorization", "Bearer "+issued.Data.Token)
	}), http.StatusUnauthorized)
	download := h.do(http.MethodGet, publicPath, "", "", func(r *http.Request) { r.Header.Del("Cookie") })
	if download.Code != 200 {
		t.Fatalf("public download returned %d", download.Code)
	}
	if download.Header().Get("Cache-Control") != "private, no-store" || download.Body.Len() == 0 {
		t.Fatal("public snapshot headers or bytes missing")
	}
	requireSubDTO(t, h.do(http.MethodGet, path+"/tokens", "", "", nil), 200, "TokenListResponse")
	export := h.do(http.MethodPost, "/api/v1/exports", subJSON(t, map[string]any{"type": "publication", "publication_id": fixtureHead(t, fixture), "target_keys": []string{b.Outputs[0].TargetKey}, "format": b.Outputs[0].Format, "include_secrets": false}), "", nil)
	requireSubDTO(t, export, 200, "ExportResponse")
	revoke := h.do(http.MethodPost, "/api/v1/tokens/"+string(issued.Data.Metadata.TokenID)+"/revoke", `{"reason":"test"}`, revisionTag(1), nil)
	requireSubDTO(t, revoke, 200, "TokenResponse")
	revoked := h.do(http.MethodGet, publicPath, "", "", nil)
	if revoked.Code != 404 || revoked.Header().Get("Cache-Control") != "private, no-store" || bytes.Contains(revoked.Body.Bytes(), []byte(postgresTestSecret)) {
		t.Fatal("revoked download did not fail closed")
	}
	if _, err := fixture.store.Download(h.env.ctx, issued.Data.Token, fixture.keys[0]); err == nil {
		t.Fatal("revoke did not commit before response")
	}
}
func fixtureHead(t *testing.T, h *publicationTest) ir.ID {
	t.Helper()
	head, err := h.store.Head(h.env.ctx, h.env.scope, h.profile.Metadata.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	return head.PublicationID
}
