package mihomo

import (
	"bytes"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"gopkg.in/yaml.v3"
)

const (
	ContentType = adapter.YAMLContentType
	MixedPort   = 17803
)

type document struct {
	MixedPort          int      `yaml:"mixed-port"`
	BindAddress        string   `yaml:"bind-address"`
	AllowLAN           bool     `yaml:"allow-lan"`
	Mode               string   `yaml:"mode"`
	LogLevel           string   `yaml:"log-level"`
	ExternalController string   `yaml:"external-controller"`
	IPv6               bool     `yaml:"ipv6"`
	GeodataMode        bool     `yaml:"geodata-mode"`
	GeoAutoUpdate      bool     `yaml:"geo-auto-update"`
	FindProcessMode    string   `yaml:"find-process-mode"`
	Proxies            []proxy  `yaml:"proxies"`
	Rules              []string `yaml:"rules"`
}

type proxy struct {
	Name              string   `yaml:"name"`
	Type              string   `yaml:"type"`
	Server            string   `yaml:"server"`
	Port              int      `yaml:"port"`
	Password          string   `yaml:"password"`
	Network           string   `yaml:"network,omitempty"`
	SNI               string   `yaml:"sni"`
	ALPN              []string `yaml:"alpn,omitempty"`
	SkipCertVerify    bool     `yaml:"skip-cert-verify"`
	UDP               *bool    `yaml:"udp,omitempty"`
	ClientFingerprint string   `yaml:"client-fingerprint,omitempty"`
	DialerProxy       string   `yaml:"dialer-proxy,omitempty"`
}

func Emit(input adapter.EmitInput) (adapter.Artifact, []ir.Diagnostic, error) {
	exit, refs, err := input.OrderedOutboundRefs()
	if err != nil {
		return adapter.Artifact{}, asDiagnostics(err), err
	}
	proxies := make([]proxy, 0, len(refs))
	for _, ref := range refs {
		mapped, mapErr := adapter.MapTrojanNativeTLS(ref.Resource, ref.FieldPath, input.TargetKey)
		if mapErr != nil {
			return adapter.Artifact{}, asDiagnostics(mapErr), mapErr
		}
		item := trojanProxy(ref.Tag, mapped, ref.DialerTag)
		proxies = append(proxies, item)
	}
	doc := document{
		MixedPort:          MixedPort,
		BindAddress:        adapter.LoopbackAddr,
		AllowLAN:           false,
		Mode:               "rule",
		LogLevel:           "silent",
		ExternalController: "",
		IPv6:               false,
		GeodataMode:        false,
		GeoAutoUpdate:      false,
		FindProcessMode:    "off",
		Proxies:            proxies,
		Rules:              []string{"MATCH," + exit},
	}
	if implicitDirect(doc) {
		d := adapter.CompileIssue(ir.CompileDialConflict, "/rules", input.TargetKey, "")
		return adapter.Artifact{}, []ir.Diagnostic{d}, ir.Diagnostics{d}
	}
	payload, err := marshalYAML(doc)
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

func trojanProxy(tag string, mapped adapter.TrojanTLS, dialer string) proxy {
	item := proxy{
		Name:           tag,
		Type:           "trojan",
		Server:         mapped.Endpoint.Host,
		Port:           mapped.Endpoint.Port,
		Password:       adapter.SecretBytes(mapped.Password),
		SNI:            mapped.ServerName,
		ALPN:           mapped.ALPN,
		SkipCertVerify: !mapped.VerifyCert,
		DialerProxy:    dialer,
	}
	if mapped.UDPFalse {
		network := "tcp"
		udp := false
		item.Network = network
		item.UDP = &udp
	}
	if mapped.Fingerprint != "" {
		item.ClientFingerprint = mapped.Fingerprint
	}
	return item
}

func implicitDirect(doc document) bool {
	if doc.Mode != "rule" || doc.GeoAutoUpdate || doc.GeodataMode {
		return true
	}
	if len(doc.Rules) == 0 {
		return true
	}
	for _, rule := range doc.Rules {
		upper := strings.ToUpper(rule)
		if strings.Contains(upper, ",DIRECT") || strings.HasSuffix(upper, ",DIRECT") || upper == "DIRECT" {
			return true
		}
	}
	return false
}

func marshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	out := buf.Bytes()
	if bytes.HasPrefix(out, []byte("---\n")) {
		out = out[4:]
	}
	return out, nil
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
