package importparse

import (
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"golang.org/x/net/idna"
)

// ParseURI parses a single share link. It never returns the original URI or
// exposes errors from url/json libraries, whose messages may contain secrets.
func ParseURI(raw string) Candidate {
	c := Candidate{Line: 1, Status: StatusInvalid, Diagnostics: ir.Diagnostics{}}
	if len(raw) > MaxURIBytes {
		c.fail(URITooLarge, "")
		return c
	}
	if !utf8.ValidString(raw) {
		c.fail(InvalidUTF8, "")
		return c
	}
	if !safeText(raw) {
		c.fail(InvalidURI, "")
		return c
	}
	scheme, ok := uriScheme(raw)
	if !ok {
		c.fail(InvalidURI, "")
		return c
	}
	var node ir.Node
	var err error
	switch scheme {
	case "vmess":
		c.Dialect = "vmess-json-v2"
		node, err = parseVMess(raw, &c)
	case "ss":
		c.Dialect = "ss-sip002"
		node, err = parseSS(raw, &c)
	case "vless", "trojan", "socks5", "http", "https":
		c.Dialect = scheme + "-uri-v1"
		node, err = parseURLNode(raw, scheme, &c)
	default:
		c.fail(UnknownScheme, "")
		return c
	}
	if err == nil && c.Name != "" && !safeText(c.Name) {
		err = failure(InvalidValue, "/name")
	}
	if err == nil {
		err = supportedCombination(node)
	}
	if err == nil {
		err = node.Validate()
	}
	if err != nil {
		var diagnostics ir.Diagnostics
		if errors.As(err, &diagnostics) {
			c.Diagnostics = append(c.Diagnostics, diagnostics...)
			for _, d := range diagnostics {
				if d.Code == Unsupported || d.Code == UnknownScheme || d.Code == NativeConfig {
					c.Status = StatusUnsupported
				}
			}
		} else {
			c.fail(InvalidValue, "")
		}
		return c
	}
	c.Status, c.Node = StatusValid, &node
	return c
}

func safeText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func newNode(protocol ir.Protocol) ir.Node {
	return ir.Node{
		SchemaVersion: ir.SchemaVersion, Protocol: protocol,
		Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP},
		Security:  &ir.NoSecurity{Mode: ir.SecurityNone},
	}
}

func supportedCombination(node ir.Node) error {
	_, tcp := node.Transport.(*ir.NativeTCPTransport)
	_, tls := node.Security.(*ir.TLSSecurity)
	_, reality := node.Security.(*ir.RealitySecurity)
	if (node.Protocol == ir.SOCKS5 || node.Protocol == ir.HTTP) && !tcp {
		return failure(Unsupported, "/transport")
	}
	if node.Protocol == ir.Trojan && !tls || reality && (node.Protocol != ir.VLESS || !tcp) {
		return failure(Unsupported, "/security")
	}
	if node.Features.ProtocolVariant != nil && (!tcp || !tls && !reality) {
		return failure(Unsupported, "/features/protocol_variant")
	}
	return nil
}

func parseURLNode(raw, scheme string, c *Candidate) (ir.Node, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.Host == "" || (u.Path != "" && u.Path != "/") || strings.ContainsAny(u.Host, " \\") {
		return ir.Node{}, failure(InvalidURI, "")
	}
	c.Name = u.Fragment // net/url unescapes fragment once, preserving literal +.
	q, err := strictQuery(u.RawQuery)
	if err != nil {
		return ir.Node{}, err
	}
	allowed := fields("type", "security", "sni", "alpn", "fp", "allowInsecure", "insecure", "skip-cert-verify")
	if scheme == "vless" || scheme == "trojan" {
		allowed["path"], allowed["host"] = true, true
	}
	if scheme == "vless" {
		allowed["encryption"], allowed["flow"], allowed["pbk"], allowed["sid"] = true, true, true, true
	}
	if err := isolateUnknown(c, q, allowed); err != nil {
		return ir.Node{}, err
	}
	protocol := ir.Protocol(scheme)
	if scheme == "https" {
		protocol = ir.HTTP
	}
	node := newNode(protocol)
	defaultPort := 0
	if scheme == "http" {
		defaultPort = 80
	} else if scheme == "https" {
		defaultPort = 443
	}
	node.Endpoint, err = endpoint(u, defaultPort)
	if err != nil {
		return ir.Node{}, err
	}
	switch scheme {
	case "vless":
		if u.User == nil || u.User.Username() == "" {
			return ir.Node{}, failure(RequiredField, "/auth/uuid")
		}
		if _, ok := u.User.Password(); ok {
			return ir.Node{}, failure(Ambiguous, "/auth")
		}
		node.Auth = &ir.UUIDAuth{Kind: ir.AuthUUID, UUID: ir.Secret(strings.ToLower(u.User.Username()))}
		if value, ok := q["encryption"]; ok && value != "none" {
			return ir.Node{}, failure(Unsupported, "/auth")
		}
		if flow, ok := q["flow"]; ok {
			if flow != string(ir.XTLSVision) {
				return ir.Node{}, failure(Unsupported, "/features/protocol_variant")
			}
			variant := ir.ProtocolVariant(flow)
			node.Features.ProtocolVariant = &variant
		}
	case "trojan":
		if u.User == nil || u.User.Username() == "" {
			return ir.Node{}, failure(RequiredField, "/auth/password")
		}
		if _, ok := u.User.Password(); ok {
			return ir.Node{}, failure(Ambiguous, "/auth")
		}
		node.Auth = &ir.PasswordAuth{Kind: ir.AuthPassword, Password: ir.Secret(u.User.Username())}
	default:
		if u.User == nil {
			node.Auth = &ir.NoAuth{Kind: ir.AuthNone}
		} else {
			password, hasPassword := u.User.Password()
			if !hasPassword || u.User.Username() == "" || password == "" {
				return ir.Node{}, failure(RequiredField, "/auth")
			}
			node.Auth = &ir.UsernamePasswordAuth{Kind: ir.AuthUsernamePassword, Username: ir.Secret(u.User.Username()), Password: ir.Secret(password)}
		}
	}
	transport, err := parseTransport(q, "tcp")
	if err != nil {
		return ir.Node{}, err
	}
	node.Transport = transport
	defaultSecurity := "none"
	if scheme == "trojan" || scheme == "https" {
		defaultSecurity = "tls"
	}
	if (scheme == "http" || scheme == "https") && q["security"] != "" && q["security"] != defaultSecurity {
		return ir.Node{}, failure(Ambiguous, "/security")
	}
	node.Security, err = parseSecurity(q, defaultSecurity, node.Endpoint.Host)
	if err != nil {
		return ir.Node{}, err
	}
	return node, nil
}

func parseSS(raw string, c *Candidate) (ir.Node, error) {
	// Parse SIP002 userinfo separately: padded standard Base64 may contain /,
	// which a generic URL parser would otherwise treat as an authority boundary.
	body := raw[strings.Index(raw, "://")+3:]
	body, fragment, _ := strings.Cut(body, "#")
	name, err := url.PathUnescape(fragment)
	if err != nil {
		return ir.Node{}, failure(InvalidURI, "/name")
	}
	c.Name = name
	body, query, _ := strings.Cut(body, "?")
	q, err := strictQuery(query)
	if err != nil {
		return ir.Node{}, err
	}
	if err := isolateUnknown(c, q, fields()); err != nil {
		return ir.Node{}, err
	}
	if strings.Count(body, "@") != 1 {
		return ir.Node{}, failure(InvalidURI, "/auth")
	}
	userinfo, address, _ := strings.Cut(body, "@")
	u, err := url.Parse("ss://" + address)
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") {
		return ir.Node{}, failure(InvalidURI, "/endpoint")
	}
	node := newNode(ir.Shadowsocks)
	node.Endpoint, err = endpoint(u, 0)
	if err != nil {
		return ir.Node{}, err
	}
	var method, password string
	if strings.Contains(userinfo, ":") {
		method, password, _ = strings.Cut(userinfo, ":")
		method, err = url.PathUnescape(method)
		if err != nil {
			return ir.Node{}, failure(InvalidURI, "/auth")
		}
		password, err = url.PathUnescape(password)
		if err != nil {
			return ir.Node{}, failure(InvalidURI, "/auth")
		}
	} else {
		decoded, err := decodeBase64(userinfo, false)
		if err != nil || !utf8.Valid(decoded) {
			return ir.Node{}, failure(InvalidBase64, "/auth")
		}
		var ok bool
		method, password, ok = strings.Cut(string(decoded), ":")
		if !ok {
			return ir.Node{}, failure(RequiredField, "/auth")
		}
	}
	if method == "" || password == "" {
		return ir.Node{}, failure(RequiredField, "/auth")
	}
	if method != string(ir.AES128GCM) && method != string(ir.AES256GCM) && method != string(ir.ChaCha20IETFPoly1305) {
		return ir.Node{}, failure(Unsupported, "/auth/method")
	}
	node.Auth = &ir.MethodPasswordAuth{Kind: ir.AuthMethodPassword, Method: ir.ShadowsocksMethod(method), Password: ir.Secret(password)}
	return node, nil
}

func endpoint(u *url.URL, defaultPort int) (ir.Endpoint, error) {
	if strings.Count(u.Host, ":") > 1 && !strings.HasPrefix(u.Host, "[") {
		return ir.Endpoint{}, failure(InvalidURI, "/endpoint")
	}
	host, err := normalizeHost(u.Hostname())
	if err != nil {
		return ir.Endpoint{}, failure(InvalidValue, "/endpoint/host")
	}
	port := defaultPort
	if raw := u.Port(); raw != "" {
		port, err = decimalPort(raw)
		if err != nil {
			return ir.Endpoint{}, err
		}
	} else if strings.HasSuffix(u.Host, ":") || port == 0 {
		return ir.Endpoint{}, failure(RequiredField, "/endpoint/port")
	}
	return ir.Endpoint{Host: host, Port: port}, nil
}

func decimalPort(raw string) (int, error) {
	if raw == "" {
		return 0, failure(RequiredField, "/endpoint/port")
	}
	for _, b := range []byte(raw) {
		if b < '0' || b > '9' {
			return 0, failure(InvalidValue, "/endpoint/port")
		}
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, failure(InvalidValue, "/endpoint/port")
	}
	return port, nil
}

func normalizeHost(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || !utf8.ValidString(raw) {
		return "", failure(InvalidValue, "")
	}
	if addr, err := netip.ParseAddr(raw); err == nil {
		if addr.Zone() != "" {
			return "", failure(Unsupported, "")
		}
		return addr.Unmap().String(), nil
	}
	if strings.ContainsAny(raw, ":/[]%@?#\\") {
		return "", failure(InvalidValue, "")
	}
	ascii, err := idna.Lookup.ToASCII(raw)
	if err != nil {
		return "", failure(InvalidValue, "")
	}
	ascii = strings.ToLower(strings.TrimSuffix(ascii, "."))
	if err := (ir.Endpoint{Host: ascii, Port: 1}).Validate(); err != nil {
		return "", failure(InvalidValue, "")
	}
	return ascii, nil
}

func strictQuery(raw string) (map[string]string, error) {
	out := make(map[string]string)
	if raw == "" {
		return out, nil
	}
	for _, entry := range strings.Split(raw, "&") {
		if entry == "" || strings.ContainsRune(entry, ';') {
			return nil, failure(InvalidURI, "/query")
		}
		key, value, _ := strings.Cut(entry, "=")
		key, err := url.QueryUnescape(key)
		if err != nil || key == "" || !safeText(key) {
			return nil, failure(InvalidURI, "/query")
		}
		value, err = url.QueryUnescape(value)
		if err != nil || !utf8.ValidString(value) {
			return nil, failure(InvalidURI, "/query")
		}
		if _, exists := out[key]; exists {
			return nil, failure(DuplicateField, "/query")
		}
		out[key] = value
	}
	return out, nil
}

func fields(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

func metadataKey(key string) bool {
	return strings.HasPrefix(key, "x-") || key == "remarks" || key == "remark" || key == "group" || key == "tag" || key == "name"
}

func isolateUnknown(c *Candidate, q map[string]string, allowed map[string]bool) error {
	keys := make([]string, 0, len(q))
	for key := range q {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	unsupported := false
	for _, key := range keys {
		if allowed[key] {
			continue
		}
		value, _ := json.Marshal(q[key])
		c.metadata(key, value)
		c.warnMetadata()
		if !metadataKey(key) {
			unsupported = true
		}
	}
	if unsupported {
		return failure(Unsupported, "/query")
	}
	return nil
}

func parseTransport(q map[string]string, defaultType string) (ir.Transport, error) {
	kind := defaultType
	if v, ok := q["type"]; ok {
		kind = v
	}
	switch kind {
	case "tcp":
		if _, ok := q["path"]; ok {
			return nil, failure(Unsupported, "/transport/path")
		}
		if _, ok := q["host"]; ok {
			return nil, failure(Unsupported, "/transport/host")
		}
		return &ir.NativeTCPTransport{Kind: ir.NativeTCP}, nil
	case "ws":
		path := "/"
		if v, ok := q["path"]; ok {
			path = v
		}
		transport := &ir.WebSocketTransport{Kind: ir.WebSocket, Path: path}
		if v, ok := q["host"]; ok {
			host, err := normalizeHost(v)
			if err != nil {
				return nil, failure(InvalidValue, "/transport/host")
			}
			transport.Host = &host
		}
		return transport, nil
	default:
		return nil, failure(Unsupported, "/transport")
	}
}

func parseSecurity(q map[string]string, defaultMode, host string) (ir.Security, error) {
	mode := defaultMode
	if value, ok := q["security"]; ok {
		mode = value
	}
	insecureCount := 0
	for _, key := range []string{"allowInsecure", "insecure", "skip-cert-verify"} {
		if value, ok := q[key]; ok {
			insecureCount++
			if value == "1" || value == "true" {
				return nil, failure(UnsafeTLS, "/security/verify_certificate")
			}
			if value != "0" && value != "false" {
				return nil, failure(InvalidValue, "/security/verify_certificate")
			}
		}
	}
	if insecureCount > 1 {
		return nil, failure(Ambiguous, "/security/verify_certificate")
	}
	if mode == "none" {
		for _, key := range []string{"sni", "alpn", "fp", "pbk", "sid", "allowInsecure", "insecure", "skip-cert-verify"} {
			if _, exists := q[key]; exists {
				return nil, failure(Unsupported, "/security")
			}
		}
		return &ir.NoSecurity{Mode: ir.SecurityNone}, nil
	}
	if mode != "tls" && mode != "reality" {
		return nil, failure(Unsupported, "/security")
	}
	serverName := host
	if value, ok := q["sni"]; ok {
		var err error
		serverName, err = normalizeHost(value)
		if err != nil {
			return nil, failure(InvalidValue, "/security/server_name")
		}
	}
	var alpn []string
	if value, ok := q["alpn"]; ok {
		alpn = strings.Split(value, ",")
		seen := make(map[string]bool)
		for _, protocol := range alpn {
			if protocol == "" || len(protocol) > 255 || !safeText(protocol) || strings.ContainsRune(protocol, ' ') || seen[protocol] {
				return nil, failure(InvalidValue, "/security/alpn")
			}
			seen[protocol] = true
		}
	}
	if mode == "reality" {
		for _, key := range []string{"sni", "pbk", "sid", "fp"} {
			value, ok := q[key]
			if !ok || value == "" && key != "sid" {
				return nil, failure(RequiredField, "/security")
			}
		}
		if insecureCount > 0 {
			return nil, failure(Unsupported, "/security/verify_certificate")
		}
		return &ir.RealitySecurity{Mode: ir.Reality, ServerName: serverName, PublicKey: ir.Secret(q["pbk"]), ShortID: ir.Secret(strings.ToLower(q["sid"])), ClientFingerprint: q["fp"], ALPN: alpn}, nil
	}
	if _, ok := q["pbk"]; ok {
		return nil, failure(Unsupported, "/security/public_key")
	}
	if _, ok := q["sid"]; ok {
		return nil, failure(Unsupported, "/security/short_id")
	}
	verify := true
	tls := &ir.TLSSecurity{Mode: ir.TLS, ServerName: serverName, VerifyCertificate: &verify, ALPN: alpn}
	if value, ok := q["fp"]; ok {
		tls.ClientFingerprint = &value
	}
	return tls, nil
}
