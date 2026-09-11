package mihomo

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"gopkg.in/yaml.v3"
)

type dnsRuleProvider struct {
	Type     string   `yaml:"type"`
	Behavior string   `yaml:"behavior"`
	Payload  []string `yaml:"payload"`
}
type dnsPolicy struct{ key, server string }
type dnsPolicies []dnsPolicy

func (p dnsPolicies) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, item := range p {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item.key}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item.server})
	}
	return node, nil
}

type dnsConfig struct {
	Enable                bool        `yaml:"enable"`
	IPv6                  bool        `yaml:"ipv6"`
	EnhancedMode          string      `yaml:"enhanced-mode"`
	UseHosts              bool        `yaml:"use-hosts"`
	UseSystemHosts        bool        `yaml:"use-system-hosts"`
	RespectRules          bool        `yaml:"respect-rules"`
	NameServer            []string    `yaml:"nameserver"`
	DefaultNameServer     []string    `yaml:"default-nameserver"`
	ProxyServerNameServer []string    `yaml:"proxy-server-nameserver"`
	NameServerPolicy      dnsPolicies `yaml:"nameserver-policy,omitempty"`
}

func applyDNS(doc *document, in adapter.EmitInput) error {
	if in.DNS == nil {
		return nil
	}
	p := in.DNS.Profile
	fail := func(path string) error {
		return ir.Diagnostics{adapter.CompileIssue(ir.CompileUnmappedField, path, in.TargetKey, in.DNS.ResourceID)}
	}
	bootstrap := map[string]string{}
	for _, b := range p.Bootstrap {
		if b.Kind == ir.DNSLocal {
			bootstrap[b.ResolverID] = "system"
		} else {
			bootstrap[b.ResolverID] = "udp://" + net.JoinHostPort(b.Address, strconv.Itoa(b.Port))
		}
	}
	// This core has one bootstrap resolver for every remote DNS transport.
	// Unused bootstrap definitions need not change the selected transport.
	selected := ""
	refs, err := in.OutboundRefs()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if node, ok := ref.Resource.Payload.(*ir.Node); ok && node.NeedsBootstrap() {
			if len(p.Bootstrap) == 0 {
				return fail("/payload/bootstrap")
			}
			selected = bootstrap[p.Bootstrap[0].ResolverID]
		}
	}
	servers := map[string]string{}
	for i, r := range p.Resolvers {
		path := "/payload/resolvers/" + strconv.Itoa(i)
		tag := in.ResolvePolicyTag(in.DNS.OutboundTags[r.ResolverID])
		if tag == "direct" {
			tag = "DIRECT"
		}
		if tag == adapter.BlockTag {
			tag = "REJECT"
		}
		var server string
		switch r.Kind {
		case ir.DNSLocal:
			server = "system"
		case ir.DNSUDP:
			if tag == "" {
				return fail(path + "/outbound")
			}
			server = "udp://" + net.JoinHostPort(r.Address, strconv.Itoa(r.Port)) + "#" + tag
		case ir.DNSHTTPS:
			if tag == "" {
				return fail(path + "/outbound")
			}
			u, err := url.Parse(r.URL)
			if err != nil {
				return fail(path + "/url")
			}
			// The pinned parser rebuilds URL from Scheme/Host/Path only.
			if u.RawQuery != "" || u.ForceQuery || u.RawPath != "" {
				return fail(path + "/url")
			}
			if _, err := netip.ParseAddr(u.Hostname()); err != nil {
				// TCP DNS through P0 proxy adapters forwards the hostname to
				// the remote peer. Only DIRECT invokes the configured bootstrap.
				if tag != "DIRECT" {
					return fail(path + "/bootstrap_resolver_id")
				}
				b, ok := bootstrap[r.BootstrapResolverID]
				if !ok {
					return fail(path + "/bootstrap_resolver_id")
				}
				if selected != "" && selected != b {
					return fail(path + "/bootstrap_resolver_id")
				}
				selected = b
			}
			u.Fragment = tag
			server = u.String()
		default:
			return fail(path + "/kind")
		}
		servers[r.ResolverID] = server
	}
	if selected == "" {
		if len(p.Bootstrap) == 0 {
			return fail("/payload/bootstrap")
		}
		selected = bootstrap[p.Bootstrap[0].ResolverID]
	}
	final, ok := servers[p.FinalResolver]
	if !ok {
		return fail("/payload/final_resolver")
	}
	doc.DNS = &dnsConfig{Enable: true, IPv6: true, EnhancedMode: "redir-host", NameServer: []string{final}, DefaultNameServer: []string{selected}, ProxyServerNameServer: []string{selected}}
	doc.IPv6 = true
	limit := func(path string) error {
		return ir.Diagnostics{adapter.CompileIssue(ir.InputLimitExceeded, path, in.TargetKey, in.DNS.ResourceID)}
	}
	if len(in.DNS.Rules) > adapter.MaxRules {
		return limit("/payload/rules")
	}
	count := 0
	for i, r := range in.DNS.Rules {
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
			return limit(r.FieldPath + "/match")
		}
		if len(domains) == 0 {
			continue
		}
		name := fmt.Sprintf("dns_rule_%04d", i)
		if doc.RuleProviders == nil {
			doc.RuleProviders = map[string]dnsRuleProvider{}
		}
		if _, exists := doc.RuleProviders[name]; exists {
			return fail(r.FieldPath)
		}
		payload := make([]string, 0, len(domains))
		for _, domain := range domains {
			prefix, value, _ := strings.Cut(domain, ":")
			if prefix == "suffix" {
				value = "+." + value
			}
			payload = append(payload, value)
		}
		doc.RuleProviders[name] = dnsRuleProvider{Type: "inline", Behavior: "domain", Payload: payload}
		// Each rule-set becomes an independent ordered matcher. Plain domain
		// keys merge into a specificity trie and would change first-match order.
		doc.DNS.NameServerPolicy = append(doc.DNS.NameServerPolicy, dnsPolicy{key: "rule-set:" + name, server: server})
	}
	return nil
}

// Domain conjunctions reduce to intersections of exact names and suffix trees.
// The result is a union; contradictory rules stay empty, never match-all.
func dnsDomains(c adapter.Condition) ([]string, ir.DiagnosticCode) {
	if c.Kind == "domain" || c.Kind == "domain_suffix" {
		if len(c.Values) > adapter.MaxRules {
			return nil, ir.InputLimitExceeded
		}
		prefix := "exact:"
		if c.Kind == "domain_suffix" {
			prefix = "suffix:"
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
		result := map[string]bool{}
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
			if set["suffix:"+name] {
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
	out := []string{}
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
