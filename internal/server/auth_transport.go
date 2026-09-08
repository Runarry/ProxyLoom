package server

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/identity"
)

type AuthenticationConfig struct {
	PublicURL      string
	Development    bool
	TrustedProxies []string
}

type Authentication struct {
	service        identity.Service
	origin         string
	secureCookie   bool
	trustedProxies []netip.Prefix
}

// NewAuthentication validates the transport boundary without trusting request
// Host or forwarding headers to determine the public origin or cookie policy.
func NewAuthentication(service identity.Service, config AuthenticationConfig) (*Authentication, error) {
	invalid := errors.New("server_authentication_config_invalid")
	if service == nil {
		return nil, invalid
	}
	u, err := url.Parse(config.PublicURL)
	if err != nil {
		return nil, invalid
	}
	origin, err := canonicalOrigin(u, config.PublicURL, false)
	if err != nil {
		return nil, invalid
	}
	secure := u.Scheme == "https"
	if !secure {
		ip, _ := netip.ParseAddr(u.Hostname())
		if !config.Development || !strings.EqualFold(u.Hostname(), "localhost") && (!ip.IsValid() || !ip.Unmap().IsLoopback()) {
			return nil, invalid
		}
	}
	a := &Authentication{service: service, origin: origin, secureCookie: secure}
	for _, cidr := range config.TrustedProxies {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil || prefix.Addr().Is4In6() {
			return nil, invalid
		}
		a.trustedProxies = append(a.trustedProxies, prefix.Masked())
	}
	return a, nil
}

func canonicalOrigin(u *url.URL, raw string, requestOrigin bool) (string, error) {
	invalid := errors.New("invalid_origin")
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") || requestOrigin && (u.Path != "" || u.RawPath != "") {
		return "", invalid
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || strings.HasSuffix(u.Host, ":") {
		return "", invalid
	}
	ip, ipErr := netip.ParseAddr(host)
	if strings.HasPrefix(u.Host, "[") != strings.Contains(host, ":") {
		return "", invalid
	}
	if ipErr == nil {
		if ip.Zone() != "" {
			return "", invalid
		}
		host = ip.String()
	} else {
		if len(host) > 253 {
			return "", invalid
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", invalid
			}
			for _, char := range label {
				if !(char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-') {
					return "", invalid
				}
			}
		}
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	for _, char := range port {
		if char < '0' || char > '9' {
			return "", invalid
		}
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return "", invalid
	}
	return u.Scheme + "://" + net.JoinHostPort(host, strconv.Itoa(portNumber)), nil
}

func (a *Authentication) validOrigin(r *http.Request) bool {
	values := r.Header.Values("Origin")
	if len(values) != 1 || values[0] == "" {
		return false
	}
	u, err := url.Parse(values[0])
	if err != nil {
		return false
	}
	origin, err := canonicalOrigin(u, values[0], true)
	return err == nil && origin == a.origin
}

func (a *Authentication) source(r *http.Request) (identity.Source, error) {
	invalid := apicontract.NewError(apicontract.MalformedRequest)
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return identity.Source{}, invalid
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || peer.Zone() != "" {
		return identity.Source{}, invalid
	}
	peer = peer.Unmap()
	ip := peer
	if a.trusted(peer) {
		values := r.Header.Values("X-Forwarded-For")
		if len(values) != 0 {
			if len(values) != 1 {
				return identity.Source{}, invalid
			}
			parts := strings.Split(values[0], ",")
			if len(parts) > 32 {
				return identity.Source{}, invalid
			}
			chain := make([]netip.Addr, len(parts))
			for i, part := range parts {
				address, err := netip.ParseAddr(strings.TrimSpace(part))
				if err != nil || address.Zone() != "" {
					return identity.Source{}, invalid
				}
				chain[i] = address.Unmap()
			}
			for i := len(chain) - 1; i >= 0; i-- {
				ip = chain[i]
				if !a.trusted(ip) {
					break
				}
			}
		} else if real := r.Header.Values("X-Real-IP"); len(real) != 0 {
			if len(real) != 1 {
				return identity.Source{}, invalid
			}
			address, err := netip.ParseAddr(real[0])
			if err != nil || address.Zone() != "" {
				return identity.Source{}, invalid
			}
			ip = address.Unmap()
		}
	}
	return identity.Source{IP: ip.String(), RequestID: apicontract.RequestID(r.Context())}, nil
}

func (a *Authentication) trusted(ip netip.Addr) bool {
	for _, prefix := range a.trustedProxies {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}
