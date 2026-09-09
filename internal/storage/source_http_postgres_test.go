package storage

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/safefetch"
	"github.com/Runarry/ProxyLoom/internal/server"
)

const sourceItemPassword = "SYNTHETIC_T014_ITEM_PASSWORD"

type sourceHTTPAcceptance struct {
	*nodeHTTPAcceptance
	queue   *Jobs
	sources *Sources
}

func newSourceHTTPAcceptance(t *testing.T) *sourceHTTPAcceptance {
	t.Helper()
	env := newPostgres(t, true)
	setup := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x53}, 32))
	service, err := NewIdentity(env.runtime, identity.Options{ScopeID: env.scope, SetupToken: []byte(setup), TokenPepper: bytes.Repeat([]byte{0x42}, 32)})
	if err != nil {
		t.Fatal("source acceptance identity construction failed")
	}
	queue, err := NewJobs(env.runtime, env.box)
	if err != nil {
		t.Fatal("source acceptance jobs construction failed")
	}
	allow, err := netip.ParsePrefix("127.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	sources, err := NewSources(env.store, queue, &safefetch.Client{AllowNets: []netip.Prefix{allow}})
	if err != nil {
		t.Fatal("source store construction failed")
	}
	mac, err := apicontract.NewCursorHMAC(bytes.Repeat([]byte{0x57}, 32))
	if err != nil {
		t.Fatal(err)
	}
	codec, err := apicontract.NewCursorCodec(mac)
	if err != nil {
		t.Fatal(err)
	}
	web := t.TempDir()
	if os.WriteFile(filepath.Join(web, "index.html"), []byte("<html>synthetic</html>"), 0600) != nil {
		t.Fatal("source acceptance static fixture failed")
	}
	handler, err := server.NewHandler(web, server.Dependencies{Database: env.runtime.Ping, Secrets: func() error { return nil },
		Identity: service, PublicURL: nodeAcceptanceOrigin, Development: true,
		Nodes: &server.NodeDependencies{Repository: env.store, Cursor: codec}, Sources: sources, Jobs: queue, JobCursor: codec},
		slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal("source acceptance handler construction failed")
	}
	t.Cleanup(func() { _ = handler.Close() })
	h := &sourceHTTPAcceptance{nodeHTTPAcceptance: &nodeHTTPAcceptance{env: env, h: handler}, queue: queue, sources: sources}
	response := h.do(http.MethodPost, "/api/v1/setup", `{"setup_token":"`+setup+`","username":"source-admin","password":"`+nodeAcceptancePassword+`"}`, "", nil)
	h.setSession(t, response, http.StatusCreated)
	return h
}

func sourceBody(t *testing.T, name, rawURL, mode string, enabled bool) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"name": name, "tags": []string{"feed"}, "enabled": enabled, "source": map[string]any{
		"schema_version": 1, "url": rawURL, "format": "uri_list", "auth": map[string]string{"kind": "none"},
		"refresh_policy": map[string]any{"enabled": true, "interval_seconds": 60, "commit_mode": mode, "missing_policy": "retain"},
		"fetch_limits":   map[string]int{"timeout_ms": 5000, "max_compressed_bytes": 65536, "max_decoded_bytes": 65536, "max_redirects": 2},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requireSource(t *testing.T, response *httptest.ResponseRecorder, expected int) apicontract.SourceResource {
	t.Helper()
	requireNodeStatus(t, response, expected)
	if strings.Contains(response.Body.String(), sourceItemPassword) {
		t.Fatal("source response disclosed a synthetic secret")
	}
	var body apicontract.SourceResponse
	if json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatal("invalid source read DTO")
	}
	etag, _ := apicontract.ETag(int64(body.Data.Metadata.Revision))
	if response.Header().Get("ETag") != etag {
		t.Fatal("source ETag disagreed with revision")
	}
	return body.Data
}

func TestPostgresSourceCRUDRefreshFailClosedAndUniqueJob(t *testing.T) {
	h := newSourceHTTPAcceptance(t)
	hidden := url.URL{Scheme: "https", Host: "feed.example.invalid", Path: "/subscription", RawQuery: "token=1"}
	created := requireSource(t, h.do(http.MethodPost, "/api/v1/sources", sourceBody(t, "Feed", hidden.String(), "manual", true), "", nil), http.StatusCreated)
	if created.Metadata.Kind != ir.KindSource || created.Source.URLDisplay != "https://feed.example.invalid" || created.Source.HasURL != true {
		t.Fatal("source create did not redact the URL to scheme and authority")
	}
	if strings.Contains(responseString(h.do(http.MethodGet, "/api/v1/sources/"+string(created.Metadata.ResourceID), "", "", nil)), "/subscription") {
		t.Fatal("source GET disclosed a URL path")
	}
	listed := h.do(http.MethodGet, "/api/v1/sources?tag=feed&enabled=true", "", "", nil)
	requireNodeStatus(t, listed, http.StatusOK)
	patched := requireSource(t, h.do(http.MethodPatch, "/api/v1/sources/"+string(created.Metadata.ResourceID), `{"name":"Renamed feed"}`, revisionTag(int64(created.Metadata.Revision)), nil), http.StatusOK)
	if patched.Metadata.Name != "Renamed feed" || patched.Metadata.Revision != 2 {
		t.Fatal("source patch did not create a new revision")
	}
	var scheduled int64
	if err := h.env.runtime.QueryRow(h.env.ctx, `SELECT COUNT(*) FROM public.source_schedules WHERE source_id=$1`, dbID(created.Metadata.ResourceID)).Scan(&scheduled); err != nil || scheduled != 1 {
		t.Fatal("enabled source did not persist a recoverable schedule")
	}

	item := url.URL{Scheme: "http", Host: "source-item.example.invalid:8080", Fragment: "SourceHTTP"}
	item.User = url.UserPassword("item-user", sourceItemPassword)
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, item.String()+"\n")
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)

	live := requireSource(t, h.do(http.MethodPost, "/api/v1/sources", sourceBody(t, "Live", upstream.URL+"/path", "safe_updates", true), "", nil), http.StatusCreated)
	first := h.do(http.MethodPost, "/api/v1/sources/"+string(live.Metadata.ResourceID)+"/refresh", "", revisionTag(int64(live.Metadata.Revision)), nil)
	requireNodeStatus(t, first, http.StatusAccepted)
	conflict := h.do(http.MethodPost, "/api/v1/sources/"+string(live.Metadata.ResourceID)+"/refresh", "", revisionTag(int64(live.Metadata.Revision)), nil)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("duplicate refresh HTTP %d", conflict.Code)
	}

	lease, err := h.queue.Claim(h.env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.SourceRefresh}})
	if err != nil || lease == nil {
		t.Fatal("source refresh was not claimable by the API worker")
	}
	result, commit, err := h.sources.HandleRefresh(h.env.ctx, *lease)
	if err != nil || result.State != jobs.Succeeded || commit == nil {
		t.Fatalf("successful refresh failed: %v %#v", err, result)
	}
	if _, err := h.queue.CompleteTx(h.env.ctx, lease.Identity, result, commit); err != nil {
		t.Fatal(err)
	}
	nodes := requireNodeList(t, h.do(http.MethodGet, "/api/v1/nodes", "", "", nil))
	if len(nodes.Data) != 1 || nodes.Data[0].Metadata.Name != "SourceHTTP" {
		t.Fatal("safe_updates refresh did not create the imported node")
	}
	var success int64
	if err := h.env.runtime.QueryRow(h.env.ctx, `SELECT COUNT(*) FROM public.source_snapshots WHERE source_id=$1 AND state='success'`, dbID(live.Metadata.ResourceID)).Scan(&success); err != nil || success != 1 {
		t.Fatal("success snapshot was not stored")
	}

	refreshed, err := h.sources.Head(h.env.ctx, h.env.scope, live.Metadata.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	second := h.do(http.MethodPost, "/api/v1/sources/"+string(live.Metadata.ResourceID)+"/refresh", "", revisionTag(int64(refreshed.Metadata.Revision)), nil)
	requireNodeStatus(t, second, http.StatusAccepted)
	lease, err = h.queue.Claim(h.env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.SourceRefresh}})
	if err != nil || lease == nil {
		t.Fatal("failed refresh was not claimed")
	}
	result, commit, err = h.sources.HandleRefresh(h.env.ctx, *lease)
	if err != nil || result.State != jobs.Failed || commit == nil {
		t.Fatalf("failed refresh was not fail-closed: %v %#v", err, result)
	}
	if _, err := h.queue.CompleteTx(h.env.ctx, lease.Identity, result, commit); err != nil {
		t.Fatal(err)
	}
	if err := h.env.runtime.QueryRow(h.env.ctx, `SELECT COUNT(*) FROM public.source_snapshots WHERE source_id=$1 AND state='success'`, dbID(live.Metadata.ResourceID)).Scan(&success); err != nil || success != 1 {
		t.Fatal("failed refresh replaced the last good snapshot")
	}
	afterFail := requireNodeList(t, h.do(http.MethodGet, "/api/v1/nodes", "", "", nil))
	if len(afterFail.Data) != 1 {
		t.Fatal("failed refresh mutated imported nodes")
	}
	failedDoc, err := h.sources.Head(h.env.ctx, h.env.scope, live.Metadata.ResourceID)
	if err != nil || failedDoc.Source.LastError == nil || failedDoc.Source.LastError.Code != "SERVICE_UNAVAILABLE" {
		t.Fatal("failed refresh did not record last_error")
	}

	requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/sources/"+string(nodes.Data[0].Metadata.ResourceID), "", "", nil), http.StatusNotFound)
	deleted := h.do(http.MethodDelete, "/api/v1/sources/"+string(patched.Metadata.ResourceID), "", revisionTag(int64(patched.Metadata.Revision)), nil)
	requireNodeStatus(t, deleted, http.StatusOK)
	requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/sources/"+string(patched.Metadata.ResourceID), "", "", nil), http.StatusNotFound)
	if err := h.env.runtime.QueryRow(h.env.ctx, `SELECT COUNT(*) FROM public.source_schedules WHERE source_id=$1`, dbID(patched.Metadata.ResourceID)).Scan(&scheduled); err != nil || scheduled != 0 {
		t.Fatal("deleted source left a refresh schedule")
	}
}

func responseString(response *httptest.ResponseRecorder) string { return response.Body.String() }
