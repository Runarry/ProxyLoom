package importparse

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

// ExportError reports a field without retaining any secret-bearing values.
type ExportError struct{ FieldPath string }

func (*ExportError) Error() string { return "export_unrepresentable" }

func EncodeURI(name string, node ir.Node) (string, error) {
	if node.Validate() != nil {
		return "", &ExportError{FieldPath: "/node"}
	}
	if !safeText(name) {
		return "", &ExportError{FieldPath: "/name"}
	}
	if node.Features.UDP != nil {
		return "", &ExportError{FieldPath: "/node/features/udp"}
	}
	if node.Features.Multiplex != nil {
		return "", &ExportError{FieldPath: "/node/features/multiplex"}
	}
	if security, ok := node.Security.(*ir.TLSSecurity); ok && security.VerifyCertificate != nil && !*security.VerifyCertificate {
		return "", &ExportError{FieldPath: "/node/security/verify_certificate"}
	}
	var uri string
	var err error
	switch node.Protocol {
	case ir.Shadowsocks:
		uri, err = encodeShadowsocks(name, node)
	case ir.VMess:
		uri, err = encodeVMessURI(name, node)
	case ir.VLESS, ir.Trojan, ir.SOCKS5, ir.HTTP:
		uri, err = encodeURLNode(name, node)
	default:
		return "", &ExportError{FieldPath: "/node/protocol"}
	}
	if err != nil {
		return "", &ExportError{FieldPath: "/node"}
	}
	// Check against the supported import dialect, so no encoded field can be
	// silently discarded. Origin is local bookkeeping, not connection data.
	parsed := ParseURI(uri)
	if !parsed.Valid() {
		path := "/node"
		if len(parsed.Diagnostics) > 0 && parsed.Diagnostics[0].FieldPath != "" {
			path += parsed.Diagnostics[0].FieldPath
		}
		return "", &ExportError{FieldPath: path}
	}
	if parsed.Name != name {
		return "", &ExportError{FieldPath: "/name"}
	}
	for _, field := range []struct {
		path          string
		before, after any
	}{
		{"/node/protocol", node.Protocol, parsed.Node.Protocol},
		{"/node/endpoint", node.Endpoint, parsed.Node.Endpoint},
		{"/node/auth", node.Auth, parsed.Node.Auth},
		{"/node/transport", node.Transport, parsed.Node.Transport},
		{"/node/security", node.Security, parsed.Node.Security},
		{"/node/features", node.Features, parsed.Node.Features},
	} {
		if !reflect.DeepEqual(field.before, field.after) {
			return "", &ExportError{FieldPath: field.path}
		}
	}
	return uri, nil
}

func encodeShadowsocks(name string, node ir.Node) (string, error) {
	auth, ok := node.Auth.(*ir.MethodPasswordAuth)
	if !ok {
		return "", fmt.Errorf("export_unrepresentable")
	}
	if _, tcp := node.Transport.(*ir.NativeTCPTransport); !tcp {
		return "", fmt.Errorf("export_unrepresentable")
	}
	if _, ok := node.Security.(*ir.NoSecurity); !ok {
		return "", fmt.Errorf("export_unrepresentable")
	}
	userinfo := base64.RawURLEncoding.EncodeToString([]byte(string(auth.Method) + ":" + string(auth.Password)))
	return "ss://" + userinfo + "@" + hostPort(node.Endpoint) + encodeFragment(name), nil
}

func encodeVMessURI(name string, node ir.Node) (string, error) {
	auth, ok := node.Auth.(*ir.VMessAuth)
	if !ok {
		return "", fmt.Errorf("export_unrepresentable")
	}
	network, security, extra, err := shareQuery(node, false)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"v": "2", "ps": name, "add": node.Endpoint.Host, "port": strconv.Itoa(node.Endpoint.Port),
		"id": string(auth.UUID), "aid": "0", "scy": string(auth.Cipher), "net": network, "type": "none", "tls": security,
	}
	for _, key := range []string{"host", "path", "sni", "alpn", "fp"} {
		if extra[key] != "" {
			body[key] = extra[key]
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("export_unrepresentable")
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(encoded), nil
}

func encodeURLNode(name string, node ir.Node) (string, error) {
	scheme := string(node.Protocol)
	userinfo, err := urlUserinfo(node)
	if err != nil {
		return "", err
	}
	if node.Protocol == ir.HTTP {
		if _, tls := node.Security.(*ir.TLSSecurity); tls {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	query, err := encodeShareQuery(node)
	if err != nil {
		return "", err
	}
	return scheme + "://" + userinfo + hostPort(node.Endpoint) + query + encodeFragment(name), nil
}

func urlUserinfo(node ir.Node) (string, error) {
	switch auth := node.Auth.(type) {
	case *ir.UUIDAuth:
		return url.User(string(auth.UUID)).String() + "@", nil
	case *ir.PasswordAuth:
		return url.User(string(auth.Password)).String() + "@", nil
	case *ir.UsernamePasswordAuth:
		return url.UserPassword(string(auth.Username), string(auth.Password)).String() + "@", nil
	case *ir.NoAuth:
		if node.Protocol != ir.SOCKS5 && node.Protocol != ir.HTTP {
			return "", fmt.Errorf("export_unrepresentable")
		}
		return "", nil
	default:
		return "", fmt.Errorf("export_unrepresentable")
	}
}

func encodeShareQuery(node ir.Node) (string, error) {
	_, _, extra, err := shareQuery(node, true)
	if err != nil {
		return "", err
	}
	if node.Protocol == ir.VLESS {
		if node.Features.ProtocolVariant != nil {
			extra["flow"] = string(*node.Features.ProtocolVariant)
		}
	} else if node.Features.ProtocolVariant != nil {
		return "", fmt.Errorf("export_unrepresentable")
	}
	if len(extra) == 0 {
		return "", nil
	}
	keys := make([]string, 0, len(extra))
	for key := range extra {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, url.QueryEscape(key)+"="+url.QueryEscape(extra[key]))
	}
	return "?" + strings.Join(parts, "&"), nil
}

func shareQuery(node ir.Node, urlNode bool) (network, security string, extra map[string]string, err error) {
	extra = map[string]string{}
	switch transport := node.Transport.(type) {
	case *ir.NativeTCPTransport:
		network = "tcp"
		if urlNode {
			extra["type"] = "tcp"
		}
	case *ir.WebSocketTransport:
		network = "ws"
		extra["type"] = "ws"
		if transport.Path != "" && transport.Path != "/" {
			extra["path"] = transport.Path
		} else if transport.Path == "/" {
			extra["path"] = "/"
		}
		if transport.Host != nil {
			extra["host"] = *transport.Host
		}
	default:
		return "", "", nil, fmt.Errorf("export_unrepresentable")
	}
	switch sec := node.Security.(type) {
	case *ir.NoSecurity:
		security = "none"
		if node.Protocol == ir.Trojan {
			return "", "", nil, fmt.Errorf("export_unrepresentable")
		}
		if urlNode && node.Protocol != ir.HTTP && node.Protocol != ir.SOCKS5 {
			extra["security"] = "none"
		}
	case *ir.TLSSecurity:
		security = "tls"
		if sec.VerifyCertificate != nil && !*sec.VerifyCertificate {
			return "", "", nil, fmt.Errorf("export_unrepresentable")
		}
		if urlNode || node.Protocol == ir.VMess {
			if node.Protocol != ir.HTTP {
				extra["security"] = "tls"
			}
			if sec.ServerName != "" && sec.ServerName != node.Endpoint.Host {
				extra["sni"] = sec.ServerName
			}
			if len(sec.ALPN) > 0 {
				extra["alpn"] = strings.Join(sec.ALPN, ",")
			}
			if sec.ClientFingerprint != nil && *sec.ClientFingerprint != "" {
				extra["fp"] = *sec.ClientFingerprint
			}
		}
	case *ir.RealitySecurity:
		security = "reality"
		extra["security"] = "reality"
		extra["sni"] = sec.ServerName
		extra["pbk"] = string(sec.PublicKey)
		extra["sid"] = string(sec.ShortID)
		extra["fp"] = sec.ClientFingerprint
		if len(sec.ALPN) > 0 {
			extra["alpn"] = strings.Join(sec.ALPN, ",")
		}
	default:
		return "", "", nil, fmt.Errorf("export_unrepresentable")
	}
	if urlNode && network == "tcp" && extra["type"] == "tcp" {
		if node.Protocol == ir.SOCKS5 || node.Protocol == ir.HTTP {
			delete(extra, "type")
		}
	}
	return network, security, extra, nil
}

func hostPort(endpoint ir.Endpoint) string {
	host := endpoint.Host
	if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() {
		host = "[" + addr.String() + "]"
	}
	return host + ":" + strconv.Itoa(endpoint.Port)
}

func encodeFragment(name string) string {
	if name == "" {
		return ""
	}
	return "#" + url.PathEscape(name)
}
