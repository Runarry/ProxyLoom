package storage

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestPostgresChainCRUDSwapReuseAndHopRejection(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	first := h.create(t, "Hop-A", syntheticNode(), []string{"edge"}, true)
	secondNode := syntheticNode()
	secondNode.Endpoint.Host = "exit.example.invalid"
	second := h.create(t, "Hop-B", secondNode, []string{"exit"}, true)
	disabledNode := syntheticNode()
	disabledNode.Endpoint.Host = "disabled.example.invalid"
	disabled := h.create(t, "Disabled", disabledNode, []string{}, false)
	a, b := first.Metadata.ResourceID, second.Metadata.ResourceID

	forward := requireChain(t, h.do(http.MethodPost, "/api/v1/chains", chainBody(t, "A-to-B", a, b, []string{"pair"}), "", nil), http.StatusCreated)
	if forward.Metadata.Kind != ir.KindChain || len(forward.Chain.Hops) != 2 || forward.Chain.Hops[0].NodeID != a || forward.Chain.Hops[1].NodeID != b {
		t.Fatal("forward chain hops were not stored as requested")
	}
	reverse := requireChain(t, h.do(http.MethodPost, "/api/v1/chains", chainBody(t, "B-to-A", b, a, []string{"pair"}), "", nil), http.StatusCreated)
	if reverse.Metadata.ResourceID == forward.Metadata.ResourceID || reverse.Chain.Hops[0].NodeID != b {
		t.Fatal("reversed hops reused the forward chain identity")
	}
	listed := requireChainList(t, h.do(http.MethodGet, "/api/v1/chains?tag=pair&enabled=true", "", "", nil))
	if len(listed.Data) != 2 {
		t.Fatal("chain list did not return both directions")
	}

	self := h.do(http.MethodPost, "/api/v1/chains", chainBody(t, "loop", a, a, nil), "", nil)
	requireNodeStatus(t, self, http.StatusBadRequest)
	missing := h.do(http.MethodPost, "/api/v1/chains", chainBody(t, "missing", a, "33333333-3333-4333-8333-333333333333", nil), "", nil)
	if missing.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing hop HTTP %d", missing.Code)
	}
	off := h.do(http.MethodPost, "/api/v1/chains", chainBody(t, "disabled-hop", a, disabled.Metadata.ResourceID, nil), "", nil)
	if off.Code != http.StatusUnprocessableEntity {
		t.Fatalf("disabled hop HTTP %d", off.Code)
	}

	swapped := requireChain(t, h.do(http.MethodPatch, "/api/v1/chains/"+string(forward.Metadata.ResourceID),
		`{"hops":[{"node_id":"`+string(b)+`"},{"node_id":"`+string(a)+`"}]}`, revisionTag(int64(forward.Metadata.Revision)), nil), http.StatusOK)
	if swapped.Chain.Hops[0].NodeID != b || swapped.Metadata.Revision != 2 {
		t.Fatal("hop swap did not create a new chain revision")
	}
	unchangedA := requireNode(t, h.do(http.MethodGet, nodePath(a), "", "", nil), http.StatusOK)
	unchangedB := requireNode(t, h.do(http.MethodGet, nodePath(b), "", "", nil), http.StatusOK)
	if unchangedA.Metadata.Revision != 1 || unchangedB.Metadata.Revision != 1 || unchangedA.Metadata.SecurityEpoch != 1 {
		t.Fatal("chain edit mutated hop node revisions")
	}
	refs := h.do(http.MethodGet, nodePath(a)+"/references", "", "", nil)
	requireNodeStatus(t, refs, http.StatusOK)
	var body apicontract.ReferenceListResponse
	if json.Unmarshal(refs.Body.Bytes(), &body) != nil || len(body.Data) == 0 {
		t.Fatal("node references omitted the chain")
	}
	found := false
	for _, item := range body.Data {
		if item.SourceResourceID == swapped.Metadata.ResourceID && item.SourceKind == ir.KindChain {
			found = true
		}
	}
	if !found {
		t.Fatal("chain was not visible from the hop node")
	}

	requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/chains/"+string(a), "", "", nil), http.StatusNotFound)
	deleted := h.do(http.MethodDelete, "/api/v1/chains/"+string(swapped.Metadata.ResourceID), "", revisionTag(int64(swapped.Metadata.Revision)), nil)
	requireNodeStatus(t, deleted, http.StatusOK)
	requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/chains/"+string(swapped.Metadata.ResourceID), "", "", nil), http.StatusNotFound)
	if requireNode(t, h.do(http.MethodGet, nodePath(a), "", "", nil), http.StatusOK).Metadata.Revision != 1 {
		t.Fatal("chain delete cascaded onto hop nodes")
	}
}

func chainBody(t *testing.T, name string, first, second ir.ID, tags []string) string {
	t.Helper()
	if tags == nil {
		tags = []string{}
	}
	data, err := json.Marshal(map[string]any{"name": name, "tags": tags, "enabled": true, "failure_policy": "fail_closed",
		"hops": []map[string]string{{"node_id": string(first)}, {"node_id": string(second)}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func requireChain(t *testing.T, response *httptest.ResponseRecorder, expected int) apicontract.ChainResource {
	t.Helper()
	requireNodeStatus(t, response, expected)
	var body apicontract.ChainReadResponse
	if json.Unmarshal(response.Body.Bytes(), &body) != nil {
		t.Fatal("invalid chain read DTO")
	}
	etag, _ := apicontract.ETag(int64(body.Data.Metadata.Revision))
	if response.Header().Get("ETag") != etag {
		t.Fatal("chain ETag disagreed with revision")
	}
	return body.Data
}

func requireChainList(t *testing.T, response *httptest.ResponseRecorder) apicontract.ChainListResponse {
	t.Helper()
	requireNodeStatus(t, response, http.StatusOK)
	var page apicontract.ChainListResponse
	if json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatal("invalid chain list DTO")
	}
	return page
}
