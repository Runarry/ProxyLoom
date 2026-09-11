package storage

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/catalog"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func routingBody(t *testing.T, profile ir.RoutingProfile) string {
	t.Helper()
	data, err := json.Marshal(apicontract.RoutingProfileCreateRequest{Name: "routing", Tags: []string{"routing-test"}, RoutingProfile: profile})
	if err != nil {
		t.Fatal("synthetic routing request failed")
	}
	return string(data)
}
func routingPath(id ir.ID) string { return "/api/v1/routing-profiles/" + string(id) }
func ruleSetPath(id ir.ID) string { return "/api/v1/rule-sets/" + string(id) }
func routingPayload(target ir.TargetRef, setID ir.ID) ir.RoutingProfile {
	return ir.RoutingProfile{SchemaVersion: 1, Rules: []ir.RoutingRule{{Match: ir.RouteMatch{RuleSetIDs: []ir.ID{setID}, Network: []ir.Network{ir.NetworkTCP}}, Action: target, Enabled: true, Comment: "ordered first"}}, Final: ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Reject}, DomainResolutionMode: ir.PreserveDomain}
}
func requireRouting(t *testing.T, response *httptest.ResponseRecorder, status int) apicontract.RoutingProfileResource {
	t.Helper()
	requireNodeStatus(t, response, status)
	var body apicontract.RoutingProfileResponse
	if json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data.Metadata.Kind != ir.KindRoutingProfile {
		t.Fatal("invalid routing response")
	}
	etag, _ := apicontract.ETag(int64(body.Data.Metadata.Revision))
	if response.Header().Get("ETag") != etag {
		t.Fatal("routing ETag disagrees with revision")
	}
	return body.Data
}
func requireRuleSet(t *testing.T, response *httptest.ResponseRecorder, status int) apicontract.RuleSetResource {
	t.Helper()
	requireNodeStatus(t, response, status)
	var body apicontract.RuleSetResponse
	if json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data.Metadata.Kind != ir.KindRuleSet || body.Data.RuleSet.Validate() != nil {
		t.Fatal("invalid rule-set response/hash")
	}
	etag, _ := apicontract.ETag(int64(body.Data.Metadata.Revision))
	if response.Header().Get("ETag") != etag {
		t.Fatal("rule-set ETag disagrees with revision")
	}
	return body.Data
}

const ruleSetCreateBody = `{"name":"domains and CIDRs","tags":["routing-test"],"rule_set":{"schema_version":1,"format":"domain_cidr_text","entries":[{"kind":"domain","domain":"example.invalid","match":"suffix"},{"kind":"cidr","cidr":"192.0.2.0/24"}]}}`

func TestPostgresRoutingRuleSetCRUDHistoryPaginationAndAudit(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	a := h.create(t, "routing-node", syntheticNode(), []string{}, true)
	sets := []apicontract.RuleSetResource{}
	for range 3 {
		sets = append(sets, requireRuleSet(t, h.do(http.MethodPost, "/api/v1/rule-sets", ruleSetCreateBody, "", nil), http.StatusCreated))
	}
	set := sets[0]
	profile := routingPayload(ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: a.Metadata.ResourceID}, set.Metadata.ResourceID)
	profiles := []apicontract.RoutingProfileResource{}
	for range 3 {
		profiles = append(profiles, requireRouting(t, h.do(http.MethodPost, "/api/v1/routing-profiles", routingBody(t, profile), "", nil), http.StatusCreated))
	}
	route := profiles[0]
	for _, collection := range []string{"routing-profiles", "rule-sets"} {
		response := h.do(http.MethodGet, "/api/v1/"+collection+"?limit=2&enabled=true&tag=routing-test", "", "", nil)
		requireNodeStatus(t, response, http.StatusOK)
		var first struct {
			Data []json.RawMessage    `json:"data"`
			Page apicontract.PageInfo `json:"page"`
		}
		if json.Unmarshal(response.Body.Bytes(), &first) != nil || len(first.Data) != 2 || first.Page.NextCursor == "" {
			t.Fatal("routing list page missing cursor")
		}
		second := h.do(http.MethodGet, "/api/v1/"+collection+"?limit=2&enabled=true&tag=routing-test&cursor="+url.QueryEscape(first.Page.NextCursor), "", "", nil)
		requireNodeStatus(t, second, http.StatusOK)
		var next struct {
			Data []json.RawMessage    `json:"data"`
			Page apicontract.PageInfo `json:"page"`
		}
		if json.Unmarshal(second.Body.Bytes(), &next) != nil || len(next.Data) != 1 || next.Page.NextCursor != "" || string(next.Data[0]) == string(first.Data[0]) {
			t.Fatal("routing pagination dropped/repeated a resource")
		}
		requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/"+collection+"?enabled=false&cursor="+url.QueryEscape(first.Page.NextCursor), "", "", nil), http.StatusBadRequest)
	}
	requireNodeStatus(t, h.do(http.MethodGet, routingPath(set.Metadata.ResourceID), "", "", nil), http.StatusNotFound)
	requireNodeStatus(t, h.do(http.MethodGet, ruleSetPath(route.Metadata.ResourceID), "", "", nil), http.StatusNotFound)
	requireNodeStatus(t, h.do(http.MethodPatch, routingPath(route.Metadata.ResourceID), `{"name":"next"}`, "", nil), http.StatusPreconditionRequired)
	updated := requireRouting(t, h.do(http.MethodPatch, routingPath(route.Metadata.ResourceID), `{"routing_profile":{"rules":[]}}`, revisionTag(1), nil), http.StatusOK)
	if updated.Metadata.Revision != 2 || len(updated.RoutingProfile.Rules) != 0 || updated.RoutingProfile.Final.Builtin != ir.Reject {
		t.Fatal("routing replacement lost explicit final or revision")
	}
	requireNodeStatus(t, h.do(http.MethodPatch, routingPath(route.Metadata.ResourceID), `{"name":"stale"}`, revisionTag(1), nil), http.StatusPreconditionFailed)
	old, err := h.env.store.Revision(h.env.ctx, h.env.scope, route.Metadata.ResourceID, 1)
	if err != nil || len(old.Payload.(*ir.RoutingProfile).Rules) != 1 {
		t.Fatal("routing update rewrote immutable history")
	}
	refs, err := h.env.store.References(h.env.ctx, h.env.scope, set.Metadata.ResourceID, catalog.ReferenceOptions{IncludeHistorical: true, Limit: 100})
	if err != nil {
		t.Fatal("routing references unreadable")
	}
	found := false
	for _, ref := range refs.Items {
		if ref.SourceID == route.Metadata.ResourceID {
			found = true
			if ref.Current || ref.Path != "/payload/rules/0/match/rule_set_ids/0" || ref.SourceKind != ir.KindRoutingProfile {
				t.Fatal("rule-set historical reference lost kind or path")
			}
		}
	}
	if !found {
		t.Fatal("rule-set reference missing")
	}
	changedSet := requireRuleSet(t, h.do(http.MethodPatch, ruleSetPath(set.Metadata.ResourceID), `{"rule_set":{"entries":[{"kind":"cidr","cidr":"2001:db8::/32"}]}}`, revisionTag(1), nil), http.StatusOK)
	if changedSet.Metadata.Revision != 2 || changedSet.RuleSet.ContentHash == set.RuleSet.ContentHash {
		t.Fatal("rule-set update failed to replace hash")
	}
	oldSet, err := h.env.store.Revision(h.env.ctx, h.env.scope, set.Metadata.ResourceID, 1)
	if err != nil || oldSet.Payload.(*ir.RuleSet).ContentHash != set.RuleSet.ContentHash {
		t.Fatal("rule-set hash history changed")
	}
	requireNodeStatus(t, h.do(http.MethodPatch, ruleSetPath(set.Metadata.ResourceID), `{"name":"stale"}`, revisionTag(1), nil), http.StatusPreconditionFailed)
	requireNodeStatus(t, h.do(http.MethodDelete, ruleSetPath(set.Metadata.ResourceID), "", revisionTag(2), nil), http.StatusOK)
	requireNodeStatus(t, h.do(http.MethodGet, ruleSetPath(set.Metadata.ResourceID), "", "", nil), http.StatusNotFound)
	requireNodeStatus(t, h.do(http.MethodPatch, routingPath(profiles[1].Metadata.ResourceID), `{"name":"invalid-ref"}`, revisionTag(1), nil), http.StatusUnprocessableEntity)
	requireNodeStatus(t, h.do(http.MethodDelete, routingPath(profiles[1].Metadata.ResourceID), "", revisionTag(1), nil), http.StatusOK)
	deleted, err := h.env.store.Revision(h.env.ctx, h.env.scope, profiles[1].Metadata.ResourceID, 2)
	if err != nil || deleted.Metadata.Enabled || deleted.Metadata.SecurityEpoch != 2 {
		t.Fatal("routing deletion did not revoke revision")
	}
	var audits int
	if h.env.runtime.QueryRow(h.env.ctx, "SELECT count(*) FROM public.resource_audit_events WHERE action LIKE 'routing_profile.%' OR action LIKE 'rule_set.%'").Scan(&audits) != nil || audits != 10 {
		t.Fatalf("routing audit count differs: %d", audits)
	}
	if requireNode(t, h.do(http.MethodGet, nodePath(a.Metadata.ResourceID), "", "", nil), http.StatusOK).Metadata.Revision != 1 {
		t.Fatal("routing CRUD changed node")
	}
}

func TestPostgresRoutingRejectsInvalidTargetsEntriesAndUnauditedWrites(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	a := h.create(t, "entry", syntheticNode(), []string{}, true)
	b := h.create(t, "exit", syntheticNode(), []string{}, true)
	disabled := h.create(t, "disabled", syntheticNode(), []string{}, false)
	chain := requireChain(t, h.do(http.MethodPost, "/api/v1/chains", chainBody(t, "chain", a.Metadata.ResourceID, b.Metadata.ResourceID, nil), "", nil), http.StatusCreated)
	policy := requirePolicy(t, h.do(http.MethodPost, "/api/v1/policy-groups", policyBody(t, policyGroupPayload(a.Metadata.ResourceID, chain.Metadata.ResourceID, ir.PolicyFixed)), "", nil), http.StatusCreated)
	set := requireRuleSet(t, h.do(http.MethodPost, "/api/v1/rule-sets", ruleSetCreateBody, "", nil), http.StatusCreated)
	profile := routingPayload(ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindPolicyGroup, ResourceID: policy.Metadata.ResourceID}, set.Metadata.ResourceID)
	body := routingBody(t, profile)
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/routing-profiles", body, "", func(r *http.Request) { r.Header.Del("Cookie") }), http.StatusUnauthorized)
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/rule-sets", ruleSetCreateBody, "", func(r *http.Request) { r.Header.Del("X-CSRF-Token") }), http.StatusForbidden)
	for _, target := range []ir.ID{disabled.Metadata.ResourceID, chain.Metadata.ResourceID, "99999999-9999-4999-8999-999999999999"} {
		bad := profile.Clone()
		bad.Final = ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: target}
		response := h.do(http.MethodPost, "/api/v1/routing-profiles", routingBody(t, bad), "", nil)
		requireNodeStatus(t, response, http.StatusUnprocessableEntity)
		if !strings.Contains(response.Body.String(), "/routing_profile/final/resource_id") {
			t.Fatal("final target error lacks pointer")
		}
	}
	bad := profile.Clone()
	bad.Rules[0].Match.RuleSetIDs = []ir.ID{a.Metadata.ResourceID}
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/routing-profiles", routingBody(t, bad), "", nil), http.StatusUnprocessableEntity)
	for _, test := range []struct{ body, path string }{
		{strings.Replace(ruleSetCreateBody, "192.0.2.0/24", "192.0.2.1/24", 1), "/rule_set/entries/1"},
		{strings.Replace(ruleSetCreateBody, "192.0.2.0/24", "192.0.2.0/99", 1), "/rule_set/entries/1"},
	} {
		response := h.do(http.MethodPost, "/api/v1/rule-sets", test.body, "", nil)
		if response.Code != http.StatusBadRequest && response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid CIDR HTTP %d", response.Code)
		}
		if !strings.Contains(response.Body.String(), test.path) || strings.Contains(response.Body.String(), "192.0.2.") {
			t.Fatal("invalid entry path leaked input or lost index")
		}
	}
	created := requireRouting(t, h.do(http.MethodPost, "/api/v1/routing-profiles", body, "", nil), http.StatusCreated)
	requireNode(t, h.do(http.MethodPatch, nodePath(b.Metadata.ResourceID), `{"enabled":false}`, revisionTag(1), nil), http.StatusOK)
	requireNodeStatus(t, h.do(http.MethodPatch, routingPath(created.Metadata.ResourceID), `{"name":"broken-closure"}`, revisionTag(1), nil), http.StatusUnprocessableEntity)
	if _, err := h.env.admin.Exec(h.env.ctx, "REVOKE INSERT (id,scope_id,actor_id,object_id,revision,action,request_id) ON public.resource_audit_events FROM proxyloom"); err != nil {
		t.Fatal("could not prepare audit rejection")
	}
	requireNodeStatus(t, h.do(http.MethodPatch, ruleSetPath(set.Metadata.ResourceID), `{"name":"unaudited"}`, revisionTag(1), nil), http.StatusServiceUnavailable)
	head, err := h.env.store.Head(h.env.ctx, h.env.scope, set.Metadata.ResourceID)
	if err != nil || head.Metadata.Revision != 1 {
		t.Fatal("audit failure committed a rule-set mutation")
	}
	if _, err := h.env.store.Revision(h.env.ctx, h.env.scope, set.Metadata.ResourceID, 2); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("audit rollback left revision")
	}
}
