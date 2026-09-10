package compiler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"golang.org/x/net/proxy"
	"gopkg.in/yaml.v3"
)

type nativeM1Result struct {
	Family         ir.CoreFamily `json:"family"`
	BuildID        ir.ID         `json:"build_id"`
	BuildSHA256    string        `json:"build_sha256"`
	Fixture        string        `json:"fixture"`
	ArtifactSHA256 string        `json:"artifact_sha256"`
	Check          string        `json:"check"`
	Negative       string        `json:"negative"`
}

func m1Core(t *testing.T, family ir.CoreFamily) (string, capability.Build) {
	t.Helper()
	if os.Getenv("PROXYLOOM_M1_NATIVE") != "1" {
		t.Skip("requires isolated pinned linux/amd64 cores")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Fatal("native evidence requires linux/amd64")
	}
	catalog, err := capability.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range catalog.Builds() {
		if b.Family != family || b.OS != "linux" || b.Arch != "amd64" {
			continue
		}
		path := filepath.Join(os.Getenv("PROXYLOOM_CORES_ROOT"), string(b.Family), b.GitTag, b.Arch, b.BinaryName)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != b.BinarySHA256 {
			t.Fatal("pinned binary digest mismatch")
		}
		return path, b
	}
	t.Fatal("pinned native build missing")
	return "", capability.Build{}
}

func m1Args(family ir.CoreFamily, path string, check bool) []string {
	switch family {
	case ir.Xray:
		if check {
			return []string{"run", "-test", "-c", path}
		}
		return []string{"run", "-c", path}
	case ir.SingBox:
		if check {
			return []string{"check", "-c", path}
		}
		return []string{"run", "-c", path}
	default:
		args := []string{"-d", filepath.Dir(path), "-f", path}
		if check {
			args = append([]string{"-t"}, args...)
		}
		return args
	}
}

func m1Report(t *testing.T, name string, data any) {
	t.Helper()
	if dir := os.Getenv("PROXYLOOM_M1_REPORT"); dir != "" {
		encoded, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), append(encoded, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestM1NativeConfigMatrix(t *testing.T) {
	var results []nativeM1Result
	for _, family := range []ir.CoreFamily{ir.Xray, ir.SingBox, ir.Mihomo} {
		binary, build := m1Core(t, family)
		ext := ".json"
		if family == ir.Mihomo {
			ext = ".yaml"
		}
		paths, err := filepath.Glob(filepath.Join("..", "..", "fixtures", "compiler", "native-p0", "*-"+string(family)+ext))
		if err != nil || len(paths) < 15 {
			t.Fatalf("native golden matrix missing for %s", family)
		}
		for _, source := range paths {
			t.Run(filepath.Base(source), func(t *testing.T) {
				data, err := os.ReadFile(source)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "config"+ext)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				output, err := exec.CommandContext(ctx, binary, m1Args(family, path, true)...).CombinedOutput()
				if err != nil {
					t.Fatalf("native check rejected %s: %s", filepath.Base(source), output)
				}
				var doc map[string]any
				if family == ir.Mihomo {
					if err := yaml.Unmarshal(data, &doc); err != nil {
						t.Fatal(err)
					}
					doc["proxies"].([]any)[0].(map[string]any)["type"] = "invalid-protocol"
					data, err = yaml.Marshal(doc)
				} else {
					if err := json.Unmarshal(data, &doc); err != nil {
						t.Fatal(err)
					}
					field := "type"
					if family == ir.Xray {
						field = "protocol"
					}
					doc["outbounds"].([]any)[0].(map[string]any)[field] = "invalid-protocol"
					data, err = json.Marshal(doc)
				}
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := exec.CommandContext(ctx, binary, m1Args(family, path, true)...).Run(); err == nil {
					t.Fatal("invalid protocol passed native config check")
				}
				original, _ := os.ReadFile(source)
				digest := sha256.Sum256(original)
				results = append(results, nativeM1Result{Family: family, BuildID: build.ID, BuildSHA256: build.BinarySHA256, Fixture: filepath.Base(source), ArtifactSHA256: hex.EncodeToString(digest[:]), Check: "pass", Negative: "invalid protocol rejected"})
			})
		}
	}
	if !t.Failed() {
		m1Report(t, "native-config-matrix.json", results)
	}
}

// The local CONNECT relays expose which selected proxy carried the request.
// Their destination is always the test's loopback endpoint; no external host
// or public health-check service can be contacted by this fixture.
func m1Relay(t *testing.T, destination string, count *atomic.Int64, aliases ...map[string]string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", 405)
			return
		}
		remote := destination
		for _, alias := range aliases {
			if address, ok := alias[r.Host]; ok {
				remote = address
			}
		}
		up, err := net.DialTimeout("tcp", remote, time.Second)
		if err != nil {
			http.Error(w, "unavailable", 502)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			up.Close()
			return
		}
		if remote == destination {
			count.Add(1)
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { defer up.Close(); defer conn.Close(); _, _ = io.Copy(up, conn) }()
		go func() { defer up.Close(); defer conn.Close(); _, _ = io.Copy(conn, up) }()
	}))
	t.Cleanup(server.Close)
	return server
}

func m1RuntimeSpec(t *testing.T, family ir.CoreFamily, strategy ir.PolicyStrategy, relayA, relayB string) ir.FrozenInputSpec {
	t.Helper()
	spec := policySpec(t, strategy)
	first := spec.Resources[0].Metadata.ResourceID
	second := ir.ID("22222222-2222-4222-8222-222222222222")
	for i, address := range []string{relayA, relayB} {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := strconv.Atoi(port)
		resource := spec.Resources[0]
		clone := *resource.Payload.(*ir.Node)
		resource.Payload = &clone
		if i == 1 {
			resource.Metadata.ResourceID = second
		}
		node := resource.Payload.(*ir.Node)
		node.Protocol, node.Endpoint = ir.HTTP, ir.Endpoint{Host: host, Port: n}
		node.Auth, node.Security, node.Transport = &ir.NoAuth{Kind: ir.AuthNone}, &ir.NoSecurity{Mode: ir.SecurityNone}, &ir.NativeTCPTransport{Kind: ir.NativeTCP}
		if i == 0 {
			spec.Resources[0] = resource
		} else {
			spec.Resources = append(spec.Resources, resource)
		}
	}
	for _, r := range spec.Resources {
		if p, ok := r.Payload.(*ir.PolicyGroup); ok {
			p.Members = []ir.TargetRef{{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: first}, {Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: second}}
			p.DefaultMember = p.Members[0]
		}
		if p, ok := r.Payload.(*ir.ClientPreset); ok && p.CoreFamily == family && strategy == ir.PolicyManualSelect {
			port := ir.SingBoxControlPort
			if family == ir.Mihomo {
				port = ir.MihomoControlPort
			}
			p.ControlAPI = ir.ControlAPIPreset{Enabled: true, Listen: ir.PresetLoopbackAddress, Port: port}
		}
	}
	return spec
}

func m1Start(t *testing.T, binary string, family ir.CoreFamily, data []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if family == ir.Mihomo {
		path += ".yaml"
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	check, err := exec.Command(binary, m1Args(family, path, true)...).CombinedOutput()
	if err != nil {
		t.Fatalf("runtime config check: %s", check)
	}
	logfile, err := os.CreateTemp(t.TempDir(), "native-log-")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, m1Args(family, path, false)...)
	cmd.Stdout, cmd.Stderr = logfile, logfile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("core not reaped")
		}
		_ = logfile.Close()
	})
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:1080", 100*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	contents, _ := os.ReadFile(logfile.Name())
	t.Fatalf("native listener did not start: %s", contents)
}

func m1Request(url string) error {
	dialer, err := proxy.SOCKS5("tcp", "127.0.0.1:1080", nil, &net.Dialer{Timeout: time.Second})
	if err != nil {
		return err
	}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.(proxy.ContextDialer).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 2 * time.Second}).Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err == nil && string(body) != "controlled fixture" {
		return fmt.Errorf("unexpected fixture response")
	}
	return err
}

func TestM1NativeManualSelectionAndFailure(t *testing.T) {
	var results []map[string]any
	for _, family := range []ir.CoreFamily{ir.SingBox, ir.Mihomo} {
		t.Run(string(family), func(t *testing.T) {
			binary, build := m1Core(t, family)
			var requests, countA, countB atomic.Int64
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.WriteString(w, "controlled fixture")
			}))
			defer endpoint.Close()
			a := m1Relay(t, endpoint.Listener.Addr().String(), &countA)
			b := m1Relay(t, endpoint.Listener.Addr().String(), &countB)
			spec := m1RuntimeSpec(t, family, ir.PolicyManualSelect, a.Listener.Addr().String(), b.Listener.Addr().String())
			input, err := ir.NewFrozenInput(spec)
			if err != nil {
				t.Fatal(err)
			}
			var target ir.Target
			for _, v := range input.Spec().Targets {
				if v.CoreFamily == family {
					target = v
				}
			}
			c := mustCompiler(t)
			graph, _, err := c.Prepare(input, target)
			if err != nil {
				t.Fatal(err)
			}
			artifact, _, err := c.Compile(context.Background(), input, target)
			if err != nil {
				t.Fatal(err)
			}
			m1Start(t, binary, family, artifact.Bytes)
			port := ir.SingBoxControlPort
			if family == ir.Mihomo {
				port = ir.MihomoControlPort
			}
			controller := "http://127.0.0.1:" + strconv.Itoa(port)
			group := graph.Policies[0]
			client := &http.Client{Timeout: time.Second}
			for deadline := time.Now().Add(3 * time.Second); ; {
				resp, e := client.Get(controller + "/proxies/" + group.Tag)
				if e == nil {
					resp.Body.Close()
					break
				}
				if time.Now().After(deadline) {
					t.Fatal(e)
				}
				time.Sleep(25 * time.Millisecond)
			}
			if err := m1Request(endpoint.URL); err != nil {
				t.Fatal(err)
			}
			if countA.Load() != 1 || countB.Load() != 0 {
				t.Fatal("startup default was not used")
			}
			next := group.Members[1]
			payload, _ := json.Marshal(map[string]string{"name": next})
			req, _ := http.NewRequest(http.MethodPut, controller+"/proxies/"+group.Tag, bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != 204 {
				t.Fatalf("selection returned %d", resp.StatusCode)
			}
			if err := m1Request(endpoint.URL); err != nil {
				t.Fatal(err)
			}
			if countA.Load() != 1 || countB.Load() != 1 {
				t.Fatal("runtime selection did not move traffic")
			}
			for _, origin := range []string{controller, "http://external.invalid"} {
				req, _ := http.NewRequest(http.MethodGet, controller+"/proxies/"+group.Tag, nil)
				req.Header.Set("Origin", origin)
				resp, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				allow := resp.Header.Get("Access-Control-Allow-Origin")
				if origin == controller && allow != controller {
					t.Fatal("own origin missing")
				}
				if origin != controller && allow != "" {
					t.Fatal("external origin permitted")
				}
			}
			b.Close()
			if err := m1Request(endpoint.URL); err == nil {
				t.Fatal("unavailable selected member fell back")
			}
			if countA.Load() != 1 || requests.Load() != 2 {
				t.Fatal("failed selection bypassed proxy")
			}
			digest := sha256.Sum256(artifact.Bytes)
			results = append(results, map[string]any{"family": family, "build_id": build.ID, "build_sha256": build.BinarySHA256, "artifact_sha256": hex.EncodeToString(digest[:]), "manual_switch": "pass", "startup_default": "pass", "selected_unavailable": "fail_closed", "cors": "own origin only", "direct_bypass": "none"})
		})
	}
	if !t.Failed() {
		m1Report(t, "native-manual-selection.json", results)
	}
}

func TestM1NativeRoundRobinAndAllUnavailable(t *testing.T) {
	var results []map[string]any
	for _, family := range []ir.CoreFamily{ir.Xray, ir.Mihomo} {
		t.Run(string(family), func(t *testing.T) {
			binary, build := m1Core(t, family)
			var requests, countA, countB, probes atomic.Int64
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				_, _ = io.WriteString(w, "controlled fixture")
			}))
			defer endpoint.Close()
			health := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { probes.Add(1); w.WriteHeader(204) }))
			defer health.Close()
			certPath := filepath.Join(t.TempDir(), "fixture-ca.pem")
			if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: health.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SSL_CERT_FILE", certPath)
			aliases := map[string]string{health.Listener.Addr().String(): health.Listener.Addr().String()}
			a := m1Relay(t, endpoint.Listener.Addr().String(), &countA, aliases)
			b := m1Relay(t, endpoint.Listener.Addr().String(), &countB, aliases)
			spec := m1RuntimeSpec(t, family, ir.PolicyRoundRobin, a.Listener.Addr().String(), b.Listener.Addr().String())
			if family == ir.Mihomo {
				for _, r := range spec.Resources {
					if p, ok := r.Payload.(*ir.PolicyGroup); ok {
						interval, timeout, tolerance := 1000, 500, 0
						p.HealthCheck = ir.PolicyHealthCheck{Enabled: true, URL: health.URL, IntervalMS: &interval, TimeoutMS: &timeout, ToleranceMS: &tolerance}
					}
				}
			}
			input, err := ir.NewFrozenInput(spec)
			if err != nil {
				t.Fatal(err)
			}
			var target ir.Target
			for _, v := range input.Spec().Targets {
				if v.CoreFamily == family {
					target = v
				}
			}
			artifact, _, err := mustCompiler(t).Compile(context.Background(), input, target)
			if err != nil {
				t.Fatal(err)
			}
			m1Start(t, binary, family, artifact.Bytes)
			if family == ir.Mihomo {
				for deadline := time.Now().Add(5 * time.Second); probes.Load() < 2; {
					if time.Now().After(deadline) {
						t.Fatal("explicit local health probes never succeeded")
					}
					time.Sleep(50 * time.Millisecond)
				}
			}
			for i := 0; i < 6; i++ {
				if err := m1Request(endpoint.URL); err != nil {
					t.Fatal(err)
				}
			}
			if countA.Load() != 3 || countB.Load() != 3 || requests.Load() != 6 {
				t.Fatalf("round-robin distribution A=%d B=%d requests=%d", countA.Load(), countB.Load(), requests.Load())
			}
			a.Close()
			b.Close()
			if err := m1Request(endpoint.URL); err == nil {
				t.Fatal("all-down policy accepted traffic")
			}
			if requests.Load() != 6 {
				t.Fatal("all-down policy bypassed relays")
			}
			if family == ir.Xray && probes.Load() != 0 {
				t.Fatal("health-disabled Xray unexpectedly probed")
			}
			digest := sha256.Sum256(artifact.Bytes)
			results = append(results, map[string]any{"family": family, "build_id": build.ID, "build_sha256": build.BinarySHA256, "artifact_sha256": hex.EncodeToString(digest[:]), "round_robin": "3 requests per member", "all_unavailable": "fail_closed", "direct_bypass": "none", "health_destination": "local fixture only"})
		})
	}
	if !t.Failed() {
		m1Report(t, "native-round-robin.json", results)
	}
}

func TestM1NativeRoutingANDOROrder(t *testing.T) {
	var results []map[string]any
	for _, family := range []ir.CoreFamily{ir.Xray, ir.SingBox, ir.Mihomo} {
		t.Run(string(family), func(t *testing.T) {
			binary, build := m1Core(t, family)
			var countA, countB atomic.Int64
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "controlled fixture") }))
			defer endpoint.Close()
			a := m1Relay(t, endpoint.Listener.Addr().String(), &countA)
			b := m1Relay(t, endpoint.Listener.Addr().String(), &countB)
			spec := m1RuntimeSpec(t, family, ir.PolicyFixed, a.Listener.Addr().String(), b.Listener.Addr().String())
			first := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: spec.Resources[0].Metadata.ResourceID}
			second := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: "22222222-2222-4222-8222-222222222222"}
			reject := ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Reject}
			for _, r := range spec.Resources {
				if p, ok := r.Payload.(*ir.RoutingProfile); ok {
					p.Final = reject
					p.Rules = []ir.RoutingRule{
						{Enabled: true, Match: ir.RouteMatch{DomainExact: []string{"blocked.fixture.invalid"}}, Action: reject},
						{Enabled: true, Match: ir.RouteMatch{DomainExact: []string{"api.fixture.invalid", "misplaced.invalid"}, DomainSuffix: []string{"fixture.invalid"}, DestinationPorts: []ir.PortRange{{From: 80, To: 81}}, Network: []ir.Network{ir.NetworkTCP}}, Action: first},
						{Enabled: true, Match: ir.RouteMatch{DomainSuffix: []string{"fixture.invalid"}}, Action: second},
					}
				}
			}
			input, err := ir.NewFrozenInput(spec)
			if err != nil {
				t.Fatal(err)
			}
			var target ir.Target
			for _, v := range input.Spec().Targets {
				if v.CoreFamily == family {
					target = v
				}
			}
			artifact, _, err := mustCompiler(t).Compile(context.Background(), input, target)
			if err != nil {
				t.Fatal(err)
			}
			m1Start(t, binary, family, artifact.Bytes)
			for _, host := range []string{"api.fixture.invalid:80", "api.fixture.invalid:81", "other.fixture.invalid:80", "api.fixture.invalid:82", "fixture.invalid:80"} {
				if err := m1Request("http://" + host); err != nil {
					t.Fatalf("routing %s: %v", host, err)
				}
			}
			for _, host := range []string{"misplaced.invalid:80", "blocked.fixture.invalid:80", "notfixture.invalid:80"} {
				if err := m1Request("http://" + host); err == nil {
					t.Fatalf("invalid route %s matched", host)
				}
			}
			if countA.Load() != 2 || countB.Load() != 3 {
				t.Fatalf("AND/OR/order changed: A=%d B=%d", countA.Load(), countB.Load())
			}
			results = append(results, map[string]any{"family": family, "build_id": build.ID, "build_sha256": build.BinarySHA256, "and_fields": "pass", "or_values": "pass", "first_match": "pass", "suffix_apex": "pass", "suffix_boundary": "pass", "unmatched_final": "reject"})
		})
	}
	if !t.Failed() {
		m1Report(t, "native-routing.json", results)
	}
}

func m1ChainBootstrapSpec(t *testing.T, relayA, relayB string) ir.FrozenInputSpec {
	spec := m1RuntimeSpec(t, ir.Xray, ir.PolicyFixed, relayA, relayB)
	first := spec.Resources[0].Metadata.ResourceID
	second := ir.ID("22222222-2222-4222-8222-222222222222")
	chainID := ir.ID("33333333-3333-4333-8333-333333333333")
	for _, r := range spec.Resources {
		if r.Metadata.ResourceID == second {
			r.Payload.(*ir.Node).Endpoint.Host = "localhost"
		}
		if p, ok := r.Payload.(*ir.PolicyGroup); ok {
			p.Members = []ir.TargetRef{{Type: ir.ResourceRef, Kind: ir.KindChain, ResourceID: chainID}}
			p.DefaultMember = p.Members[0]
		}
	}
	meta := spec.Resources[0].Metadata
	meta.ResourceID, meta.Kind = chainID, ir.KindChain
	spec.Resources = append(spec.Resources, ir.Resource{Metadata: meta, Payload: &ir.Chain{SchemaVersion: 1, Hops: []ir.NodeRef{{NodeID: first}, {NodeID: second}}, FailurePolicy: ir.FailClosed}})
	return spec
}

func TestM1NativeXrayLocalBootstrapBeforeChain(t *testing.T) {
	binary, build := m1Core(t, ir.Xray)
	var countA, countB atomic.Int64
	var remoteDomain atomic.Bool
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "controlled fixture") }))
	defer endpoint.Close()
	b := m1Relay(t, endpoint.Listener.Addr().String(), &countB)
	a := m1Relay(t, b.Listener.Addr().String(), &countA)
	handler := a.Config.Handler
	a.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.Host)
		if net.ParseIP(host) == nil {
			remoteDomain.Store(true)
		}
		handler.ServeHTTP(w, r)
	})
	input, err := ir.NewFrozenInput(m1ChainBootstrapSpec(t, a.Listener.Addr().String(), b.Listener.Addr().String()))
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("xray-default")
	artifact, _, err := mustCompiler(t).Compile(context.Background(), input, target)
	if err != nil {
		t.Fatal(err)
	}
	m1Start(t, binary, ir.Xray, artifact.Bytes)
	if err := m1Request(endpoint.URL); err != nil {
		t.Fatal(err)
	}
	if remoteDomain.Load() || countA.Load() != 1 || countB.Load() != 1 {
		t.Fatal("H2 hostname escaped local bootstrap or chain order")
	}
	b.Close()
	if err := m1Request(endpoint.URL); err == nil {
		t.Fatal("H2-down chain bypassed exit")
	}
	m1Report(t, "native-chain-bootstrap.json", map[string]any{"family": ir.Xray, "build_id": build.ID, "build_sha256": build.BinarySHA256, "bootstrap": "local before H1 redirect", "two_hop": "pass", "h2_unavailable": "fail_closed"})
}

func TestM1NativeLatencyBestAndAllUnavailable(t *testing.T) {
	binary, build := m1Core(t, ir.Mihomo)
	var requests, countA, countB, probes atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "controlled fixture")
	}))
	defer endpoint.Close()
	health := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { probes.Add(1); w.WriteHeader(204) }))
	defer health.Close()
	certPath := filepath.Join(t.TempDir(), "fixture-ca.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: health.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSL_CERT_FILE", certPath)
	aliases := map[string]string{health.Listener.Addr().String(): health.Listener.Addr().String()}
	a := m1Relay(t, endpoint.Listener.Addr().String(), &countA, aliases)
	b := m1Relay(t, endpoint.Listener.Addr().String(), &countB, aliases)
	handler := a.Config.Handler
	a.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == health.Listener.Addr().String() {
			time.Sleep(300 * time.Millisecond)
		}
		handler.ServeHTTP(w, r)
	})
	spec := m1RuntimeSpec(t, ir.Mihomo, ir.PolicyLatencyBest, a.Listener.Addr().String(), b.Listener.Addr().String())
	for _, r := range spec.Resources {
		if p, ok := r.Payload.(*ir.PolicyGroup); ok {
			interval, timeout, tolerance := 1000, 900, 5
			p.HealthCheck = ir.PolicyHealthCheck{Enabled: true, URL: health.URL, IntervalMS: &interval, TimeoutMS: &timeout, ToleranceMS: &tolerance}
		}
	}
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("mihomo-default")
	artifact, _, err := mustCompiler(t).Compile(context.Background(), input, target)
	if err != nil {
		t.Fatal(err)
	}
	m1Start(t, binary, ir.Mihomo, artifact.Bytes)
	for deadline := time.Now().Add(5 * time.Second); probes.Load() < 2; {
		if time.Now().After(deadline) {
			t.Fatal("explicit local latency probes did not finish")
		}
		time.Sleep(25 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 3; i++ {
		if err := m1Request(endpoint.URL); err != nil {
			t.Fatal(err)
		}
	}
	if countA.Load() != 0 || countB.Load() != 3 || requests.Load() != 3 {
		t.Fatalf("latency-best did not select fast member: A=%d B=%d", countA.Load(), countB.Load())
	}
	a.Close()
	b.Close()
	if err := m1Request(endpoint.URL); err == nil {
		t.Fatal("all-down latency policy accepted traffic")
	}
	if requests.Load() != 3 {
		t.Fatal("all-down latency policy bypassed relays")
	}
	digest := sha256.Sum256(artifact.Bytes)
	m1Report(t, "native-latency-best.json", map[string]any{"family": ir.Mihomo, "build_id": build.ID, "build_sha256": build.BinarySHA256, "artifact_sha256": hex.EncodeToString(digest[:]), "latency_best": "fast member selected over default slow member", "requests_through_fast_member": 3, "health_interval_ms": 1000, "health_timeout_ms": 900, "health_tolerance_ms": 5, "health_destination": "local TLS fixture only", "all_unavailable": "fail_closed", "direct_bypass": "none"})
}
