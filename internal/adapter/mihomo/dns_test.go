package mihomo

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/adapter/singbox"
	"github.com/Runarry/ProxyLoom/internal/adapter/xray"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"golang.org/x/net/dns/dnsmessage"
	socksproxy "golang.org/x/net/proxy"
	"gopkg.in/yaml.v3"
)

func dnsInput() adapter.EmitInput {
	return adapter.EmitInput{TargetKey: "mihomo-dns", FinalTag: "direct", DNS: &adapter.DNSInput{
		ResourceID: "bb19ca69-a9ab-421b-8723-af89a445f905",
		Profile: ir.DNSProfile{Bootstrap: []ir.BootstrapResolver{{ResolverID: "boot", Kind: ir.DNSUDP, Address: "192.0.2.53", Port: 53}}, Resolvers: []ir.DNSResolver{
			{ResolverID: "a", Kind: ir.DNSUDP, Address: "192.0.2.1", Port: 53},
			{ResolverID: "b", Kind: ir.DNSHTTPS, URL: "https://192.0.2.2:8443/dns-query", BootstrapResolverID: "boot"},
			{ResolverID: "local", Kind: ir.DNSLocal},
		}, FinalResolver: "b"}, OutboundTags: map[string]string{"a": "direct", "b": "n_exit"},
	}}
}

func TestDNSOrderedPoliciesAndIntersection(t *testing.T) {
	in := dnsInput()
	in.DNS.Rules = []adapter.RouteRule{
		{Condition: adapter.Condition{Kind: "domain_suffix", Values: []string{"example.org"}}, Target: "a", FieldPath: "/payload/rules/0"},
		{Condition: adapter.Condition{Kind: "domain", Values: []string{"a.example.org"}}, Target: "b", FieldPath: "/payload/rules/1"},
		{Condition: adapter.Condition{Kind: "and", Terms: []adapter.Condition{{Kind: "domain", Values: []string{"yes.example.net", "outside.org"}}, {Kind: "domain_suffix", Values: []string{"example.net"}}}}, Target: "a", FieldPath: "/payload/rules/2"},
		{Condition: adapter.Condition{Kind: "and", Terms: []adapter.Condition{{Kind: "domain", Values: []string{"outside.org"}}, {Kind: "domain_suffix", Values: []string{"example.net"}}}}, Target: "a", FieldPath: "/payload/rules/3"},
	}
	doc := document{}
	if err := applyDNS(&doc, in); err != nil {
		t.Fatal(err)
	}
	if len(doc.RuleProviders) != 3 || len(doc.DNS.NameServerPolicy) != 3 {
		t.Fatal("contradiction became match-all")
	}
	if !reflect.DeepEqual(doc.RuleProviders["dns_rule_0002"].Payload, []string{"yes.example.net"}) {
		t.Fatal("AND became OR")
	}
	if doc.DNS.NameServer[0] != "https://192.0.2.2:8443/dns-query#n_exit" || doc.DNS.DefaultNameServer[0] != "udp://192.0.2.53:53" {
		t.Fatal("explicit resolver route/bootstrap lost")
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Index(data, []byte("rule-set:dns_rule_0000")) > bytes.Index(data, []byte("rule-set:dns_rule_0001")) {
		t.Fatal("policy order changed")
	}
	for i := 0; i < 5; i++ {
		next, _ := yaml.Marshal(doc)
		if !bytes.Equal(data, next) {
			t.Fatal("nondeterministic DNS mapping")
		}
	}
	got := dnsDomainIntersection([]string{"suffix:example.org", "exact:a.net"}, []string{"suffix:sub.example.org", "exact:notexample.org", "exact:a.net"})
	if !reflect.DeepEqual(got, []string{"exact:a.net", "suffix:sub.example.org"}) {
		t.Fatalf("intersection %v", got)
	}
}

func TestDNSPreciseBootstrapAndURLDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		mutate     func(*adapter.EmitInput)
	}{
		{"query", "/payload/resolvers/1/url", func(in *adapter.EmitInput) { in.DNS.Profile.Resolvers[1].URL += "?key=secret" }},
		{"encoded-slash", "/payload/resolvers/1/url", func(in *adapter.EmitInput) { in.DNS.Profile.Resolvers[1].URL = "https://dns.example.org/dns%2Fquery" }},
		{"incompatible-bootstrap", "/payload/resolvers/3/bootstrap_resolver_id", func(in *adapter.EmitInput) {
			in.DNS.Profile.Resolvers[1].URL = "https://dns.example.org/dns-query"
			in.DNS.OutboundTags["b"] = "direct"
			in.DNS.Profile.Bootstrap = append(in.DNS.Profile.Bootstrap, ir.BootstrapResolver{ResolverID: "other", Kind: ir.DNSLocal})
			in.DNS.Profile.Resolvers = append(in.DNS.Profile.Resolvers, ir.DNSResolver{ResolverID: "c", Kind: ir.DNSHTTPS, URL: "https://second.example.org/dns-query", BootstrapResolverID: "other"})
			in.DNS.OutboundTags["c"] = "direct"
		}},
		{"proxied-hostname-bootstrap", "/payload/resolvers/1/bootstrap_resolver_id", func(in *adapter.EmitInput) {
			in.DNS.Profile.Resolvers[1].URL = "https://dns.example.org/dns-query"
		}},
		{"non-domain", "/payload/rules/0/match", func(in *adapter.EmitInput) {
			in.DNS.Rules = []adapter.RouteRule{{Condition: adapter.Condition{Kind: "network", Values: []string{"udp"}}, Target: "a", FieldPath: "/payload/rules/0"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := dnsInput()
			tc.mutate(&in)
			err := applyDNS(&document{}, in)
			ds, ok := err.(ir.Diagnostics)
			if !ok || len(ds) != 1 || ds[0].Code != ir.CompileUnmappedField || ds[0].FieldPath != tc.path || ds[0].ResourceID != in.DNS.ResourceID || ds[0].TargetKey != in.TargetKey {
				t.Fatalf("diagnostic %v", err)
			}
		})
	}
	in := dnsInput()
	in.DNS.Profile.Bootstrap = append(in.DNS.Profile.Bootstrap, ir.BootstrapResolver{ResolverID: "unused", Kind: ir.DNSLocal})
	if err := applyDNS(&document{}, in); err != nil {
		t.Fatalf("unused bootstrap rejected: %v", err)
	}
}

// Opt-in native checks run inside a network-none Linux container. Every server
// below is a loopback test fixture; no public DNS or business HTTP is exercised.
func TestDNSNativeExecution(t *testing.T) {
	coreDir := os.Getenv("PROXYLOOM_DNS_NATIVE_CORES")
	if coreDir == "" {
		t.Skip("set PROXYLOOM_DNS_NATIVE_CORES to locked executable directory")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("native DNS evidence requires linux/amd64")
	}
	for _, core := range []dnsNativeCore{
		{"xray", "8255dd939c34cf966cc91517b6324dd3c8d0bcf49ffac8beca049a38c46845ed", xray.Emit, []string{"run", "-test", "-c"}, []string{"run", "-c"}, ""},
		{"sing-box", "57b3da14e264b6e05e8f46aee027c02d7dd7f1594d19aa39e2f4d2b9459bbd04", singbox.Emit, []string{"check", "-c"}, []string{"run", "-c"}, ""},
		{"mihomo", "3e92df24f5e80e86b9cf9183ceb7bb575f0bd132a9dc4081dae42e80f21076ae", Emit, []string{"-t", "-f"}, []string{"-f"}, ""},
	} {
		t.Run(core.name, func(t *testing.T) {
			core.path = filepath.Join(coreDir, core.name)
			binaryBytes, err := os.ReadFile(core.path)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(binaryBytes)
			if hex.EncodeToString(sum[:]) != core.hash {
				t.Fatal("locked core SHA256 mismatch")
			}
			t.Run("local-config", func(t *testing.T) {
				in := dnsNativeInput(t)
				in.DNS.Profile.Resolvers = []ir.DNSResolver{{ResolverID: "local", Kind: ir.DNSLocal}}
				in.DNS.Profile.FinalResolver = "local"
				core.check(t, in, nil)
			})
			t.Run("udp-order-and-no-fallback", func(t *testing.T) { testDNSNativeUDP(t, core) })
			t.Run("https-explicit-outbound-bootstrap", func(t *testing.T) { testDNSNativeHTTPS(t, core, false) })
			t.Run("https-hostname-direct-bootstrap", func(t *testing.T) { testDNSNativeHTTPS(t, core, true) })
		})
	}
}

type dnsNativeCore struct {
	name, hash         string
	emit               func(adapter.EmitInput) (adapter.Artifact, []ir.Diagnostic, error)
	checkArgs, runArgs []string
	path               string
}

func dnsNativeInput(t *testing.T) adapter.EmitInput {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return adapter.EmitInput{FinalTag: "direct", TargetKey: "native-dns", Preset: &ir.ClientPreset{LocalListener: ir.LocalListener{Protocol: "socks5", Listen: "127.0.0.1", Port: port}}, DNS: &adapter.DNSInput{ResourceID: "bb19ca69-a9ab-421b-8723-af89a445f905", Profile: ir.DNSProfile{SchemaVersion: 1, Bootstrap: []ir.BootstrapResolver{{ResolverID: "boot", Kind: ir.DNSLocal}}}, OutboundTags: map[string]string{}}}
}

func (core dnsNativeCore) check(t *testing.T, in adapter.EmitInput, env []string) (string, []byte) {
	t.Helper()
	artifact, ds, err := core.emit(in)
	if err != nil {
		t.Fatalf("emit %v %v", err, ds)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if core.name == "mihomo" {
		path = filepath.Join(dir, "config.yaml")
	}
	if err := os.WriteFile(path, artifact.Bytes, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args := append(append([]string{}, core.checkArgs...), path)
	if core.name == "mihomo" {
		args = append(args, "-d", dir)
	}
	cmd := exec.CommandContext(ctx, core.path, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native config check %v: %s", err, output)
	}
	return path, artifact.Bytes
}

func (core dnsNativeCore) start(t *testing.T, in adapter.EmitInput, env []string) {
	t.Helper()
	path, _ := core.check(t, in, env)
	args := append(append([]string{}, core.runArgs...), path)
	if core.name == "mihomo" {
		args = append(args, "-d", filepath.Dir(path))
	}
	cmd := exec.Command(core.path, args...)
	cmd.Dir = filepath.Dir(path)
	cmd.Env = append(os.Environ(), env...)
	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("core did not stop")
		}
		if t.Failed() {
			t.Logf("native core output: %s", logs.Bytes())
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(in.Preset.LocalListener.Port))
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("native SOCKS listener did not start")
}

type dnsFixture struct {
	conn    *net.UDPConn
	mu      sync.Mutex
	queries map[string]int
	fail    string
}

func newDNSFixture(t *testing.T, fail string) *dnsFixture {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	f := &dnsFixture{conn: conn, queries: map[string]int{}, fail: fail}
	t.Cleanup(func() { conn.Close() })
	go func() {
		packet := make([]byte, 4096)
		for {
			n, peer, err := conn.ReadFromUDP(packet)
			if err != nil {
				return
			}
			response, err := f.answer(packet[:n])
			if err == nil {
				_, _ = conn.WriteToUDP(response, peer)
			}
		}
	}()
	return f
}
func (f *dnsFixture) port() int             { return f.conn.LocalAddr().(*net.UDPAddr).Port }
func (f *dnsFixture) count(name string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.queries[name] }
func (f *dnsFixture) answer(packet []byte) ([]byte, error) {
	var query dnsmessage.Message
	if err := query.Unpack(packet); err != nil {
		return nil, err
	}
	if len(query.Questions) != 1 {
		return nil, fmt.Errorf("one question required")
	}
	q := query.Questions[0]
	name := strings.TrimSuffix(q.Name.String(), ".")
	f.mu.Lock()
	f.queries[name]++
	f.mu.Unlock()
	response := dnsmessage.Message{Header: dnsmessage.Header{ID: query.Header.ID, Response: true, Authoritative: true, RecursionDesired: query.Header.RecursionDesired, RecursionAvailable: true}, Questions: query.Questions}
	if name == f.fail {
		response.Header.RCode = dnsmessage.RCodeNameError
	} else if q.Type == dnsmessage.TypeA {
		response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 30}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}
	}
	return response.Pack()
}

func dnsProbeTarget(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("dns-ok"))
			conn.Close()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port
}

func dnsProbe(t *testing.T, in adapter.EmitInput, name string, port int, wantSuccess bool) {
	t.Helper()
	dialer, err := socksproxy.SOCKS5("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(in.Preset.LocalListener.Port)), nil, &net.Dialer{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	conn, err := dialer.(socksproxy.ContextDialer).DialContext(ctx, "tcp", net.JoinHostPort(name, strconv.Itoa(port)))
	if err == nil {
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
		got := make([]byte, 6)
		_, err = io.ReadFull(conn, got)
		if err == nil && string(got) != "dns-ok" {
			err = fmt.Errorf("unexpected fixture bytes")
		}
	}
	if wantSuccess && err != nil {
		t.Fatalf("DNS probe %s: %v", name, err)
	}
	if !wantSuccess && err == nil {
		t.Fatalf("failed DNS unexpectedly connected: %s", name)
	}
}

func testDNSNativeUDP(t *testing.T, core dnsNativeCore) {
	a, b, final := newDNSFixture(t, "blocked.example.invalid"), newDNSFixture(t, ""), newDNSFixture(t, "")
	in := dnsNativeInput(t)
	for _, item := range []struct {
		id      string
		fixture *dnsFixture
	}{{"a", a}, {"b", b}, {"final", final}} {
		in.DNS.Profile.Resolvers = append(in.DNS.Profile.Resolvers, ir.DNSResolver{ResolverID: item.id, Kind: ir.DNSUDP, Address: "127.0.0.1", Port: item.fixture.port()})
		in.DNS.OutboundTags[item.id] = "direct"
	}
	in.DNS.Profile.FinalResolver = "final"
	in.DNS.Rules = []adapter.RouteRule{
		{Condition: adapter.Condition{Kind: "domain_suffix", Values: []string{"overlap.example.invalid"}}, Target: "a", FieldPath: "/payload/rules/0"},
		{Condition: adapter.Condition{Kind: "domain", Values: []string{"x.overlap.example.invalid"}}, Target: "b", FieldPath: "/payload/rules/1"},
		{Condition: adapter.Condition{Kind: "and", Terms: []adapter.Condition{{Kind: "domain", Values: []string{"yes.and.example.invalid", "outside.example.invalid"}}, {Kind: "or", Terms: []adapter.Condition{{Kind: "domain_suffix", Values: []string{"and.example.invalid"}}, {Kind: "domain", Values: []string{"alternative.example.invalid"}}}}}}, Target: "b", FieldPath: "/payload/rules/2"},
		{Condition: adapter.Condition{Kind: "and", Terms: []adapter.Condition{{Kind: "domain", Values: []string{"contradict.example.invalid"}}, {Kind: "domain_suffix", Values: []string{"unrelated.example.invalid"}}}}, Target: "a", FieldPath: "/payload/rules/3"},
		{Condition: adapter.Condition{Kind: "domain", Values: []string{"blocked.example.invalid"}}, Target: "a", FieldPath: "/payload/rules/4"},
	}
	core.start(t, in, nil)
	port := dnsProbeTarget(t)
	for _, probe := range []struct {
		name     string
		resolver *dnsFixture
	}{{"x.overlap.example.invalid", a}, {"overlap.example.invalid", a}, {"notoverlap.example.invalid", final}, {"yes.and.example.invalid", b}, {"outside.example.invalid", final}, {"contradict.example.invalid", final}, {"unmatched.example.invalid", final}} {
		dnsProbe(t, in, probe.name, port, true)
		for _, server := range []*dnsFixture{a, b, final} {
			if (server.count(probe.name) > 0) != (server == probe.resolver) {
				t.Fatalf("incorrect resolver selected for %s", probe.name)
			}
		}
	}
	dnsProbe(t, in, "blocked.example.invalid", port, false)
	if a.count("blocked.example.invalid") == 0 || b.count("blocked.example.invalid") != 0 || final.count("blocked.example.invalid") != 0 {
		t.Fatal("failure retried through another DNS resolver")
	}
	t.Log("PASS: first-match, suffix root/boundary, AND/rule-set OR, contradiction, explicit final, selected-resolver failure without fallback")
}

func testDNSNativeHTTPS(t *testing.T, core dnsNativeCore, directHostname bool) {
	bootstrap, unused, answers := newDNSFixture(t, ""), newDNSFixture(t, ""), newDNSFixture(t, "")
	cert, ca := dnsFixtureCertificate(t)
	requests := atomic.Int32{}
	requestPath := "/dns-query"
	requestQuery := ""
	if directHostname && core.name != "mihomo" {
		requestPath = "/dns%2Fquery"
	}
	if directHostname && core.name == "xray" {
		requestQuery = "?key=a%26b"
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != requestPath || requestQuery != "" && r.URL.Query().Get("key") != "a&b" {
			t.Errorf("DoH path changed: %s", r.URL.EscapedPath())
			w.WriteHeader(400)
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if r.Method == http.MethodGet {
			data, err = base64.RawURLEncoding.DecodeString(r.URL.Query().Get("dns"))
		}
		if err != nil {
			w.WriteHeader(400)
			return
		}
		reply, err := answers.answer(data)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(reply)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	caPath := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err := os.WriteFile(caPath, ca, 0600); err != nil {
		t.Fatal(err)
	}
	in := dnsNativeInput(t)
	host, portText, _ := net.SplitHostPort(server.Listener.Addr().String())
	port, _ := strconv.Atoi(portText)
	urlHost := "doh.bootstrap.invalid"
	if !directHostname && core.name != "sing-box" {
		urlHost = host
	}
	if directHostname && core.name == "xray" {
		urlHost = "localhost"
	}
	in.DNS.Profile.Bootstrap = []ir.BootstrapResolver{{ResolverID: "boot", Kind: ir.DNSUDP, Address: "127.0.0.1", Port: bootstrap.port()}, {ResolverID: "unused", Kind: ir.DNSUDP, Address: "127.0.0.1", Port: unused.port()}}
	if core.name == "xray" {
		in.DNS.Profile.Bootstrap[0] = ir.BootstrapResolver{ResolverID: "boot", Kind: ir.DNSLocal}
	}
	in.DNS.Profile.Resolvers = []ir.DNSResolver{{ResolverID: "doh", Kind: ir.DNSHTTPS, URL: "https://" + net.JoinHostPort(urlHost, portText) + requestPath + requestQuery, BootstrapResolverID: "boot"}}
	in.DNS.Profile.FinalResolver = "doh"
	in.DNS.OutboundTags["doh"] = "n_dns_exit"
	exitPort, hits := dnsSOCKSRelay(t, host, port)
	node, err := ir.DecodeNode([]byte(`{"schema_version":1,"protocol":"socks5","endpoint":{"host":"127.0.0.1","port":1080},"auth":{"kind":"none"},"transport":{"kind":"native_tcp"},"security":{"mode":"none"},"features":{"udp":false},"extensions":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	node.Endpoint.Port = exitPort
	in.Independents = []adapter.IndependentOutbound{{Tag: "n_dns_exit", Resource: ir.Resource{Payload: &node}}}
	if directHostname {
		in.Independents = nil
		in.DNS.OutboundTags["doh"] = "direct"
	}
	core.start(t, in, []string{"SSL_CERT_FILE=" + caPath, "SSL_CERT_DIR=" + filepath.Dir(caPath)})
	dnsProbe(t, in, "via-doh.example.invalid", dnsProbeTarget(t), true)
	if requests.Load() == 0 || !directHostname && hits.Load() == 0 || answers.count("via-doh.example.invalid") == 0 {
		t.Fatal("HTTPS did not traverse explicit DNS outbound")
	}
	if (core.name == "sing-box" || core.name == "mihomo" && directHostname) && bootstrap.count("doh.bootstrap.invalid") == 0 {
		t.Fatal("explicit UDP bootstrap unused")
	}
	if unused.count("doh.bootstrap.invalid") != 0 || bootstrap.count("via-doh.example.invalid") != 0 {
		t.Fatal("bootstrap leaked into business DNS")
	}
	t.Logf("PASS: TLS-verified local DoH, explicit DNS outbound, independent bootstrap; direct=%v, host=%s", directHostname, urlHost)
}

func dnsFixtureCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "DNS fixture"}, DNSNames: []string{"doh.bootstrap.invalid", "localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(pemCert, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
	if err != nil {
		t.Fatal(err)
	}
	return pair, pemCert
}

func dnsSOCKSRelay(t *testing.T, allowedHost string, allowedPort int) (int, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	hits := new(atomic.Int32)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				header := make([]byte, 2)
				if _, err := io.ReadFull(conn, header); err != nil {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(conn, methods); err != nil {
					return
				}
				_, _ = conn.Write([]byte{5, 0})
				request := make([]byte, 4)
				if _, err := io.ReadFull(conn, request); err != nil || request[0] != 5 || request[1] != 1 {
					return
				}
				var host string
				switch request[3] {
				case 1:
					ip := make([]byte, 4)
					if _, err := io.ReadFull(conn, ip); err != nil {
						return
					}
					host = net.IP(ip).String()
				case 4:
					ip := make([]byte, 16)
					if _, err := io.ReadFull(conn, ip); err != nil {
						return
					}
					host = net.IP(ip).String()
				case 3:
					length := make([]byte, 1)
					if _, err := io.ReadFull(conn, length); err != nil {
						return
					}
					name := make([]byte, int(length[0]))
					if _, err := io.ReadFull(conn, name); err != nil {
						return
					}
					host = string(name)
				default:
					return
				}
				portBytes := make([]byte, 2)
				if _, err := io.ReadFull(conn, portBytes); err != nil {
					return
				}
				port := int(binary.BigEndian.Uint16(portBytes))
				if host != allowedHost || port != allowedPort {
					t.Errorf("DNS exit received unbootstrapped or wrong destination %s:%d, want %s:%d", host, port, allowedHost, allowedPort)
					return
				}
				remote, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), time.Second)
				if err != nil {
					return
				}
				defer remote.Close()
				hits.Add(1)
				_, _ = conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				go func() { _, _ = io.Copy(remote, conn) }()
				_, _ = io.Copy(conn, remote)
			}()
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, hits
}
