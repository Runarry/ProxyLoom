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

func dnsFixture(target ir.TargetRef, ruleSet ir.ID) ir.DNSProfile {
	profile := ir.DNSProfile{SchemaVersion: 1,
		Bootstrap: []ir.BootstrapResolver{{ResolverID: "bootstrap", Kind: ir.DNSUDP, Address: "192.0.2.53", Port: 53}},
		Resolvers: []ir.DNSResolver{
			{ResolverID: "local", Kind: ir.DNSLocal},
			{ResolverID: "udp", Kind: ir.DNSUDP, Address: "198.51.100.53", Port: 53, Outbound: &target},
			{ResolverID: "https", Kind: ir.DNSHTTPS, URL: "https://dns.example.invalid/dns-query", BootstrapResolverID: "bootstrap", Outbound: &target},
		},
		Rules: []ir.DNSRule{{Match: ir.DomainMatch{DomainSuffix: []string{"example.invalid"}}, ResolverID: "https", Enabled: true, Comment: "ordered-first"},
			{Match: ir.DomainMatch{DomainExact: []string{"exact.example.invalid"}}, ResolverID: "local", Enabled: false, Comment: "ordered-second"}},
		FinalResolver: "udp"}
	if ruleSet != "" {
		profile.Rules[1].Match.RuleSetIDs = []ir.ID{ruleSet}
	}
	return profile
}

func dnsBody(t *testing.T, profile ir.DNSProfile) string {
	t.Helper()
	data, err := json.Marshal(apicontract.DNSProfileCreateRequest{Name: "DNS", Tags: []string{"dns-test"}, DNSProfile: profile})
	if err != nil {
		t.Fatal("could not encode synthetic DNS request")
	}
	return string(data)
}

func requireDNS(t *testing.T, response *httptest.ResponseRecorder, status int) apicontract.DNSProfileResource {
	t.Helper()
	requireNodeStatus(t, response, status)
	var body apicontract.DNSProfileResponse
	if json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Data.Metadata.Kind != ir.KindDNSProfile || body.Data.Diagnostics == nil {
		t.Fatal("DNS response did not match the typed contract")
	}
	etag, _ := apicontract.ETag(int64(body.Data.Metadata.Revision))
	if response.Header().Get("ETag") != etag {
		t.Fatal("DNS ETag disagreed with revision")
	}
	if strings.Contains(response.Body.String(), postgresTestSecret) {
		t.Fatal("DNS response exposed an outbound credential")
	}
	return body.Data
}

func TestPostgresDNSCRUDOrderedRulesReferencesAndAudit(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	a := h.create(t, "dns-a", syntheticNode(), []string{}, true)
	b := h.create(t, "dns-b", syntheticNode(), []string{}, true)
	chain := requireChain(t, h.do(http.MethodPost, "/api/v1/chains", chainBody(t, "dns-chain", a.Metadata.ResourceID, b.Metadata.ResourceID, nil), "", nil), http.StatusCreated)
	group := policyGroupPayload(a.Metadata.ResourceID, chain.Metadata.ResourceID, ir.PolicyFixed)
	policy := requirePolicy(t, h.do(http.MethodPost, "/api/v1/policy-groups", policyBody(t, group), "", nil), http.StatusCreated)
	set, err := ir.NewRuleSet(ir.RuleSetWrite{SchemaVersion: 1, Format: ir.DomainCIDRText, Entries: []ir.RuleSetEntry{{Kind: ir.RuleSetDomain, Match: ir.DomainSuffix, Domain: "example.invalid"}}})
	if err != nil {
		t.Fatal("invalid DNS rule set fixture")
	}
	rules := mustCreate(t, h.env, catalog.CreateInput{Name: "dns-domains", Enabled: true, Payload: &set})
	profile := dnsFixture(ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindPolicyGroup, ResourceID: policy.Metadata.ResourceID}, rules.Metadata.ResourceID)
	first := requireDNS(t, h.do(http.MethodPost, "/api/v1/dns-profiles", dnsBody(t, profile), "", nil), http.StatusCreated)
	second := requireDNS(t, h.do(http.MethodPost, "/api/v1/dns-profiles", dnsBody(t, profile), "", nil), http.StatusCreated)
	path := "/api/v1/dns-profiles/" + string(first.Metadata.ResourceID)
	read := requireDNS(t, h.do(http.MethodGet, path, "", "", nil), http.StatusOK)
	if read.DNSProfile.Rules[0].Comment != "ordered-first" || read.DNSProfile.Rules[1].Enabled || read.DNSProfile.FinalResolver != "udp" || len(read.DNSProfile.Bootstrap) != 1 {
		t.Fatal("DNS changed rule order, disabled rule, final resolver or explicit bootstrap")
	}
	pageResponse := h.do(http.MethodGet, "/api/v1/dns-profiles?limit=1&tag=dns-test&enabled=true", "", "", nil)
	requireNodeStatus(t, pageResponse, http.StatusOK)
	var page apicontract.DNSProfileListResponse
	if json.Unmarshal(pageResponse.Body.Bytes(), &page) != nil || len(page.Data) != 1 || page.Page.NextCursor == "" {
		t.Fatal("DNS pagination missing next cursor")
	}
	nextResponse := h.do(http.MethodGet, "/api/v1/dns-profiles?limit=1&tag=dns-test&enabled=true&cursor="+url.QueryEscape(page.Page.NextCursor), "", "", nil)
	requireNodeStatus(t, nextResponse, http.StatusOK)
	var next apicontract.DNSProfileListResponse
	if json.Unmarshal(nextResponse.Body.Bytes(), &next) != nil || len(next.Data) != 1 || next.Data[0].Metadata.ResourceID != second.Metadata.ResourceID || next.Page.NextCursor != "" {
		t.Fatal("DNS pagination repeated or lost an item")
	}
	requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/dns-profiles?limit=1&enabled=false&cursor="+url.QueryEscape(page.Page.NextCursor), "", "", nil), http.StatusBadRequest)
	requireNodeStatus(t, h.do(http.MethodGet, "/api/v1/dns-profiles/"+string(a.Metadata.ResourceID), "", "", nil), http.StatusNotFound)
	requireNodeStatus(t, h.do(http.MethodPatch, path, `{"name":"missing-etag"}`, "", nil), http.StatusPreconditionRequired)
	updated := requireDNS(t, h.do(http.MethodPatch, path, `{"name":"renamed","dns_profile":{"final_resolver":"local"}}`, revisionTag(1), nil), http.StatusOK)
	if updated.Metadata.Revision != 2 || updated.DNSProfile.FinalResolver != "local" {
		t.Fatal("DNS patch did not append requested revision")
	}
	requireNodeStatus(t, h.do(http.MethodPatch, path, `{"name":"stale"}`, revisionTag(1), nil), http.StatusPreconditionFailed)
	old, err := h.env.store.Revision(h.env.ctx, h.env.scope, first.Metadata.ResourceID, 1)
	if err != nil || old.Payload.(*ir.DNSProfile).FinalResolver != "udp" {
		t.Fatal("DNS patch rewrote immutable history")
	}
	for _, target := range []struct {
		id    ir.ID
		count int
	}{{policy.Metadata.ResourceID, 4}, {rules.Metadata.ResourceID, 2}} {
		refs, err := h.env.store.References(h.env.ctx, h.env.scope, target.id, catalog.ReferenceOptions{IncludeHistorical: true, Limit: 100})
		count := 0
		for _, ref := range refs.Items {
			if ref.SourceID == first.Metadata.ResourceID && ref.SourceKind == ir.KindDNSProfile {
				count++
			}
		}
		if err != nil || count != target.count {
			t.Fatal("DNS reference history omitted outbound or disabled domain rule")
		}
	}
	requireNode(t, h.do(http.MethodPatch, nodePath(b.Metadata.ResourceID), `{"enabled":false}`, revisionTag(1), nil), http.StatusOK)
	requireNodeStatus(t, h.do(http.MethodPatch, path, `{"name":"unavailable-closure"}`, revisionTag(2), nil), http.StatusUnprocessableEntity)
	requireNodeStatus(t, h.do(http.MethodDelete, path, "", revisionTag(2), nil), http.StatusOK)
	requireNodeStatus(t, h.do(http.MethodGet, path, "", "", nil), http.StatusNotFound)
	deleted, err := h.env.store.Revision(h.env.ctx, h.env.scope, first.Metadata.ResourceID, 3)
	if err != nil || deleted.Metadata.Enabled || deleted.Metadata.SecurityEpoch != 2 {
		t.Fatal("DNS delete did not revoke its current revision")
	}
	var audits int
	if h.env.runtime.QueryRow(h.env.ctx, "SELECT count(*) FROM public.resource_audit_events WHERE action IN ('dns_profile.create','dns_profile.update','dns_profile.delete')").Scan(&audits) != nil || audits != 4 {
		t.Fatal("DNS mutations missing exact audit count")
	}
}

func TestPostgresDNSRejectsCyclesImplicitResolversAndUnavailableReferences(t *testing.T) {
	h := newNodeHTTPAcceptance(t)
	profile := dnsFixture(ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Direct}, "")
	good := dnsBody(t, profile)
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/dns-profiles", good, "", func(r *http.Request) { r.Header.Del("Cookie") }), http.StatusUnauthorized)
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/dns-profiles", good, "", func(r *http.Request) { r.Header.Del("X-CSRF-Token") }), http.StatusForbidden)
	for name, mutate := range map[string]func(*ir.DNSProfile){
		"self-cycle": func(p *ir.DNSProfile) { p.Resolvers[2].BootstrapResolverID = "https" },
		"indirect-cycle": func(p *ir.DNSProfile) {
			second := p.Resolvers[2].Clone()
			second.ResolverID = "other"
			second.BootstrapResolverID = "https"
			p.Resolvers[2].BootstrapResolverID = "other"
			p.Resolvers = append(p.Resolvers, second)
		},
		"unknown-bootstrap":  func(p *ir.DNSProfile) { p.Resolvers[2].BootstrapResolverID = "missing" },
		"unknown-final":      func(p *ir.DNSProfile) { p.FinalResolver = "missing" },
		"unknown-rule":       func(p *ir.DNSProfile) { p.Rules[1].ResolverID = "missing" },
		"duplicate-resolver": func(p *ir.DNSProfile) { p.Resolvers[2].ResolverID = "udp" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := profile.Clone()
			mutate(&bad)
			requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/dns-profiles", dnsBody(t, bad), "", nil), http.StatusUnprocessableEntity)
		})
	}
	for _, mutate := range []func(*ir.DNSProfile){
		func(p *ir.DNSProfile) { p.Bootstrap = []ir.BootstrapResolver{} },
		func(p *ir.DNSProfile) { p.Bootstrap[0].Address = "implicit.example.invalid" },
		func(p *ir.DNSProfile) { p.Resolvers[1].Outbound = nil },
		func(p *ir.DNSProfile) { p.Resolvers[1].Address = "dns.example.invalid" },
	} {
		bad := profile.Clone()
		mutate(&bad)
		requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/dns-profiles", dnsBody(t, bad), "", nil), http.StatusBadRequest)
	}
	disabled := h.create(t, "disabled-dns-outbound", syntheticNode(), []string{}, false)
	for _, id := range []ir.ID{disabled.Metadata.ResourceID, "99999999-9999-4999-8999-999999999999"} {
		bad := dnsFixture(ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: id}, "")
		requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/dns-profiles", dnsBody(t, bad), "", nil), http.StatusUnprocessableEntity)
	}
	set, err := ir.NewRuleSet(ir.RuleSetWrite{SchemaVersion: 1, Format: ir.DomainCIDRText, Entries: []ir.RuleSetEntry{{Kind: ir.RuleSetCIDR, CIDR: "192.0.2.0/24"}}})
	if err != nil {
		t.Fatal("invalid DNS CIDR fixture")
	}
	rules := mustCreate(t, h.env, catalog.CreateInput{Name: "CIDR-not-DNS", Enabled: true, Payload: &set})
	wrongKind := dnsFixture(ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: rules.Metadata.ResourceID}, "")
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/dns-profiles", dnsBody(t, wrongKind), "", nil), http.StatusUnprocessableEntity)
	otherScope := ir.ID("20000000-0000-4000-8000-000000000001")
	if err := h.env.store.EnsureScope(h.env.ctx, otherScope, "DNS other workspace"); err != nil {
		t.Fatal("cannot create DNS cross-scope fixture")
	}
	var cross ir.Resource
	if err := h.env.store.Transact(h.env.ctx, otherScope, func(tx catalog.Tx) error {
		var err error
		cross, err = tx.Create(h.env.ctx, catalog.CreateInput{Name: "cross-scope", Enabled: true, Payload: syntheticNode()})
		return err
	}); err != nil {
		t.Fatal("cannot create DNS cross-scope node")
	}
	crossProfile := dnsFixture(ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: cross.Metadata.ResourceID}, "")
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/dns-profiles", dnsBody(t, crossProfile), "", nil), http.StatusUnprocessableEntity)
	bad := dnsFixture(ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Direct}, rules.Metadata.ResourceID)
	requireNodeStatus(t, h.do(http.MethodPost, "/api/v1/dns-profiles", dnsBody(t, bad), "", nil), http.StatusUnprocessableEntity)
	before, _ := h.env.store.Scope(h.env.ctx, h.env.scope)
	err = h.env.store.Transact(h.env.ctx, h.env.scope, func(tx catalog.Tx) error {
		if _, err := tx.Create(h.env.ctx, catalog.CreateInput{Name: "roll-back", Enabled: true, Payload: &profile}); err != nil {
			return err
		}
		_, _ = tx.Create(h.env.ctx, catalog.CreateInput{Name: "invalid-cidr", Enabled: true, Payload: &bad})
		return nil
	})
	if !errors.Is(err, catalog.ErrInvalidReference) {
		t.Fatal("storage did not latch invalid DNS references")
	}
	after, _ := h.env.store.Scope(h.env.ctx, h.env.scope)
	if before.CatalogRevision != after.CatalogRevision {
		t.Fatal("invalid DNS transaction changed catalog revision")
	}
	created := requireDNS(t, h.do(http.MethodPost, "/api/v1/dns-profiles", good, "", nil), http.StatusCreated)
	if _, err := h.env.admin.Exec(h.env.ctx, "REVOKE INSERT (id,scope_id,actor_id,object_id,revision,action,request_id) ON public.resource_audit_events FROM proxyloom"); err != nil {
		t.Fatal("could not prepare DNS audit failure")
	}
	requireNodeStatus(t, h.do(http.MethodPatch, "/api/v1/dns-profiles/"+string(created.Metadata.ResourceID), `{"name":"unaudited"}`, revisionTag(1), nil), http.StatusServiceUnavailable)
	if _, err := h.env.store.Revision(h.env.ctx, h.env.scope, created.Metadata.ResourceID, 2); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatal("DNS audit failure left a revision")
	}
}
