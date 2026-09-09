package importparse

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

func EncodeURI(name string, node ir.Node) (string, error) {
	if node.Validate() != nil || !safeText(name) {
		return "", fmt.Errorf("export_unrepresentable")
	}
	if node.Features.UDP != nil || node.Features.Multiplex != nil {
		return "", fmt.Errorf("export_unrepresentable")
	}
	switch node.Protocol {
	case ir.Shadowsocks:
		return encodeShadowsocks(name, node)
	case ir.VMess:
		return encodeVMessURI(name, node)
	case ir.VLESS, ir.Trojan, ir.SOCKS5, ir.HTTP:
		return encodeURLNode(name, node)
	default:
		return "", fmt.Errorf("export_unrepresentable")
	}
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
