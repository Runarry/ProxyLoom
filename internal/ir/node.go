// Package ir defines the typed, versioned compile input. It contains no database,
// network, current-time or native-core execution dependencies.
package ir

import (
	"encoding/json"
	"fmt"
	"log/slog"
)

const SchemaVersion = 1

type ID string
type Protocol string
type AuthKind string
type TransportKind string
type SecurityMode string
type ShadowsocksMethod string
type VMessCipher string
type ProtocolVariant string

const (
	Shadowsocks           Protocol          = "shadowsocks"
	VMess                 Protocol          = "vmess"
	VLESS                 Protocol          = "vless"
	Trojan                Protocol          = "trojan"
	SOCKS5                Protocol          = "socks5"
	HTTP                  Protocol          = "http"
	AuthMethodPassword    AuthKind          = "method_password"
	AuthVMessAEAD         AuthKind          = "vmess_aead"
	AuthUUID              AuthKind          = "uuid"
	AuthPassword          AuthKind          = "password"
	AuthUsernamePassword  AuthKind          = "username_password"
	AuthNone              AuthKind          = "none"
	NativeTCP             TransportKind     = "native_tcp"
	WebSocket             TransportKind     = "websocket"
	SecurityNone          SecurityMode      = "none"
	TLS                   SecurityMode      = "tls"
	Reality               SecurityMode      = "reality"
	AES128GCM             ShadowsocksMethod = "aes-128-gcm"
	AES256GCM             ShadowsocksMethod = "aes-256-gcm"
	ChaCha20IETFPoly1305  ShadowsocksMethod = "chacha20-ietf-poly1305"
	VMessAuto             VMessCipher       = "auto"
	VMessAES128GCM        VMessCipher       = "aes-128-gcm"
	VMessChaCha20Poly1305 VMessCipher       = "chacha20-poly1305"
	VMessNone             VMessCipher       = "none"
	VMessZero             VMessCipher       = "zero"
	XTLSVision            ProtocolVariant   = "xtls-rprx-vision"
)

// Secret deliberately serializes to its actual value in the compile IR. It is
// redacted by fmt and slog, but is not an encryption container or API patch DTO.
type Secret string

func (Secret) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func (Secret) String() string                 { return "[REDACTED]" }
func (Secret) GoString() string               { return "[REDACTED]" }
func (Secret) LogValue() slog.Value           { return slog.StringValue("[REDACTED]") }

type Endpoint struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// The unexported marker restricts ordinary callers to known typed variants;
// Node.Validate also checks exact dynamic types, including typed nil pointers.
type Authentication interface {
	authentication()
	Validate() error
}

type MethodPasswordAuth struct {
	Kind     AuthKind          `json:"kind"`
	Method   ShadowsocksMethod `json:"method"`
	Password Secret            `json:"password"`
}
type VMessAuth struct {
	Kind   AuthKind    `json:"kind"`
	UUID   Secret      `json:"uuid"`
	Cipher VMessCipher `json:"cipher"`
}
type UUIDAuth struct {
	Kind AuthKind `json:"kind"`
	UUID Secret   `json:"uuid"`
}
type PasswordAuth struct {
	Kind     AuthKind `json:"kind"`
	Password Secret   `json:"password"`
}
type UsernamePasswordAuth struct {
	Kind     AuthKind `json:"kind"`
	Username Secret   `json:"username"`
	Password Secret   `json:"password"`
}
type NoAuth struct {
	Kind AuthKind `json:"kind"`
}

func (*MethodPasswordAuth) authentication()   {}
func (*VMessAuth) authentication()            {}
func (*UUIDAuth) authentication()             {}
func (*PasswordAuth) authentication()         {}
func (*UsernamePasswordAuth) authentication() {}
func (*NoAuth) authentication()               {}

type Transport interface {
	transport()
	Validate() error
}
type NativeTCPTransport struct {
	Kind TransportKind `json:"kind"`
}
type WebSocketTransport struct {
	Kind TransportKind `json:"kind"`
	Path string        `json:"path"`
	Host *string       `json:"host,omitempty"`
}

func (*NativeTCPTransport) transport() {}
func (*WebSocketTransport) transport() {}

type Security interface {
	security()
	Validate() error
}
type NoSecurity struct {
	Mode SecurityMode `json:"mode"`
}
type TLSSecurity struct {
	Mode              SecurityMode `json:"mode"`
	ServerName        string       `json:"server_name"`
	VerifyCertificate *bool        `json:"verify_certificate"`
	ALPN              []string     `json:"alpn,omitempty"`
	ClientFingerprint *string      `json:"client_fingerprint,omitempty"`
}
type RealitySecurity struct {
	Mode              SecurityMode `json:"mode"`
	ServerName        string       `json:"server_name"`
	PublicKey         Secret       `json:"public_key"`
	ShortID           Secret       `json:"short_id"`
	ClientFingerprint string       `json:"client_fingerprint"`
	ALPN              []string     `json:"alpn,omitempty"`
}

func (*NoSecurity) security()      {}
func (*TLSSecurity) security()     {}
func (*RealitySecurity) security() {}

type Features struct {
	UDP             *bool            `json:"udp,omitempty"`
	Multiplex       *bool            `json:"multiplex,omitempty"`
	ProtocolVariant *ProtocolVariant `json:"protocol_variant,omitempty"`
}

// Extensions has an empty v1 whitelist. Native configuration maps are not IR.
type Extensions struct{}

type MatchMethod string

const (
	StableExternalKey MatchMethod = "stable_external_key"
	ManualBinding     MatchMethod = "manual_binding"
	ExactFingerprint  MatchMethod = "exact_fingerprint"
)

type Origin struct {
	SourceResourceID ID          `json:"source_resource_id"`
	SourceItemID     ID          `json:"source_item_id"`
	MatchMethod      MatchMethod `json:"match_method"`
}

// Node deliberately contains no chain, detour, dialerProxy, or dialer-proxy field.
type Node struct {
	SchemaVersion int            `json:"schema_version"`
	Protocol      Protocol       `json:"protocol"`
	Endpoint      Endpoint       `json:"endpoint"`
	Auth          Authentication `json:"auth"`
	Transport     Transport      `json:"transport"`
	Security      Security       `json:"security"`
	Features      Features       `json:"features"`
	Extensions    Extensions     `json:"extensions"`
	Origin        *Origin        `json:"origin,omitempty"`
}

func (*Node) resourcePayload() {}

func (n Node) Validate() error {
	switch n.Auth.(type) {
	case *MethodPasswordAuth, *VMessAuth, *UUIDAuth, *PasswordAuth, *UsernamePasswordAuth, *NoAuth:
	default:
		return Diagnostics{issue(InvalidUnion, "/auth")}
	}
	switch n.Transport.(type) {
	case *NativeTCPTransport, *WebSocketTransport:
	default:
		return Diagnostics{issue(InvalidUnion, "/transport")}
	}
	switch n.Security.(type) {
	case *NoSecurity, *TLSSecurity, *RealitySecurity:
	default:
		return Diagnostics{issue(InvalidUnion, "/security")}
	}
	return validateValue(n, "node")
}

func DecodeNode(data []byte) (Node, error) {
	var node Node
	err := json.Unmarshal(data, &node)
	return node, safeDecodeError(err)
}

func (n *Node) UnmarshalJSON(data []byte) error {
	canonical, err := validatedBytes(data, "node")
	if err != nil {
		return err
	}
	type wireNode struct {
		SchemaVersion int             `json:"schema_version"`
		Protocol      Protocol        `json:"protocol"`
		Endpoint      Endpoint        `json:"endpoint"`
		Auth          json.RawMessage `json:"auth"`
		Transport     json.RawMessage `json:"transport"`
		Security      json.RawMessage `json:"security"`
		Features      Features        `json:"features"`
		Extensions    Extensions      `json:"extensions"`
		Origin        *Origin         `json:"origin,omitempty"`
	}
	var wire wireNode
	if err := json.Unmarshal(canonical, &wire); err != nil {
		return safeDecodeError(err)
	}
	var discriminator struct {
		Kind string `json:"kind"`
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(wire.Auth, &discriminator)
	var auth Authentication
	switch AuthKind(discriminator.Kind) {
	case AuthMethodPassword:
		auth = &MethodPasswordAuth{}
	case AuthVMessAEAD:
		auth = &VMessAuth{}
	case AuthUUID:
		auth = &UUIDAuth{}
	case AuthPassword:
		auth = &PasswordAuth{}
	case AuthUsernamePassword:
		auth = &UsernamePasswordAuth{}
	case AuthNone:
		auth = &NoAuth{}
	default:
		return Diagnostics{issue(InvalidUnion, "/auth/kind")}
	}
	if err := json.Unmarshal(wire.Auth, auth); err != nil {
		return safeDecodeError(err)
	}
	_ = json.Unmarshal(wire.Transport, &discriminator)
	var transport Transport
	switch TransportKind(discriminator.Kind) {
	case NativeTCP:
		transport = &NativeTCPTransport{}
	case WebSocket:
		transport = &WebSocketTransport{}
	default:
		return Diagnostics{issue(InvalidUnion, "/transport/kind")}
	}
	if err := json.Unmarshal(wire.Transport, transport); err != nil {
		return safeDecodeError(err)
	}
	_ = json.Unmarshal(wire.Security, &discriminator)
	var security Security
	switch SecurityMode(discriminator.Mode) {
	case SecurityNone:
		security = &NoSecurity{}
	case TLS:
		security = &TLSSecurity{}
	case Reality:
		security = &RealitySecurity{}
	default:
		return Diagnostics{issue(InvalidUnion, "/security/mode")}
	}
	if err := json.Unmarshal(wire.Security, security); err != nil {
		return safeDecodeError(err)
	}
	*n = Node{SchemaVersion: wire.SchemaVersion, Protocol: wire.Protocol, Endpoint: wire.Endpoint, Auth: auth, Transport: transport, Security: security, Features: wire.Features, Extensions: wire.Extensions, Origin: wire.Origin}
	return nil
}
