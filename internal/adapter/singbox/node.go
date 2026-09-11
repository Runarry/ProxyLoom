package singbox

import (
	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func nodeOutbound(tag string, m adapter.MappedNode, dialer string) outbound {
	n := m.Node
	item := outbound{Type: string(n.Protocol), Tag: tag, Server: n.Endpoint.Host, ServerPort: n.Endpoint.Port, Detour: dialer}
	if n.Features.UDP != nil && !*n.Features.UDP && n.Protocol != ir.HTTP {
		item.Network = "tcp"
	}
	switch n.Protocol {
	case ir.Trojan:
		item.Password = adapter.SecretBytes(m.Password)
	case ir.Shadowsocks:
		item.Method, item.Password = m.Cipher, adapter.SecretBytes(m.Password)
	case ir.VMess:
		item.UUID, item.Security = adapter.SecretBytes(m.UUID), m.Cipher
	case ir.VLESS:
		item.UUID, item.Flow = adapter.SecretBytes(m.UUID), m.Flow
	case ir.SOCKS5, ir.HTTP:
		if n.Protocol == ir.SOCKS5 {
			item.Type, item.Version = "socks", "5"
		}
		item.Username, item.Password = adapter.SecretBytes(m.Username), adapter.SecretBytes(m.Password)
	}
	if m.WS != nil {
		item.Transport = &wsTransport{Type: "ws", Path: m.WS.Path}
		if m.WS.Host != nil {
			item.Transport.Headers = map[string]string{"Host": *m.WS.Host}
		}
	}
	if t := m.TLS; t != nil {
		item.TLS = &tls{Enabled: true, ServerName: t.ServerName, Insecure: !*t.VerifyCertificate, ALPN: t.ALPN}
		if t.ClientFingerprint != nil {
			item.TLS.UTLS = &utls{Enabled: true, Fingerprint: *t.ClientFingerprint}
		}
	}
	if r := m.Reality; r != nil {
		item.TLS = &tls{Enabled: true, ServerName: r.ServerName, ALPN: r.ALPN, UTLS: &utls{Enabled: true, Fingerprint: r.ClientFingerprint}, Reality: &reality{Enabled: true, PublicKey: adapter.SecretBytes(r.PublicKey), ShortID: adapter.SecretBytes(r.ShortID)}}
	}
	return item
}
