package isolation_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/isolation"
)

func TestComposeContainerSmoke(t *testing.T) {
	if os.Getenv("PROXYLOOM_COMPOSE_SMOKE") != "1" {
		t.Skip("compose container smoke runs from scripts/verify-isolation-compose.mjs")
	}
	caPath := os.Getenv("PROXYLOOM_ISOLATION_CA")
	if caPath == "" {
		t.Fatal("PROXYLOOM_ISOLATION_CA is required")
	}
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		t.Fatal("cannot load shared test CA")
	}
	phase := os.Getenv("PROXYLOOM_COMPOSE_SMOKE_PHASE")
	if phase == "" {
		phase = "startup"
	}
	clientA := isolation.ClientConfig{
		Address:    envOr("PROXYLOOM_ISOLATION_A", "a.proxyloom.test:8443"),
		ServerName: "a.proxyloom.test", Password: "EXAMPLE_ONLY_A", RootCAs: pool,
	}
	clientB := isolation.ClientConfig{
		Address:    envOr("PROXYLOOM_ISOLATION_B", "b.proxyloom.test:8443"),
		ServerName: "b.proxyloom.test", Password: "EXAMPLE_ONLY_B", RootCAs: pool,
	}
	targetHost, targetPort := splitHostPort(t, envOr("PROXYLOOM_ISOLATION_TARGET", "target:8080"))
	eventsA := envOr("PROXYLOOM_ISOLATION_EVENTS_A", "http://proxy-a:9090")
	eventsB := envOr("PROXYLOOM_ISOLATION_EVENTS_B", "http://proxy-b:9090")
	eventsTarget := envOr("PROXYLOOM_ISOLATION_EVENTS_TARGET", "http://target:9090")
	httpClient := &http.Client{Timeout: 4 * time.Second, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}

	switch phase {
	case "startup":
		deadline := time.Now().Add(30 * time.Second)
		for _, base := range []string{eventsA, eventsB, eventsTarget} {
			var last error
			for time.Now().Before(deadline) {
				last = getOK(httpClient, base+"/health")
				if last == nil {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			if last != nil {
				t.Fatalf("events health %s: %v", base, last)
			}
		}
		wrong, err := isolation.NewTestCA()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		bad := clientA
		bad.RootCAs = wrong.Pool
		if _, err := isolation.DialTrojan(ctx, bad, targetHost, targetPort); err == nil {
			t.Fatal("wrong CA was accepted; certificate verification is not active")
		}
		conn, err := isolation.DialChain(ctx, clientA, clientB, targetHost, targetPort)
		if err != nil {
			t.Fatalf("A→B dial: %v", err)
		}
		body, err := isolation.ProbeHTTPWithID(conn, "target.proxyloom.test", "compose-startup")
		conn.Close()
		if err != nil || !bytes.Contains(body, []byte(`"ok":true`)) {
			t.Fatalf("A→B probe: %v %s", err, body)
		}
		if isolation.RemoteIP(remoteFromBody(t, body)) != "172.30.253.20" {
			t.Fatalf("target source %s, want B 172.30.253.20", remoteFromBody(t, body))
		}
		if !hasEventResult(t, httpClient, eventsA, "ok") || !hasEventResult(t, httpClient, eventsB, "ok") || !hasEventResult(t, httpClient, eventsTarget, "ok") {
			t.Fatal("startup missing A/B/target ok events")
		}
		fmt.Printf("COMPOSE_SMOKE target_ok=%d b_forbidden=%d\n", countEventResult(t, httpClient, eventsTarget, "ok"), countEventResult(t, httpClient, eventsB, "forbidden_source"))
	case "bypass":
		before := countEventResult(t, httpClient, eventsTarget, "ok")
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		direct, err := isolation.DialTrojan(ctx, clientB, targetHost, targetPort)
		if err == nil {
			_, _ = isolation.ProbeHTTP(direct, "target.proxyloom.test")
			direct.Close()
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) && !hasEventResult(t, httpClient, eventsB, "forbidden_source") {
			time.Sleep(50 * time.Millisecond)
		}
		if !hasEventResult(t, httpClient, eventsB, "forbidden_source") {
			t.Fatal("direct B did not record forbidden_source; TLS/auth errors are not a bypass proof")
		}
		if countEventResult(t, httpClient, eventsTarget, "ok") != before {
			t.Fatal("direct B with the correct password reached the target")
		}
		fmt.Printf("COMPOSE_SMOKE target_ok=%d b_forbidden=%d\n", countEventResult(t, httpClient, eventsTarget, "ok"), countEventResult(t, httpClient, eventsB, "forbidden_source"))
	case "after-stop-a":
		before := countEventResult(t, httpClient, eventsTarget, "ok")
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if _, err := isolation.DialChain(ctx, clientA, clientB, targetHost, targetPort); err == nil {
			t.Fatal("chain succeeded after A stopped")
		}
		if countEventResult(t, httpClient, eventsTarget, "ok") != before {
			t.Fatal("stopping A produced target success")
		}
		fmt.Printf("COMPOSE_SMOKE target_ok=%d b_forbidden=%d\n", before, countEventResult(t, httpClient, eventsB, "forbidden_source"))
	default:
		t.Fatalf("unknown compose smoke phase %q", phase)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func splitHostPort(t *testing.T, address string) (string, int) {
	t.Helper()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range port {
		if c < '0' || c > '9' {
			t.Fatalf("invalid port %s", port)
		}
		n = n*10 + int(c-'0')
	}
	return host, n
}

func getOK(client *http.Client, url string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func eventsPayload(t *testing.T, client *http.Client, base string) []map[string]any {
	t.Helper()
	resp, err := client.Get(strings.TrimRight(base, "/") + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("events json: %v %s", err, body)
	}
	return parsed.Events
}

func hasEventResult(t *testing.T, client *http.Client, base, result string) bool {
	t.Helper()
	return countEventResult(t, client, base, result) > 0
}

func countEventResult(t *testing.T, client *http.Client, base, result string) int {
	t.Helper()
	n := 0
	for _, event := range eventsPayload(t, client, base) {
		if fmt.Sprint(event["result"]) == result {
			n++
		}
	}
	return n
}

func remoteFromBody(t *testing.T, body []byte) string {
	t.Helper()
	idx := bytes.Index(body, []byte("{"))
	if idx < 0 {
		t.Fatalf("no json in %s", body)
	}
	var parsed map[string]any
	if err := json.Unmarshal(body[idx:], &parsed); err != nil {
		t.Fatalf("probe json: %v %s", err, body)
	}
	remote, _ := parsed["remote_addr"].(string)
	return remote
}
