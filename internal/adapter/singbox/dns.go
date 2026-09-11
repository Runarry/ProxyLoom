package singbox

import (
	"net/netip"
	"net/url"
	"strconv"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type dnsConfig struct {
	Servers []dnsServer `json:"servers"`
	Rules   []dnsRule   `json:"rules,omitempty"`
	Final   string      `json:"final"`
}
type dnsServer struct {
	Type           string `json:"type"`
	Tag            string `json:"tag"`
	Server         string `json:"server,omitempty"`
	ServerPort     int    `json:"server_port,omitempty"`
	Path           string `json:"path,omitempty"`
	TLS            *tls   `json:"tls,omitempty"`
	DomainResolver string `json:"domain_resolver,omitempty"`
	Detour         string `json:"detour,omitempty"`
}
type dnsRule struct {
	routeRule
	Server string `json:"server"`
}

func applyDNS(doc *document, in adapter.EmitInput) error {
	if in.DNS == nil {
		return nil
	}
	p := in.DNS.Profile
	fail := func(path string) error {
		return ir.Diagnostics{adapter.CompileIssue(ir.CompileUnmappedField, path, in.TargetKey, in.DNS.ResourceID)}
	}
	if len(in.DNS.Rules) > adapter.MaxRules {
		return ir.Diagnostics{adapter.CompileIssue(ir.InputLimitExceeded, "/payload/rules", in.TargetKey, in.DNS.ResourceID)}
	}
	doc.DNS = &dnsConfig{Final: p.FinalResolver}
	resolvers := make(map[string]bool, len(p.Resolvers))
	for _, b := range p.Bootstrap {
		doc.DNS.Servers = append(doc.DNS.Servers, dnsServer{Type: string(b.Kind), Tag: b.ResolverID, Server: b.Address, ServerPort: b.Port})
	}
	for i, r := range p.Resolvers {
		path := "/payload/resolvers/" + strconv.Itoa(i)
		server := dnsServer{Type: string(r.Kind), Tag: r.ResolverID, Server: r.Address, ServerPort: r.Port}
		tag := in.ResolvePolicyTag(in.DNS.OutboundTags[r.ResolverID])
		if r.Kind != ir.DNSLocal && (tag == "" || tag == adapter.BlockTag) {
			return fail(path + "/outbound")
		}
		if r.Kind != ir.DNSLocal && tag != "direct" {
			server.Detour = tag
		}
		if r.Kind == ir.DNSHTTPS {
			u, err := url.Parse(r.URL)
			if err != nil {
				return fail(path + "/url")
			}
			// v1.14 exposes only a URL path. Appending ?query is escaped as
			// path text by URLSetPath, changing the administrator's endpoint.
			if u.RawQuery != "" || u.ForceQuery {
				return fail(path + "/url")
			}
			server.Server = u.Hostname()
			server.ServerPort = 443
			if u.Port() != "" {
				server.ServerPort, _ = strconv.Atoi(u.Port())
			}
			server.Path = u.EscapedPath()
			if server.Path == "" {
				server.Path = "/"
			}
			server.TLS = &tls{Enabled: true, ServerName: u.Hostname()}
			if _, err := netip.ParseAddr(u.Hostname()); err != nil {
				// v1.14 explicitly wraps detour dialers with domain_resolver;
				// the bootstrap query stays independent of the business resolver.
				server.DomainResolver = r.BootstrapResolverID
			}
		} else if r.Kind != ir.DNSUDP && r.Kind != ir.DNSLocal {
			return fail(path + "/kind")
		}
		doc.DNS.Servers = append(doc.DNS.Servers, server)
		resolvers[r.ResolverID] = true
	}
	if !resolvers[p.FinalResolver] {
		return fail("/payload/final_resolver")
	}
	for _, rule := range in.DNS.Rules {
		if !resolvers[rule.Target] {
			return fail(rule.FieldPath + "/resolver_id")
		}
		if !dnsDomainCondition(rule.Condition) {
			return fail(rule.FieldPath + "/match")
		}
		doc.DNS.Rules = append(doc.DNS.Rules, dnsRule{routeRule: nativeCondition(rule.Condition), Server: rule.Target})
	}
	return nil
}

func dnsDomainCondition(c adapter.Condition) bool {
	if c.Kind == "domain" || c.Kind == "domain_suffix" {
		return len(c.Values) > 0
	}
	if c.Kind != "and" && c.Kind != "or" || len(c.Terms) == 0 {
		return false
	}
	for _, term := range c.Terms {
		if !dnsDomainCondition(term) {
			return false
		}
	}
	return true
}
