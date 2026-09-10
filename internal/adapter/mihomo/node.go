package mihomo

import (
	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func nodeProxy(tag string, m adapter.MappedNode, dialer string) proxy {
	n := m.Node
	item := proxy{Name: tag, Type: string(n.Protocol), Server: n.Endpoint.Host, Port: n.Endpoint.Port, DialerProxy: dialer, UDP: n.Features.UDP}
	switch n.Protocol {
	case ir.Trojan:
		item.Password = adapter.SecretBytes(m.Password)
		if n.Features.UDP != nil && !*n.Features.UDP {
			item.Network = "tcp"
		}
	case ir.Shadowsocks:
		item.Type, item.Cipher, item.Password = "ss", m.Cipher, adapter.SecretBytes(m.Password)
	case ir.VMess:
		zero := 0
		item.UUID, item.Cipher, item.AlterID = adapter.SecretBytes(m.UUID), m.Cipher, &zero
	case ir.VLESS:
		item.UUID, item.Flow = adapter.SecretBytes(m.UUID), m.Flow
	case ir.SOCKS5, ir.HTTP:
		item.Username, item.Password = adapter.SecretBytes(m.Username), adapter.SecretBytes(m.Password)
	}
	if m.WS != nil {
		item.Network, item.WS = "ws", &wsOptions{Path: m.WS.Path}
		if m.WS.Host != nil {
			item.WS.Headers = map[string]string{"Host": *m.WS.Host}
		}
	}
	if t := m.TLS; t != nil {
		item.ALPN, item.SkipCertVerify = t.ALPN, !*t.VerifyCertificate
		if n.Protocol == ir.VMess || n.Protocol == ir.VLESS {
			item.ServerName = t.ServerName
		} else {
			item.SNI = t.ServerName
		}
		if n.Protocol != ir.Trojan {
			enabled := true
			item.TLS = &enabled
		}
		if t.ClientFingerprint != nil {
			item.ClientFingerprint = *t.ClientFingerprint
		}
	}
	if r := m.Reality; r != nil {
		enabled := true
		item.TLS, item.ServerName, item.ALPN, item.ClientFingerprint = &enabled, r.ServerName, r.ALPN, r.ClientFingerprint
		item.Reality = &realityOptions{PublicKey: adapter.SecretBytes(r.PublicKey), ShortID: adapter.SecretBytes(r.ShortID)}
	}
	return item
}
