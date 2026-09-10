package ir

import (
	"encoding/json"
	"net/netip"
	"slices"
	"strconv"
)

type DNSResolverKind string

const (
	DNSLocal DNSResolverKind = "local"
	DNSUDP   DNSResolverKind = "udp"
	DNSHTTPS DNSResolverKind = "https"
)

// Bootstrap is explicit and independent of business DNS. UDP addresses are
// literals, so a bootstrap resolver cannot recursively require name resolution.
type BootstrapResolver struct {
	ResolverID string          `json:"resolver_id"`
	Kind       DNSResolverKind `json:"kind"`
	Address    string          `json:"address,omitempty"`
	Port       int             `json:"port,omitempty"`
}

func (v BootstrapResolver) Validate() error { return validateValue(v, "bootstrap_resolver") }
func (v *BootstrapResolver) UnmarshalJSON(data []byte) error {
	type plain BootstrapResolver
	var next plain
	if err := decodePlain(data, "bootstrap_resolver", &next); err != nil {
		return err
	}
	*v = BootstrapResolver(next)
	return nil
}

// DNSResolver is a strict union. Only UDP has Address/Port; HTTPS has URL and
// BootstrapResolverID; each remote resolver requires an explicit Outbound.
type DNSResolver struct {
	ResolverID          string          `json:"resolver_id"`
	Kind                DNSResolverKind `json:"kind"`
	Address             string          `json:"address,omitempty"`
	Port                int             `json:"port,omitempty"`
	URL                 string          `json:"url,omitempty"`
	BootstrapResolverID string          `json:"bootstrap_resolver_id,omitempty"`
	Outbound            *TargetRef      `json:"outbound,omitempty"`
}

func (v DNSResolver) Validate() error {
	if err := validateValue(v, "dns_resolver"); err != nil {
		return err
	}
	if v.Kind == DNSHTTPS {
		// Reuse the same pure HTTPS syntax checks as runtime health URLs.
		health := PolicyHealthCheck{Enabled: false, URL: v.URL}
		if err := health.Validate(); err != nil {
			return err
		}
	}
	return nil
}
func (v *DNSResolver) UnmarshalJSON(data []byte) error {
	type plain DNSResolver
	var next plain
	if err := decodePlain(data, "dns_resolver", &next); err != nil {
		return err
	}
	candidate := DNSResolver(next)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}
func (v DNSResolver) Clone() DNSResolver { v.Outbound = clonePointer(v.Outbound); return v }

type DNSRule struct {
	Match      DomainMatch `json:"match"`
	ResolverID string      `json:"resolver_id"`
	Enabled    bool        `json:"enabled"`
	Comment    string      `json:"comment"`
}

func (v DNSRule) Validate() error { return validateValue(v, "dns_rule") }
func (v *DNSRule) UnmarshalJSON(data []byte) error {
	type plain DNSRule
	var next plain
	if err := decodePlain(data, "dns_rule", &next); err != nil {
		return err
	}
	*v = DNSRule(next)
	return nil
}

type DNSProfile struct {
	SchemaVersion int                 `json:"schema_version"`
	Bootstrap     []BootstrapResolver `json:"bootstrap"`
	Resolvers     []DNSResolver       `json:"resolvers"`
	Rules         []DNSRule           `json:"rules"`
	FinalResolver string              `json:"final_resolver"`
}

func (*DNSProfile) resourcePayload() {}
func (v DNSProfile) Validate() error {
	if err := validateValue(v, "dns_profile"); err != nil {
		return err
	}
	var diagnostics Diagnostics
	bootstrap := make(map[string]bool, len(v.Bootstrap))
	resolvers := make(map[string]bool, len(v.Resolvers))
	for i, resolver := range v.Bootstrap {
		if bootstrap[resolver.ResolverID] {
			diagnostics = append(diagnostics, issue(DuplicateResource, "/bootstrap/"+strconv.Itoa(i)+"/resolver_id"))
		}
		bootstrap[resolver.ResolverID] = true
	}
	for i, resolver := range v.Resolvers {
		path := "/resolvers/" + strconv.Itoa(i)
		if resolvers[resolver.ResolverID] || bootstrap[resolver.ResolverID] {
			diagnostics = append(diagnostics, issue(DuplicateResource, path+"/resolver_id"))
		}
		resolvers[resolver.ResolverID] = true
	}
	for i, resolver := range v.Resolvers {
		path := "/resolvers/" + strconv.Itoa(i)
		if err := resolver.Validate(); err != nil {
			diagnostics = append(diagnostics, prefixDiagnostics(err, path, "").(Diagnostics)...)
		}
		if resolver.Kind == DNSHTTPS && !bootstrap[resolver.BootstrapResolverID] {
			code := ReferenceMissing
			if resolvers[resolver.BootstrapResolverID] {
				code = ReferenceKind
			}
			diagnostics = append(diagnostics, issue(code, path+"/bootstrap_resolver_id"))
		}
	}
	// Report every edge of a forbidden resolver-to-resolver bootstrap cycle,
	// using array pointers rather than echoing input names or resolver URLs.
	indices := make(map[string]int, len(v.Resolvers))
	for i, resolver := range v.Resolvers {
		indices[resolver.ResolverID] = i
	}
	colors := make([]uint8, len(v.Resolvers))
	var stack []int
	var visit func(int)
	visit = func(i int) {
		colors[i] = 1
		stack = append(stack, i)
		resolver := v.Resolvers[i]
		if next, exists := indices[resolver.BootstrapResolverID]; resolver.Kind == DNSHTTPS && exists {
			if colors[next] == 0 {
				visit(next)
			} else if colors[next] == 1 {
				start := slices.Index(stack, next)
				for _, index := range stack[start:] {
					diagnostics = append(diagnostics, issue(ReferenceCycle, "/resolvers/"+strconv.Itoa(index)+"/bootstrap_resolver_id"))
				}
			}
		}
		stack = stack[:len(stack)-1]
		colors[i] = 2
	}
	for i := range v.Resolvers {
		if colors[i] == 0 {
			visit(i)
		}
	}
	if !resolvers[v.FinalResolver] {
		diagnostics = append(diagnostics, issue(ReferenceMissing, "/final_resolver"))
	}
	for i, rule := range v.Rules {
		if !resolvers[rule.ResolverID] {
			diagnostics = append(diagnostics, issue(ReferenceMissing, "/rules/"+strconv.Itoa(i)+"/resolver_id"))
		}
	}
	if len(diagnostics) > 0 {
		return stableDiagnostics(diagnostics)
	}
	return nil
}

type dnsResolverFields DNSResolver

func (v *DNSProfile) UnmarshalJSON(data []byte) error {
	type plain DNSProfile
	var next struct {
		plain
		Resolvers []dnsResolverFields `json:"resolvers"`
	}
	if err := decodePlain(data, "dns_profile", &next); err != nil {
		return err
	}
	candidate := DNSProfile(next.plain)
	candidate.Resolvers = make([]DNSResolver, len(next.Resolvers))
	for i, resolver := range next.Resolvers {
		candidate.Resolvers[i] = DNSResolver(resolver)
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*v = candidate
	return nil
}
func DecodeDNSProfile(data []byte) (DNSProfile, error) {
	var value DNSProfile
	err := json.Unmarshal(data, &value)
	return value, safeDecodeError(err)
}
func (v DNSProfile) Clone() DNSProfile {
	v.Bootstrap = slices.Clone(v.Bootstrap)
	v.Resolvers = slices.Clone(v.Resolvers)
	for i := range v.Resolvers {
		v.Resolvers[i] = v.Resolvers[i].Clone()
	}
	v.Rules = slices.Clone(v.Rules)
	for i := range v.Rules {
		v.Rules[i].Match = v.Rules[i].Match.Clone()
	}
	return v
}

// NeedsBootstrap reports whether a node endpoint needs the explicitly chosen
// bootstrap DNS. It performs address parsing only and never resolves a host.
func (v Node) NeedsBootstrap() bool { _, err := netip.ParseAddr(v.Endpoint.Host); return err != nil }
