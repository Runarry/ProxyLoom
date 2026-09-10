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
	"domain_strategy",
	"network_strategy",
	"network_type",
	"fallback_network_type",
	"fallback_delay",
}

type document struct {
	Log          logConfig     `json:"log"`
	Inbounds     []inbound     `json:"inbounds"`
	Outbounds    []outbound    `json:"outbounds"`
	Route        route         `json:"route"`
	DNS          *dnsConfig    `json:"dns,omitempty"`
	Experimental *experimental `json:"experimental,omitempty"`
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
	Type           string       `json:"type"`
	Tag            string       `json:"tag"`
	Server         string       `json:"server,omitempty"`
	ServerPort     int          `json:"server_port,omitempty"`
	Password       string       `json:"password,omitempty"`
	Network        string       `json:"network,omitempty"`
	TLS            *tls         `json:"tls,omitempty"`
	Detour         string       `json:"detour,omitempty"`
	Method         string       `json:"method,omitempty"`
	UUID           string       `json:"uuid,omitempty"`
	Security       string       `json:"security,omitempty"`
	Flow           string       `json:"flow,omitempty"`
	Username       string       `json:"username,omitempty"`
	Version        string       `json:"version,omitempty"`
	Transport      *wsTransport `json:"transport,omitempty"`
	DomainResolver string       `json:"domain_resolver,omitempty"`
	Outbounds      []string     `json:"outbounds,omitempty"`
	Default        string       `json:"default,omitempty"`
}

type tls struct {
	Enabled    bool     `json:"enabled"`
	ServerName string   `json:"server_name"`
	Insecure   bool     `json:"insecure"`
	ALPN       []string `json:"alpn,omitempty"`
	UTLS       *utls    `json:"utls,omitempty"`
	Reality    *reality `json:"reality,omitempty"`
}

type wsTransport struct {
	Type    string            `json:"type"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
}
type reality struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id"`
}

type utls struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint"`
}

type route struct {
	Rules []routeRule `json:"rules"`
	Final string      `json:"final,omitempty"`
}

type routeRule struct {
	Inbound      []string    `json:"inbound,omitempty"`
	Outbound     string      `json:"outbound,omitempty"`
	Type         string      `json:"type,omitempty"`
	Mode         string      `json:"mode,omitempty"`
	Rules        []routeRule `json:"rules,omitempty"`
	Domain       []string    `json:"domain,omitempty"`
	DomainSuffix []string    `json:"domain_suffix,omitempty"`
	IPCIDR       []string    `json:"ip_cidr,omitempty"`
	PortRange    []string    `json:"port_range,omitempty"`
	Network      []string    `json:"network,omitempty"`
	Action       string      `json:"action,omitempty"`
}

func Emit(input adapter.EmitInput) (adapter.Artifact, []ir.Diagnostic, error) {
	exit, refs, err := input.OrderedOutboundRefs()
	if err != nil {
		return adapter.Artifact{}, asDiagnostics(err), err
	}
	outbounds := make([]outbound, 0, len(refs))
	for _, ref := range refs {
		mapped, mapErr := adapter.MapNode(ref.Resource, ref.FieldPath, input.TargetKey)
		if mapErr != nil {
			return adapter.Artifact{}, asDiagnostics(mapErr), mapErr
		}
		item := nodeOutbound(ref.Tag, mapped, ref.DialerTag)
		if input.DNS != nil && mapped.Node.NeedsBootstrap() {
			if len(input.DNS.Profile.Bootstrap) == 0 {
				d := adapter.CompileIssue(ir.CompileDialConflict, ref.FieldPath+"/endpoint/host", input.TargetKey, ref.Resource.Metadata.ResourceID)
				return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
			}
			item.DomainResolver = input.DNS.Profile.Bootstrap[0].ResolverID
		}
		if conflict := DialFieldConflict(item); conflict != "" {
			d := adapter.CompileIssue(ir.CompileDialConflict, ref.FieldPath+"/"+conflict, input.TargetKey, ref.Resource.Metadata.ResourceID)
			return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
		}
		outbounds = append(outbounds, item)
	}
	doc := document{
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
	}
	if err := applyOrchestration(&doc, input); err != nil {
		return adapter.Artifact{}, asDiagnostics(err), err
	}
	rules := len(doc.Route.Rules)
	if doc.DNS != nil {
		rules += len(doc.DNS.Rules)
	}
	if err := adapter.CheckNativeCounts(input, len(doc.Outbounds), rules); err != nil {
		return adapter.Artifact{}, asDiagnostics(err), err
	}
	payload, err := adapter.EncodeJSON(doc)
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
