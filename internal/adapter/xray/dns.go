package xray

import (
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type dnsConfig struct {
	Servers                []dnsServer `json:"servers"`
	DisableFallback        bool        `json:"disableFallback"`
	DisableFallbackIfMatch bool        `json:"disableFallbackIfMatch"`
	EnableParallelQuery    bool        `json:"enableParallelQuery"`
	UseSystemHosts         bool        `json:"useSystemHosts"`
}

type dnsServer struct {
	Address      string   `json:"address"`
	Port         int      `json:"port,omitempty"`
	Domains      []string `json:"domains,omitempty"`
	Tag          string   `json:"tag,omitempty"`
	SkipFallback bool     `json:"skipFallback"`
	FinalQuery   bool     `json:"finalQuery"`
}

func applyDNS(doc *document, in adapter.EmitInput) error {
	if in.DNS == nil {
		return nil
	}
	p := in.DNS.Profile
	fail := func(path string) error {
		return ir.Diagnostics{adapter.CompileIssue(ir.CompileUnmappedField, path, in.TargetKey, in.DNS.ResourceID)}
	}
	bootstrap := map[string]ir.BootstrapResolver{}
	for _, b := range p.Bootstrap {
		bootstrap[b.ResolverID] = b
	}
	// Xray's socket resolver is system/global, not selectable per outbound.
	// Literal endpoints need no lookup; named node endpoints require local bootstrap.
	refs, err := in.OutboundRefs()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if node, ok := ref.Resource.Payload.(*ir.Node); ok && node.NeedsBootstrap() {
			if len(p.Bootstrap) == 0 || p.Bootstrap[0].Kind != ir.DNSLocal {
				return fail("/payload/bootstrap")
			}
		}
	}
	servers := map[string]dnsServer{}
	kinds := map[string]ir.DNSResolverKind{}
	indices := map[string]int{}
	var routes []routingRule
	for i, r := range p.Resolvers {
		path := "/payload/resolvers/" + strconv.Itoa(i)
		server := dnsServer{Address: r.Address, Port: r.Port, Tag: "dns_resolver_" + strconv.Itoa(i), FinalQuery: true, SkipFallback: true}
		tag := in.ResolvePolicyTag(in.DNS.OutboundTags[r.ResolverID])
		switch r.Kind {
		case ir.DNSLocal:
			server.Address, server.Tag = "localhost", ""
		case ir.DNSUDP:
			if tag == "" {
				return fail(path + "/outbound")
			}
		case ir.DNSHTTPS:
			if tag == "" {
				return fail(path + "/outbound")
			}
			u, err := url.Parse(r.URL)
			if err != nil {
				return fail(path + "/url")
			}
			if _, err := netip.ParseAddr(u.Hostname()); err != nil {
				// The pinned DNS transport provides system bootstrap only in
				// DOHL mode. Remote DOH delegates hostname resolution to its
				// outbound, which cannot honor a separate bootstrap reference.
				b, ok := bootstrap[r.BootstrapResolverID]
				if !ok || b.Kind != ir.DNSLocal || tag != "direct" {
					return fail(path + "/bootstrap_resolver_id")
				}
				u.Scheme = "https+local"
				server.Tag = ""
			}
			server.Address = u.String()
		default:
			return fail(path + "/kind")
		}
		if server.Tag != "" {
			route := routingRule{Type: "field", InboundTag: []string{server.Tag}, OutboundTag: tag}
			for _, b := range doc.Routing.Balancers {
				if b.Tag == tag {
					route.OutboundTag = ""
					route.BalancerTag = tag
				}
			}
			routes = append(routes, route)
		}
		servers[r.ResolverID], kinds[r.ResolverID], indices[r.ResolverID] = server, r.Kind, i
	}
	final, ok := servers[p.FinalResolver]
	if !ok {
		return fail("/payload/final_resolver")
	}
	doc.DNS = &dnsConfig{DisableFallbackIfMatch: true}
	if len(in.DNS.Rules) > adapter.MaxRules {
		return ir.Diagnostics{adapter.CompileIssue(ir.InputLimitExceeded, "/payload/rules", in.TargetKey, in.DNS.ResourceID)}
	}
	count := 0
	localRule := ""
	for _, r := range in.DNS.Rules {
		server, ok := servers[r.Target]
		if !ok {
			return fail(r.FieldPath + "/resolver_id")
		}
		domains, code := dnsDomains(r.Condition)
		if code != "" {
			return ir.Diagnostics{adapter.CompileIssue(code, r.FieldPath+"/match", in.TargetKey, in.DNS.ResourceID)}
		}
		count += len(domains)
		if count > adapter.MaxRules {
			return ir.Diagnostics{adapter.CompileIssue(ir.InputLimitExceeded, r.FieldPath+"/match", in.TargetKey, in.DNS.ResourceID)}
		}
		if len(domains) == 0 {
			continue
		}
		// localhost injects private-TLD priority rules internally. It is safe
		// as final, but using it only for selected domains broadens the match.
		if kinds[r.Target] == ir.DNSLocal && kinds[p.FinalResolver] != ir.DNSLocal {
			return fail("/payload/resolvers/" + strconv.Itoa(indices[r.Target]) + "/kind")
		}
		// A conditional localhost client adds its private-TLD rules before
		// every later client. Reject only when those additions could preempt
		// a later remote rule; an explicit local final by itself is safe.
		if kinds[r.Target] == ir.DNSLocal {
			localRule = r.FieldPath
		} else if localRule != "" && dnsMatchesPrivate(domains) {
			return fail(localRule + "/resolver_id")
		}
		server.Domains = domains
		doc.DNS.Servers = append(doc.DNS.Servers, server)
	}
	// Pinned Xray finalQuery stops at the first domain-matching server.
	// Only the explicit final participates when no rule matched; no secondary
	// server is tried after a selected server fails.
	final.SkipFallback = false
	doc.DNS.Servers = append(doc.DNS.Servers, final)
	doc.Routing.Rules = append(routes, doc.Routing.Rules...)
	return nil
}

// Reduce domain-only boolean expressions by intersection, without distributing
// products of large rule sets. Contradictions remain empty, never match-all.
func dnsDomains(c adapter.Condition) ([]string, ir.DiagnosticCode) {
	if c.Kind == "domain" || c.Kind == "domain_suffix" {
		if len(c.Values) > adapter.MaxRules {
			return nil, ir.InputLimitExceeded
		}
		prefix := "full:"
		if c.Kind == "domain_suffix" {
			prefix = "domain:"
		}
		out := make([]string, len(c.Values))
		for i, v := range c.Values {
			out[i] = prefix + v
		}
		return out, ""
	}
	if c.Kind != "and" && c.Kind != "or" || len(c.Terms) == 0 {
		return nil, ir.CompileUnmappedField
	}
	var out []string
	for i, term := range c.Terms {
		next, code := dnsDomains(term)
		if code != "" {
			return nil, code
		}
		if c.Kind == "or" {
			out = append(out, next...)
		} else if i == 0 {
			out = next
		} else {
			out = dnsDomainIntersection(out, next)
		}
		if len(out) > adapter.MaxRules {
			return nil, ir.InputLimitExceeded
		}
	}
	slices.Sort(out)
	return slices.Compact(out), ""
}

func dnsDomainIntersection(a, b []string) []string {
	index := func(values []string) map[string]bool {
		result := make(map[string]bool, len(values))
		for _, v := range values {
			result[v] = true
		}
		return result
	}
	contains := func(set map[string]bool, pattern string) bool {
		if set[pattern] {
			return true
		}
		_, name, _ := strings.Cut(pattern, ":")
		for {
			if set["domain:"+name] {
				return true
			}
			_, rest, ok := strings.Cut(name, ".")
			if !ok {
				return false
			}
			name = rest
		}
	}
	ai, bi := index(a), index(b)
	var out []string
	for _, v := range a {
		if contains(bi, v) {
			out = append(out, v)
		}
	}
	for _, v := range b {
		if contains(ai, v) {
			out = append(out, v)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func dnsMatchesPrivate(domains []string) bool {
	for _, pattern := range domains {
		kind, name, _ := strings.Cut(pattern, ":")
		if !strings.Contains(name, ".") {
			return true
		}
		for _, suffix := range []string{"local", "localdomain", "localhost", "lan", "home.arpa", "example", "invalid", "test"} {
			if name == suffix || strings.HasSuffix(name, "."+suffix) || kind == "domain" && strings.HasSuffix(suffix, "."+name) {
				return true
			}
		}
	}
	return false
}
