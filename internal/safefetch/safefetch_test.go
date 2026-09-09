package safefetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

type mapResolver map[string][]netip.Addr

func (m mapResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	if ips, ok := m[strings.ToLower(host)]; ok {
		return append([]netip.Addr{}, ips...), nil
	}
	return nil, errors.New("nxdomain")
}

func loopbackClient(t *testing.T, resolver Resolver, roots *x509.CertPool) *Client {
	t.Helper()
	return &Client{Resolver: resolver, AllowNets: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, RootCAs: roots}
}

func TestBlockedAddressesIncludeMappedAndMetadata(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254",
		"::ffff:169.254.169.254", "100.64.0.1", "0.0.0.0", "224.0.0.1", "240.0.0.1", "192.0.0.8", "fd00:ec2::254", "2001:db8::1"} {
		if !blocked(netip.MustParseAddr(raw)) {
			t.Fatalf("blocked address accepted: %s", raw)
		}
	}
	if blocked(netip.MustParseAddr("8.8.8.8")) || blocked(netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("public address blocked")
	}
}

func TestHTTPDisabledAndUserinfoRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(server.Close)
	client := loopbackClient(t, nil, nil)
	if _, err := client.Fetch(context.Background(), server.URL, Request{}); !errors.Is(err, ErrHTTPDisabled) {
		t.Fatalf("HTTP without allow: %v", err)
	}
	blockedURL := url.URL{Scheme: "https", User: url.UserPassword("user", "pass"), Host: "example.invalid"}
	if _, err := client.Fetch(context.Background(), blockedURL.String(), Request{}); !errors.Is(err, ErrInvalidURL) {
		t.Fatalf("userinfo accepted: %v", err)
	}
}

func TestFetchPinsIPPreservesHostAndIgnoresProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	var seenHost, seenSNI string
	roots, tlsConfig := testCertificate(t, "source.test")
	listener, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenHost = r.Host
		if r.TLS != nil {
			seenSNI = r.TLS.ServerName
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	port := listener.Addr().(*net.TCPAddr).Port
	client := loopbackClient(t, mapResolver{"source.test": {netip.MustParseAddr("127.0.0.1")}}, roots)
	result, err := client.Fetch(context.Background(), "https://source.test:"+strconv.Itoa(port)+"/list", Request{})
	if err != nil || string(result.Body) != "ok" {
		t.Fatalf("pin fetch failed: %v %#v", err, result)
	}
	if seenSNI != "source.test" || !strings.HasPrefix(seenHost, "source.test") {
		t.Fatalf("host/SNI rewritten: host=%q sni=%q", seenHost, seenSNI)
	}
	if strings.Contains(result.URLDisplay, "/list") || strings.Contains(result.URLDisplay, "@") {
		t.Fatal("display URL retained path or userinfo")
	}
}

func TestPrivateMappedAndRedirectTargetsAreBlocked(t *testing.T) {
	client := &Client{Resolver: mapResolver{
		"loop.test":     {netip.MustParseAddr("127.0.0.1")},
		"mapped.test":   {netip.MustParseAddr("::ffff:169.254.169.254")},
		"metadata.test": {netip.MustParseAddr("169.254.169.254")},
	}}
	for _, raw := range []string{"https://loop.test/", "https://mapped.test/", "https://metadata.test/"} {
		if _, err := client.Fetch(context.Background(), raw, Request{}); !errors.Is(err, ErrBlockedAddress) {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://169.254.169.254/latest")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(server.Close)
	allowed := loopbackClient(t, nil, nil)
	if _, err := allowed.Fetch(context.Background(), server.URL, Request{AllowHTTP: true}); !errors.Is(err, ErrRedirect) && !errors.Is(err, ErrBlockedAddress) && !errors.Is(err, ErrHTTPDisabled) {
		t.Fatalf("metadata redirect accepted: %v", err)
	}
}

func TestCrossOriginRedirectStripsAuthorization(t *testing.T) {
	var secondAuth string
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("second"))
	}))
	t.Cleanup(second.Close)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, second.URL, http.StatusFound)
	}))
	t.Cleanup(first.Close)
	client := loopbackClient(t, nil, nil)
	header := make(http.Header)
	header.Set("Authorization", "Bearer "+"SYNTHETIC_SOURCE_TOKEN")
	result, err := client.Fetch(context.Background(), first.URL, Request{AllowHTTP: true, Header: header})
	if err != nil || string(result.Body) != "second" {
		t.Fatalf("redirect fetch: %v %#v", err, result)
	}
	if secondAuth != "" {
		t.Fatal("authorization forwarded across origin")
	}
}

func TestLimitsAndGzip(t *testing.T) {
	var body bytes.Buffer
	writer := gzip.NewWriter(&body)
	_, _ = writer.Write(bytes.Repeat([]byte("a"), 64))
	_ = writer.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/big" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bytes.Repeat([]byte("x"), 32))
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(body.Bytes())
	}))
	t.Cleanup(server.Close)
	client := loopbackClient(t, nil, nil)
	if _, err := client.Fetch(context.Background(), server.URL+"/big", Request{AllowHTTP: true, MaxCompressedBytes: 8}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize accepted: %v", err)
	}
	result, err := client.Fetch(context.Background(), server.URL+"/", Request{AllowHTTP: true})
	if err != nil || len(result.Body) != 64 {
		t.Fatalf("gzip decode: %v len=%d", err, len(result.Body))
	}
}

func TestErrorsDoNotIncludeURLOrSecrets(t *testing.T) {
	client := &Client{Resolver: mapResolver{"secret.test": {netip.MustParseAddr("127.0.0.1")}}}
	secretURL := url.URL{Scheme: "https", User: url.UserPassword("user", "SYNTHETIC_PASS"), Host: "secret.test"}
	_, err := client.Fetch(context.Background(), secretURL.String(), Request{})
	if err == nil || strings.Contains(err.Error(), "SYNTHETIC_PASS") || strings.Contains(err.Error(), "secret.test") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestPublicLiteralIPIsNotRequiredForAllowList(t *testing.T) {
	if (&Client{}).permitted(netip.MustParseAddr("1.1.1.1")) != true {
		t.Fatal("public IP rejected")
	}
	if (&Client{}).permitted(netip.MustParseAddr("127.0.0.1")) {
		t.Fatal("loopback permitted in production client")
	}
}

func testCertificate(t *testing.T, dns string) (*x509.CertPool, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: dns}, DNSNames: []string{dns},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return pool, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
}
