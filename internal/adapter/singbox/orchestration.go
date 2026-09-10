package singbox

import (
	"net"
	"strconv"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type experimental struct {
	ClashAPI clashAPI `json:"clash_api"`
}
type clashAPI struct {
	ExternalController               string   `json:"external_controller"`
	AccessControlAllowOrigin         []string `json:"access_control_allow_origin"`
	AccessControlAllowPrivateNetwork bool     `json:"access_control_allow_private_network"`
}

func nativeCondition(c adapter.Condition) routeRule {
	r := routeRule{}
	switch c.Kind {
	case "and", "or":
		r.Type, r.Mode = "logical", c.Kind
		for _, term := range c.Terms {
			r.Rules = append(r.Rules, nativeCondition(term))
		}
	case "domain":
		r.Domain = c.Values
	case "domain_suffix":
		r.DomainSuffix = c.Values
	case "ip_cidr":
		r.IPCIDR = c.Values
	case "port":
		for _, v := range c.Values {
			if _, err := strconv.Atoi(v); err == nil {
				v = v + ":" + v
			}
			r.PortRange = append(r.PortRange, v)
		}
	case "network":
		r.Network = c.Values
	}
	return r
}

func conditionUsesIP(c adapter.Condition) bool {
	if c.Kind == "ip_cidr" {
		return true
	}
	for _, term := range c.Terms {
		if conditionUsesIP(term) {
			return true
		}
	}
	return false
}

func applyOrchestration(doc *document, in adapter.EmitInput) error {
	listener := in.Listener("socks5", ListenPort)
	protocol := listener.Protocol
	if protocol == "socks5" {
		protocol = "socks"
	}
	doc.Inbounds[0].Type, doc.Inbounds[0].Listen, doc.Inbounds[0].ListenPort = protocol, listener.Listen, listener.Port
	controlled := in.Preset != nil && in.Preset.ControlAPI.Enabled
	if controlled {
		address := net.JoinHostPort(in.Preset.ControlAPI.Listen, strconv.Itoa(in.Preset.ControlAPI.Port))
		doc.Experimental = &experimental{ClashAPI: clashAPI{ExternalController: address, AccessControlAllowOrigin: []string{"http://" + address}}}
	}
	for _, p := range in.Policies {
		if p.Strategy == ir.PolicyManualSelect && controlled {
			doc.Outbounds = append(doc.Outbounds, outbound{Type: "selector", Tag: p.Tag, Outbounds: p.Members, Default: p.Default})
		} else if p.Strategy != ir.PolicyFixed {
			return ir.Diagnostics{adapter.CompileIssue(ir.CapabilityUnsupported, "/payload/strategy", in.TargetKey, p.ResourceID)}
		}
	}
	if in.UsesTag("direct") {
		direct := outbound{Type: "direct", Tag: "direct"}
		if in.DNS != nil {
			direct.DomainResolver = in.DNS.Profile.FinalResolver
		}
		doc.Outbounds = append(doc.Outbounds, direct)
	}
	targetRule := func(rule routeRule, tag string) routeRule {
		tag = in.ResolvePolicyTag(tag)
		if tag == adapter.BlockTag {
			rule.Action = "reject"
		} else {
			rule.Outbound = tag
		}
		return rule
	}
	appendTarget := func(rule routeRule, tag string) {
		if in.DNS != nil && in.ResolvePolicyTag(tag) == "direct" {
			resolve := rule
			resolve.Action = "resolve"
			doc.Route.Rules = append(doc.Route.Rules, resolve)
		}
		doc.Route.Rules = append(doc.Route.Rules, targetRule(rule, tag))
	}
	doc.Route.Rules = nil
	if in.Routing != nil {
		for _, r := range in.Routing.Rules {
			// Pinned sing-box stops routing on resolve errors, whereas this
			// mode must continue to later rules when an IP lookup cannot match.
			if in.Routing.Mode == ir.ResolveForIPRules && conditionUsesIP(r.Condition) {
				return ir.Diagnostics{adapter.CompileIssue(ir.CompileUnmappedField, "/payload/domain_resolution_mode", in.TargetKey, in.Routing.ResourceID)}
			}
			appendTarget(nativeCondition(r.Condition), r.Target)
		}
	}
	exit, _ := in.ExitTag()
	exit = in.ResolvePolicyTag(exit)
	appendTarget(routeRule{Inbound: []string{adapter.InboundTag}}, exit)
	doc.Route.Final = exit
	if exit == adapter.BlockTag {
		doc.Route.Final = ""
	}
	return applyDNS(doc, in)
}
