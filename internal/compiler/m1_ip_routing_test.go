package compiler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/ir"
	"golang.org/x/net/dns/dnsmessage"
)

type m1RoutingDNS struct {
	conn    *net.UDPConn
	mu      sync.Mutex
	queries map[string]int
}

func m1NewRoutingDNS(t *testing.T) *m1RoutingDNS {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	dns := &m1RoutingDNS{conn: conn, queries: map[string]int{}}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		packet := make([]byte, 4096)
		for {
			n, peer, err := conn.ReadFromUDP(packet)
			if err != nil {
				return
			}
			var query dnsmessage.Message
			if query.Unpack(packet[:n]) != nil || len(query.Questions) != 1 {
				continue
			}
			q := query.Questions[0]
			name := strings.TrimSuffix(q.Name.String(), ".")
			dns.mu.Lock()
			dns.queries[name]++
			dns.mu.Unlock()
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, Authoritative: true, RecursionDesired: query.RecursionDesired, RecursionAvailable: true}, Questions: query.Questions}
			address := [4]byte{192, 0, 2, 123}
			switch name {
			case "early.fixture.invalid", "later.outside.invalid", "later-port.fixture.invalid", "later-needed.fixture.invalid", "no-ip.fixture.invalid":
				response.RCode = dnsmessage.RCodeNameError
			case "alternate.fixture.invalid":
				address = [4]byte{203, 0, 113, 123}
			case "miss.fixture.invalid":
				address = [4]byte{198, 18, 0, 123}
			case "reject.fixture.invalid":
				address = [4]byte{198, 51, 100, 123}
			}
			if response.RCode == dnsmessage.RCodeSuccess && q.Type == dnsmessage.TypeA {
				response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 30}, Body: &dnsmessage.AResource{A: address}}}
			}
			if data, err := response.Pack(); err == nil {
				_, _ = conn.WriteToUDP(data, peer)
			}
		}
	}()
	return dns
}

func (d *m1RoutingDNS) count(name string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.queries[name]
}

func (d *m1RoutingDNS) configure(spec ir.FrozenInputSpec) {
	for _, resource := range spec.Resources {
		if profile, ok := resource.Payload.(*ir.DNSProfile); ok {
			profile.Resolvers = []ir.DNSResolver{{ResolverID: "business", Kind: ir.DNSUDP, Address: "127.0.0.1", Port: d.conn.LocalAddr().(*net.UDPAddr).Port, Outbound: &ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Direct}}}
			profile.Rules = []ir.DNSRule{}
			profile.FinalResolver = "business"
		}
	}
}

func TestM1NativeIPRoutingResolutionAndOrder(t *testing.T) {
	var results []map[string]any
	for _, family := range []ir.CoreFamily{ir.Xray, ir.SingBox, ir.Mihomo} {
		for _, mode := range []ir.DomainResolutionMode{ir.PreserveDomain, ir.ResolveForIPRules} {
			t.Run(string(family)+"/"+string(mode), func(t *testing.T) {
				binary, build := m1Core(t, family)
				dns := m1NewRoutingDNS(t)
				var countA, countB atomic.Int64
				endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "controlled fixture") }))
				t.Cleanup(endpoint.Close)
				a := m1Relay(t, endpoint.Listener.Addr().String(), &countA)
				b := m1Relay(t, endpoint.Listener.Addr().String(), &countB)
				spec := m1RuntimeSpec(t, family, ir.PolicyFixed, a.Listener.Addr().String(), b.Listener.Addr().String())
				dns.configure(spec)
				first := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: spec.Resources[0].Metadata.ResourceID}
				second := ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: "22222222-2222-4222-8222-222222222222"}
				reject := ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Reject}
				for _, resource := range spec.Resources {
					if profile, ok := resource.Payload.(*ir.RoutingProfile); ok {
						profile.DomainResolutionMode, profile.Final = mode, second
						profile.Rules = []ir.RoutingRule{
							{Enabled: true, Match: ir.RouteMatch{DomainExact: []string{"early.fixture.invalid"}}, Action: first},
							{Enabled: true, Match: ir.RouteMatch{DomainSuffix: []string{"fixture.invalid"}, IPCIDRs: []string{"192.0.2.0/24", "203.0.113.0/24"}, DestinationPorts: []ir.PortRange{{From: 80, To: 81}}, Network: []ir.Network{ir.NetworkTCP}}, Action: first},
							{Enabled: true, Match: ir.RouteMatch{DomainExact: []string{"later.outside.invalid", "later-port.fixture.invalid"}}, Action: first},
							{Enabled: true, Match: ir.RouteMatch{IPCIDRs: []string{"198.51.100.0/24"}}, Action: reject},
							{Enabled: true, Match: ir.RouteMatch{DomainExact: []string{"later-needed.fixture.invalid"}}, Action: first},
						}
					}
				}
				input, err := ir.NewFrozenInput(spec)
				if err != nil {
					t.Fatal(err)
				}
				var target ir.Target
				for _, candidate := range input.Spec().Targets {
					if candidate.CoreFamily == family {
						target = candidate
					}
				}
				artifact, diagnostics, err := mustCompiler(t).Compile(context.Background(), input, target)
				if family == ir.SingBox && mode == ir.ResolveForIPRules {
					if err == nil || len(artifact.Bytes) != 0 || len(diagnostics) != 1 || diagnostics[0].Code != ir.CompileUnmappedField || diagnostics[0].FieldPath != "/payload/domain_resolution_mode" || diagnostics[0].ResourceID != spec.RoutingProfile.ResourceID || diagnostics[0].TargetKey != target.Key {
						t.Fatalf("unsupported IP resolution must fail compilation: %v, %v", diagnostics, err)
					}
					results = append(results, map[string]any{"family": family, "mode": mode, "build_id": build.ID, "build_sha256": build.BinarySHA256, "compile": "unsupported", "artifact": "none", "diagnostics": diagnostics})
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				m1Start(t, binary, family, artifact.Bytes)
				for _, probe := range []struct {
					host, port, preserveRoute, resolvedRoute, lookup string
				}{
					{"early.fixture.invalid", "80", "A", "A", "none"},
					{"later.outside.invalid", "80", "A", "A", "optional"},
					{"later-port.fixture.invalid", "82", "A", "A", "optional"},
					{"later-needed.fixture.invalid", "80", "A", "A", "resolved"},
					{"match.fixture.invalid", "80", "B", "A", "resolved"},
					{"alternate.fixture.invalid", "81", "B", "A", "resolved"},
					{"match.fixture.invalid", "82", "B", "B", "optional"},
					{"miss.fixture.invalid", "80", "B", "B", "optional"},
					{"outside.invalid", "80", "B", "B", "optional"},
					{"reject.fixture.invalid", "80", "B", "reject", "resolved"},
					{"192.0.2.123", "80", "B", "B", "none"},
					{"198.51.100.123", "80", "reject", "reject", "none"},
				} {
					t.Run(probe.host+":"+probe.port, func(t *testing.T) {
						want := probe.resolvedRoute
						if mode == ir.PreserveDomain {
							want = probe.preserveRoute
						}
						beforeA, beforeB := countA.Load(), countB.Load()
						err := m1Request("http://" + net.JoinHostPort(probe.host, probe.port))
						if (err == nil) != (want != "reject") {
							t.Errorf("route %s: request error %v", want, err)
						}
						wantA, wantB := int64(0), int64(0)
						if want == "A" {
							wantA = 1
						} else if want == "B" {
							wantB = 1
						}
						if gotA, gotB := countA.Load()-beforeA, countB.Load()-beforeB; gotA != wantA || gotB != wantB {
							t.Errorf("route %s: relay counts A=%d B=%d", want, gotA, gotB)
						}
						queries := dns.count(probe.host)
						if mode == ir.PreserveDomain || probe.lookup == "none" {
							if queries != 0 {
								t.Errorf("unexpected IP-rule DNS lookup: %d", queries)
							}
						} else if probe.lookup == "resolved" && queries == 0 {
							t.Error("IP rule did not use explicit DNS")
						}
					})
				}
				digest := sha256.Sum256(artifact.Bytes)
				results = append(results, map[string]any{"family": family, "mode": mode, "build_id": build.ID, "build_sha256": build.BinarySHA256, "artifact_sha256": hex.EncodeToString(digest[:]), "early_domain_nxdomain": "proxy_without_lookup", "ip_cidr_or_domain_port_and": "pass", "literal_ip": "pass", "final": "explicit_proxy", "ip_reject": "fail_closed"})
			})
		}
	}
	if !t.Failed() {
		m1Report(t, "native-ip-routing.json", results)
	}
}

func TestM1SingBoxIPResolutionFailsClosed(t *testing.T) {
	for _, scenario := range []string{"ip_cidr", "mixed_rule_set", "disabled_ip", "preserve_domain", "domain_only", "domain_rule_set"} {
		t.Run(scenario, func(t *testing.T) {
			spec := policySpec(t, ir.PolicyFixed)
			match := ir.RouteMatch{IPCIDRs: []string{"192.0.2.0/24"}}
			if scenario == "domain_only" {
				match = ir.RouteMatch{DomainExact: []string{"fixture.invalid"}}
			} else if scenario == "mixed_rule_set" || scenario == "domain_rule_set" {
				entries := []ir.RuleSetEntry{{Kind: ir.RuleSetDomain, Match: ir.DomainExact, Domain: "fixture.invalid"}}
				if scenario == "mixed_rule_set" {
					entries = append(entries, ir.RuleSetEntry{Kind: ir.RuleSetCIDR, CIDR: "192.0.2.0/24"})
				}
				set, err := ir.NewRuleSet(ir.RuleSetWrite{SchemaVersion: 1, Format: ir.DomainCIDRText, Entries: entries})
				if err != nil {
					t.Fatal(err)
				}
				meta := spec.Resources[0].Metadata
				meta.ResourceID, meta.Kind = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", ir.KindRuleSet
				spec.Resources = append(spec.Resources, ir.Resource{Metadata: meta, Payload: &set})
				match = ir.RouteMatch{DomainSuffix: []string{"invalid"}, RuleSetIDs: []ir.ID{meta.ResourceID}}
			}
			for _, resource := range spec.Resources {
				if profile, ok := resource.Payload.(*ir.RoutingProfile); ok {
					profile.DomainResolutionMode = ir.ResolveForIPRules
					if scenario == "preserve_domain" {
						profile.DomainResolutionMode = ir.PreserveDomain
					}
					profile.Rules = []ir.RoutingRule{{Enabled: scenario != "disabled_ip", Match: match, Action: ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Reject}}}
				}
			}
			input, err := ir.NewFrozenInput(spec)
			if err != nil {
				t.Fatal(err)
			}
			target, _ := input.Target("singbox-default")
			artifact, diagnostics, err := mustCompiler(t).Compile(context.Background(), input, target)
			if scenario != "ip_cidr" && scenario != "mixed_rule_set" {
				if err != nil || len(artifact.Bytes) == 0 {
					t.Fatalf("supported profile rejected: %v", err)
				}
				return
			}
			if err == nil || len(artifact.Bytes) != 0 || len(diagnostics) != 1 || diagnostics[0].Code != ir.CompileUnmappedField || diagnostics[0].FieldPath != "/payload/domain_resolution_mode" || diagnostics[0].ResourceID != spec.RoutingProfile.ResourceID || diagnostics[0].TargetKey != target.Key {
				t.Fatalf("unsupported resolution produced an artifact or wrong diagnostic: %v, %v", diagnostics, err)
			}
		})
	}
}

// This deliberately modified native fixture characterizes the pinned core; it
// is not compiler output and must never be treated as a supported artifact.
func TestM1NativeSingBoxIPDNSFailureLimitation(t *testing.T) {
	binary, build := m1Core(t, ir.SingBox)
	var results []map[string]any
	for _, resolve := range []bool{false, true} {
		name := "preserve_domain_control"
		if resolve {
			name = "raw_resolve_counterexample"
		}
		t.Run(name, func(t *testing.T) {
			dns := m1NewRoutingDNS(t)
			var countA, countB atomic.Int64
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "controlled fixture") }))
			t.Cleanup(endpoint.Close)
			a := m1Relay(t, endpoint.Listener.Addr().String(), &countA)
			b := m1Relay(t, endpoint.Listener.Addr().String(), &countB)
			spec := m1RuntimeSpec(t, ir.SingBox, ir.PolicyFixed, a.Listener.Addr().String(), b.Listener.Addr().String())
			dns.configure(spec)
			for _, resource := range spec.Resources {
				if profile, ok := resource.Payload.(*ir.RoutingProfile); ok {
					profile.Final = ir.TargetRef{Type: ir.BuiltinRef, Builtin: ir.Reject}
					profile.Rules = []ir.RoutingRule{
						{Enabled: true, Match: ir.RouteMatch{IPCIDRs: []string{"198.51.100.0/24"}}, Action: profile.Final},
						{Enabled: true, Match: ir.RouteMatch{DomainExact: []string{"later-needed.fixture.invalid"}}, Action: ir.TargetRef{Type: ir.ResourceRef, Kind: ir.KindNode, ResourceID: spec.Resources[0].Metadata.ResourceID}},
					}
				}
			}
			input, err := ir.NewFrozenInput(spec)
			if err != nil {
				t.Fatal(err)
			}
			target, _ := input.Target("singbox-default")
			artifact, _, err := mustCompiler(t).Compile(context.Background(), input, target)
			if err != nil {
				t.Fatal(err)
			}
			data := artifact.Bytes
			if resolve {
				var doc map[string]json.RawMessage
				if err := json.Unmarshal(data, &doc); err != nil {
					t.Fatal(err)
				}
				var route map[string]json.RawMessage
				if err := json.Unmarshal(doc["route"], &route); err != nil {
					t.Fatal(err)
				}
				var rules []json.RawMessage
				if err := json.Unmarshal(route["rules"], &rules); err != nil {
					t.Fatal(err)
				}
				rules = append([]json.RawMessage{json.RawMessage(`{"action":"resolve"}`)}, rules...)
				route["rules"], err = json.Marshal(rules)
				if err != nil {
					t.Fatal(err)
				}
				doc["route"], err = json.Marshal(route)
				if err != nil {
					t.Fatal(err)
				}
				data, err = json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
			}
			m1Start(t, binary, ir.SingBox, data)
			err = m1Request("http://later-needed.fixture.invalid:80")
			queries := dns.count("later-needed.fixture.invalid")
			if resolve {
				if err == nil || countA.Load() != 0 || countB.Load() != 0 || queries == 0 {
					t.Fatalf("pinned resolve failure behavior changed: err=%v A=%d B=%d DNS=%d", err, countA.Load(), countB.Load(), queries)
				}
			} else if err != nil || countA.Load() != 1 || countB.Load() != 0 || queries != 0 {
				t.Fatalf("preserve-domain control failed: err=%v A=%d B=%d DNS=%d", err, countA.Load(), countB.Load(), queries)
			}
			digest := sha256.Sum256(data)
			results = append(results, map[string]any{"fixture": name, "build_id": build.ID, "build_sha256": build.BinarySHA256, "config_sha256": hex.EncodeToString(digest[:]), "compiler_artifact": !resolve, "request_success": err == nil, "dns_queries": queries, "proxy_A_requests": countA.Load(), "proxy_B_requests": countB.Load()})
		})
	}
	if !t.Failed() {
		m1Report(t, "native-ip-routing-limitation.json", results)
	}
}

func TestM1NativeRoutingWithoutIPRulesDoesNotResolve(t *testing.T) {
	for _, family := range []ir.CoreFamily{ir.Xray, ir.SingBox, ir.Mihomo} {
		t.Run(string(family), func(t *testing.T) {
			binary, _ := m1Core(t, family)
			dns := m1NewRoutingDNS(t)
			var countA, countB atomic.Int64
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "controlled fixture") }))
			t.Cleanup(endpoint.Close)
			a := m1Relay(t, endpoint.Listener.Addr().String(), &countA)
			b := m1Relay(t, endpoint.Listener.Addr().String(), &countB)
			spec := m1RuntimeSpec(t, family, ir.PolicyFixed, a.Listener.Addr().String(), b.Listener.Addr().String())
			dns.configure(spec)
			for _, resource := range spec.Resources {
				if profile, ok := resource.Payload.(*ir.RoutingProfile); ok {
					profile.DomainResolutionMode = ir.ResolveForIPRules
				}
			}
			input, err := ir.NewFrozenInput(spec)
			if err != nil {
				t.Fatal(err)
			}
			var target ir.Target
			for _, candidate := range input.Spec().Targets {
				if candidate.CoreFamily == family {
					target = candidate
				}
			}
			artifact, _, err := mustCompiler(t).Compile(context.Background(), input, target)
			if err != nil {
				t.Fatal(err)
			}
			m1Start(t, binary, family, artifact.Bytes)
			if err := m1Request("http://no-ip.fixture.invalid:80"); err != nil {
				t.Errorf("explicit proxy final with no IP rule: %v", err)
			}
			if countA.Load() != 1 || countB.Load() != 0 || dns.count("no-ip.fixture.invalid") != 0 {
				t.Fatalf("no-IP plan unexpectedly resolved or changed final: A=%d B=%d DNS=%d", countA.Load(), countB.Load(), dns.count("no-ip.fixture.invalid"))
			}
		})
	}
}
