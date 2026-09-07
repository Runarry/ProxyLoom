package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/adapter"
	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestCompileChainPlanIsDeterministicAndNameIndependent(t *testing.T) {
	compiler := mustCompiler(t)
	input := mustFrozen(t, "frozen-chain-a-b.json")
	target, ok := input.Target("xray-default")
	if !ok {
		t.Fatal("missing xray target")
	}
	graph, diags, err := compiler.Prepare(input, target)
	if err != nil {
		t.Fatal(err)
	}
	if !hasInfo(diags, ir.CapabilityUnverified) {
		t.Fatal("unverified capability must be recorded")
	}
	plan := BuildPlan(graph)
	if plan.CapabilityState != capability.Unverified {
		t.Fatal("compiler must not mark capabilities verified")
	}
	if plan.Target.CoreBuildID != target.CoreBuildID || plan.Target.AdapterVersion != capability.AdapterVersion {
		t.Fatal("plan target drifted from frozen pin")
	}
	if len(plan.Chains) != 1 || plan.Chains[0].ExitTag != plan.Chains[0].TagH2 || plan.Chains[0].ExitTag == plan.Chains[0].TagH1 {
		t.Fatalf("chain exit must be h2: %+v", plan.Chains)
	}
	if len(plan.Outbounds) != 2 {
		t.Fatalf("chain-only plan should not emit independent node tags: %d", len(plan.Outbounds))
	}
	var h1, h2 PlanOutbound
	for _, outbound := range plan.Outbounds {
		switch outbound.Kind {
		case KindChainH1:
			h1 = outbound
		case KindChainH2:
			h2 = outbound
		default:
			t.Fatalf("unexpected outbound kind %s", outbound.Kind)
		}
		if strings.Contains(outbound.Tag, "Trojan") || strings.Contains(outbound.Tag, "A") && strings.HasPrefix(outbound.Tag, "A") {
			t.Fatal("tag used a display name")
		}
	}
	if h2.DialerTag != h1.Tag || h1.DialerTag != "" {
		t.Fatalf("h2 must dial h1: %+v %+v", h1, h2)
	}
	payload, err := marshalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(payload, []byte("EXAMPLE_ONLY")) {
		t.Fatal("compile plan leaked fixture secrets")
	}

	first, _, err := compiler.Compile(context.Background(), input, target)
	if err != nil {
		t.Fatal(err)
	}
	if first.ContentType != "application/json" || first.SnapshotID != "ffffffff-ffff-4fff-8fff-ffffffffffff" {
		t.Fatalf("artifact identity %+v", first)
	}
	if !bytes.Contains(first.Bytes, []byte(`"dialerProxy": "`+h1.Tag+`"`)) && !bytes.Contains(first.Bytes, []byte(`"dialerProxy":"`+h1.Tag+`"`)) {
		t.Fatal("xray chain missing dialerProxy to h1")
	}

	renamed := mustFrozen(t, "frozen-chain-a-b.json")
	spec := renamed.Spec()
	for i := range spec.Resources {
		spec.Resources[i].Metadata.Name = "renamed-" + string(spec.Resources[i].Metadata.ResourceID)
	}
	renamed, err = ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := compiler.Compile(context.Background(), renamed, target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes, second.Bytes) {
		t.Fatal("rename changed compile bytes")
	}

	var last []byte
	for i := 0; i < 100; i++ {
		artifact, _, err := compiler.Compile(context.Background(), input, target)
		if err != nil {
			t.Fatal(err)
		}
		if last != nil && !bytes.Equal(last, artifact.Bytes) {
			t.Fatalf("repeat %d produced different bytes", i)
		}
		last = artifact.Bytes
	}

	for _, key := range []string{"singbox-default", "mihomo-default"} {
		familyTarget, ok := input.Target(key)
		if !ok {
			t.Fatal(key)
		}
		artifact, _, err := compiler.Compile(context.Background(), input, familyTarget)
		if err != nil {
			t.Fatal(err)
		}
		familyGraph, _, err := compiler.Prepare(input, familyTarget)
		if err != nil {
			t.Fatal(err)
		}
		familyPlan := BuildPlan(familyGraph)
		if familyPlan.Chains[0].TagH1 != plan.Chains[0].TagH1 || familyPlan.Chains[0].TagH2 != plan.Chains[0].TagH2 {
			t.Fatal("labels must be independent of core family")
		}
		if familyPlan.Target.Key != key || familyPlan.Target.CoreFamily != familyTarget.CoreFamily {
			t.Fatal("family pin missing from plan")
		}
		if len(artifact.Bytes) == 0 {
			t.Fatal("native artifact empty")
		}
	}

	var logBuf bytes.Buffer
	slog.New(slog.NewJSONHandler(&logBuf, nil)).Info("graph", "graph", mustGraph(t, compiler, input, target), "artifact", first)
	if strings.Contains(logBuf.String(), "EXAMPLE_ONLY") {
		t.Fatal("graph or artifact logging leaked secrets")
	}
}

func TestIndependentMembersKeepOriginalNodesUntaggedFromChain(t *testing.T) {
	compiler := mustCompiler(t)
	spec := mustFrozen(t, "frozen-chain-a-b.json").Spec()
	spec.Members = append(spec.Members,
		ir.FrozenRef{ResourceID: "11111111-1111-4111-8111-111111111111", Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1},
		ir.FrozenRef{ResourceID: "22222222-2222-4222-8222-222222222222", Kind: ir.KindNode, Revision: 1, SecurityEpoch: 1},
	)
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("xray-default")
	graph, _, err := compiler.Prepare(input, target)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(graph)
	if len(plan.Outbounds) != 4 {
		t.Fatalf("expected two independent nodes and two chain hops, got %d", len(plan.Outbounds))
	}
	var independent, hops int
	for _, outbound := range plan.Outbounds {
		switch outbound.Kind {
		case KindIndependent:
			independent++
			if outbound.DialerTag != "" {
				t.Fatal("independent node received a chain dialer")
			}
		case KindChainH1, KindChainH2:
			hops++
		}
	}
	if independent != 2 || hops != 2 {
		t.Fatalf("independents=%d hops=%d", independent, hops)
	}
}

func TestCompileRejectsUnknownAndMismatchedPins(t *testing.T) {
	compiler := mustCompiler(t)
	input := mustFrozen(t, "frozen-chain-a-b.json")
	target, _ := input.Target("xray-default")

	wrong := target
	wrong.CoreBuildSHA256 = strings.Repeat("0", 64)
	if _, _, err := compiler.Compile(context.Background(), input, wrong); !hasCode(err, ir.CompileTargetMismatch) {
		t.Fatalf("mutated target: %v", err)
	}

	irFixture, err := ir.DecodeFrozenInput(readIR(t, "positive/frozen-a-b.json"))
	if err != nil {
		t.Fatal(err)
	}
	irTarget, _ := irFixture.Target("xray-default")
	if _, _, err := compiler.Compile(context.Background(), irFixture, irTarget); !hasCode(err, ir.CompileUnknownBuild) && !hasCode(err, ir.CompileAdapterVersion) {
		t.Fatalf("example-only pin: %v", err)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := compiler.Compile(canceled, input, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: %v", err)
	}
}

func TestUnknownProtocolCombinationFails(t *testing.T) {
	compiler := mustCompiler(t)
	spec := mustFrozen(t, "frozen-chain-a-b.json").Spec()
	node := spec.Resources[1].Payload.(*ir.Node)
	cloned, err := cloneNodeForTest(*node)
	if err != nil {
		t.Fatal(err)
	}
	cloned.Protocol = ir.SOCKS5
	cloned.Auth = &ir.NoAuth{Kind: ir.AuthNone}
	cloned.Security = &ir.TLSSecurity{Mode: ir.TLS, ServerName: "b.example.invalid", VerifyCertificate: boolPtr(true)}
	spec.Resources[1].Payload = &cloned
	input, err := ir.NewFrozenInput(spec)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := input.Target("xray-default")
	if _, _, err := compiler.Compile(context.Background(), input, target); !hasCode(err, ir.CompileUnknownCapability) {
		t.Fatalf("socks5+tls: %v", err)
	}
}

func TestUnsupportedCapabilityHelperFailsClosed(t *testing.T) {
	compiler := mustCompiler(t)
	singbox, err := compiler.catalog.Lookup(ir.SingBox, "1.14.0", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	record, err := compiler.catalog.Capability(singbox.ID, "policy.round_robin")
	if err != nil || record.State != capability.Unsupported {
		t.Fatalf("round_robin: %v %+v", err, record)
	}
	if err := compiler.catalog.RequireVerified(singbox.ID, "policy.round_robin"); !errors.Is(err, capability.ErrUnsupported) {
		t.Fatalf("require: %v", err)
	}
}

func TestGoldenChainPlan(t *testing.T) {
	compiler := mustCompiler(t)
	input := mustFrozen(t, "frozen-chain-a-b.json")
	target, _ := input.Target("xray-default")
	graph, _, err := compiler.Prepare(input, target)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := marshalPlan(BuildPlan(graph))
	if err != nil {
		t.Fatal(err)
	}
	goldenPath := filepath.Join("..", "..", "fixtures", "compiler", "golden", "xray-chain-a-b.json")
	if os.Getenv("PROXYLOOM_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, payload, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, payload) {
		t.Fatalf("golden mismatch\nwant %s\ngot  %s", want, payload)
	}
}

func mustCompiler(t *testing.T) *Compiler {
	t.Helper()
	compiler, err := Load()
	if err != nil || compiler == nil {
		t.Fatalf("load compiler: %v", err)
	}
	return compiler
}

func mustFrozen(t *testing.T, name string) ir.FrozenInput {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "compiler", name))
	if err != nil {
		t.Fatal(err)
	}
	input, err := ir.DecodeFrozenInput(data)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func readIR(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "fixtures", "ir", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustGraph(t *testing.T, compiler *Compiler, input ir.FrozenInput, target ir.Target) Graph {
	t.Helper()
	graph, _, err := compiler.Prepare(input, target)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func hasInfo(diags []ir.Diagnostic, code ir.DiagnosticCode) bool {
	for _, d := range diags {
		if d.Code == code && d.Severity == ir.SeverityInfo {
			return true
		}
	}
	return false
}

func hasCode(err error, code ir.DiagnosticCode) bool {
	var diags ir.Diagnostics
	if !errors.As(err, &diags) {
		return false
	}
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func boolPtr(v bool) *bool { return &v }

func cloneNodeForTest(node ir.Node) (ir.Node, error) {
	data, err := json.Marshal(node)
	if err != nil {
		return ir.Node{}, err
	}
	return ir.DecodeNode(data)
}

func TestArtifactStillRedacted(t *testing.T) {
	artifact := adapter.Artifact{Bytes: []byte("EXAMPLE_ONLY_A")}
	if strings.Contains(fmt.Sprintf("%v", artifact), "EXAMPLE_ONLY") {
		t.Fatal("artifact format leaked")
	}
}
