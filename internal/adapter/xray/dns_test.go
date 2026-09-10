package xray

import (
	"reflect"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func dnsInput() adapter.EmitInput {
	return adapter.EmitInput{TargetKey: "xray-dns", FinalTag: "direct", DNS: &adapter.DNSInput{
		ResourceID: "bb19ca69-a9ab-421b-8723-af89a445f905",
		Profile: ir.DNSProfile{Bootstrap: []ir.BootstrapResolver{{ResolverID: "boot", Kind: ir.DNSLocal}}, Resolvers: []ir.DNSResolver{
			{ResolverID: "a", Kind: ir.DNSUDP, Address: "192.0.2.1", Port: 53},
			{ResolverID: "b", Kind: ir.DNSHTTPS, URL: "https://192.0.2.2:8443/dns-query?q=1", BootstrapResolverID: "boot"},
			{ResolverID: "local", Kind: ir.DNSLocal},
		}, FinalResolver: "b"}, OutboundTags: map[string]string{"a": "direct", "b": "n_exit"},
	}}
}

func TestDNSOrderedServersAndConjunction(t *testing.T) {
	in := dnsInput()
	in.DNS.Rules = []adapter.RouteRule{
		{Condition: adapter.Condition{Kind: "domain_suffix", Values: []string{"example.org"}}, Target: "a", FieldPath: "/payload/rules/0"},
		{Condition: adapter.Condition{Kind: "domain", Values: []string{"a.example.org"}}, Target: "b", FieldPath: "/payload/rules/1"},
		{Condition: adapter.Condition{Kind: "and", Terms: []adapter.Condition{
			{Kind: "domain", Values: []string{"yes.example.net", "outside.org"}}, {Kind: "domain_suffix", Values: []string{"example.net"}},
		}}, Target: "a", FieldPath: "/payload/rules/2"},
		{Condition: adapter.Condition{Kind: "and", Terms: []adapter.Condition{
			{Kind: "domain", Values: []string{"outside.org"}}, {Kind: "domain_suffix", Values: []string{"example.net"}},
		}}, Target: "a", FieldPath: "/payload/rules/3"},
	}
	doc := document{}
	if err := applyDNS(&doc, in); err != nil {
		t.Fatal(err)
	}
	if len(doc.DNS.Servers) != 4 {
		t.Fatalf("servers: %d", len(doc.DNS.Servers))
	}
	for i, s := range doc.DNS.Servers {
		if !s.FinalQuery || s.SkipFallback != (i < 3) {
			t.Fatalf("fallback changed at %d", i)
		}
	}
	if !reflect.DeepEqual(doc.DNS.Servers[2].Domains, []string{"full:yes.example.net"}) {
		t.Fatal("AND became OR")
	}
	if doc.DNS.Servers[1].Tag != doc.DNS.Servers[3].Tag || doc.DNS.Servers[3].Address != in.DNS.Profile.Resolvers[1].URL {
		t.Fatal("resolver identity or URL lost")
	}
	if len(doc.Routing.Rules) != 2 || doc.Routing.Rules[1].OutboundTag != "n_exit" {
		t.Fatal("DNS explicit outbound lost")
	}
}

func TestDNSMappingDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		mutate     func(*adapter.EmitInput)
	}{
		{"udp-bootstrap-hostname-doh", "/payload/resolvers/1/bootstrap_resolver_id", func(in *adapter.EmitInput) {
			in.DNS.Profile.Bootstrap[0].Kind = ir.DNSUDP
			in.DNS.Profile.Bootstrap[0].Address = "192.0.2.53"
			in.DNS.Profile.Resolvers[1].URL = "https://dns.example.org/dns-query"
		}},
		{"conditional-local", "/payload/resolvers/2/kind", func(in *adapter.EmitInput) {
			in.DNS.Rules = []adapter.RouteRule{{Condition: adapter.Condition{Kind: "domain", Values: []string{"a.example.org"}}, Target: "local", FieldPath: "/payload/rules/0"}}
		}},
		{"local-preempts-later-private", "/payload/rules/0/resolver_id", func(in *adapter.EmitInput) {
			in.DNS.Profile.FinalResolver = "local"
			in.DNS.Rules = []adapter.RouteRule{{Condition: adapter.Condition{Kind: "domain", Values: []string{"a.example.org"}}, Target: "local", FieldPath: "/payload/rules/0"}, {Condition: adapter.Condition{Kind: "domain", Values: []string{"a.test"}}, Target: "a", FieldPath: "/payload/rules/1"}}
		}},
		{"non-domain-condition", "/payload/rules/0/match", func(in *adapter.EmitInput) {
			in.DNS.Rules = []adapter.RouteRule{{Condition: adapter.Condition{Kind: "ip_cidr", Values: []string{"192.0.2.0/24"}}, Target: "a", FieldPath: "/payload/rules/0"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := dnsInput()
			tc.mutate(&in)
			err := applyDNS(&document{}, in)
			ds, ok := err.(ir.Diagnostics)
			if !ok || len(ds) != 1 || ds[0].FieldPath != tc.path || ds[0].ResourceID != in.DNS.ResourceID || ds[0].TargetKey != in.TargetKey {
				t.Fatalf("diagnostic: %v", err)
			}
		})
	}
	// System bootstrap and an explicit direct hostname DoH remain supported.
	in := dnsInput()
	in.DNS.Profile.Resolvers[1].URL = "https://dns.example.org/dns-query"
	in.DNS.OutboundTags["b"] = "direct"
	doc := document{}
	if err := applyDNS(&doc, in); err != nil {
		t.Fatal(err)
	}
	if doc.DNS.Servers[0].Address != "https+local://dns.example.org/dns-query" {
		t.Fatal("local bootstrap mapping missing")
	}
}

func TestDNSDomainIntersectionBoundaries(t *testing.T) {
	got := dnsDomainIntersection([]string{"domain:example.org", "full:a.net"}, []string{"domain:sub.example.org", "full:notexample.org", "full:a.net"})
	want := []string{"domain:sub.example.org", "full:a.net"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("intersection %v", got)
	}
	_, code := dnsDomains(adapter.Condition{Kind: "domain", Values: make([]string, adapter.MaxRules+1)})
	if code != ir.InputLimitExceeded {
		t.Fatalf("limit %s", code)
	}
}
