// deployment-probe is an isolated acceptance client, not a shipped service.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Runarry/ProxyLoom/internal/ir"
)

type saved struct{ Password, Cookie, CSRF, NodeID, MetricsToken string }
type client struct {
	saved
	base string
	http *http.Client
}

func require(ok bool, message string) {
	if !ok {
		panic(message)
	}
}
func read(path string) string {
	b, e := os.ReadFile(path)
	require(e == nil, "fixture_file_unreadable")
	return strings.TrimSpace(string(b))
}
func token() string {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	require(e == nil, "fixture_random_failed")
	return base64.RawURLEncoding.EncodeToString(b)
}
func (c *client) request(method, path string, body any, status int, auth string) map[string]any {
	var encoded []byte
	if body != nil {
		encoded, _ = json.Marshal(body)
	}
	req, e := http.NewRequest(method, c.base+path, bytes.NewReader(encoded))
	require(e == nil, "request_invalid")
	req.Header.Set("Origin", c.base)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", token())
	}
	if auth == "session" && c.Cookie != "" {
		req.Header.Set("Cookie", c.Cookie)
		req.Header.Set("X-CSRF-Token", c.CSRF)
	}
	if auth == "metrics" {
		req.Header.Set("Authorization", "Bearer "+c.MetricsToken)
	}
	res, e := c.http.Do(req)
	require(e == nil, "request_failed")
	defer res.Body.Close()
	b, e := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	require(e == nil, "response_unreadable")
	require(res.StatusCode == status, fmt.Sprintf("unexpected_status_%s_%d_expected_%d", path, res.StatusCode, status))
	if strings.Contains(path, "auth/login") || path == "/api/v1/setup" {
		for _, cookie := range res.Cookies() {
			if cookie.Name == "proxyloom_session" {
				c.Cookie = cookie.Name + "=" + cookie.Value
			}
		}
	}
	if path == "/metrics" && status == 200 {
		require(bytes.Contains(b, []byte("proxyloom_jobs")), "durable_metrics_missing")
		require(!bytes.Contains(b, []byte(c.MetricsToken)), "metrics_secret_leak")
		return nil
	}
	if path == "/" {
		require(bytes.Contains(b, []byte("<div id=\"app\">")), "frontend_missing")
		return nil
	}
	var envelope struct {
		Data any `json:"data"`
	}
	decodeErr := json.Unmarshal(b, &envelope)
	require(status >= 400 || decodeErr == nil, "response_json_invalid")
	if items, ok := envelope.Data.([]any); ok {
		return map[string]any{"items": items}
	}
	data, _ := envelope.Data.(map[string]any)
	if csrf, ok := data["csrf_token"].(string); ok {
		c.CSRF = csrf
	}
	return data
}
func (c *client) verifyNode() {
	data := c.request("GET", "/api/v1/nodes/"+c.NodeID, nil, 200, "session")
	require(data["metadata"].(map[string]any)["name"] == "部署验收合成节点", "persisted_node_missing")
}
func (c *client) validateCores() {
	data := c.request("GET", "/api/v1/cores", nil, 200, "session")
	count := 0
	for _, raw := range data["items"].([]any) {
		core := raw.(map[string]any)
		if core["architecture"] != "amd64" {
			continue
		}
		count++
		batch := c.request("POST", "/api/v1/tests", map[string]any{"subjects": []any{map[string]any{"kind": "node", "id": c.NodeID}}, "core_build_id": core["core_build_id"], "type": "config_validate", "limits": map[string]any{"duration_ms": 10000, "max_bytes": 0}}, 202, "session")
		id := batch["batch_id"].(string)
		deadline := time.Now().Add(70 * time.Second)
		for {
			batch = c.request("GET", "/api/v1/jobs/"+id, nil, 200, "session")
			state := batch["state"]
			if state == "succeeded" {
				require(batch["verdict"] == "pass", "real_core_validation_failed")
				break
			}
			require(state == "queued" || state == "running", "core_job_failed")
			require(time.Now().Before(deadline), "core_job_deadline")
			time.Sleep(250 * time.Millisecond)
		}
		fmt.Println("PASS: embedded-core-validation-" + core["core_family"].(string))
	}
	require(count == 3, "three_architecture_builds_required")
}
func main() {
	defer func() {
		if failure := recover(); failure != nil {
			fmt.Fprintln(os.Stderr, failure)
			os.Exit(1)
		}
	}()
	require(len(os.Args) == 4, "usage_mode_state_probe_state")
	mode, state, record := os.Args[1], os.Args[2], os.Args[3]
	if mode == "dial" {
		conn, err := net.DialTimeout("tcp", state, time.Second)
		if conn != nil {
			conn.Close()
		}
		require((err == nil) == (record == "allow"), "network_guard_dial_expectation_failed")
		fmt.Println("PASS: network-dial-" + record)
		return
	}
	c := &client{base: "http://127.0.0.1:8080", http: &http.Client{Timeout: 15 * time.Second}}
	if mode == "init" || mode == "init-legacy" {
		c.Password = "Deployment-" + token() + "!"
		c.MetricsToken = read(filepath.Join(state, "secrets/metrics_token"))
		c.request("GET", "/", nil, 200, "")
		c.request("GET", "/api/v1/nodes", nil, 401, "")
		if mode == "init" {
			c.request("GET", "/metrics", nil, 401, "")
		}
		c.request("POST", "/api/v1/setup", map[string]string{"setup_token": read(filepath.Join(state, "secrets/setup_token")), "username": "deployment-admin", "password": c.Password}, 201, "")
		udp := false
		node := &ir.Node{SchemaVersion: 1, Protocol: ir.SOCKS5, Endpoint: ir.Endpoint{Host: "192.0.2.10", Port: 1080}, Auth: &ir.UsernamePasswordAuth{Kind: ir.AuthUsernamePassword, Username: "fixture", Password: ir.Secret(token())}, Transport: &ir.NativeTCPTransport{Kind: ir.NativeTCP}, Security: &ir.NoSecurity{Mode: ir.SecurityNone}, Features: ir.Features{UDP: &udp}, Extensions: ir.Extensions{}}
		created := c.request("POST", "/api/v1/nodes", map[string]any{"name": "部署验收合成节点", "node": node}, 201, "session")
		c.NodeID = created["metadata"].(map[string]any)["resource_id"].(string)
		c.verifyNode()
		if mode == "init" {
			c.validateCores()
			c.request("GET", "/metrics", nil, 200, "metrics")
		}
		b, _ := json.Marshal(c.saved)
		require(os.WriteFile(record, b, 0600) == nil, "fixture_save_failed")
	} else {
		require(json.Unmarshal([]byte(read(record)), &c.saved) == nil, "fixture_state_invalid")
		if mode == "restored" {
			c.request("GET", "/api/v1/auth/me", nil, 401, "session")
			c.request("GET", "/metrics", nil, 401, "metrics")
			c.Cookie = ""
			c.CSRF = ""
			c.request("POST", "/api/v1/auth/login", map[string]string{"username": "deployment-admin", "password": c.Password}, 200, "")
			c.MetricsToken = read(filepath.Join(state, "secrets/metrics_token"))
		} else {
			require(mode == "persisted" || mode == "upgraded", "unknown_mode")
			c.request("GET", "/api/v1/auth/me", nil, 200, "session")
		}
		c.verifyNode()
		if mode == "restored" || mode == "upgraded" {
			c.validateCores()
		}
		c.request("GET", "/metrics", nil, 200, "metrics")
		if mode == "restored" {
			b, _ := json.Marshal(c.saved)
			require(os.WriteFile(record, b, 0600) == nil, "restored_fixture_save_failed")
		}
	}
	fmt.Println("PASS: deployment-" + mode)
}
