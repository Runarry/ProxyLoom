package mihomo

import (
	"net"
	"strconv"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type policyGroup struct {
	Name          string   `yaml:"name"`
	Type          string   `yaml:"type"`
	Proxies       []string `yaml:"proxies"`
	Strategy      string   `yaml:"strategy,omitempty"`
	URL           string   `yaml:"url,omitempty"`
	Interval      int      `yaml:"interval,omitempty"`
	Timeout       *int     `yaml:"timeout,omitempty"`
	Tolerance     *int     `yaml:"tolerance,omitempty"`
	Lazy          bool     `yaml:"lazy"`
	DisableUDP    bool     `yaml:"disable-udp"`
	EmptyFallback string   `yaml:"empty-fallback"`
}

type controllerCORS struct {
	AllowOrigins        []string `yaml:"allow-origins"`
	AllowPrivateNetwork bool     `yaml:"allow-private-network"`
}

func applyOrchestration(doc *document, in adapter.EmitInput) error {
	listener := in.Listener("mixed", MixedPort)
	controlled := in.Preset != nil && in.Preset.ControlAPI.Enabled
	if controlled {
		doc.ExternalController = net.JoinHostPort(in.Preset.ControlAPI.Listen, strconv.Itoa(in.Preset.ControlAPI.Port))
		doc.ExternalControllerCORS = &controllerCORS{AllowOrigins: []string{"http://" + doc.ExternalController}}
	}
	doc.MixedPort, doc.BindAddress = 0, listener.Listen
	switch listener.Protocol {
	case "socks5":
		doc.SOCKSPort = listener.Port
	case "http":
		doc.HTTPPort = listener.Port
	case "mixed":
		doc.MixedPort = listener.Port
	}
	for _, p := range in.Policies {
		if p.Strategy == ir.PolicyFixed {
			continue
		}
		fail := func(path string) error {
			return ir.Diagnostics{adapter.CompileIssue(ir.CompileUnmappedField, path, in.TargetKey, p.ResourceID)}
		}
		group := policyGroup{Name: p.Tag, Proxies: p.Members, DisableUDP: true, EmptyFallback: "REJECT"}
		switch p.Strategy {
		case ir.PolicyManualSelect:
			if !controlled {
				return ir.Diagnostics{adapter.CompileIssue(ir.CapabilityUnsupported, "/payload/strategy", in.TargetKey, p.ResourceID)}
			}
			group.Type = "select"
		case ir.PolicyLatencyBest:
			group.Type = "url-test"
			group.Tolerance = p.Health.ToleranceMS
		case ir.PolicyRoundRobin:
			group.Type, group.Strategy = "load-balance", "round-robin"
			if !p.Health.Enabled {
				return fail("/payload/health_check/enabled")
			}
			if p.Health.ToleranceMS != nil && *p.Health.ToleranceMS != 0 {
				return fail("/payload/health_check/tolerance_ms")
			}
		default:
			return ir.Diagnostics{adapter.CompileIssue(ir.CapabilityUnsupported, "/payload/strategy", in.TargetKey, p.ResourceID)}
		}
		if p.Health.Enabled {
			if *p.Health.IntervalMS%1000 != 0 {
				return fail("/payload/health_check/interval_ms")
			}
			group.URL, group.Interval, group.Timeout = p.Health.URL, *p.Health.IntervalMS/1000, p.Health.TimeoutMS
		}
		doc.Groups = append(doc.Groups, group)
	}
	tag := func(value string) string {
		value = in.ResolvePolicyTag(value)
		if value == adapter.BlockTag {
			return "REJECT"
		}
		if value == "direct" {
			return "DIRECT"
		}
		return value
	}
	doc.Rules = nil
	if in.Routing != nil {
		for _, r := range in.Routing.Rules {
			condition := nativeCondition(r.Condition, in.Routing.Mode == ir.PreserveDomain)
			if strings.HasSuffix(condition, ",no-resolve") {
				doc.Rules = append(doc.Rules, strings.TrimSuffix(condition, ",no-resolve")+","+tag(r.Target)+",no-resolve")
			} else {
				doc.Rules = append(doc.Rules, condition+","+tag(r.Target))
			}
		}
	}
	exit, _ := in.ExitTag()
	doc.Rules = append(doc.Rules, "MATCH,"+tag(exit))
	return applyDNS(doc, in)
}

func nativeCondition(c adapter.Condition, noResolve bool) string {
	if c.Kind == "and" || c.Kind == "or" {
		terms := make([]string, 0, len(c.Terms))
		for _, term := range c.Terms {
			terms = append(terms, "("+nativeCondition(term, noResolve)+")")
		}
		if len(terms) == 1 {
			return nativeCondition(c.Terms[0], noResolve)
		}
		return strings.ToUpper(c.Kind) + ",(" + strings.Join(terms, ",") + ")"
	}
	terms := make([]string, 0, len(c.Values))
	for _, value := range c.Values {
		kind, suffix := "", ""
		switch c.Kind {
		case "domain":
			kind = "DOMAIN"
		case "domain_suffix":
			kind = "DOMAIN-SUFFIX"
		case "ip_cidr":
			kind = "IP-CIDR"
			if strings.Contains(value, ":") {
				kind = "IP-CIDR6"
			}
			if noResolve {
				suffix = ",no-resolve"
			}
		case "port":
			kind = "DST-PORT"
			value = strings.ReplaceAll(value, ":", "-")
		case "network":
			kind = "NETWORK"
			value = strings.ToUpper(value)
		}
		terms = append(terms, kind+","+value+suffix)
	}
	if len(terms) == 1 {
		return terms[0]
	}
	return "OR,((" + strings.Join(terms, "),(") + "))"
}
