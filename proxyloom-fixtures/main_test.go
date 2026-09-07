package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/isolation"
)

func testCertificates(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "certs")
	if err := run([]string{"init-certs", "--out", out}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestInitCertificatesTLS(t *testing.T) {
	out := testCertificates(t)
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 5 {
		t.Fatalf("expected exactly five certificate files: entries=%d err=%v", len(entries), err)
	}
	caPEM, err := os.ReadFile(filepath.Join(out, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	block, rest := pem.Decode(caPEM)
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		t.Fatal("CA export must contain exactly one public certificate")
	}
	caCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !caCert.IsCA {
		t.Fatalf("invalid CA certificate: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("cannot reload shared CA")
	}
	wrongCA, err := isolation.NewTestCA()
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"a", "b"} {
		t.Run(role, func(t *testing.T) {
			pair, err := tls.LoadX509KeyPair(filepath.Join(out, role+".pem"), filepath.Join(out, role+"-key.pem"))
			if err != nil {
				t.Fatal(err)
			}
			keyPEM, err := os.ReadFile(filepath.Join(out, role+"-key.pem"))
			if err != nil {
				t.Fatal(err)
			}
			keyBlock, rest := pem.Decode(keyPEM)
			if keyBlock == nil || keyBlock.Type != "PRIVATE KEY" || len(rest) != 0 {
				t.Fatal("leaf key must use PKCS8 encoding")
			}
			if _, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes); err != nil {
				t.Fatal(err)
			}
			leaf, err := x509.ParseCertificate(pair.Certificate[0])
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(leaf.RawSubjectPublicKeyInfo, caCert.RawSubjectPublicKeyInfo) {
				t.Fatal("CA private key must never be exported as a leaf key")
			}
			for _, tc := range []struct {
				name string
				pool *x509.CertPool
				host string
				ok   bool
			}{
				{"shared CA", pool, role + ".proxyloom.test", true},
				{"wrong CA", wrongCA.Pool, role + ".proxyloom.test", false},
				{"wrong SNI", pool, "wrong.proxyloom.test", false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					err := handshake(t, pair, tc.pool, tc.host)
					if (err == nil) != tc.ok {
						t.Fatalf("TLS handshake success=%v, want %v: %v", err == nil, tc.ok, err)
					}
				})
			}
		})
	}
	if runtime.GOOS != "windows" {
		assertMode(t, out, 0755)
		for _, entry := range entries {
			assertMode(t, filepath.Join(out, entry.Name()), 0644)
		}
	}
	if err := initCerts(out); err == nil {
		t.Fatal("existing directory must be refused")
	}
	after, err := os.ReadFile(filepath.Join(out, "ca.pem"))
	if err != nil || !bytes.Equal(caPEM, after) {
		t.Fatal("refused initialization modified the existing CA")
	}
	empty := t.TempDir()
	if err := initCerts(empty); err == nil {
		t.Fatal("even an empty existing output directory must be refused")
	}
}

func handshake(t *testing.T, cert tls.Certificate, roots *x509.CertPool, host string) error {
	t.Helper()
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		_ = conn.(*tls.Conn).Handshake()
	}()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", listener.Addr().String(), &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12})
	if conn != nil {
		_ = conn.Close()
	}
	_ = listener.Close()
	<-done
	return err
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("mode %o, want %o", info.Mode().Perm(), want)
	}
}

func TestProxyStartupConfig(t *testing.T) {
	out := testCertificates(t)
	for _, tc := range []struct {
		name, role string
		allow      *string
		cert, key  string
		wantError  string
	}{
		{"valid A", "a", nil, "a.pem", "a-key.pem", ""},
		{"valid B", "b", str(" 172.30.253.10 , ::1 "), "b.pem", "b-key.pem", ""},
		{"B missing allow", "b", nil, "b.pem", "b-key.pem", "ALLOW_FROM"},
		{"B empty allow", "b", str(""), "b.pem", "b-key.pem", "ALLOW_FROM"},
		{"A empty allow", "a", str(""), "a.pem", "a-key.pem", "ALLOW_FROM"},
		{"invalid allow", "b", str("bad"), "b.pem", "b-key.pem", "ALLOW_FROM"},
		{"mixed allow", "b", str("172.30.253.10,bad"), "b.pem", "b-key.pem", "ALLOW_FROM"},
		{"trailing empty", "b", str("172.30.253.10,"), "b.pem", "b-key.pem", "ALLOW_FROM"},
		{"leading empty", "a", str(",127.0.0.1"), "a.pem", "a-key.pem", "ALLOW_FROM"},
		{"missing cert env", "a", nil, "", "a-key.pem", "CERT_FILE"},
		{"missing key env", "a", nil, "a.pem", "", "KEY_FILE"},
		{"missing cert file", "a", nil, "absent.pem", "a-key.pem", "cannot load"},
		{"missing key file", "a", nil, "a.pem", "absent.pem", "cannot load"},
		{"mismatched pair", "a", nil, "a.pem", "b-key.pem", "cannot load"},
		{"invalid certificate", "a", nil, "a-key.pem", "a-key.pem", "cannot load"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"PROXYLOOM_ISOLATION_BIND": "127.0.0.1:0"}
			if tc.allow != nil {
				env["PROXYLOOM_ISOLATION_ALLOW_FROM"] = *tc.allow
			}
			if tc.cert != "" {
				env["PROXYLOOM_ISOLATION_CERT_FILE"] = filepath.Join(out, tc.cert)
			}
			if tc.key != "" {
				env["PROXYLOOM_ISOLATION_KEY_FILE"] = filepath.Join(out, tc.key)
			}
			cfg, err := loadProxyConfig(tc.role, func(key string) (string, bool) { value, ok := env[key]; return value, ok })
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("expected %q error, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Role != tc.role || cfg.Host != tc.role+".proxyloom.test" || cfg.Bind != "127.0.0.1:0" || len(cfg.Certificate.Certificate) == 0 {
				t.Fatal("startup configuration incomplete")
			}
			if tc.role == "b" && (len(cfg.AllowFrom) != 2 || !cfg.AllowFrom[0].Equal(net.ParseIP("172.30.253.10"))) {
				t.Fatal("valid allowlist not preserved")
			}
		})
	}
}

func str(value string) *string { return &value }

func TestInitCertsUsage(t *testing.T) {
	for _, args := range [][]string{{"--unknown"}, {"extra"}, {"--out", ""}, {"--out"}} {
		if err := runInitCerts(args); err == nil {
			t.Fatalf("expected usage error for %q", args)
		}
	}
}
