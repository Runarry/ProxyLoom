package xray

import (
	"net/netip"
	"slices"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type balancer struct {
	Tag         string           `json:"tag"`
	Selector    []string         `json:"selector"`
	FallbackTag string           `json:"fallbackTag"`
	Strategy    balancerStrategy `json:"strategy"`
}
type balancerStrategy struct {
	Type string `json:"type"`
}

// Xray requires this feature when a round-robin balancer has fallbackTag.
// An empty selector starts no probe loop and preserves health_check=false.
type observatory struct {
	SubjectSelector []string `json:"subjectSelector"`
}

func applyOrchestration(doc *document, in adapter.EmitInput) error {
	listener := in.Listener("socks5", ListenPort)
	protocol := listener.Protocol
	if protocol == "mixed" {
		return ir.Diagnostics{adapter.CompileIssue(ir.CompileUnmappedField, "/payload/local_listener/protocol", in.TargetKey, "")}
	}
	if protocol == "socks5" {
		protocol = "socks"
	}
	doc.Inbounds[0].Protocol, doc.Inbounds[0].Listen, doc.Inbounds[0].Port = protocol, listener.Listen, listener.Port
	for _, p := range in.Policies {
		if p.Strategy == ir.PolicyFixed {
			continue
		}
		if p.Strategy != ir.PolicyRoundRobin {
			return ir.Diagnostics{adapter.CompileIssue(ir.CapabilityUnsupported, "/payload/strategy", in.TargetKey, p.ResourceID)}
		}
		// Native selector is a prefix match. Generated node/chain tags must
		// select precisely one outbound each, never other independent instances.
		for _, tag := range p.Members {
			for _, out := range doc.Outbounds {
				if out.Tag != tag && strings.HasPrefix(out.Tag, tag) {
					return ir.Diagnostics{adapter.CompileIssue(ir.CompileLabelCollision, "/payload/members", in.TargetKey, p.ResourceID)}
				}
			}
		}
		doc.Routing.Balancers = append(doc.Routing.Balancers, balancer{Tag: p.Tag, Selector: p.Members, FallbackTag: adapter.BlockTag, Strategy: balancerStrategy{Type: "roundRobin"}})
		doc.Observatory = &observatory{SubjectSelector: []string{}}
	}
	if in.UsesTag("direct") {
		direct := outbound{Protocol: "freedom", Tag: "direct"}
		if in.DNS != nil {
			direct.Settings = struct {
				DomainStrategy string `json:"domainStrategy"`
			}{DomainStrategy: "ForceIP"}
		}
		doc.Outbounds = append(doc.Outbounds, direct)
	}
	targetRule := func(tag string) routingRule {
		tag = in.ResolvePolicyTag(tag)
		rule := routingRule{Type: "field", InboundTag: []string{adapter.InboundTag}, OutboundTag: tag}
		for _, p := range doc.Routing.Balancers {
			if p.Tag == tag {
				rule.OutboundTag = ""
				rule.BalancerTag = tag
			}
		}
		return rule
	}
	exit, _ := in.ExitTag()
	doc.Routing.Rules = nil
	if in.Routing != nil {
		if in.Routing.Mode == ir.ResolveForIPRules {
			doc.Routing.DomainStrategy = "IPOnDemand"
		}
		for _, r := range in.Routing.Rules {
			branches := distribute(r.Condition)
			for _, terms := range branches {
				rule := targetRule(r.Target)
				matched := true
				for _, term := range terms {
					if !addCondition(&rule, term) {
						matched = false
						break
					}
				}
				if matched {
					doc.Routing.Rules = append(doc.Routing.Rules, rule)
				}
			}
		}
	}
	doc.Routing.Rules = append(doc.Routing.Rules, targetRule(exit))
	return applyDNS(doc, in)
}

// The compiler creates one flat AND and at most one OR (the rule-set union).
// Distribution keeps each original rule's alternatives contiguous.
func distribute(c adapter.Condition) [][]adapter.Condition {
	if c.Kind != "and" && c.Kind != "or" {
		return [][]adapter.Condition{{c}}
	}
	if c.Kind == "or" {
		var result [][]adapter.Condition
		for _, term := range c.Terms {
			result = append(result, distribute(term)...)
		}
		return result
	}
	result := [][]adapter.Condition{{}}
	for _, term := range c.Terms {
		var next [][]adapter.Condition
		for _, a := range result {
			for _, b := range distribute(term) {
				next = append(next, append(slices.Clone(a), b...))
			}
		}
		result = next
	}
	return result
}

func addCondition(rule *routingRule, c adapter.Condition) bool {
	switch c.Kind {
	case "domain", "domain_suffix":
		prefix := "full:"
		if c.Kind == "domain_suffix" {
			prefix = "domain:"
		}
		var incoming []string
		for _, v := range c.Values {
			incoming = append(incoming, prefix+v)
		}
		if len(rule.Domain) == 0 {
			rule.Domain = incoming
			return true
		}
		var intersection []string
		for _, a := range rule.Domain {
			for _, b := range incoming {
				if domainIncludes(a, b) {
					intersection = append(intersection, b)
				} else if domainIncludes(b, a) {
					intersection = append(intersection, a)
				}
			}
		}
		slices.Sort(intersection)
		rule.Domain = slices.Compact(intersection)
		return len(rule.Domain) > 0
	case "ip_cidr":
		if len(rule.IP) == 0 {
			rule.IP = slices.Clone(c.Values)
			return true
		}
		var intersection []string
		for _, a := range rule.IP {
			pa := netip.MustParsePrefix(a)
			for _, b := range c.Values {
				pb := netip.MustParsePrefix(b)
				if pa.Contains(pb.Addr()) && pa.Bits() <= pb.Bits() {
					intersection = append(intersection, b)
				} else if pb.Contains(pa.Addr()) && pb.Bits() <= pa.Bits() {
					intersection = append(intersection, a)
				}
			}
		}
		slices.Sort(intersection)
		rule.IP = slices.Compact(intersection)
		return len(rule.IP) > 0
	case "port":
		rule.Port = strings.ReplaceAll(strings.Join(c.Values, ","), ":", "-")
	case "network":
		rule.Network = strings.Join(c.Values, ",")
	default:
		return false
	}
	return true
}
func domainIncludes(a, b string) bool {
	ap, av, _ := strings.Cut(a, ":")
	bp, bv, _ := strings.Cut(b, ":")
	if ap == "full" {
		return bp == "full" && av == bv
	}
	return av == bv || strings.HasSuffix(bv, "."+av)
}
