package xray

import (
	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

type credentialUser struct {
	ID         string `json:"id,omitempty"`
	Encryption string `json:"encryption,omitempty"`
	Security   string `json:"security,omitempty"`
	Flow       string `json:"flow,omitempty"`
	User       string `json:"user,omitempty"`
	Pass       string `json:"pass,omitempty"`
}
type remoteServer struct {
	Address  string           `json:"address"`
	Port     int              `json:"port"`
	Method   string           `json:"method,omitempty"`
	Password string           `json:"password,omitempty"`
	Users    []credentialUser `json:"users,omitempty"`
}
type serverSettings struct {
	Servers []remoteServer `json:"servers"`
}
type vnextSettings struct {
	VNext []remoteServer `json:"vnext"`
}

func nodeOutbound(tag string, m adapter.MappedNode, dialer string) outbound {
	n := m.Node
	s := &stream{Network: "tcp", Security: "none"}
	if m.WS != nil {
		s.Network = "ws"
		s.WS = &wsConf{Path: m.WS.Path}
		if m.WS.Host != nil {
			s.WS.Headers = map[string]string{"Host": *m.WS.Host}
		}
	}
	if t := m.TLS; t != nil {
		s.Security = "tls"
		s.TLS = &tlsConf{ServerName: t.ServerName, AllowInsecure: !*t.VerifyCertificate, ALPN: t.ALPN}
		if t.ClientFingerprint != nil {
			s.TLS.Fingerprint = *t.ClientFingerprint
		}
	}
	if r := m.Reality; r != nil {
		s.Security = "reality"
		s.Reality = &realityConf{ServerName: r.ServerName, Fingerprint: r.ClientFingerprint, PublicKey: adapter.SecretBytes(r.PublicKey), ShortID: adapter.SecretBytes(r.ShortID)}
	}
	if dialer != "" {
		s.Sockopt = &sockopt{DialerProxy: dialer}
	}
	item := outbound{Protocol: string(n.Protocol), StreamSettings: s, Tag: tag}
	server := remoteServer{Address: n.Endpoint.Host, Port: n.Endpoint.Port}
	switch n.Protocol {
	case ir.Trojan:
		item.Settings = trojanSettings{Servers: []trojanServer{{Address: n.Endpoint.Host, Port: n.Endpoint.Port, Password: adapter.SecretBytes(m.Password)}}}
	case ir.Shadowsocks:
		server.Method, server.Password = m.Cipher, adapter.SecretBytes(m.Password)
		item.Settings = serverSettings{Servers: []remoteServer{server}}
	case ir.VMess, ir.VLESS:
		user := credentialUser{ID: adapter.SecretBytes(m.UUID), Flow: m.Flow}
		if n.Protocol == ir.VMess {
			user.Security = m.Cipher
		} else {
			user.Encryption = "none"
		}
		server.Users = []credentialUser{user}
		item.Settings = vnextSettings{VNext: []remoteServer{server}}
	case ir.SOCKS5, ir.HTTP:
		if n.Protocol == ir.SOCKS5 {
			item.Protocol = "socks"
		}
		if _, ok := n.Auth.(*ir.UsernamePasswordAuth); ok {
			server.Users = []credentialUser{{User: adapter.SecretBytes(m.Username), Pass: adapter.SecretBytes(m.Password)}}
		}
		item.Settings = serverSettings{Servers: []remoteServer{server}}
	}
	return item
}
