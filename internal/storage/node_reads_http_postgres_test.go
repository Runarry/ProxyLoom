package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/server"
)

const nodeAcceptanceOrigin = "http://127.0.0.1:8080"
const nodeAcceptancePassword = "SYNTHETIC_T011_ADMIN_PASSWORD"
const nodeRotatedSecret = "SYNTHETIC_T011_ROTATED_CREDENTIAL"

type nodeHTTPAcceptance struct {
	env    *postgresEnv
	h      *server.Handler
	cookie *http.Cookie
	csrf   string
	actor  ir.ID
}

func newNodeHTTPAcceptance(t *testing.T) *nodeHTTPAcceptance {
	t.Helper()
	env := newPostgres(t, true)
	setup := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x53}, 32))
	service, err := NewIdentity(env.runtime, identity.Options{ScopeID: env.scope, SetupToken: []byte(setup), TokenPepper: bytes.Repeat([]byte{0x42}, 32)})
	if err != nil {
		t.Fatal("node acceptance identity construction failed")
	}
	mac, err := apicontract.NewCursorHMAC(bytes.Repeat([]byte{0x57}, 32))
	if err != nil {
		t.Fatal("node acceptance cursor key failed")
	}
	codec, err := apicontract.NewCursorCodec(mac)
	if err != nil {
		t.Fatal("node acceptance cursor construction failed")
	}
	web := t.TempDir()
	if os.WriteFile(filepath.Join(web, "index.html"), []byte("<html>synthetic</html>"), 0600) != nil {
		t.Fatal("node acceptance static fixture failed")
	}
	handler, err := server.NewHandler(web, server.Dependencies{Database: env.runtime.Ping, Secrets: func() error { return nil },
		Identity: service, PublicURL: nodeAcceptanceOrigin, Development: true,
		Nodes: &server.NodeDependencies{Repository: env.store, Cursor: codec}}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal("node acceptance handler construction failed")
	}
	t.Cleanup(func() { _ = handler.Close() })
	h := &nodeHTTPAcceptance{env: env, h: handler}
	response := h.do(http.MethodPost, "/api/v1/setup", `{"setup_token":"`+setup+`","username":"node-admin","password":"`+nodeAcceptancePassword+`"}`, "", nil)
	h.setSession(t, response, http.StatusCreated)
	return h
}

func (h *nodeHTTPAcceptance) do(method, path, body, etag string, modify func(*http.Request)) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, nodeAcceptanceOrigin+path, strings.NewReader(body))
	request.RemoteAddr = "127.0.0.1:12345"
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		request.Header.Set("Origin", nodeAcceptanceOrigin)
		if h.csrf != "" {
			request.Header.Set("X-CSRF-Token", h.csrf)
		}
	}
	if h.cookie != nil {
		request.AddCookie(h.cookie)
	}
	if etag != "" {
		request.Header.Set("If-Match", etag)
	}
	if modify != nil {
		modify(request)
	}
	response := httptest.NewRecorder()
	h.h.ServeHTTP(response, request)
	return response
}

func (h *nodeHTTPAcceptance) setSession(t *testing.T, response *httptest.ResponseRecorder, status int) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("authentication returned HTTP %d, expected %d", response.Code, status)
	}
	var session apicontract.SessionResponse
	if json.Unmarshal(response.Body.Bytes(), &session) != nil {
		t.Fatal("invalid authentication DTO")
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("authentication did not issue one session cookie")
	}
	h.cookie, h.csrf = cookies[0], session.Data.CSRFToken
	h.actor = session.Data.UserID
}

func requireNodeStatus(t *testing.T, response *httptest.ResponseRecorder, expected int) {
	t.Helper()
	if response.Code != expected {
		t.Fatalf("node API returned HTTP %d, expected %d", response.Code, expected)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("node response was cacheable")
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("node response omitted request ID")
	}
	for _, secret := range []string{postgresTestSecret, nodeRotatedSecret, nodeAcceptancePassword} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatal("ordinary node response disclosed a synthetic secret")
		}
	}
}

func requireNode(t *testing.T, response *httptest.ResponseRecorder, expected int) apicontract.NodeResource {
	t.Helper()
	requireNodeStatus(t, response, expected)
	var body apicontract.NodeReadResponse
	if json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatal("invalid node read DTO")
	}
	etag, _ := apicontract.ETag(int64(body.Data.Metadata.Revision))
	if response.Header().Get("ETag") != etag {
		t.Fatal("node response ETag and revision disagree")
	}
	return body.Data
}

func nodeBody(t *testing.T, name string, node *ir.Node, tags []string, enabled bool) string {
	t.Helper()
	data, err := json.Marshal(struct {
		Name    string   `json:"name"`
		Node    *ir.Node `json:"node"`
		Tags    []string `json:"tags"`
		Enabled bool     `json:"enabled"`
	}{name, node, tags, enabled})
	if err != nil {
		t.Fatal("node fixture encoding failed")
	}
	return string(data)
}

func (h *nodeHTTPAcceptance) create(t *testing.T, name string, node *ir.Node, tags []string, enabled bool) apicontract.NodeResource {
	t.Helper()
	return requireNode(t, h.do(http.MethodPost, "/api/v1/nodes", nodeBody(t, name, node, tags, enabled), "", nil), http.StatusCreated)
}

func nodePath(id ir.ID) string          { return "/api/v1/nodes/" + string(id) }
func revisionTag(revision int64) string { value, _ := apicontract.ETag(revision); return value }

func requireNodeList(t *testing.T, response *httptest.ResponseRecorder) apicontract.NodeListResponse {
	t.Helper()
	requireNodeStatus(t, response, http.StatusOK)
	var page apicontract.NodeListResponse
	if json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatal("invalid node list DTO")
	}
	return page
}

func TestPostgresNodeCRUDHistoryCloneAndReferences(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	initial := h.create(t, "Primary_%", syntheticNode(), []string{"blue", "shared"}, true)
	id := initial.Metadata.ResourceID
	if initial.Metadata.Revision != 1 || initial.Metadata.SecurityEpoch != 1 || initial.Metadata.ScopeID != h.env.scope {
		t.Fatal("node creation metadata was not server-owned")
	}
	otherNode := syntheticNode()
	otherNode.Protocol, otherNode.Auth, otherNode.Security = ir.SOCKS5, &ir.NoAuth{Kind: ir.AuthNone}, &ir.NoSecurity{Mode: ir.SecurityNone}
	other := h.create(t, "Secondary", otherNode, []string{"blue"}, false)
	filtered := requireNodeList(t, h.do(http.MethodGet, "/api/v1/nodes?q=%25&tag=blue&enabled=true&protocol=trojan", "", "", nil))
	if len(filtered.Data) != 1 || filtered.Data[0].Metadata.ResourceID != id {
		t.Fatal("name/tag/enabled/protocol filters did not intersect literally")
	}
	page := requireNodeList(t, h.do(http.MethodGet, "/api/v1/nodes?limit=1", "", "", nil))
	if len(page.Data) != 1 || page.Page.NextCursor == "" {
		t.Fatal("node pagination did not expose the next page")
	}
	second := requireNodeList(t, h.do(http.MethodGet, "/api/v1/nodes?limit=1&cursor="+url.QueryEscape(page.Page.NextCursor), "", "", nil))
	if len(second.Data) != 1 || second.Data[0].Metadata.ResourceID == page.Data[0].Metadata.ResourceID || second.Page.NextCursor != "" {
		t.Fatal("node pagination repeated or lost a row")
	}
	requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/nodes?tag=blue&cursor="+url.QueryEscape(page.Page.NextCursor), "", "", nil), http.StatusBadRequest)
	requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/nodes?cursor=X"+url.QueryEscape(page.Page.NextCursor[1:]), "", "", nil), http.StatusBadRequest)

	updated := requireNode(t, h.do(http.MethodPatch, nodePath(id), `{"name":"Renamed","node":{"auth":{"kind":"password"}}}`, `"r1"`, nil), http.StatusOK)
	if updated.Metadata.Revision != 2 || updated.Metadata.SecurityEpoch != 1 {
		t.Fatal("ordinary update changed the wrong revision counters")
	}
	head, err := h.env.store.Head(h.env.ctx, h.env.scope, id)
	if err != nil || head.Payload.(*ir.Node).Auth.(*ir.PasswordAuth).Password != ir.Secret(postgresTestSecret) {
		t.Fatal("omitted credential did not preserve stored value")
	}
	requireNodeStatus(t, h.do(http.MethodPatch, nodePath(id), `{"node":{"auth":{"kind":"password","password":null}}}`, `"r2"`, nil), http.StatusUnprocessableEntity)
	requireNodeStatus(t, h.do(http.MethodPatch, nodePath(id), `{"name":"first","name":"second"}`, `"r2"`, nil), http.StatusBadRequest)
	requireNodeStatus(t, h.do(http.MethodPatch, nodePath(id), `{"security_epoch":"99"}`, `"r2"`, nil), http.StatusBadRequest)
	requireNodeStatus(t, h.do(http.MethodPatch, nodePath(id), `{"name":"stale"}`, `"r1"`, nil), http.StatusPreconditionFailed)
	requireNodeStatus(t, h.do(http.MethodPatch, nodePath(id), `{"name":"missing"}`, "", nil), http.StatusPreconditionRequired)
	clone := requireNode(t, h.do(http.MethodPost, nodePath(id)+"/clone", `{"name":"Copy"}`, `"r2"`, nil), http.StatusCreated)
	if clone.Metadata.ResourceID == id || clone.Metadata.Revision != 1 || clone.Metadata.SecurityEpoch != 1 || !slices.Equal(clone.Metadata.Tags, updated.Metadata.Tags) || !clone.Metadata.Enabled {
		t.Fatal("clone did not create an independent resource with preserved metadata")
	}
	copyHead, err := h.env.store.Head(h.env.ctx, h.env.scope, clone.Metadata.ResourceID)
	if err != nil || copyHead.Payload.(*ir.Node).Auth.(*ir.PasswordAuth).Password != ir.Secret(postgresTestSecret) {
		t.Fatal("clone did not preserve the encrypted credential")
	}
	requireNodeStatus(t, h.do(http.MethodPost, nodePath(id)+"/clone", `{"name":"Stale copy"}`, `"r1"`, nil), http.StatusPreconditionFailed)
	requireNodeStatus(t, h.do(http.MethodPost, nodePath(id)+"/clone", `{"name":"No precondition"}`, "", nil), http.StatusPreconditionRequired)

	rotated := requireNode(t, h.do(http.MethodPatch, nodePath(id), `{"node":{"auth":{"kind":"password","password":"`+nodeRotatedSecret+`"}}}`, `"r2"`, nil), http.StatusOK)
	if rotated.Metadata.Revision != 3 || rotated.Metadata.SecurityEpoch != 2 {
		t.Fatal("credential rotation did not invalidate the old epoch")
	}
	disabled := requireNode(t, h.do(http.MethodPatch, nodePath(id), `{"enabled":false}`, `"r3"`, nil), http.StatusOK)
	enabled := requireNode(t, h.do(http.MethodPatch, nodePath(id), `{"enabled":true}`, `"r4"`, nil), http.StatusOK)
	if disabled.Metadata.SecurityEpoch != 3 || enabled.Metadata.SecurityEpoch != 3 || !enabled.Metadata.Enabled {
		t.Fatal("disable/re-enable security epoch semantics changed")
	}
	chain := mustCreate(t, h.env, catalog.CreateInput{Name: "Referrer", Tags: []string{}, Enabled: true, Payload: &ir.Chain{SchemaVersion: 1,
		Hops: []ir.NodeRef{{NodeID: id}, {NodeID: clone.Metadata.ResourceID}}, FailurePolicy: ir.FailClosed}})
	if err := h.env.store.Transact(h.env.ctx, h.env.scope, func(tx catalog.Tx) error {
		_, err := tx.Update(h.env.ctx, chain.Metadata.ResourceID, 1, catalog.UpdateInput{Name: "Referrer v2", Tags: []string{}, Enabled: true, Payload: chain.Payload})
		return err
	}); err != nil {
		t.Fatal("reference history fixture failed")
	}
	for _, state := range []string{"active", "historical", "all"} {
		response := h.do(http.MethodGet, nodePath(id)+"/references?reference_state="+state+"&limit=1", "", "", nil)
		requireNodeStatus(t, response, http.StatusOK)
		var refs apicontract.ReferenceListResponse
		if json.Unmarshal(response.Body.Bytes(), &refs) != nil || len(refs.Data) != 1 || refs.Data[0].SourceKind != ir.KindChain {
			t.Fatal("reference response lost source type")
		}
		if state != "all" && refs.Data[0].State != state {
			t.Fatal("reference-state filter returned the wrong revision")
		}
		if state == "all" {
			if refs.Page.NextCursor == "" {
				t.Fatal("reference pagination omitted remaining history")
			}
			next := h.do(http.MethodGet, nodePath(id)+"/references?reference_state=all&limit=1&cursor="+url.QueryEscape(refs.Page.NextCursor), "", "", nil)
			requireNodeStatus(t, next, http.StatusOK)
			var nextRefs apicontract.ReferenceListResponse
			if json.Unmarshal(next.Body.Bytes(), &nextRefs) != nil || len(nextRefs.Data) != 1 || nextRefs.Data[0].SourceRevision == refs.Data[0].SourceRevision {
				t.Fatal("reference cursor repeated history")
			}
		}
	}
	batchBody := `{"operation":"set_enabled","node_ids":["` + string(id) + `","` + string(clone.Metadata.ResourceID) + `","` + string(other.Metadata.ResourceID) + `","30000000-0000-4000-8000-000000000099"],"preconditions":[{"node_id":"` + string(id) + `","revision":"5"},{"node_id":"` + string(clone.Metadata.ResourceID) + `","revision":"99"},{"node_id":"30000000-0000-4000-8000-000000000099","revision":"1"}],"enabled":false}`
	batchResponse := h.do(http.MethodPost, "/api/v1/nodes/batch", batchBody, "", nil)
	requireNodeStatus(t, batchResponse, http.StatusOK)
	var batch apicontract.NodeBatchResponse
	if json.Unmarshal(batchResponse.Body.Bytes(), &batch) != nil || len(batch.Data) != 4 {
		t.Fatal("invalid batch response")
	}
	for index, status := range []int{200, 412, 428, 404} {
		if batch.Data[index].HTTPStatus != status {
			t.Fatal("batch did not preserve per-item preconditions and outcomes")
		}
	}
	for index, operation := range []string{"add_tags", "remove_tags"} {
		body := `{"operation":"` + operation + `","node_ids":["` + string(clone.Metadata.ResourceID) + `"],"preconditions":[{"node_id":"` + string(clone.Metadata.ResourceID) + `","revision":"` + strconv.Itoa(index+1) + `"}],"tags":["shared","new"]}`
		requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/nodes/batch", body, "", nil), http.StatusOK)
	}
	cloneRead := requireNode(t, h.do(http.MethodGet, nodePath(clone.Metadata.ResourceID), "", "", nil), http.StatusOK)
	if cloneRead.Metadata.Revision != 3 || !slices.Equal(cloneRead.Metadata.Tags, []string{"blue"}) {
		t.Fatal("batch tag set operations did not preserve unrelated tags")
	}

	requireNodeStatus(t, h.do(http.MethodDelete, nodePath(id), "", `"r6"`, nil), http.StatusOK)
	requireNodeStatus(t, h.do(http.MethodGet, nodePath(id), "", "", nil), http.StatusNotFound)
	requireNodeStatus(t, h.do(http.MethodPost, nodePath(id)+"/clone", `{"name":"Deleted clone"}`, `"r7"`, nil), http.StatusNotFound)
	history := requireNodeList(t, h.do(http.MethodGet, nodePath(id)+"/revisions?limit=2", "", "", nil))
	if len(history.Data) != 2 || history.Data[0].Metadata.Name != "Primary_%" || history.Data[0].Metadata.Revision != 1 || history.Data[1].Metadata.Name != "Renamed" || history.Page.NextCursor == "" {
		t.Fatal("historical metadata was replaced by the editing head")
	}
	seen := len(history.Data)
	for history.Page.NextCursor != "" {
		history = requireNodeList(t, h.do(http.MethodGet, nodePath(id)+"/revisions?limit=2&cursor="+url.QueryEscape(history.Page.NextCursor), "", "", nil))
		seen += len(history.Data)
	}
	if seen != 7 || history.Data[len(history.Data)-1].Metadata.SecurityEpoch != 5 {
		t.Fatal("deletion lost history or did not revoke the epoch")
	}
	requireNodeStatus(t, h.do(http.MethodGet, nodePath(id)+"/references", "", "", nil), http.StatusOK)
}

func TestPostgresNodeAuthenticationRevealConcurrencyAndAudit(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Cookie") }, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x46}, 32)))
	}} {
		requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/nodes", "", "", change), http.StatusUnauthorized)
	}
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/nodes", nodeBody(t, "Rejected", syntheticNode(), []string{}, true), "", func(r *http.Request) { r.Header.Set("X-CSRF-Token", "wrong") }), http.StatusForbidden)
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/nodes", nodeBody(t, "Rejected", syntheticNode(), []string{}, true), "", func(r *http.Request) { r.Header.Set("Origin", "http://foreign.invalid") }), http.StatusForbidden)
	uncheckedSource := syntheticNode()
	uncheckedSource.Origin = &ir.Origin{SourceResourceID: "30000000-0000-4000-8000-000000000098", SourceItemID: "30000000-0000-4000-8000-000000000099", MatchMethod: ir.ManualBinding}
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/nodes", nodeBody(t, "Unchecked provenance", uncheckedSource, []string{}, true), "", nil), http.StatusUnprocessableEntity)
	node := h.create(t, "Concurrent", syntheticNode(), []string{}, true)
	id := node.Metadata.ResourceID
	for _, invalid := range []string{"?limit=0", "?limit=01", "?enabled=yes", "?protocol=unknown", "?q=first&q=second", "?source_id=30000000-0000-4000-8000-000000000099"} {
		requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/nodes"+invalid, "", "", nil), http.StatusBadRequest)
	}
	otherScope := ir.ID("10000000-0000-4000-8000-000000000099")
	if h.env.store.EnsureScope(h.env.ctx, otherScope, "Other scope") != nil {
		t.Fatal("cross-scope fixture setup failed")
	}
	var foreign ir.Resource
	if h.env.store.Transact(h.env.ctx, otherScope, func(tx catalog.Tx) error {
		var err error
		foreign, err = tx.Create(h.env.ctx, catalog.CreateInput{Name: "Foreign", Tags: []string{}, Enabled: true, Payload: syntheticNode()})
		return err
	}) != nil {
		t.Fatal("cross-scope node fixture failed")
	}
	requireNodeStatus(t, h.do(http.MethodGet, nodePath(foreign.Metadata.ResourceID), "", "", nil), http.StatusNotFound)
	requireNodeStatus(t, h.do(http.MethodPatch, nodePath(foreign.Metadata.ResourceID), `{"name":"Unauthorized"}`, `"r1"`, nil), http.StatusNotFound)
	requireNodeStatus(t, h.do(http.MethodGet, nodePath(foreign.Metadata.ResourceID)+"/revisions", "", "", nil), http.StatusNotFound)

	var wait sync.WaitGroup
	results := make(chan int, 2)
	for _, name := range []string{"First edit", "Second edit"} {
		wait.Go(func() { results <- h.do(http.MethodPatch, nodePath(id), `{"name":"`+name+`"}`, `"r1"`, nil).Code })
	}
	wait.Wait()
	close(results)
	counts := map[int]int{}
	for status := range results {
		counts[status]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusPreconditionFailed] != 1 {
		t.Fatalf("concurrent patches did not produce exactly one CAS winner: HTTP status counts %v", counts)
	}
	var audits int
	if h.env.runtime.QueryRow(h.env.ctx, "SELECT count(*) FROM public.resource_audit_events WHERE object_id=$1 AND action='node.update'", dbID(id)).Scan(&audits) != nil || audits != 1 {
		t.Fatal("concurrent update did not commit exactly one mutation audit")
	}
	requireNodeStatus(t, h.do(http.MethodPost, nodePath(id)+"/reveal", "", `"r2"`, nil), http.StatusForbidden)
	h.setSession(t, h.do(http.MethodPost, "/api/v1/auth/reauth", `{"password":"`+nodeAcceptancePassword+`"}`, "", nil), http.StatusOK)
	requireNodeStatus(t, h.do(http.MethodPost, nodePath(id)+"/reveal", "", `"r1"`, nil), http.StatusPreconditionFailed)
	reveal := h.do(http.MethodPost, nodePath(id)+"/reveal", "", `"r2"`, nil)
	if reveal.Code != http.StatusOK || reveal.Header().Get("Cache-Control") != "no-store" || !strings.Contains(reveal.Body.String(), postgresTestSecret) {
		t.Fatal("reauthenticated reveal did not release the selected credential")
	}
	if h.env.runtime.QueryRow(h.env.ctx, "SELECT count(*) FROM public.identity_audit_events WHERE action='secret.reveal' AND outcome='success'").Scan(&audits) != nil || audits != 1 {
		t.Fatal("revealed output did not have a committed sensitive audit")
	}
	var priorRevisions int
	if h.env.runtime.QueryRow(h.env.ctx, "SELECT count(*) FROM public.resource_revisions WHERE resource_id=$1", dbID(id)).Scan(&priorRevisions) != nil {
		t.Fatal("audit rollback baseline failed")
	}
	if _, err := h.env.admin.Exec(h.env.ctx, "REVOKE INSERT (id,scope_id,actor_id,object_id,revision,action,request_id) ON public.resource_audit_events FROM proxyloom"); err != nil {
		t.Fatal("audit failure fixture failed")
	}
	requireNodeStatus(t, h.do(http.MethodPatch, nodePath(id), `{"name":"Must roll back"}`, `"r2"`, nil), http.StatusServiceUnavailable)
	var afterRevisions int
	if h.env.runtime.QueryRow(h.env.ctx, "SELECT count(*) FROM public.resource_revisions WHERE resource_id=$1", dbID(id)).Scan(&afterRevisions) != nil || afterRevisions != priorRevisions {
		t.Fatal("audit failure committed an unaudited resource revision")
	}
	if _, err := h.env.admin.Exec(h.env.ctx, "REVOKE INSERT (id,scope_id,actor_id,action,outcome,account_hash,source_hash,request_id) ON public.identity_audit_events FROM proxyloom"); err != nil {
		t.Fatal("sensitive audit failure fixture failed")
	}
	requireNodeStatus(t, h.do(http.MethodPost, nodePath(id)+"/reveal", "", `"r2"`, nil), http.StatusServiceUnavailable)
	h.env.runtime.Close()
	requireNodeStatus(t, h.do(http.MethodGet, nodePath(id), "", "", nil), http.StatusServiceUnavailable)
}

func TestPostgresNodeAuditLockOrderAndImmutability(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	node := h.create(t, "Audit locking", syntheticNode(), []string{}, true)
	id := node.Metadata.ResourceID
	// Reproduce the identity side of the previous lock inversion deterministically.
	// Its administrator lock must not block the scope-first catalog audit write.
	actorLock, err := h.env.admin.Begin(h.env.ctx)
	if err != nil {
		t.Fatal("actor lock fixture could not start")
	}
	defer func() { _ = actorLock.Rollback(context.Background()) }()
	if _, err := actorLock.Exec(h.env.ctx, "SELECT id FROM public.users WHERE id=$1 FOR UPDATE", dbID(h.actor)); err != nil {
		t.Fatal("actor lock fixture failed")
	}
	deadline, cancel := context.WithTimeout(h.env.ctx, 2*time.Second)
	defer cancel()
	err = h.env.store.Transact(deadline, h.env.scope, func(tx catalog.Tx) error {
		audited := tx.(catalog.AuditedTx)
		old, err := audited.Head(deadline, id)
		if err != nil {
			return err
		}
		next, err := tx.Update(deadline, id, 1, catalog.UpdateInput{Name: "Audit commit", Tags: old.Metadata.Tags, Enabled: true, Payload: old.Payload})
		if err != nil {
			return err
		}
		return audited.Audit(deadline, catalog.MutationAudit{PrincipalID: h.actor, ObjectID: id, Revision: next.Metadata.Revision, RequestID: "test-lock-order", Action: catalog.AuditNodeUpdate})
	})
	if err != nil {
		t.Fatal("scope-first audit waited on the identity administrator lock")
	}
	if actorLock.Rollback(h.env.ctx) != nil {
		t.Fatal("actor lock fixture release failed")
	}

	// Server methods also refuse an actor outside the audit scope and latch an
	// ignored audit failure so no unaudited preceding revision can be committed.
	err = h.env.store.Transact(h.env.ctx, h.env.scope, func(tx catalog.Tx) error {
		audited := tx.(catalog.AuditedTx)
		old, err := audited.Head(h.env.ctx, id)
		if err != nil {
			return err
		}
		next, err := tx.Update(h.env.ctx, id, 2, catalog.UpdateInput{Name: "Must not commit", Tags: old.Metadata.Tags, Enabled: true, Payload: old.Payload})
		if err != nil {
			return err
		}
		_ = audited.Audit(h.env.ctx, catalog.MutationAudit{PrincipalID: "20000000-0000-4000-8000-000000000099", ObjectID: id,
			Revision: next.Metadata.Revision, RequestID: "test-invalid-actor", Action: catalog.AuditNodeUpdate})
		return nil
	})
	if err == nil {
		t.Fatal("an ignored invalid-actor audit failure committed")
	}
	head, err := h.env.store.Head(h.env.ctx, h.env.scope, id)
	if err != nil || head.Metadata.Revision != 2 || head.Metadata.Name != "Audit commit" {
		t.Fatal("failed audit changed the resource history")
	}
	for _, statement := range []string{
		"UPDATE public.resource_audit_events SET outcome='success'",
		"DELETE FROM public.resource_audit_events",
		"TRUNCATE public.resource_audit_events",
	} {
		if _, err := h.env.admin.Exec(h.env.ctx, statement); err == nil {
			t.Fatal("audit history permitted destructive modification")
		}
	}
}

func TestPostgresNodeBatchAcceptsTwoHundredPreconditions(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	enabled := false
	request := apicontract.NodeBatchRequest{Operation: "set_enabled", NodeIDs: make([]ir.ID, 0, 200), Preconditions: make([]apicontract.NodePrecondition, 0, 200), Enabled: &enabled}
	for index := 1; index <= 200; index++ {
		id := ir.ID(fmt.Sprintf("30000000-0000-4000-8000-%012d", index))
		request.NodeIDs = append(request.NodeIDs, id)
		request.Preconditions = append(request.Preconditions, apicontract.NodePrecondition{NodeID: id, Revision: 1})
	}
	encoded, err := json.Marshal(request)
	if err != nil || len(encoded) <= server.MaxAuthJSONBytes {
		t.Fatal("bounded batch fixture did not exercise the management request limit")
	}
	response := h.do(http.MethodPost, "/api/v1/nodes/batch", string(encoded), "", nil)
	requireNodeStatus(t, response, http.StatusOK)
	var result apicontract.NodeBatchResponse
	if json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Data) != 200 {
		t.Fatal("valid maximum-size batch did not retain all item outcomes")
	}
	for index, item := range result.Data {
		if item.NodeID != request.NodeIDs[index] || item.HTTPStatus != http.StatusNotFound {
			t.Fatal("bounded batch changed request order or fabricated a node")
		}
	}
}
