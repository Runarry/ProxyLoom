package isolation_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/isolation"
)

func TestChainSucceedsAndDirectBIsBlocked(t *testing.T) {
	ca, err := isolation.NewTestCA()
	if err != nil {
		t.Fatal(err)
	}
	leafA, err := ca.Issue("a.proxyloom.test")
	if err != nil {
		t.Fatal(err)
	}
	leafB, err := ca.Issue("b.proxyloom.test")
	if err != nil {
		t.Fatal(err)
	}
	ipA := net.IPv4(127, 0, 1, 1)
	ipB := net.IPv4(127, 0, 2, 1)
	logA, logB, logTarget := &isolation.Log{}, &isolation.Log{}, &isolation.Log{}
	target, err := isolation.StartHTTPTarget("127.0.0.1:0", logTarget)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	proxyB, err := isolation.StartTrojan(isolation.ProxyConfig{
		Bind: net.JoinHostPort(ipB.String(), "0"), Host: "b.proxyloom.test", Password: "EXAMPLE_ONLY_B",
		Certificate: leafB.Certificate, AllowFrom: []net.IP{ipA}, Role: "b", Log: logB,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxyB.Close()
	proxyA, err := isolation.StartTrojan(isolation.ProxyConfig{
		Bind: net.JoinHostPort(ipA.String(), "0"), Host: "a.proxyloom.test", Password: "EXAMPLE_ONLY_A",
		Certificate: leafA.Certificate, DialLocal: ipA, Role: "a", Log: logA,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxyA.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	targetHost, targetPort, err := net.SplitHostPort(target.Addr)
	if err != nil {
		t.Fatal(err)
	}
	port := atoi(t, targetPort)
	clientA := isolation.ClientConfig{Address: proxyA.Addr, ServerName: "a.proxyloom.test", Password: "EXAMPLE_ONLY_A", RootCAs: ca.Pool}
	clientB := isolation.ClientConfig{Address: proxyB.Addr, ServerName: "b.proxyloom.test", Password: "EXAMPLE_ONLY_B", RootCAs: ca.Pool}
	conn, err := isolation.DialChain(ctx, clientA, clientB, targetHost, port)
	if err != nil {
		t.Fatal(err)
	}
	body, err := isolation.ProbeHTTP(conn, "target.proxyloom.test")
	conn.Close()
	if err != nil || !bytes.Contains(body, []byte(`"ok":true`)) {
		t.Fatalf("chain probe failed: %v %s", err, body)
	}
	if logA.Count("ok") == 0 || logB.Count("ok") == 0 || logTarget.Count("ok") == 0 {
		t.Fatal("chain did not record A, B and target connections")
	}

	before := logTarget.Count("ok")
	direct, err := isolation.DialTrojan(ctx, clientB, targetHost, port)
	if err == nil {
		_, _ = isolation.ProbeHTTP(direct, "target.proxyloom.test")
		direct.Close()
	}
	time.Sleep(50 * time.Millisecond)
	if logB.Count("forbidden_source") == 0 {
		t.Fatal("direct connection to B was not blocked")
	}
	if logTarget.Count("ok") != before {
		t.Fatal("direct connection reached the target")
	}

	_ = proxyA.Close()
	if _, err := isolation.DialChain(ctx, clientA, clientB, targetHost, port); err == nil {
		t.Fatal("chain succeeded after A stopped")
	}
	if logTarget.Count("ok") != before {
		t.Fatal("stopping A produced an implicit direct success")
	}
}

func TestChainExitSourceIsLastHopAndDirectIsObservable(t *testing.T) {
	ca, err := isolation.NewTestCA()
	if err != nil {
		t.Fatal(err)
	}
	leafA, err := ca.Issue("a.proxyloom.test")
	if err != nil {
		t.Fatal(err)
	}
	leafB, err := ca.Issue("b.proxyloom.test")
	if err != nil {
		t.Fatal(err)
	}
	ipA := net.IPv4(127, 0, 1, 1)
	ipB := net.IPv4(127, 0, 2, 1)
	logA, logB, logTarget := &isolation.Log{}, &isolation.Log{}, &isolation.Log{}
	target, err := isolation.StartHTTPTarget("127.0.9.1:0", logTarget)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	proxyB, err := isolation.StartTrojan(isolation.ProxyConfig{
		Bind: net.JoinHostPort(ipB.String(), "0"), Host: "b.proxyloom.test", Password: "EXAMPLE_ONLY_B",
		Certificate: leafB.Certificate, AllowFrom: []net.IP{ipA}, DialLocal: ipB, Role: "b", Log: logB,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxyB.Close()
	proxyA, err := isolation.StartTrojan(isolation.ProxyConfig{
		Bind: net.JoinHostPort(ipA.String(), "0"), Host: "a.proxyloom.test", Password: "EXAMPLE_ONLY_A",
		Certificate: leafA.Certificate, DialLocal: ipA, Role: "a", Log: logA,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer proxyA.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	targetHost, targetPort, err := net.SplitHostPort(target.Addr)
	if err != nil {
		t.Fatal(err)
	}
	clientA := isolation.ClientConfig{Address: proxyA.Addr, ServerName: "a.proxyloom.test", Password: "EXAMPLE_ONLY_A", RootCAs: ca.Pool}
	clientB := isolation.ClientConfig{Address: proxyB.Addr, ServerName: "b.proxyloom.test", Password: "EXAMPLE_ONLY_B", RootCAs: ca.Pool}
	conn, err := isolation.DialChain(ctx, clientA, clientB, targetHost, atoi(t, targetPort))
	if err != nil {
		t.Fatal(err)
	}
	body, err := isolation.ProbeHTTPWithID(conn, "target.proxyloom.test", "chain-source")
	conn.Close()
	if err != nil || !bytes.Contains(body, []byte(`"ok":true`)) || !bytes.Contains(body, []byte("chain-source")) {
		t.Fatalf("chain probe failed: %v %s", err, body)
	}
	okEvent, ok := logTarget.Last("target", "ok")
	if !ok {
		t.Fatal("target missing ok event")
	}
	if isolation.RemoteIP(okEvent.Remote) != ipB.String() {
		t.Fatalf("chain exit source %q, want B %s", okEvent.Remote, ipB)
	}

	directTarget, err := net.DialTimeout("tcp", target.Addr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	directBody, err := isolation.ProbeHTTPWithID(directTarget, "target.proxyloom.test", "direct-source")
	directTarget.Close()
	if err != nil || !bytes.Contains(directBody, []byte(`"ok":true`)) {
		t.Fatalf("direct probe failed: %v %s", err, directBody)
	}
	foundDirect := false
	for _, event := range logTarget.After(okEvent.Seq) {
		if event.Result == "ok" && isolation.RemoteIP(event.Remote) != ipB.String() {
			foundDirect = true
		}
	}
	if !foundDirect {
		t.Fatal("observation did not record a non-B direct target source")
	}
}

func TestListenEventsServesHealthAndReset(t *testing.T) {
	log := &isolation.Log{}
	log.Record(isolation.Event{Role: "b", Result: "forbidden_source", Remote: "127.0.0.1:9"})
	server, err := isolation.ListenEvents("127.0.0.1:0", log)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	health, err := client.Get("http://" + server.Addr + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer health.Body.Close()
	if health.StatusCode != 200 {
		t.Fatalf("health %d", health.StatusCode)
	}
	events, err := client.Get("http://" + server.Addr + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer events.Body.Close()
	body, err := io.ReadAll(events.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte(`"forbidden_source"`)) || bytes.Contains(body, []byte("EXAMPLE_ONLY")) {
		t.Fatalf("events payload %s", body)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+server.Addr+"/reset", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if log.Count("forbidden_source") != 0 {
		t.Fatal("reset did not clear events")
	}
}

func TestRedactLogRemovesPasswordAndHandshake(t *testing.T) {
	secret := "EXAMPLE_ONLY_A"
	line := []byte("password=" + secret + " hash-follows")
	redacted := isolation.RedactLog(line, []string{secret})
	if bytes.Contains(redacted, []byte(secret)) {
		t.Fatal("password left in log")
	}
	if fmt.Sprintf("%v", isolation.Event{Remote: secret}) != "isolation.Event{[REDACTED]}" {
		t.Fatal("event formatting leaked fields")
	}
}

func TestProductionDefaultsExcludeIsolationPolicy(t *testing.T) {
	root := filepath.Join("..", "..")
	dev := read(t, filepath.Join(root, "deploy", "compose.dev.yaml"))
	isolationCompose := read(t, filepath.Join(root, "deploy", "compose.isolation.yaml"))
	for _, name := range []string{"Dockerfile.api", "Dockerfile.runner"} {
		body := read(t, filepath.Join(root, "deploy", name))
		if strings.Contains(body, "test-only") || strings.Contains(body, "127.0.1.1") || strings.Contains(strings.ToLower(body), "tun") {
			t.Fatalf("%s contains isolation or tun defaults", name)
		}
	}
	if strings.Contains(dev, "compose.isolation") || strings.Contains(dev, "proxyloom.test-only") || strings.Contains(dev, "PROXYLOOM_TEST_ONLY") {
		t.Fatal("development compose includes isolation policy")
	}
	if !strings.Contains(isolationCompose, "proxyloom.test-only") || !strings.Contains(isolationCompose, "test-only") {
		t.Fatal("isolation compose is missing the test-only marker")
	}
	ports := bindPorts(t, isolationCompose)
	if len(ports) < 3 {
		t.Fatal("isolation compose is missing service bind addresses")
	}
	for _, port := range ports {
		if port < 1024 {
			t.Fatalf("isolation compose binds privileged port %d; UID 10003 cannot listen below 1024", port)
		}
	}
}

func bindPorts(t *testing.T, compose string) []int {
	t.Helper()
	var ports []int
	for _, line := range strings.Split(compose, "\n") {
		line = strings.TrimSpace(line)
		const prefix = "PROXYLOOM_ISOLATION_BIND:"
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		addr := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatalf("isolation bind is not host:port: %s", addr)
		}
		ports = append(ports, atoi(t, port))
	}
	return ports
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func atoi(t *testing.T, value string) int {
	t.Helper()
	n := 0
	for _, c := range value {
		if c < '0' || c > '9' {
			t.Fatalf("invalid port %s", value)
		}
		n = n*10 + int(c-'0')
	}
	return n
}
