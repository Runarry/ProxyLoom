package networktest

import (
	"context"
	"crypto/x509"
	"encoding/binary"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureSOCKS(t *testing.T, reject bool) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		for c := range connections {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	wg.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			connections[conn] = true
			mu.Unlock()
			wg.Go(func() {
				defer func() { conn.Close(); mu.Lock(); delete(connections, conn); mu.Unlock() }()
				conn.SetDeadline(time.Now().Add(10 * time.Second))
				header := make([]byte, 2)
				if _, err := io.ReadFull(conn, header); err != nil {
					return
				}
				methods := make([]byte, int(header[1]))
				if _, err := io.ReadFull(conn, methods); err != nil {
					return
				}
				if _, err := conn.Write([]byte{5, 0}); err != nil {
					return
				}
				header = make([]byte, 4)
				if _, err := io.ReadFull(conn, header); err != nil {
					return
				}
				size := 4
				if header[3] == 4 {
					size = 16
				}
				if header[3] != 1 && header[3] != 4 {
					return
				}
				address := make([]byte, size+2)
				if _, err := io.ReadFull(conn, address); err != nil {
					return
				}
				if reject {
					conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				ip, ok := netip.AddrFromSlice(address[:size])
				if !ok || !ip.IsLoopback() {
					return
				}
				remote, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(int(binary.BigEndian.Uint16(address[size:])))), time.Second)
				if err != nil {
					return
				}
				defer remote.Close()
				conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
				done := make(chan struct{})
				go func() { io.Copy(remote, conn); remote.Close(); close(done) }()
				io.Copy(conn, remote)
				conn.Close()
				<-done
			})
		}
	})
	return listener.Addr().String()
}
func probePayload(serverURL, kind string) runnerprotocol.FrozenPayload {
	u, _ := url.Parse(serverURL)
	u.Host = net.JoinHostPort("unresolvable.fixture.invalid", u.Port())
	return runnerprotocol.FrozenPayload{Type: kind, Limits: runnerprotocol.Limits{DurationMS: 10000, MaxBytes: 20 << 20}, MinimumSampleBytes: 1 << 20, TestTarget: &runnerprotocol.FrozenTestTarget{URL: u.String(), ValidatedIPs: []string{"127.0.0.1"}, ExpectedResponse: runnerprotocol.HTTPExpectation{StatusCodes: []int{200}, MaxResponseBytes: 20 << 20}}}
}
func TestPinnedSOCKSConnectivityAndNoDirectFallback(t *testing.T) {
	var gets, heads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			heads.Add(1)
			return
		}
		gets.Add(1)
		if r.Header.Get("Accept-Encoding") != "identity" || r.Header.Get("Cache-Control") == "" {
			t.Error("missing sample controls")
		}
		io.WriteString(w, "controlled-ok")
	}))
	defer server.Close()
	p := probePayload(server.URL, "connectivity")
	r := Probe(context.Background(), p, ProbeOptions{SOCKSAddress: fixtureSOCKS(t, false)})
	if r.Verdict != "pass" || *r.Metrics.SampleCount != 3 || *r.Metrics.FailureCount != 0 || gets.Load() != 3 || r.Metrics.ThroughputMbps != nil {
		t.Fatalf("unexpected connectivity outcome: %s", r.Verdict)
	}
	r = Probe(context.Background(), p, ProbeOptions{SOCKSAddress: fixtureSOCKS(t, true)})
	if r.Verdict != "fail" || gets.Load() != 3 || heads.Load() != 1 {
		t.Fatal("failed SOCKS probe fell back to direct GET or lost target health separation")
	}
}
func TestDownloadByteCapAndInsufficientSample(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(2<<20))
		buffer := make([]byte, 32<<10)
		for range 64 {
			if _, err := w.Write(buffer); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	p := probePayload(server.URL, "download_throughput")
	p.Limits.MaxBytes = 65539
	r := Probe(context.Background(), p, ProbeOptions{SOCKSAddress: fixtureSOCKS(t, false)})
	if *r.Metrics.BodyBytes != 65539 || r.TruncatedBy != "bytes" || r.Verdict != "inconclusive" || r.Metrics.ThroughputMbps != nil || r.Error.Code != "INSUFFICIENT_SAMPLE" {
		t.Fatal("byte cap or small-sample rule failed")
	}
}

func TestHTTPSCertificateResponseAndEgressChecks(t *testing.T) {
	var body atomic.Value
	body.Store("192.0.2.40\n")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			return
		}
		io.WriteString(w, body.Load().(string))
	}))
	defer server.Close()
	p := probePayload(server.URL, "connectivity")
	p.TestTarget.URL = server.URL // the generated certificate covers 127.0.0.1
	p.TestTarget.ExpectedResponse.EgressIPResponse = true
	options := ProbeOptions{SOCKSAddress: fixtureSOCKS(t, false), RootCAs: x509.NewCertPool()}
	r := Probe(context.Background(), p, options)
	if r.Verdict == "pass" || r.Metrics.EgressIP != nil {
		t.Fatal("untrusted HTTPS certificate accepted")
	}
	options.RootCAs.AddCert(server.Certificate())
	r = Probe(context.Background(), p, options)
	if r.Verdict != "pass" || r.Metrics.EgressIP == nil || *r.Metrics.EgressIP != "192.0.2.40" || r.Metrics.TargetTLSMS == nil {
		t.Fatal("pinned verified HTTPS sample failed")
	}
	body.Store(`{"ip":"192.0.2.40"}`)
	r = Probe(context.Background(), p, options)
	if r.Verdict != "fail" || r.Error.Code != "HTTP_EXPECTATION_FAILED" || r.Metrics.EgressIP != nil {
		t.Fatal("accepted an arbitrary egress response format")
	}
	p.TestTarget.ExpectedResponse.EgressIPResponse = false
	p.TestTarget.ExpectedResponse.BodySHA256 = runnerprotocol.Digest([]byte("different body"))
	r = Probe(context.Background(), p, options)
	if r.Verdict != "fail" || r.Error.Code != "HTTP_EXPECTATION_FAILED" {
		t.Fatal("response digest mismatch accepted")
	}
}
func TestDownloadDurationCapUsesMeasuredBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buffer := make([]byte, 64<<10)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if _, err := w.Write(buffer); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer server.Close()
	p := probePayload(server.URL, "download_throughput")
	p.Limits.DurationMS = 1200
	start := time.Now()
	r := Probe(context.Background(), p, ProbeOptions{SOCKSAddress: fixtureSOCKS(t, false)})
	if time.Since(start) > 3*time.Second || r.TruncatedBy != "duration" || r.Verdict != "pass" || r.Metrics.ThroughputMbps == nil || runnerprotocol.ValidateMetrics(r.Metrics) != nil {
		t.Fatalf("duration limit or measured throughput failed: %s %s", r.Verdict, r.TruncatedBy)
	}
}

type fixedResolver struct{ ips []netip.Addr }

func (r fixedResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r.ips, nil
}
func TestAddressPolicyRejectsMixedAnswersAndProtectedIPs(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "169.254.169.254", "10.0.0.1", "::ffff:10.0.0.1", "100.64.0.1"} {
		if _, err := (Resolver{}).Resolve(context.Background(), host); err == nil {
			t.Fatalf("accepted protected address %s", host)
		}
	}
	r := Resolver{DNS: fixedResolver{[]netip.Addr{netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("10.0.0.1")}}}
	if _, err := r.Resolve(context.Background(), "fixture.invalid"); err == nil {
		t.Fatal("accepted a mixed public/private answer")
	}
}

func TestProxyEndpointAllowlistDoesNotApplyToTestTarget(t *testing.T) {
	r := Resolver{ProxyAllowNets: []netip.Prefix{netip.MustParsePrefix("192.168.5.0/24")}}
	if values, err := r.ResolveEndpoint(context.Background(), "192.168.5.65"); err != nil || len(values) != 1 || values[0] != "192.168.5.65" {
		t.Fatalf("allowlisted proxy endpoint rejected: %v %#v", err, values)
	}
	if _, err := r.Resolve(context.Background(), "192.168.5.65"); err == nil {
		t.Fatal("private test target inherited the proxy allowlist")
	}
}
