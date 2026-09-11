package adapter

import "github.com/Runarry/ProxyLoom/internal/ir"

type PolicyInstance struct {
	Tag        string               `json:"tag"`
	ResourceID ir.ID                `json:"resource_id"`
	Strategy   ir.PolicyStrategy    `json:"strategy"`
	Members    []string             `json:"members"`
	Default    string               `json:"default"`
	Health     ir.PolicyHealthCheck `json:"health_check"`
}

// Conditions use AND across Terms and OR across Values; a rule-set is an OR
// expression whose entries may mix domain and CIDR predicates.
type Condition struct {
	Kind   string      `json:"kind"`
	Values []string    `json:"values,omitempty"`
	Terms  []Condition `json:"terms,omitempty"`
}
type RouteRule struct {
	Condition Condition `json:"condition"`
	Target    string    `json:"target"`
	FieldPath string    `json:"field_path"`
}
type RoutingInput struct {
	ResourceID ir.ID                   `json:"resource_id"`
	Mode       ir.DomainResolutionMode `json:"domain_resolution_mode"`
	Rules      []RouteRule             `json:"rules"`
}
type DNSInput struct {
	ResourceID   ir.ID             `json:"resource_id"`
	Profile      ir.DNSProfile     `json:"profile"`
	OutboundTags map[string]string `json:"outbound_tags"`
	Rules        []RouteRule       `json:"rules"`
}

func (in EmitInput) UsesTag(tag string) bool {
	if exit, _ := in.ExitTag(); exit == tag {
		return true
	}
	for _, p := range in.Policies {
		for _, member := range p.Members {
			if member == tag {
				return true
			}
		}
	}
	if in.Routing != nil {
		for _, rule := range in.Routing.Rules {
			if rule.Target == tag {
				return true
			}
		}
	}
	if in.DNS != nil {
		for _, outbound := range in.DNS.OutboundTags {
			if outbound == tag {
				return true
			}
		}
	}
	return false
}

func (in EmitInput) Listener(defaultProtocol string, defaultPort int) ir.LocalListener {
	if in.Preset != nil {
		return in.Preset.LocalListener
	}
	return ir.LocalListener{Protocol: defaultProtocol, Listen: LoopbackAddr, Port: defaultPort}
}

func (in EmitInput) ResolvePolicyTag(tag string) string {
	for _, policy := range in.Policies {
		if policy.Tag == tag && policy.Strategy == ir.PolicyFixed {
			return policy.Default
		}
	}
	return tag
}

// CommonLocalDNS is the exact common subset for families whose native DNS
// priority/transport behavior cannot express the complete profile contract.
func (in EmitInput) CommonLocalDNS() error {
	if in.DNS == nil {
		return nil
	}
	p := in.DNS.Profile
	path := ""
	if len(p.Bootstrap) != 1 || p.Bootstrap[0].Kind != ir.DNSLocal {
		path = "/payload/bootstrap"
	}
	if len(p.Resolvers) != 1 || p.Resolvers[0].Kind != ir.DNSLocal {
		path = "/payload/resolvers"
	}
	if len(in.DNS.Rules) > 0 {
		path = in.DNS.Rules[0].FieldPath
	}
	if path != "" {
		return ir.Diagnostics{CompileIssue(ir.CompileUnmappedField, path, in.TargetKey, in.DNS.ResourceID)}
	}
	return nil
}
