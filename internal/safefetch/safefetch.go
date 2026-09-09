// Package safefetch retrieves remote source bytes after resolving and pinning
// a public destination address. It has no database, keyring, or Runner access.
package safefetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultTimeout            = 15 * time.Second
	DefaultMaxRedirects       = 3
	MaxRedirects              = 5
	DefaultMaxCompressedBytes = 10 << 20
	DefaultMaxDecodedBytes    = 10 << 20
	maxHeaderBytes            = 64 << 10
	maxURLBytes               = 4096
)

var (
	ErrInvalidURL     = errors.New("safefetch: invalid_url")
	ErrHTTPDisabled   = errors.New("safefetch: http_disabled")
	ErrBlockedAddress = errors.New("safefetch: blocked_address")
	ErrRedirect       = errors.New("safefetch: redirect_rejected")
	ErrTooLarge       = errors.New("safefetch: response_too_large")
	ErrTimeout        = errors.New("safefetch: timeout")
	ErrFetch          = errors.New("safefetch: fetch_failed")
)

// Resolver is optional. Production uses the process default resolver.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Client never reads HTTP_PROXY from the environment. AllowNets is a test-only
// override for loopback fixtures and must be empty in production callers.
type Client struct {
	Resolver  Resolver
	AllowNets []netip.Prefix
	RootCAs   *x509.CertPool
}

type Request struct {
	Header             http.Header
	AllowHTTP          bool
	Timeout            time.Duration
	MaxRedirects       int
	MaxCompressedBytes int64
	MaxDecodedBytes    int64
}

type Result struct {
	Status      int
	Body        []byte
	ContentType string
	URLDisplay  string
}

func (Request) Format(state fmt.State, _ rune) { _, _ = fmt.Fprint(state, "[REDACTED]") }
func (Result) Format(state fmt.State, _ rune)  { _, _ = fmt.Fprint(state, "[REDACTED]") }

func (c *Client) Fetch(ctx context.Context, raw string, request Request) (Result, error) {
	if request.Timeout == 0 {
		request.Timeout = DefaultTimeout
	}
	if request.MaxRedirects == 0 {
		request.MaxRedirects = DefaultMaxRedirects
	}
	if request.MaxRedirects < 0 || request.MaxRedirects > MaxRedirects {
		return Result{}, ErrInvalidURL
	}
	if request.MaxCompressedBytes == 0 {
		request.MaxCompressedBytes = DefaultMaxCompressedBytes
	}
	if request.MaxDecodedBytes == 0 {
		request.MaxDecodedBytes = DefaultMaxDecodedBytes
	}
	if request.MaxCompressedBytes < 1 || request.MaxCompressedBytes > DefaultMaxCompressedBytes ||
		request.MaxDecodedBytes < 1 || request.MaxDecodedBytes > DefaultMaxDecodedBytes {
		return Result{}, ErrInvalidURL
	}
	parsed, err := parseFetchURL(raw, request.AllowHTTP)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            c.dialContext,
		DisableKeepAlives:      true,
		DisableCompression:     true,
		ForceAttemptHTTP2:      false,
		MaxResponseHeaderBytes: maxHeaderBytes,
		ResponseHeaderTimeout:  request.Timeout,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: c.RootCAs},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		Timeout:   request.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > request.MaxRedirects {
				return ErrRedirect
			}
			if _, err := parseFetchURL(req.URL.String(), request.AllowHTTP); err != nil {
				if errors.Is(err, ErrHTTPDisabled) {
					return err
				}
				return ErrRedirect
			}
			if len(via) > 0 && !sameOrigin(via[len(via)-1].URL, req.URL) {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Result{}, ErrInvalidURL
	}
	if request.Header != nil {
		req.Header = request.Header.Clone()
	}
	response, err := client.Do(req)
	if err != nil {
		return Result{}, classify(ctx, err)
	}
	defer response.Body.Close()
	compressed, err := io.ReadAll(io.LimitReader(response.Body, request.MaxCompressedBytes+1))
	if err != nil {
		return Result{}, classify(ctx, err)
	}
	if int64(len(compressed)) > request.MaxCompressedBytes {
		return Result{}, ErrTooLarge
	}
	body := compressed
	encoding := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Encoding")))
	if encoding == "gzip" {
		reader, err := gzip.NewReader(bytes.NewReader(compressed))
		if err != nil {
			return Result{}, ErrFetch
		}
		decoded, err := io.ReadAll(io.LimitReader(reader, request.MaxDecodedBytes+1))
		_ = reader.Close()
		if err != nil {
			return Result{}, ErrFetch
		}
		if int64(len(decoded)) > request.MaxDecodedBytes {
			return Result{}, ErrTooLarge
		}
		body = decoded
	} else if encoding != "" && encoding != "identity" {
		return Result{}, ErrFetch
	}
	final := parsed
	if response.Request != nil && response.Request.URL != nil {
		final = response.Request.URL
	}
	return Result{Status: response.StatusCode, Body: body, ContentType: response.Header.Get("Content-Type"), URLDisplay: displayURL(final)}, nil
}

func parseFetchURL(raw string, allowHTTP bool) (*url.URL, error) {
	if raw == "" || len(raw) > maxURLBytes || strings.ContainsAny(raw, " \t\r\n") {
		return nil, ErrInvalidURL
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Fragment != "" || parsed.Host == "" {
		return nil, ErrInvalidURL
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !allowHTTP {
			return nil, ErrHTTPDisabled
		}
	default:
		return nil, ErrInvalidURL
	}
	host := parsed.Hostname()
	if host == "" {
		return nil, ErrInvalidURL
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if _, err := net.LookupPort("tcp", port); err != nil {
		return nil, ErrInvalidURL
	}
	return parsed, nil
}

func displayURL(value *url.URL) string {
	if value == nil {
		return ""
	}
	host := value.Hostname()
	port := value.Port()
	if port == "" {
		return value.Scheme + "://" + host
	}
	return value.Scheme + "://" + net.JoinHostPort(host, port)
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Hostname(), b.Hostname()) && effectivePort(a) == effectivePort(b)
}

func effectivePort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	if value.Scheme == "https" {
		return "443"
	}
	return "80"
}

func (c *Client) dialContext(ctx context.Context, _ string, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" {
		return nil, ErrInvalidURL
	}
	ips, err := c.lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	var allowed []netip.Addr
	for _, ip := range ips {
		if c.permitted(ip) {
			allowed = append(allowed, ip.Unmap())
		}
	}
	if len(allowed) == 0 {
		return nil, ErrBlockedAddress
	}
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: -1}
	for _, ip := range allowed {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, classify(ctx, ErrFetch)
}

func (c *Client) lookup(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip}, nil
	}
	resolver := Resolver(net.DefaultResolver)
	if c != nil && c.Resolver != nil {
		resolver = c.Resolver
	}
	ips, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, classify(ctx, err)
	}
	if len(ips) == 0 {
		return nil, ErrBlockedAddress
	}
	return ips, nil
}

func (c *Client) permitted(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !blocked(ip) {
		return true
	}
	if c == nil {
		return false
	}
	for _, network := range c.AllowNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func classify(ctx context.Context, err error) error {
	if err == nil {
		return ErrFetch
	}
	if errors.Is(err, ErrInvalidURL) || errors.Is(err, ErrHTTPDisabled) || errors.Is(err, ErrBlockedAddress) ||
		errors.Is(err, ErrRedirect) || errors.Is(err, ErrTooLarge) || errors.Is(err, ErrTimeout) || errors.Is(err, ErrFetch) {
		return err
	}
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrTimeout
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Err != nil {
		return classify(ctx, urlErr.Err)
	}
	return ErrFetch
}
