package singbox

import (
	"encoding/json"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

const (
	ContentType = adapter.JSONContentType
	ListenPort  = 17802
)

var dialConflictFields = []string{
	"bind_interface",
	"inet4_bind_address",
	"inet6_bind_address",
	"routing_mark",
	"reuse_addr",
	"netns",
	"connect_timeout",
	"tcp_fast_open",
	"tcp_multi_path",
	"udp_fragment",
	"domain_resolver",
	"domain_strategy",
	"network_strategy",
	"network_type",
	"fallback_network_type",
	"fallback_delay",
}

type document struct {
	Log       logConfig  `json:"log"`
	Inbounds  []inbound  `json:"inbounds"`
	Outbounds []outbound `json:"outbounds"`
	Route     route      `json:"route"`
}

type logConfig struct {
	Level     string `json:"level"`
	Timestamp bool   `json:"timestamp"`
}

type inbound struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
}

type outbound struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Server     string `json:"server,omitempty"`
	ServerPort int    `json:"server_port,omitempty"`
	Password   string `json:"password,omitempty"`
	Network    string `json:"network,omitempty"`
	TLS        *tls   `json:"tls,omitempty"`
	Detour     string `json:"detour,omitempty"`
}

type tls struct {
	Enabled    bool     `json:"enabled"`
	ServerName string   `json:"server_name"`
	Insecure   bool     `json:"insecure"`
	ALPN       []string `json:"alpn,omitempty"`
	UTLS       *utls    `json:"utls,omitempty"`
}

type utls struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint"`
}

type route struct {
	Rules []routeRule `json:"rules"`
	Final string      `json:"final"`
}

type routeRule struct {
	Inbound  []string `json:"inbound"`
	Outbound string   `json:"outbound"`
}

func Emit(input adapter.EmitInput) (adapter.Artifact, []ir.Diagnostic, error) {
	exit, refs, err := input.OrderedOutboundRefs()
	if err != nil {
		return adapter.Artifact{}, asDiagnostics(err), err
	}
	outbounds := make([]outbound, 0, len(refs))
	for _, ref := range refs {
		mapped, mapErr := adapter.MapTrojanNativeTLS(ref.Resource, ref.FieldPath, input.TargetKey)
		if mapErr != nil {
			return adapter.Artifact{}, asDiagnostics(mapErr), mapErr
		}
		item := trojanOutbound(ref.Tag, mapped, ref.DialerTag)
		if conflict := DialFieldConflict(item); conflict != "" {
			d := adapter.CompileIssue(ir.CompileDialConflict, ref.FieldPath+"/"+conflict, input.TargetKey, ref.Resource.Metadata.ResourceID)
			return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
		}
		outbounds = append(outbounds, item)
	}
	payload, err := adapter.EncodeJSON(document{
		Log: logConfig{Level: "warning", Timestamp: false},
		Inbounds: []inbound{{
			Type:       "socks",
			Tag:        adapter.InboundTag,
			Listen:     adapter.LoopbackAddr,
			ListenPort: ListenPort,
		}},
		Outbounds: outbounds,
		Route: route{
			Rules: []routeRule{{Inbound: []string{adapter.InboundTag}, Outbound: exit}},
			Final: exit,
		},
	})
	if err != nil {
		d := adapter.CompileIssue(ir.InvalidSnapshot, "", input.TargetKey, "")
		return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
	return adapter.Artifact{
		SnapshotID:  input.SnapshotID,
		TargetKey:   input.TargetKey,
		ContentType: ContentType,
		Bytes:       payload,
	}, nil, nil
}

func trojanOutbound(tag string, mapped adapter.TrojanTLS, detour string) outbound {
	item := outbound{
		Type:       "trojan",
		Tag:        tag,
		Server:     mapped.Endpoint.Host,
		ServerPort: mapped.Endpoint.Port,
		Password:   adapter.SecretBytes(mapped.Password),
		TLS: &tls{
			Enabled:    true,
			ServerName: mapped.ServerName,
			Insecure:   !mapped.VerifyCert,
			ALPN:       mapped.ALPN,
		},
		Detour: detour,
	}
	if mapped.UDPFalse {
		item.Network = "tcp"
	}
	if mapped.Fingerprint != "" {
		item.TLS.UTLS = &utls{Enabled: true, Fingerprint: mapped.Fingerprint}
	}
	return item
}

func DialFieldConflict(item outbound) string {
	raw, err := json.Marshal(item)
	if err != nil {
		return "detour"
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return "detour"
	}
	return MapDialFieldConflict(object)
}

func MapDialFieldConflict(object map[string]any) string {
	detour, _ := object["detour"].(string)
	if detour == "" {
		return ""
	}
	for _, field := range dialConflictFields {
		if _, ok := object[field]; ok {
			return field
		}
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
