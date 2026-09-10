package xray

import (
	"bytes"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const (
	ContentType = adapter.JSONContentType
	ListenPort  = 17801
)

type document struct {
	Log         logConfig    `json:"log"`
	Inbounds    []inbound    `json:"inbounds"`
	Outbounds   []outbound   `json:"outbounds"`
	Routing     routing      `json:"routing"`
	DNS         *dnsConfig   `json:"dns,omitempty"`
	Observatory *observatory `json:"observatory,omitempty"`
}

type logConfig struct {
	LogLevel string `json:"loglevel"`
}

type inbound struct {
	Listen   string          `json:"listen"`
	Port     int             `json:"port"`
	Protocol string          `json:"protocol"`
	Settings inboundSettings `json:"settings"`
	Tag      string          `json:"tag"`
}

type inboundSettings struct {
	Auth string `json:"auth"`
	UDP  bool   `json:"udp"`
}

type outbound struct {
	Protocol       string  `json:"protocol"`
	Settings       any     `json:"settings,omitempty"`
	StreamSettings *stream `json:"streamSettings,omitempty"`
	Tag            string  `json:"tag"`
}

type trojanSettings struct {
	Servers []trojanServer `json:"servers"`
}

type trojanServer struct {
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Password string `json:"password"`
}

type stream struct {
	Network  string       `json:"network"`
	Security string       `json:"security"`
	TLS      *tlsConf     `json:"tlsSettings,omitempty"`
	WS       *wsConf      `json:"wsSettings,omitempty"`
	Reality  *realityConf `json:"realitySettings,omitempty"`
	Sockopt  *sockopt     `json:"sockopt,omitempty"`
}

type wsConf struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
}
type realityConf struct {
	ServerName  string `json:"serverName"`
	Fingerprint string `json:"fingerprint"`
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId"`
}

type tlsConf struct {
	ServerName    string   `json:"serverName"`
	AllowInsecure bool     `json:"allowInsecure"`
	ALPN          []string `json:"alpn,omitempty"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
}

type sockopt struct {
	DialerProxy    string `json:"dialerProxy"`
	DomainStrategy string `json:"domainStrategy,omitempty"`
}

type routing struct {
	DomainStrategy string        `json:"domainStrategy"`
	Rules          []routingRule `json:"rules"`
	Balancers      []balancer    `json:"balancers,omitempty"`
}

type routingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag"`
	OutboundTag string   `json:"outboundTag,omitempty"`
	BalancerTag string   `json:"balancerTag,omitempty"`
	Domain      []string `json:"domain,omitempty"`
	IP          []string `json:"ip,omitempty"`
	Port        string   `json:"port,omitempty"`
	Network     string   `json:"network,omitempty"`
}

func Emit(input adapter.EmitInput) (adapter.Artifact, []ir.Diagnostic, error) {
	exit, refs, err := input.OrderedOutboundRefs()
	if err != nil {
		return adapter.Artifact{}, asDiagnostics(err), err
	}
	outbounds := make([]outbound, 0, len(refs)+1)
	for _, ref := range refs {
		mapped, mapErr := adapter.MapNode(ref.Resource, ref.FieldPath, input.TargetKey)
		if mapErr != nil {
			return adapter.Artifact{}, asDiagnostics(mapErr), mapErr
		}
		if mapped.Reality != nil && len(mapped.Reality.ALPN) > 0 {
			d := adapter.CompileIssue(ir.CompileUnmappedField, ref.FieldPath+"/security/alpn", input.TargetKey, ref.Resource.Metadata.ResourceID)
			return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
		}
		item := nodeOutbound(ref.Tag, mapped, ref.DialerTag)
		if input.DNS != nil && ref.DialerTag != "" && mapped.Node.NeedsBootstrap() {
			local := len(input.DNS.Profile.Bootstrap) > 0 && input.DNS.Profile.Bootstrap[0].Kind == ir.DNSLocal
			for _, resolver := range input.DNS.Profile.Resolvers {
				if resolver.Kind != ir.DNSLocal {
					local = false
				}
			}
			if !local {
				d := adapter.CompileIssue(ir.CompileUnmappedField, ref.FieldPath+"/endpoint/host", input.TargetKey, ref.Resource.Metadata.ResourceID)
				return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
			}
			// Resolve before redirecting to H1. Xray's socket strategy uses
			// global DNS, so it is equivalent to bootstrap only when all local.
			item.StreamSettings.Sockopt.DomainStrategy = "ForceIP"
		}
		if ref.DialerTag != "" {
			if conflict := xrayDialConflict(item); conflict != "" {
				d := adapter.CompileIssue(ir.CompileDialConflict, ref.FieldPath+"/streamSettings/"+conflict, input.TargetKey, ref.Resource.Metadata.ResourceID)
				return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
			}
		}
		outbounds = append(outbounds, item)
	}
	outbounds = append(outbounds, outbound{Protocol: "blackhole", Tag: adapter.BlockTag})
	doc := document{
		Log: logConfig{LogLevel: "warning"},
		Inbounds: []inbound{{
			Listen:   adapter.LoopbackAddr,
			Port:     ListenPort,
			Protocol: "socks",
			Settings: inboundSettings{Auth: "noauth", UDP: false},
			Tag:      adapter.InboundTag,
		}},
		Outbounds: outbounds,
		Routing: routing{
			DomainStrategy: "AsIs",
			Rules: []routingRule{{
				Type:        "field",
				InboundTag:  []string{adapter.InboundTag},
				OutboundTag: exit,
			}},
		},
	}
	if err := applyOrchestration(&doc, input); err != nil {
		return adapter.Artifact{}, asDiagnostics(err), err
	}
	rules := len(doc.Routing.Rules)
	if doc.DNS != nil {
		rules += len(doc.DNS.Servers)
	}
	if err := adapter.CheckNativeCounts(input, len(doc.Outbounds)+len(doc.Routing.Balancers), rules); err != nil {
		return adapter.Artifact{}, asDiagnostics(err), err
	}
	payload, err := adapter.EncodeJSON(doc)
	if err != nil {
		d := adapter.CompileIssue(ir.InvalidSnapshot, "", input.TargetKey, "")
		return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
	if bytes.Contains(payload, []byte(`"proxySettings":`)) || (!input.UsesTag("direct") && bytes.Contains(payload, []byte(`"protocol":"freedom"`))) {
		d := adapter.CompileIssue(ir.CompileDialConflict, "/outbounds", input.TargetKey, "")
		return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
	return adapter.Artifact{
		SnapshotID:  input.SnapshotID,
		TargetKey:   input.TargetKey,
		ContentType: ContentType,
		Bytes:       payload,
	}, nil, nil
}

func trojanOutbound(tag string, mapped adapter.TrojanTLS, dialerTag string) outbound {
	streamSettings := &stream{
		Network:  "tcp",
		Security: "tls",
		TLS: &tlsConf{
			ServerName:    mapped.ServerName,
			AllowInsecure: !mapped.VerifyCert,
			ALPN:          mapped.ALPN,
			Fingerprint:   mapped.Fingerprint,
		},
	}
	if dialerTag != "" {
		streamSettings.Sockopt = &sockopt{DialerProxy: dialerTag}
	}
	return outbound{
		Protocol: "trojan",
		Settings: trojanSettings{Servers: []trojanServer{{
			Address:  mapped.Endpoint.Host,
			Port:     mapped.Endpoint.Port,
			Password: adapter.SecretBytes(mapped.Password),
		}}},
		StreamSettings: streamSettings,
		Tag:            tag,
	}
}

func xrayDialConflict(item outbound) string {
	if item.StreamSettings == nil || item.StreamSettings.Sockopt == nil || item.StreamSettings.Sockopt.DialerProxy == "" {
		return ""
	}
	raw, err := adapter.EncodeJSON(item)
	if err != nil {
		return "sockopt"
	}
	if bytes.Contains(raw, []byte(`"proxySettings"`)) {
		return "proxySettings"
	}
	return ""
}

func asDiagnostics(err error) []ir.Diagnostic {
	if err == nil {
		return nil
	}
	if as, ok := err.(ir.Diagnostics); ok {
		return as
	}
	return []ir.Diagnostic{adapter.CompileIssue(ir.InvalidSnapshot, "", "", "")}
}
