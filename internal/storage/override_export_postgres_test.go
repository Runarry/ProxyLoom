package storage

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/importparse"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/jobs"
)

func TestPostgresOverrideStaleConflictRestoreAndExport(t *testing.T) {
	h := newSourceHTTPAcceptance(t)
	password := "SYNTHETIC_T015_NODE_PASSWORD"
	item := url.URL{Scheme: "trojan", Host: "origin.example.invalid:443", Fragment: "Upstream"}
	item.User = url.User(password)
	item.RawQuery = "security=tls&sni=origin.example.invalid"
	renamed := item
	renamed.Fragment = "Renamed-Upstream"
	renamed.Host = "origin.example.invalid:443"
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
		switch hits {
		case 1:
			_, _ = io.WriteString(w, item.String()+"\n")
		case 2:
			_, _ = io.WriteString(w, renamed.String()+"\n")
		case 3:
			_, _ = io.WriteString(w, "trojan://"+url.PathEscape(password+"-rotated")+"@origin.example.invalid:443?security=tls&sni=origin.example.invalid#Fuzzy\n")
		default:
			_, _ = io.WriteString(w, "")
		}
	}))
	t.Cleanup(upstream.Close)

	live := requireSource(t, h.do(http.MethodPost, "/api/v1/sources", sourceBody(t, "Origin", upstream.URL+"/feed", "safe_updates", true), "", nil), http.StatusCreated)
	refreshSource(t, h, live.Metadata.ResourceID, int64(live.Metadata.Revision))
	nodes := requireNodeList(t, h.do(http.MethodGet, "/api/v1/nodes", "", "", nil))
	if len(nodes.Data) != 1 || nodes.Data[0].Metadata.Name != "Upstream" || nodes.Data[0].Binding == nil {
		t.Fatalf("imported node missing binding: %#v", nodes.Data)
	}
	nodeID := nodes.Data[0].Metadata.ResourceID
	bindingRev := int64(nodes.Data[0].Binding.BindingRevision)

	patched := requireNode(t, h.do(http.MethodPatch, "/api/v1/nodes/"+string(nodeID),
		`{"name":"Custom","binding_revision":"`+revisionDecimal(bindingRev)+`"}`,
		revisionTag(int64(nodes.Data[0].Metadata.Revision)), nil), http.StatusOK)
	if patched.Metadata.Name != "Custom" || patched.Binding == nil || len(patched.Binding.OverriddenFields) == 0 {
		t.Fatalf("name overlay was not recorded: %#v", patched.Binding)
	}

	current, err := h.sources.Head(h.env.ctx, h.env.scope, live.Metadata.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	refreshSource(t, h, live.Metadata.ResourceID, int64(current.Metadata.Revision))
	kept := requireNode(t, h.do(http.MethodGet, "/api/v1/nodes/"+string(nodeID), "", "", nil), http.StatusOK)
	if kept.Metadata.Name != "Custom" {
		t.Fatal("refresh overwrote the overlay name")
	}
	if kept.Metadata.SecurityEpoch == patched.Metadata.SecurityEpoch && kept.Node.Endpoint.Host != "origin.example.invalid" {
		t.Fatal("refresh did not keep the bound node")
	}

	current, err = h.sources.Head(h.env.ctx, h.env.scope, live.Metadata.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	refreshSource(t, h, live.Metadata.ResourceID, int64(current.Metadata.Revision))
	detail := requireSource(t, h.do(http.MethodGet, "/api/v1/sources/"+string(live.Metadata.ResourceID), "", "", nil), http.StatusOK)
	if len(detail.Items) == 0 {
		t.Fatal("source GET omitted items")
	}
	conflicted := false
	for _, item := range detail.Items {
		if item.State == "conflict" {
			conflicted = true
		}
	}
	if !conflicted {
		t.Fatal("fuzzy identity match did not become a visible conflict")
	}

	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/exports", exportBody(t, nodeID, kept.Metadata.Revision), "", nil), http.StatusForbidden)
	h.setSession(t, h.do(http.MethodPost, "/api/v1/auth/reauth", `{"password":"`+nodeAcceptancePassword+`"}`, "", nil), http.StatusOK)
	exported := h.do(http.MethodPost, "/api/v1/exports", exportBody(t, nodeID, kept.Metadata.Revision), "", nil)
	if exported.Code != http.StatusOK {
		t.Fatalf("export HTTP %d %s", exported.Code, exported.Body.String())
	}
	var body apicontract.ExportResponse
	if json.Unmarshal(exported.Body.Bytes(), &body) != nil || len(body.Data.Artifacts) != 1 || !strings.Contains(body.Data.Artifacts[0].Content, "trojan://") {
		t.Fatal("export did not return a URI list")
	}
	parsed := importparse.ParseURI(strings.TrimSpace(body.Data.Artifacts[0].Content))
	if !parsed.Valid() || parsed.Node.Protocol != ir.Trojan {
		t.Fatal("exported URI did not round-trip")
	}
}

func refreshSource(t *testing.T, h *sourceHTTPAcceptance, id ir.ID, revision int64) {
	t.Helper()
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/sources/"+string(id)+"/refresh", "", revisionTag(revision), nil), http.StatusAccepted)
	lease, err := h.queue.Claim(h.env.ctx, jobs.ClaimInput{Executor: jobs.APIWorker, WorkerID: jobs.NewID(), Types: []jobs.Type{jobs.SourceRefresh}})
	if err != nil || lease == nil {
		t.Fatal("refresh was not claimable")
	}
	result, commit, err := h.sources.HandleRefresh(h.env.ctx, *lease)
	if err != nil || commit == nil {
		t.Fatalf("refresh handler failed: %v %#v", err, result)
	}
	if _, err := h.queue.CompleteTx(h.env.ctx, lease.Identity, result, commit); err != nil {
		t.Fatal(err)
	}
}

func exportBody(t *testing.T, id ir.ID, revision apicontract.Revision) string {
	t.Helper()
	data, err := json.Marshal(apicontract.ExportRequest{
		Type: "resources", Format: "uri_list", IncludeSecrets: true,
		Resources: []apicontract.ExportResourceRef{{ResourceID: id, Kind: ir.KindNode, Revision: revision}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func revisionDecimal(value int64) string { return strconv.FormatInt(value, 10) }
