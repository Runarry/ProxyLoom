package singbox

import (
	"testing"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestDNSExplicitBootstrapDetourAndURL(t *testing.T) {
	in := adapter.EmitInput{TargetKey: "singbox-dns", DNS: &adapter.DNSInput{ResourceID: "bb19ca69-a9ab-421b-8723-af89a445f905", Profile: ir.DNSProfile{
		Bootstrap: []ir.BootstrapResolver{{ResolverID: "boot", Kind: ir.DNSUDP, Address: "192.0.2.53", Port: 5353}},
		Resolvers: []ir.DNSResolver{{ResolverID: "local", Kind: ir.DNSLocal}, {ResolverID: "udp", Kind: ir.DNSUDP, Address: "2001:db8::1", Port: 53}, {ResolverID: "https", Kind: ir.DNSHTTPS, URL: "https://dns.example.org:8443/dns%2Fquery", BootstrapResolverID: "boot"}}, FinalResolver: "https",
	}, OutboundTags: map[string]string{"udp": "direct", "https": "n_exit"}, Rules: []adapter.RouteRule{{Condition: adapter.Condition{Kind: "and", Terms: []adapter.Condition{{Kind: "domain", Values: []string{"a.example.org"}}, {Kind: "domain_suffix", Values: []string{"example.org"}}}}, Target: "udp", FieldPath: "/payload/rules/0"}}}}
	doc := document{}
	if err := applyDNS(&doc, in); err != nil {
		t.Fatal(err)
	}
	if doc.DNS.Final != "https" || len(doc.DNS.Servers) != 4 {
		t.Fatal("explicit final/bootstrap lost")
	}
	s := doc.DNS.Servers[3]
	if s.Detour != "n_exit" || s.DomainResolver != "boot" || s.Path != "/dns%2Fquery" || s.TLS.Insecure || s.TLS.ServerName != "dns.example.org" {
		t.Fatalf("HTTPS mapping: %+v", s)
	}
	if doc.DNS.Rules[0].Mode != "and" || len(doc.DNS.Rules[0].Rules) != 2 {
		t.Fatal("AND lost")
	}
	in.DNS.Rules[0].Condition = adapter.Condition{Kind: "network", Values: []string{"udp"}}
	err := applyDNS(&document{}, in)
	ds, ok := err.(ir.Diagnostics)
	if !ok || ds[0].FieldPath != "/payload/rules/0/match" || ds[0].ResourceID != in.DNS.ResourceID || ds[0].TargetKey != in.TargetKey {
		t.Fatalf("diagnostic: %v", err)
	}
	in.DNS.Profile.Resolvers[2].URL += "?key=secret"
	err = applyDNS(&document{}, in)
	ds, ok = err.(ir.Diagnostics)
	if !ok || ds[0].FieldPath != "/payload/resolvers/2/url" || ds[0].ResourceID != in.DNS.ResourceID || ds[0].TargetKey != in.TargetKey {
		t.Fatalf("query diagnostic: %v", err)
	}
}

func TestDNSRemoteOutboundCannotDisappear(t *testing.T) {
	for _, tag := range []string{"", adapter.BlockTag} {
		in := adapter.EmitInput{TargetKey: "singbox-dns", DNS: &adapter.DNSInput{Profile: ir.DNSProfile{Resolvers: []ir.DNSResolver{{ResolverID: "udp", Kind: ir.DNSUDP, Address: "192.0.2.1", Port: 53}}, FinalResolver: "udp"}, OutboundTags: map[string]string{"udp": tag}}}
		if err := applyDNS(&document{}, in); err == nil {
			t.Fatal("unsupported outbound silently became direct")
		}
	}
}
