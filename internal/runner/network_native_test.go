package runner

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/networktest"
	coreexec "github.com/Runarry/ProxyLoom/internal/runner/exec"
	"github.com/Runarry/ProxyLoom/internal/runnerprotocol"
)

func TestMain(m *testing.M) {
	if handled, code := coreexec.SandboxEntrypoint(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

func nativeSOCKS(t *testing.T, onlySource, source string, password ...string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	connections := map[net.Conn]bool{}
	stop := func() { listener.Close() }
	t.Cleanup(func() {
		stop()
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
				host, _, _ := net.SplitHostPort(conn.RemoteAddr().String())
				if onlySource != "" && host != onlySource {
					return
				}
				conn.SetDeadline(time.Now().Add(30 * time.Second))
				h := make([]byte, 2)
				if _, err := io.ReadFull(conn, h); err != nil {
					return
				}
				methods := make([]byte, int(h[1]))
				if _, err := io.ReadFull(conn, methods); err != nil {
					return
				}
				if len(password) == 0 {
					conn.Write([]byte{5, 0})
				} else {
					conn.Write([]byte{5, 2})
					h = make([]byte, 2)
					if _, err := io.ReadFull(conn, h); err != nil || h[0] != 1 || h[1] == 0 {
						return
					}
					username := make([]byte, int(h[1]))
					if _, err := io.ReadFull(conn, username); err != nil {
						return
					}
					length := make([]byte, 1)
					if _, err := io.ReadFull(conn, length); err != nil || length[0] == 0 {
						return
					}
					provided := make([]byte, int(length[0]))
					if _, err := io.ReadFull(conn, provided); err != nil {
						return
					}
					if string(username) != "fixture" || string(provided) != password[0] {
						conn.Write([]byte{1, 1})
						return
					}
					conn.Write([]byte{1, 0})
				}
				h = make([]byte, 4)
				if _, err := io.ReadFull(conn, h); err != nil {
					return
				}
				size := 4
				if h[3] == 4 {
					size = 16
				}
				if h[3] != 1 && h[3] != 4 {
					return
				}
				raw := make([]byte, size+2)
				if _, err := io.ReadFull(conn, raw); err != nil {
					return
				}
				ip, ok := netip.AddrFromSlice(raw[:size])
				if !ok || !ip.IsLoopback() {
					return
				}
				port := binary.BigEndian.Uint16(raw[size:])
				dialer := net.Dialer{Timeout: time.Second, LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}
				remote, err := dialer.Dial("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(int(port))))
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
	return listener.Addr().String(), stop
}
func nativeNode(id ir.ID, address string) ir.Resource {
	host, port, _ := net.SplitHostPort(address)
	number, _ := strconv.Atoi(port)
	udp := false
	return ir.Resource{Metadata: ir.Metadata{ResourceID: id, ScopeID: "74000000-0000-4000-8000-000000000020", Kind: ir.KindNode, Revision: 1, SchemaVersion: 1, Name: "synthetic SOCKS node", Tags: []string{}, Enabled: true, SecurityEpoch: 1}, Payload: &ir.Node{SchemaVersion: 1, Protocol: ir.SOCKS5, Endpoint: ir.Endpoint{Host: host, Port: number}, Auth: &ir.NoAuth{Kind: ir.AuthNone}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}, Features: ir.Features{UDP: &udp}, Extensions: ir.Extensions{}}}
}
func TestNativeM3ThreeCoreNetwork(t *testing.T) {
	root := os.Getenv("PROXYLOOM_RUNNER_REAL_CORES")
	if root == "" || runtime.GOOS != "linux" {
		t.Skip("requires the controlled Linux native verifier")
	}
	cores, err := capability.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, build := range cores.Builds() {
		if build.Arch != runtime.GOARCH {
			continue
		}
		t.Run(string(build.Family), func(t *testing.T) {
			a, stopA := nativeSOCKS(t, "", "127.0.0.2", "native-fixture-valid")
			b, _ := nativeSOCKS(t, "127.0.0.2", "127.0.0.3")
			n1 := nativeNode("74000000-0000-4000-8000-000000000011", a)
			n1.Payload.(*ir.Node).Auth = &ir.UsernamePasswordAuth{Kind: ir.AuthUsernamePassword, Username: "fixture", Password: ir.Secret("native-fixture-valid")}
			n2 := nativeNode("74000000-0000-4000-8000-000000000012", b)
			chain := ir.Resource{Metadata: n1.Metadata, Payload: &ir.Chain{SchemaVersion: 1, Hops: []ir.NodeRef{{NodeID: n1.Metadata.ResourceID}, {NodeID: n2.Metadata.ResourceID}}, FailurePolicy: ir.FailClosed}}
			chain.Metadata.ResourceID = "74000000-0000-4000-8000-000000000013"
			chain.Metadata.Kind = ir.KindChain
			resources := map[ir.ID]ir.Resource{n1.Metadata.ResourceID: n1, n2.Metadata.ResourceID: n2, chain.Metadata.ResourceID: chain}
			addresses := map[ir.ID]string{n1.Metadata.ResourceID: "127.0.0.1", n2.Metadata.ResourceID: "127.0.0.1"}
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/unhealthy" {
					w.WriteHeader(503)
					return
				}
				if r.Method == http.MethodHead {
					return
				}
				if r.URL.Path == "/status-error" {
					w.WriteHeader(502)
					return
				}
				if r.URL.Path == "/mismatch" {
					io.WriteString(w, "unexpected-body")
					return
				}
				if r.URL.Path == "/download" {
					w.Header().Set("Content-Length", strconv.Itoa(2<<20))
					buffer := make([]byte, 16384)
					for range 128 {
						if _, err := w.Write(buffer); err != nil {
							return
						}
						w.(http.Flusher).Flush()
						time.Sleep(20 * time.Millisecond)
					}
					return
				}
				io.WriteString(w, "controlled-ok")
			}))
			defer target.Close()
			probe := func(subject ir.Resource, kind, path string, maximum int64, override ...runnerprotocol.Limits) runnerprotocol.ResultRequest {
				artifact, endpoints, err := networktest.Compile(build.Family, "74000000-0000-4000-8000-000000000030", subject, resources, addresses)
				if err != nil {
					t.Fatal(err)
				}
				lease := clientLease(t)
				lease.Core = runnerprotocol.CoreIdentity{CoreBuildID: build.ID, CoreFamily: build.Family, Version: build.Version, BuildSHA256: build.BinarySHA256, Platform: build.OS, Architecture: build.Arch, AdapterVersion: build.AdapterVersion}
				lease.Type = kind
				lease.Artifact = runnerprotocol.Artifact{ArtifactID: artifact.SnapshotID, Format: map[ir.CoreFamily]ir.OutputFormat{ir.Xray: ir.XrayJSON, ir.SingBox: ir.SingBoxJSON, ir.Mihomo: ir.MihomoYAML}[build.Family], SHA256: runnerprotocol.Digest(artifact.Bytes), ByteLength: int64(len(artifact.Bytes)), ContentBase64: base64.StdEncoding.EncodeToString(artifact.Bytes)}
				clear(artifact.Bytes)
				lease.Subject = &runnerprotocol.FrozenSubject{Kind: subject.Metadata.Kind, ID: subject.Metadata.ResourceID, Revision: 1, SecurityEpoch: 1}
				lease.Dependencies = []runnerprotocol.FrozenSubject{*lease.Subject}
				if subject.Metadata.Kind == ir.KindChain {
					for _, node := range []ir.Resource{n1, n2} {
						lease.Dependencies = append(lease.Dependencies, runnerprotocol.FrozenSubject{Kind: ir.KindNode, ID: node.Metadata.ResourceID, Revision: 1, SecurityEpoch: 1})
					}
				}
				lease.ApprovedEndpoints = endpoints
				lease.MinimumSampleBytes = 1 << 20
				lease.QuotaReservationID = "74000000-0000-4000-8000-000000000031"
				lease.Limits = runnerprotocol.Limits{DurationMS: 10000, MaxBytes: maximum}
				if len(override) > 0 {
					lease.Limits = override[0]
				}
				lease.ExecutionPolicy.Network = "controlled_target_only"
				lease.ExecutionPolicy.ProcessLimit = 128
				u, _ := url.Parse(target.URL)
				lease.TestTarget = &runnerprotocol.FrozenTestTarget{TestTargetID: "74000000-0000-4000-8000-000000000032", Revision: 1, URL: target.URL + path, ValidatedIPs: []string{u.Hostname()}, ExpectedResponse: runnerprotocol.HTTPExpectation{StatusCodes: []int{200}, MaxResponseBytes: maximum}, RedirectPolicy: "deny", VerifyCertificate: true, Compression: "disabled"}
				if path == "/mismatch" {
					lease.TestTarget.ExpectedResponse.BodySHA256 = runnerprotocol.Digest([]byte("controlled-ok"))
				}
				lease.PayloadSHA256, _ = runnerprotocol.PayloadHash(lease.FrozenPayload)
				client, _ := testClient(t, lease, coreexec.ValidateIsolated, false, nil)
				client.registry = coreexec.DiskRegistry{Root: root, Catalog: cores}
				client.start = func(ctx context.Context, registry coreexec.Registry, id ir.ID, config []byte, policy coreexec.SandboxPolicy, started func(int)) (coreexec.Result, error) {
					r, e := coreexec.StartIsolated(ctx, registry, id, config, policy, started)
					if e != nil || !r.Canceled {
						// This fixture contains only synthetic loopback endpoints.
						t.Logf("synthetic runtime: exit=%d err=%v output=%s", r.ExitCode, e, r.Output)
					}
					return r, e
				}
				client.config.NetworkEnabled = true
				client.networkPolicy = networktest.Resolver{AllowNets: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}}
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				defer cancel()
				result, err := client.runNetwork(ctx, lease)
				if err != nil {
					t.Fatal(err)
				}
				return result
			}
			for _, subject := range []ir.Resource{n1, chain} {
				result := probe(subject, "connectivity", "/check", 1024)
				if result.State != "succeeded" || result.Verdict != "pass" {
					t.Fatalf("real network probe failed: state=%s verdict=%s error=%v", result.State, result.Verdict, result.Error)
				}
			}
			for _, path := range []string{"/status-error", "/mismatch", "/unhealthy"} {
				result := probe(chain, "connectivity", path, 1024)
				want := "fail"
				if path == "/unhealthy" {
					want = "inconclusive"
				}
				if result.State != "succeeded" || result.Verdict != want {
					t.Fatalf("target negative %s: state=%s verdict=%s error=%v", path, result.State, result.Verdict, result.Error)
				}
			}
			n1.Payload.(*ir.Node).Auth.(*ir.UsernamePasswordAuth).Password = ir.Secret("native-fixture-wrong")
			wrong := probe(chain, "connectivity", "/check", 1024)
			if wrong.State != "succeeded" || wrong.Verdict != "fail" {
				t.Fatalf("wrong authentication was not rejected: state=%s verdict=%s", wrong.State, wrong.Verdict)
			}
			n1.Payload.(*ir.Node).Auth.(*ir.UsernamePasswordAuth).Password = ir.Secret("native-fixture-valid")
			result := probe(chain, "download_throughput", "/download", 1<<20)
			if result.Verdict != "pass" || result.Metrics.ThroughputMbps == nil || *result.Metrics.BodyBytes != 1<<20 || result.Observation.TruncatedBy != "bytes" {
				t.Fatalf("real bounded download failed: state=%s verdict=%s error=%v", result.State, result.Verdict, result.Error)
			}
			limited := probe(chain, "download_throughput", "/download", 1<<20, runnerprotocol.Limits{DurationMS: 700, MaxBytes: 1 << 20})
			if limited.State != "succeeded" || limited.Observation.TruncatedBy != "duration" || limited.Metrics.ThroughputMbps != nil {
				t.Fatalf("time-limited sample was not bounded or was overstated: state=%s verdict=%s error=%v", limited.State, limited.Verdict, limited.Error)
			}
			stopA()
			result = probe(chain, "connectivity", "/check", 1024)
			if result.Verdict != "fail" {
				t.Fatalf("broken first hop bypassed: verdict=%s", result.Verdict)
			}
		})
	}
}
