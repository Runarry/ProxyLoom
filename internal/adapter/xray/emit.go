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
	Log       logConfig  `json:"log"`
	Inbounds  []inbound  `json:"inbounds"`
	Outbounds []outbound `json:"outbounds"`
	Routing   routing    `json:"routing"`
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
	Network  string   `json:"network"`
	Security string   `json:"security"`
	TLS      tlsConf  `json:"tlsSettings"`
	Sockopt  *sockopt `json:"sockopt,omitempty"`
}

type tlsConf struct {
	ServerName    string   `json:"serverName"`
	AllowInsecure bool     `json:"allowInsecure"`
	ALPN          []string `json:"alpn,omitempty"`
	Fingerprint   string   `json:"fingerprint,omitempty"`
}

type sockopt struct {
	DialerProxy string `json:"dialerProxy"`
}

type routing struct {
	DomainStrategy string        `json:"domainStrategy"`
	Rules          []routingRule `json:"rules"`
}

type routingRule struct {
	Type        string   `json:"type"`
	InboundTag  []string `json:"inboundTag"`
	OutboundTag string   `json:"outboundTag"`
}

func Emit(input adapter.EmitInput) (adapter.Artifact, []ir.Diagnostic, error) {
	exit, refs, err := input.OrderedOutboundRefs()
	if err != nil {
		return adapter.Artifact{}, asDiagnostics(err), err
	}
	outbounds := make([]outbound, 0, len(refs)+1)
	for _, ref := range refs {
		mapped, mapErr := adapter.MapTrojanNativeTLS(ref.Resource, ref.FieldPath, input.TargetKey)
		if mapErr != nil {
			return adapter.Artifact{}, asDiagnostics(mapErr), mapErr
		}
		item := trojanOutbound(ref.Tag, mapped, ref.DialerTag)
		if ref.DialerTag != "" {
			if conflict := xrayDialConflict(item); conflict != "" {
				d := adapter.CompileIssue(ir.CompileDialConflict, ref.FieldPath+"/streamSettings/"+conflict, input.TargetKey, ref.Resource.Metadata.ResourceID)
				return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
			}
		}
		outbounds = append(outbounds, item)
	}
	outbounds = append(outbounds, outbound{Protocol: "blackhole", Tag: adapter.BlockTag})
	payload, err := adapter.EncodeJSON(document{
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
	})
	if err != nil {
		d := adapter.CompileIssue(ir.InvalidSnapshot, "", input.TargetKey, "")
		return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
	if bytes.Contains(payload, []byte(`"proxySettings"`)) || bytes.Contains(payload, []byte(`"freedom"`)) {
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
		TLS: tlsConf{
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
