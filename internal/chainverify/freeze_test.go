package chainverify

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/capability"
	"github.com/Runarry/ProxyLoom/internal/compiler"
	"github.com/Runarry/ProxyLoom/internal/ir"
)

func TestFreezeCompileKeepsVerifyTagsAndNodes(t *testing.T) {
	catalog, err := capability.Load()
	if err != nil {
		t.Fatal(err)
	}
	cmp, err := compiler.Load()
	if err != nil {
		t.Fatal(err)
	}
	a := Endpoint{Host: "127.0.1.1", Port: 8443, SNI: "a.proxyloom.test", Password: "EXAMPLE_ONLY_A", Name: "Trojan A"}
	b := Endpoint{Host: "127.0.2.1", Port: 8443, SNI: "b.proxyloom.test", Password: "EXAMPLE_ONLY_B", Name: "Trojan B"}
	c := Endpoint{Host: "127.0.3.1", Port: 8443, SNI: "c.proxyloom.test", Password: "EXAMPLE_ONLY_C", Name: "Trojan C"}
	input, err := freezeChain(catalog, a, b, idNodeA, idNodeB, idChainAB, "A → B")
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(input.Spec())
	if err != nil {
		t.Fatal(err)
	}
	families, err := liveFamilies(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, fam := range families {
		artifact, target, err := compileFamily(cmp, input, fam)
		if err != nil {
			t.Fatalf("%s: %v", fam.Family, err)
		}
		after, err := json.Marshal(input.Spec())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before, after) {
			t.Fatalf("%s compile mutated frozen nodes", fam.Family)
		}
		text := string(artifact.Bytes)
		switch fam.Family {
		case ir.Xray:
			if !strings.Contains(text, `"dialerProxy"`) {
				t.Fatal("xray chain missing dialerProxy")
			}
		case ir.SingBox:
			if !strings.Contains(text, `"detour"`) {
				t.Fatal("sing-box chain missing detour")
			}
		case ir.Mihomo:
			if !strings.Contains(text, "dialer-proxy:") {
				t.Fatal("mihomo chain missing dialer-proxy")
			}
		}
		graph, _, err := cmp.Prepare(input, target)
		if err != nil {
			t.Fatal(err)
		}
		if len(graph.Chains) != 1 || graph.Chains[0].TagH1 == graph.Chains[0].TagH2 {
			t.Fatalf("chain tags: %+v", graph.Chains)
		}
	}

	ind, err := freezeIndependent(catalog, b, idNodeB)
	if err != nil {
		t.Fatal(err)
	}
	for _, fam := range families {
		artifact, _, err := compileFamily(cmp, ind, fam)
		if err != nil {
			t.Fatal(err)
		}
		text := string(artifact.Bytes)
		if strings.Contains(text, "dialerProxy") || strings.Contains(text, `"detour"`) || strings.Contains(text, "dialer-proxy:") {
			t.Fatalf("%s independent B received a chain dialer", fam.Family)
		}
		if strings.Contains(text, "127.0.1.1") {
			t.Fatalf("%s independent B mixed in A", fam.Family)
		}
	}

	ac, err := freezeChain(catalog, a, c, idNodeA, idNodeC, idChainAC, "A → C")
	if err != nil {
		t.Fatal(err)
	}
	abGraph := mustPrepare(t, cmp, input, "xray-default")
	acGraph := mustPrepare(t, cmp, ac, "xray-default")
	if abGraph.Chains[0].TagH1 == acGraph.Chains[0].TagH1 || abGraph.Chains[0].TagH2 == acGraph.Chains[0].TagH2 {
		t.Fatal("A→B and A→C reused chain instance tags")
	}

	mixed, err := freezeMixedChainAndIndependentB(catalog, a, b)
	if err != nil {
		t.Fatal(err)
	}
	mixedGraph := mustPrepare(t, cmp, mixed, "xray-default")
	if len(mixedGraph.Independents) != 1 || len(mixedGraph.Chains) != 1 {
		t.Fatalf("mixed graph %+v", mixedGraph)
	}
	if mixedGraph.Independents[0].Tag == mixedGraph.Chains[0].TagH1 || mixedGraph.Independents[0].Tag == mixedGraph.Chains[0].TagH2 {
		t.Fatal("independent B reused a chain tag")
	}
}

func TestSocksConnectRequestUsesIPv4(t *testing.T) {
	req, err := socksConnectRequest("127.0.9.1", 8080)
	if err != nil {
		t.Fatal(err)
	}
	if len(req) != 10 || req[0] != 0x05 || req[1] != 0x01 || req[3] != 0x01 {
		t.Fatalf("unexpected socks request %x", req)
	}
}

func mustPrepare(t *testing.T, cmp *compiler.Compiler, input ir.FrozenInput, key string) compiler.Graph {
	t.Helper()
	target, ok := input.Target(key)
	if !ok {
		t.Fatal(key)
	}
	graph, _, err := cmp.Prepare(input, target)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}
