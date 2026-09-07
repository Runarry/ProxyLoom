package chainverify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/compiler"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/Runarry/ProxyLoom/internal/isolation"
	runnerexec "github.com/Runarry/ProxyLoom/internal/runner/exec"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if path := os.Getenv("PROXYLOOM_LIVE_REPORT"); path != "" {
		if err := writeReport(path); err != nil {
			fmt.Fprintf(os.Stderr, "live report: %v\n", err)
			if code == 0 {
				code = 1
			}
		}
	}
	os.Exit(code)
}

func TestLiveChainMatrix(t *testing.T) {
	requireLive(t)
	catalog, err := capability.Load()
	if err != nil {
		t.Fatal(err)
	}
	cmp, err := compiler.Load()
	if err != nil {
		t.Fatal(err)
	}
	registry, _, err := coresRegistry()
	if err != nil {
		t.Fatal(err)
	}
	families, err := liveFamilies(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, fam := range families {
		if _, _, err := registry.Executable(fam.BuildID); err != nil {
			t.Fatalf("locked %s core required: %v", fam.Family, err)
		}
	}
	ca, err := isolation.NewTestCA()
	if err != nil {
		t.Fatal(err)
	}
	cleanupCA, err := installLiveCA(ca.PEM)
	if err != nil {
		t.Fatalf("test CA install: %v", err)
	}
	defer cleanupCA()

	for _, fam := range families {
		fam := fam
		t.Run(string(fam.Family), func(t *testing.T) {
			h := &harness{t: t, fam: fam, catalog: catalog, cmp: cmp, registry: registry, ca: ca}
			h.run("forward_chain", h.forwardChain)
			h.run("bypass_forbidden_source", h.bypassForbiddenSource)
			h.run("direction_swap_original", h.directionSwapOriginal)
			h.run("direction_swap_reverse", h.directionSwapReverse)
			h.run("node_reuse", h.nodeReuse)
			h.run("fail_closed", h.failClosed)
			h.run("lifecycle", h.lifecycle)
			h.run("observation_detects_bypass", h.observationDetectsBypass)
		})
	}
}

type harness struct {
	t        *testing.T
	fam      Family
	catalog  *capability.Catalog
	cmp      *compiler.Compiler
	registry runnerexec.DiskRegistry
	ca       *isolation.CertificateAuthority
}

func (h *harness) run(name string, fn func()) {
	h.t.Run(name, func(t *testing.T) {
		prev := h.t
		h.t = t
		defer func() { h.t = prev }()
		failed := true
		defer func() {
			status := "pass"
			observed := ""
			if failed {
				status = "fail"
				observed = "test failed"
			}
			recordResult(string(h.fam.Family), name, status, "", observed)
		}()
		fn()
		failed = false
	})
}

func (h *harness) forwardChain() {
	t := h.t
	topo, err := startForwardTopo(h.ca)
	if err != nil {
		t.Fatal(err)
	}
	defer topo.close()
	input, err := freezeChain(h.catalog, topo.A.endpoint(), topo.B.endpoint(), idNodeA, idNodeB, idChainAB, "A → B")
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := compileFamily(h.cmp, input, h.fam)
	if err != nil {
		t.Fatal(err)
	}
	session, err := startCore(h.registry, h.fam, artifact.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	defer session.stop()
	host, port, err := topo.targetHostPort()
	if err != nil {
		t.Fatal(err)
	}
	baseTarget, baseA, baseB := topo.TargetLog.Count("ok"), topo.A.Log.Count("ok"), topo.B.Log.Count("ok")
	beforeSeq := topo.TargetLog.Seq()
	body, err := session.probe("fwd-"+string(h.fam.Family), host, port)
	if err != nil {
		t.Fatalf("probe: %v %s", err, body)
	}
	ok, remote, _ := parseProbe(body)
	if !ok {
		t.Fatalf("probe not ok: %s", body)
	}
	if isolation.RemoteIP(remote) != ipB.String() {
		t.Fatalf("final exit %s, want B %s", remote, ipB)
	}
	if topo.A.Log.Count("ok") <= baseA || topo.B.Log.Count("ok") <= baseB || topo.TargetLog.Count("ok") <= baseTarget {
		t.Fatal("missing A→B→target ok records")
	}
	for _, event := range topo.unexpectedTargetTraffic(beforeSeq) {
		ip := isolation.RemoteIP(event.Remote)
		if ip == "127.0.0.1" || ip == "::1" {
			t.Fatalf("target recorded a loopback source %s", event.Remote)
		}
	}
}

func (h *harness) bypassForbiddenSource() {
	t := h.t
	topo, err := startForwardTopo(h.ca)
	if err != nil {
		t.Fatal(err)
	}
	defer topo.close()
	input, err := freezeIndependent(h.catalog, topo.B.endpoint(), idNodeB)
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := compileFamily(h.cmp, input, h.fam)
	if err != nil {
		t.Fatal(err)
	}
	session, err := startCore(h.registry, h.fam, artifact.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	defer session.stop()
	host, port, err := topo.targetHostPort()
	if err != nil {
		t.Fatal(err)
	}
	beforeOK := topo.TargetLog.Count("ok")
	beforeSeq := topo.TargetLog.Seq()
	beforeForbidden := topo.B.Log.Count("forbidden_source")
	body, err := session.probe("bypass-"+string(h.fam.Family), host, port)
	if err == nil && probeOK(body) {
		t.Fatalf("direct B succeeded: %s", body)
	}
	if !waitCount(topo.B.Log, "b", "forbidden_source", beforeForbidden+1, 3*time.Second) {
		t.Fatalf("direct B did not record forbidden_source (auth=%d handshake=%d); cannot treat TLS/auth failure as bypass proof. body=%s err=%v",
			topo.B.Log.Count("auth_failed"), topo.B.Log.Count("handshake_failed"), body, err)
	}
	if topo.TargetLog.Count("ok") != beforeOK {
		t.Fatal("direct B reached the target")
	}
	if hits := topo.unexpectedTargetTraffic(beforeSeq); len(hits) > 0 {
		t.Fatalf("bypass produced target traffic: %+v", hits[0].Result)
	}
}

func (h *harness) directionSwapOriginal() {
	t := h.t
	topo, err := startForwardTopo(h.ca)
	if err != nil {
		t.Fatal(err)
	}
	defer topo.close()
	input, err := freezeChain(h.catalog, topo.B.endpoint(), topo.A.endpoint(), idNodeB, idNodeA, idChainBA, "B → A")
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := compileFamily(h.cmp, input, h.fam)
	if err != nil {
		t.Fatal(err)
	}
	session, err := startCore(h.registry, h.fam, artifact.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	defer session.stop()
	host, port, err := topo.targetHostPort()
	if err != nil {
		t.Fatal(err)
	}
	beforeOK := topo.TargetLog.Count("ok")
	beforeForbidden := topo.B.Log.Count("forbidden_source")
	var body []byte
	deadline := time.Now().Add(5 * time.Second)
	for {
		body, err = session.probe("swap-orig-"+string(h.fam.Family), host, port)
		if err == nil && probeOK(body) {
			t.Fatal("B→A succeeded on the original A→B source restriction")
		}
		if topo.B.Log.Count("forbidden_source") >= beforeForbidden+1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("original reverse did not hit B forbidden_source (auth=%d handshake=%d ok=%d) err=%v %s",
				topo.B.Log.Count("auth_failed"), topo.B.Log.Count("handshake_failed"), topo.B.Log.Count("ok"), err, body)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if topo.TargetLog.Count("ok") != beforeOK {
		t.Fatal("original reverse produced target success")
	}
}

func (h *harness) directionSwapReverse() {
	t := h.t
	topo, err := startReverseTopo(h.ca)
	if err != nil {
		t.Fatal(err)
	}
	defer topo.close()
	input, err := freezeChain(h.catalog, topo.B.endpoint(), topo.A.endpoint(), idNodeB, idNodeA, idChainBA, "B → A")
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := compileFamily(h.cmp, input, h.fam)
	if err != nil {
		t.Fatal(err)
	}
	session, err := startCore(h.registry, h.fam, artifact.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	defer session.stop()
	host, port, err := topo.targetHostPort()
	if err != nil {
		t.Fatal(err)
	}
	body, err := session.probe("swap-rev-"+string(h.fam.Family), host, port)
	if err != nil || !probeOK(body) {
		t.Fatalf("controlled B→A failed: %v %s", err, body)
	}
	ok, remote, _ := parseProbe(body)
	if !ok || isolation.RemoteIP(remote) != ipA.String() {
		t.Fatalf("reverse exit %s, want A %s", remote, ipA)
	}
	if topo.A.Log.Count("ok") == 0 || topo.B.Log.Count("ok") == 0 {
		t.Fatal("controlled reverse missing A/B ok records")
	}
}

func (h *harness) nodeReuse() {
	t := h.t
	topo, err := startReuseTopo(h.ca)
	if err != nil {
		t.Fatal(err)
	}
	defer topo.close()
	host, port, err := topo.targetHostPort()
	if err != nil {
		t.Fatal(err)
	}

	ab, err := freezeChain(h.catalog, topo.A.endpoint(), topo.B.endpoint(), idNodeA, idNodeB, idChainAB, "A → B")
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(ab.Spec())
	if err != nil {
		t.Fatal(err)
	}
	h.runMember(ab, host, port, "reuse-ab", ipB.String())
	after, err := json.Marshal(ab.Spec())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("A→B compile mutated original nodes")
	}

	ac, err := freezeChain(h.catalog, topo.A.endpoint(), topo.C.endpoint(), idNodeA, idNodeC, idChainAC, "A → C")
	if err != nil {
		t.Fatal(err)
	}
	h.runMember(ac, host, port, "reuse-ac", ipC.String())

	db, err := freezeChain(h.catalog, topo.D.endpoint(), topo.B.endpoint(), idNodeD, idNodeB, idChainDB, "D → B")
	if err != nil {
		t.Fatal(err)
	}
	h.runMember(db, host, port, "reuse-db", ipB.String())

	abGraph := mustPrepare(t, h.cmp, ab, h.fam.Key)
	acGraph := mustPrepare(t, h.cmp, ac, h.fam.Key)
	dbGraph := mustPrepare(t, h.cmp, db, h.fam.Key)
	if abGraph.Chains[0].TagH2 == acGraph.Chains[0].TagH2 || abGraph.Chains[0].TagH1 == dbGraph.Chains[0].TagH1 {
		t.Fatal("shared-node chain instances reused tags")
	}

	open, err := startOpenBTopo(h.ca)
	if err != nil {
		t.Fatal(err)
	}
	defer open.close()
	ind, err := freezeIndependent(h.catalog, open.B.endpoint(), idNodeB)
	if err != nil {
		t.Fatal(err)
	}
	openHost, openPort, err := open.targetHostPort()
	if err != nil {
		t.Fatal(err)
	}
	h.runMember(ind, openHost, openPort, "reuse-ind-b", ipB.String())

	mixed, err := freezeMixedChainAndIndependentB(h.catalog, topo.A.endpoint(), topo.B.endpoint())
	if err != nil {
		t.Fatal(err)
	}
	mixedGraph := mustPrepare(t, h.cmp, mixed, h.fam.Key)
	if mixedGraph.Independents[0].Tag == mixedGraph.Chains[0].TagH2 {
		t.Fatal("mixed members shared the exit tag")
	}
	h.runMember(mixed, host, port, "reuse-mixed", ipB.String())
}

func (h *harness) runMember(input ir.FrozenInput, host string, port int, requestID, wantIP string) {
	t := h.t
	t.Helper()
	artifact, _, err := compileFamily(h.cmp, input, h.fam)
	if err != nil {
		t.Fatal(err)
	}
	session, err := startCore(h.registry, h.fam, artifact.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	defer session.stop()
	body, err := session.probe(requestID, host, port)
	if err != nil || !probeOK(body) {
		t.Fatalf("%s probe: %v body=%q", requestID, err, truncate(body, 400))
	}
	_, remote, _ := parseProbe(body)
	if isolation.RemoteIP(remote) != wantIP {
		t.Fatalf("%s exit %s, want %s", requestID, remote, wantIP)
	}
}

func (h *harness) failClosed() {
	h.failClosedPhase(func(topo *topo) {
		_ = topo.A.Proxy.Close()
		topo.A.Proxy = nil
	}, "fail-stop-a", true)
	h.failClosedPhase(func(topo *topo) {
		_ = topo.B.Proxy.Close()
		topo.B.Proxy = nil
	}, "fail-stop-b", false)
	h.failClosedPhase(func(topo *topo) {
		_ = topo.A.Proxy.Close()
		_ = topo.B.Proxy.Close()
		topo.A.Proxy = nil
		topo.B.Proxy = nil
	}, "fail-stop-chain", false)
}

func (h *harness) failClosedPhase(stop func(*topo), requestID string, forbidBOK bool) {
	t := h.t
	t.Helper()
	topo, err := startForwardTopo(h.ca)
	if err != nil {
		t.Fatal(err)
	}
	defer topo.close()
	input, err := freezeChain(h.catalog, topo.A.endpoint(), topo.B.endpoint(), idNodeA, idNodeB, idChainAB, "A → B")
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := compileFamily(h.cmp, input, h.fam)
	if err != nil {
		t.Fatal(err)
	}
	session, err := startCore(h.registry, h.fam, artifact.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	defer session.stop()
	host, port, err := topo.targetHostPort()
	if err != nil {
		t.Fatal(err)
	}
	body, err := session.probe(requestID+"-warm", host, port)
	if err != nil || !probeOK(body) {
		t.Fatalf("warmup probe: %v %s", err, body)
	}
	beforeOK := topo.TargetLog.Count("ok")
	beforeSeq := topo.TargetLog.Seq()
	beforeBOK := topo.B.Log.Count("ok")
	stop(topo)
	body, err = session.probe(requestID, host, port)
	if err == nil && probeOK(body) {
		t.Fatalf("%s succeeded after fault: %s", requestID, body)
	}
	if topo.TargetLog.Count("ok") != beforeOK {
		t.Fatalf("%s produced target success after fault", requestID)
	}
	if hits := topo.unexpectedTargetTraffic(beforeSeq); len(hits) > 0 {
		t.Fatalf("%s produced target traffic result=%s remote=%s", requestID, hits[0].Result, hits[0].Remote)
	}
	if forbidBOK && topo.B.Log.Count("ok") != beforeBOK {
		t.Fatalf("%s produced extra B ok after stopping A", requestID)
	}
}

func (h *harness) lifecycle() {
	t := h.t
	topo, err := startForwardTopo(h.ca)
	if err != nil {
		t.Fatal(err)
	}
	defer topo.close()
	input, err := freezeChain(h.catalog, topo.A.endpoint(), topo.B.endpoint(), idNodeA, idNodeB, idChainAB, "A → B")
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := compileFamily(h.cmp, input, h.fam)
	if err != nil {
		t.Fatal(err)
	}
	session, err := startCore(h.registry, h.fam, artifact.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := topo.targetHostPort()
	if err != nil {
		t.Fatal(err)
	}
	body, err := session.probe("life-ok-"+string(h.fam.Family), host, port)
	if err != nil || !probeOK(body) {
		session.stop()
		t.Fatalf("lifecycle probe: %v %s", err, body)
	}
	dir := session.workspace.Directory
	res := session.stop()
	if session.leftoverWorkspace() {
		t.Fatalf("workspace leftover %s", dir)
	}
	if _, err := net.DialTimeout("tcp", h.fam.Listen, 300*time.Millisecond); err == nil {
		t.Fatal("core inbound still listening after stop")
	}
	_ = res

	session, err = startCore(h.registry, h.fam, artifact.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	res = session.stop()
	if !res.result.Canceled {
		t.Fatalf("expected cancel %+v err=%v", res.result, res.err)
	}
	if session.leftoverWorkspace() {
		t.Fatal("canceled core left workspace")
	}

	ws, err := runnerexec.Allocate("", configFilename(h.fam), bytes.Clone(artifact.Bytes))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := runSpec(h.fam, ws)
	if err != nil {
		_ = ws.Close()
		t.Fatal(err)
	}
	result, err := runnerexec.Run(context.Background(), h.registry, spec, ws, runnerexec.Options{Timeout: 400 * time.Millisecond, Redact: redactFor(h.fam)})
	_ = ws.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !result.TimedOut {
		t.Fatalf("expected timeout %+v", result)
	}
	if _, err := os.Stat(ws.Directory); err == nil {
		t.Fatal("timeout left workspace")
	}

	topo2, err := startForwardTopo(h.ca)
	if err != nil {
		t.Fatal(err)
	}
	input2, err := freezeChain(h.catalog, topo2.A.endpoint(), topo2.B.endpoint(), idNodeA, idNodeB, idChainAB, "A → B")
	if err != nil {
		topo2.close()
		t.Fatal(err)
	}
	art2, _, err := compileFamily(h.cmp, input2, h.fam)
	if err != nil {
		topo2.close()
		t.Fatal(err)
	}
	session, err = startCore(h.registry, h.fam, art2.Bytes)
	if err != nil {
		topo2.close()
		t.Fatal(err)
	}
	topo2.close()
	_, _ = session.probe("life-fail-"+string(h.fam.Family), ipTarget.String(), 9)
	res = session.stop()
	if session.leftoverWorkspace() {
		t.Fatal("failure path left workspace")
	}
	_ = res
}

func (h *harness) observationDetectsBypass() {
	t := h.t
	topo, err := startOpenBTopo(h.ca)
	if err != nil {
		t.Fatal(err)
	}
	defer topo.close()
	input, err := freezeIndependent(h.catalog, topo.B.endpoint(), idNodeB)
	if err != nil {
		t.Fatal(err)
	}
	artifact, _, err := compileFamily(h.cmp, input, h.fam)
	if err != nil {
		t.Fatal(err)
	}
	session, err := startCore(h.registry, h.fam, artifact.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	defer session.stop()
	host, port, err := topo.targetHostPort()
	if err != nil {
		t.Fatal(err)
	}
	body, err := session.probe("obs-bypass-"+string(h.fam.Family), host, port)
	if err != nil || !probeOK(body) {
		t.Fatalf("controlled bypass (open B) should succeed so observation can see it: %v %s", err, body)
	}
	_, remote, _ := parseProbe(body)
	if isolation.RemoteIP(remote) != ipB.String() {
		t.Fatalf("observation counterexample exit %s, want B %s", remote, ipB)
	}
	if topo.B.Log.Count("ok") == 0 {
		t.Fatal("observation counterexample missing B ok")
	}
	if isolation.RemoteIP(remote) == "127.0.0.1" {
		t.Fatal("counterexample looked like DIRECT rather than B")
	}
}

func requireLive(t *testing.T) {
	t.Helper()
	if os.Getenv("PROXYLOOM_LIVE_CHAIN") != "1" {
		t.Skip("set PROXYLOOM_LIVE_CHAIN=1 in the linux isolation runner")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("PROXYLOOM_LIVE_CHAIN=1 requires linux")
	}
}

func parseProbe(body []byte) (ok bool, remote, clientID string) {
	payload := body
	if _, rest, found := bytes.Cut(body, []byte("\r\n\r\n")); found {
		payload = rest
	}
	idx := bytes.Index(payload, []byte("{"))
	if idx < 0 {
		return false, "", ""
	}
	var parsed map[string]any
	if err := json.Unmarshal(payload[idx:], &parsed); err != nil {
		return false, "", ""
	}
	ok, _ = parsed["ok"].(bool)
	remote, _ = parsed["remote_addr"].(string)
	clientID, _ = parsed["client_request_id"].(string)
	return ok, remote, clientID
}

func probeOK(body []byte) bool {
	ok, _, _ := parseProbe(body)
	return ok
}

func waitCount(log *isolation.Log, role, result string, min int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if log.CountRole(role, result) >= min {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return log.CountRole(role, result) >= min
}
