package storage

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func policyGroupPayload(nodeID, chainID ir.ID, strategy ir.PolicyStrategy) ir.PolicyGroup {
	members := []ir.TargetRef{{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: nodeID},
		{Type: ir.ResourceRef, Kind: ir.KindChain, ResourceID: chainID}}
	return ir.PolicyGroup{SchemaVersion: 1, Strategy: strategy, Members: members, DefaultMember: members[1],
		HealthCheck: ir.PolicyHealthCheck{Enabled: false}, OnUnavailable: ir.FailClosed}
}

func policyBody(t *testing.T, group ir.PolicyGroup) string {
	t.Helper()
	data, err := json.Marshal(apicontract.PolicyGroupCreateRequest{Name: "policy-" + string(group.Strategy), Tags: []string{"policy-test"}, PolicyGroup: group})
	if err != nil {
		t.Fatal("could not encode synthetic policy request")
	}
	return string(data)
}

func policyPath(id ir.ID) string { return "/api/v1/policy-groups/" + string(id) }

func requirePolicy(t *testing.T, response *httptest.ResponseRecorder, status int) apicontract.PolicyGroupResource {
	t.Helper()
	requireNodeStatus(t, response, status)
	var body apicontract.PolicyGroupResponse
	if json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data.Metadata.Kind != ir.KindPolicyGroup || body.Data.Diagnostics == nil {
		t.Fatal("policy read did not match its typed contract")
	}
	etag, _ := apicontract.ETag(int64(body.Data.Metadata.Revision))
	if response.Header().Get("ETag") != etag {
		t.Fatal("policy ETag disagreed with its revision")
	}
	return body.Data
}

func requirePolicyList(t *testing.T, response *httptest.ResponseRecorder) apicontract.PolicyGroupListResponse {
	t.Helper()
	requireNodeStatus(t, response, http.StatusOK)
	var page apicontract.PolicyGroupListResponse
	if json.Unmarshal(response.Body.Bytes(), &page) != nil {
		t.Fatal("invalid policy list DTO")
	}
	return page
}

func TestPostgresPolicyGroupCRUDPaginationReferencesAndAudit(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	a := h.create(t, "policy-hop-a", syntheticNode(), []string{}, true)
	b := h.create(t, "policy-hop-b", syntheticNode(), []string{}, true)
	chain := requireChain(t, h.do(http.MethodPost, "/api/v1/chains", chainBody(t, "policy-chain", a.Metadata.ResourceID, b.Metadata.ResourceID, nil), "", nil), http.StatusCreated)
	created := []apicontract.PolicyGroupResource{}
	for _, strategy := range []ir.PolicyStrategy{ir.PolicyFixed, ir.PolicyManualSelect, ir.PolicyLatencyBest, ir.PolicyRoundRobin} {
		group := policyGroupPayload(a.Metadata.ResourceID, chain.Metadata.ResourceID, strategy)
		if strategy == ir.PolicyLatencyBest {
			interval, timeout, tolerance := 10000, 5000, 0
			group.HealthCheck = ir.PolicyHealthCheck{Enabled: true, URL: "https://health.example.invalid/check", IntervalMS: &interval, TimeoutMS: &timeout, ToleranceMS: &tolerance}
		}
		read := requirePolicy(t, h.do(http.MethodPost, "/api/v1/policy-groups", policyBody(t, group), "", nil), http.StatusCreated)
		if read.PolicyGroup.Strategy != strategy || read.PolicyGroup.OnUnavailable != ir.FailClosed || read.PolicyGroup.DefaultMember != group.DefaultMember {
			t.Fatal("policy strategy or default was changed while persisting")
		}
		if current := requirePolicy(t, h.do(http.MethodGet, policyPath(read.Metadata.ResourceID), "", "", nil), http.StatusOK); current.PolicyGroup.Strategy != strategy {
			t.Fatal("policy persisted a different strategy")
		}
		created = append(created, read)
	}
	first := requirePolicyList(t, h.do(http.MethodGet, "/api/v1/policy-groups?limit=2&tag=policy-test&enabled=true", "", "", nil))
	if len(first.Data) != 2 || first.Page.NextCursor == "" {
		t.Fatal("policy first page did not expose a bounded cursor")
	}
	second := requirePolicyList(t, h.do(http.MethodGet, "/api/v1/policy-groups?limit=2&tag=policy-test&enabled=true&cursor="+url.QueryEscape(first.Page.NextCursor), "", "", nil))
	if len(second.Data) != 2 || second.Page.NextCursor != "" || second.Data[0].Metadata.ResourceID == first.Data[0].Metadata.ResourceID {
		t.Fatal("policy pagination repeated or dropped groups")
	}
	requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/policy-groups?limit=2&enabled=false&cursor="+url.QueryEscape(first.Page.NextCursor), "", "", nil), http.StatusBadRequest)
	requireNodeStatus(t, h.do(http.MethodGet, policyPath(a.Metadata.ResourceID), "", "", nil), http.StatusNotFound)
	id := created[1].Metadata.ResourceID
	requireNodeStatus(t, h.do(http.MethodPatch, policyPath(id), `{"name":"new"}`, "", nil), http.StatusPreconditionRequired)
	updated := requirePolicy(t, h.do(http.MethodPatch, policyPath(id), `{"name":"renamed","policy_group":{"strategy":"round_robin"}}`, revisionTag(1), nil), http.StatusOK)
	if updated.Metadata.Revision != 2 || updated.Metadata.SecurityEpoch != 1 || updated.PolicyGroup.Strategy != ir.PolicyRoundRobin {
		t.Fatal("policy patch did not append an immutable revision")
	}
	requireNodeStatus(t, h.do(http.MethodPatch, policyPath(id), `{"name":"stale"}`, revisionTag(1), nil), http.StatusPreconditionFailed)
	historical, err := h.env.store.Revision(h.env.ctx, h.env.scope, id, 1)
	if err != nil || historical.Payload.(*ir.PolicyGroup).Strategy != ir.PolicyManualSelect {
		t.Fatal("policy edit rewrote the first revision")
	}
	refs, err := h.env.store.References(h.env.ctx, h.env.scope, chain.Metadata.ResourceID, catalog.ReferenceOptions{IncludeHistorical: true, Limit: 100})
	if err != nil {
		t.Fatal("policy chain reverse reference read failed")
	}
	var oldRefs, currentRefs int
	for _, ref := range refs.Items {
		if ref.SourceID == id {
			if ref.SourceKind != ir.KindPolicyGroup || ref.ExpectedKind != ir.KindChain {
				t.Fatal("policy reference lost its source or target kind")
			}
			if ref.Current {
				currentRefs++
			} else {
				oldRefs++
			}
		}
	}
	if oldRefs != 2 || currentRefs != 2 {
		t.Fatal("policy member/default reverse references lost immutable history")
	}
	nodeRefs := h.do(http.MethodGet, nodePath(a.Metadata.ResourceID)+"/references", "", "", nil)
	requireNodeStatus(t, nodeRefs, http.StatusOK)
	var nodeReferences apicontract.ReferenceListResponse
	if json.Unmarshal(nodeRefs.Body.Bytes(), &nodeReferences) != nil {
		t.Fatal("policy node reverse references could not be decoded")
	}
	found := false
	for _, ref := range nodeReferences.Data {
		found = found || ref.SourceResourceID == id && ref.SourceKind == ir.KindPolicyGroup
	}
	if !found {
		t.Fatal("node reverse references omitted its policy membership")
	}
	var audits int
	if h.env.runtime.QueryRow(h.env.ctx, "SELECT count(*) FROM public.resource_audit_events WHERE action IN ('policy_group.create','policy_group.update')").Scan(&audits) != nil || audits != 5 {
		t.Fatal("accepted policy mutations did not have exactly one audit each")
	}
	if requireNode(t, h.do(http.MethodGet, nodePath(a.Metadata.ResourceID), "", "", nil), http.StatusOK).Metadata.Revision != 1 {
		t.Fatal("policy mutation changed a referenced node")
	}
	requireNode(t, h.do(http.MethodPatch, nodePath(b.Metadata.ResourceID), `{"enabled":false}`, revisionTag(1), nil), http.StatusOK)
	requireNodeStatus(t, h.do(http.MethodPatch, policyPath(id), `{"name":"bad-closure"}`, revisionTag(2), nil), http.StatusUnprocessableEntity)
	requireNodeStatus(t, h.do(http.MethodDelete, policyPath(id), "", revisionTag(2), nil), http.StatusOK)
	requireNodeStatus(t, h.do(http.MethodGet, policyPath(id), "", "", nil), http.StatusNotFound)
	deleted, err := h.env.store.Revision(h.env.ctx, h.env.scope, id, 3)
	if err != nil || deleted.Metadata.Enabled || deleted.Metadata.SecurityEpoch != 2 {
		t.Fatal("policy delete failed to revoke its current snapshot")
	}
	if h.env.runtime.QueryRow(h.env.ctx, "SELECT count(*) FROM public.resource_audit_events WHERE object_id=$1 AND action='policy_group.delete'", dbID(id)).Scan(&audits) != nil || audits != 1 {
		t.Fatal("policy delete audit was missing")
	}
}

func TestPostgresPolicyGroupRejectsBadReferencesAndUnauditedWrites(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	a := h.create(t, "policy-a", syntheticNode(), []string{}, true)
	b := h.create(t, "policy-b", syntheticNode(), []string{}, true)
	disabled := h.create(t, "policy-disabled", syntheticNode(), []string{}, false)
	chain := requireChain(t, h.do(http.MethodPost, "/api/v1/chains", chainBody(t, "chain", a.Metadata.ResourceID, b.Metadata.ResourceID, nil), "", nil), http.StatusCreated)
	group := policyGroupPayload(a.Metadata.ResourceID, chain.Metadata.ResourceID, ir.PolicyManualSelect)
	body := policyBody(t, group)
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/policy-groups", body, "", func(r *http.Request) { r.Header.Del("Cookie") }), http.StatusUnauthorized)
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/policy-groups", body, "", func(r *http.Request) { r.Header.Del("X-CSRF-Token") }), http.StatusForbidden)
	for _, target := range []ir.ID{disabled.Metadata.ResourceID, "99999999-9999-4999-8999-999999999999", chain.Metadata.ResourceID} {
		bad := group.Clone()
		bad.Members[0].ResourceID = target
		requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/policy-groups", policyBody(t, bad), "", nil), http.StatusUnprocessableEntity)
	}
	bad := group.Clone()
	bad.DefaultMember = ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: b.Metadata.ResourceID}
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/policy-groups", policyBody(t, bad), "", nil), http.StatusUnprocessableEntity)
	for _, mutate := range []func(*ir.PolicyGroup){
		func(g *ir.PolicyGroup) { g.Members = []ir.TargetRef{} },
		func(g *ir.PolicyGroup) { g.Members = append(g.Members, g.Members[0]) },
		func(g *ir.PolicyGroup) { g.Members[0].Kind = ir.KindPolicyGroup },
		func(g *ir.PolicyGroup) { g.OnUnavailable = "direct" },
		func(g *ir.PolicyGroup) { g.HealthCheck.Enabled = true },
	} {
		bad := group.Clone()
		mutate(&bad)
		requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/policy-groups", policyBody(t, bad), "", nil), http.StatusBadRequest)
	}
	created := requirePolicy(t, h.do(http.MethodPost, "/api/v1/policy-groups", body, "", nil), http.StatusCreated)
	if _, err := h.env.admin.Exec(h.env.ctx, "REVOKE INSERT (id,scope_id,actor_id,object_id,revision,action,request_id) ON public.resource_audit_events FROM proxyloom"); err != nil {
		t.Fatal("could not prepare policy audit failure")
	}
	requireNodeStatus(t, h.do(http.MethodPatch, policyPath(created.Metadata.ResourceID), `{"name":"unaudited"}`, revisionTag(1), nil), http.StatusServiceUnavailable)
	head, err := h.env.store.Head(h.env.ctx, h.env.scope, created.Metadata.ResourceID)
	if err != nil || head.Metadata.Revision != 1 {
		t.Fatal("policy audit failure committed a resource change")
	}
	if _, err := h.env.store.Revision(h.env.ctx, h.env.scope, created.Metadata.ResourceID, 2); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("policy audit rollback left an immutable revision behind")
	}
}
