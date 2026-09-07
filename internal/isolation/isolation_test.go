package isolation_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
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
