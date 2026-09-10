package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/server"
)

const orchestrationBrowserFlag = "PROXYLOOM_ORCHESTRATION_BROWSER_TEST"

func TestOrchestrationBrowserAcceptance(t *testing.T) {
	if os.Getenv(orchestrationBrowserFlag) != "true" {
		t.Skip("orchestration browser acceptance requires an explicit disposable PostgreSQL environment")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal("orchestration repository path unavailable")
	}
	webRoot := filepath.Join(root, "proxyloom-web", "dist")
	if info, statErr := os.Stat(filepath.Join(webRoot, "index.html")); statErr != nil || !info.Mode().IsRegular() {
		t.Fatal("orchestration browser acceptance requires a completed production web build")
	}

	env := newPostgres(t, true)
	if err := env.store.EnsureBuiltinClientPresets(env.ctx, env.scope); err != nil {
		t.Fatal("orchestration client preset provisioning failed")
	}
	setupBytes := browserRandom(t, 32)
	setup := base64.RawURLEncoding.EncodeToString(setupBytes)
	clear(setupBytes)
	pepper := browserRandom(t, 32)
	identities, err := NewIdentity(env.runtime, identity.Options{ScopeID: env.scope, SetupToken: []byte(setup), TokenPepper: pepper})
	clear(pepper)
	if err != nil {
		t.Fatal("orchestration identity construction failed")
	}
	passwordBytes := browserRandom(t, 24)
	password := "Browser-Orchestration-" + base64.RawURLEncoding.EncodeToString(passwordBytes) + "!"
	clear(passwordBytes)
	username := "orchestration-admin"
	if _, err := identities.Setup(context.Background(), setup, username, password, identity.Source{IP: "127.0.0.1", RequestID: "orchestration-setup"}); err != nil {
		t.Fatal("orchestration administrator setup failed")
	}
	setup = ""

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("orchestration API listener unavailable")
	}
	baseURL := "http://" + listener.Addr().String()
	macKey := browserRandom(t, 32)
	mac, err := apicontract.NewCursorHMAC(macKey)
	clear(macKey)
	if err != nil {
		listener.Close()
		t.Fatal("orchestration cursor key construction failed")
	}
	cursor, err := apicontract.NewCursorCodec(mac)
	if err != nil {
		listener.Close()
		t.Fatal("orchestration cursor construction failed")
	}
	handler, err := server.NewHandler(webRoot, server.Dependencies{
		Database: env.runtime.Ping, Secrets: func() error { return nil }, Identity: identities,
		PublicURL: baseURL, Development: true,
		Nodes: &server.NodeDependencies{Repository: env.store, Cursor: cursor},
	}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		listener.Close()
		t.Fatal("orchestration API handler construction failed")
	}
	api := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	apiDone := make(chan error, 1)
	go func() { apiDone <- api.Serve(listener) }()
	t.Cleanup(func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = api.Shutdown(shutdown)
		_ = handler.Close()
		if serveErr := <-apiDone; serveErr != nil && serveErr != http.ErrServerClosed {
			t.Errorf("orchestration API shutdown failed")
		}
	})

	if !browserWaitReady(baseURL) {
		t.Fatal("orchestration API did not become ready")
	}
	commandContext, cancelCommand := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancelCommand()
	node := os.Getenv("PROXYLOOM_NODE_BIN")
	if node == "" {
		node = "node"
	}
	browser := os.Getenv("PROXYLOOM_E2E_BROWSER")
	if browser == "" {
		browser = "msedge"
	}
	command := exec.CommandContext(commandContext, node, "node_modules/@playwright/test/cli.js", "test", "tests/e2e/orchestration.spec.ts", "--config", "playwright.config.ts")
	command.Dir = filepath.Join(root, "proxyloom-web")
	command.Env = append(os.Environ(),
		"PROXYLOOM_E2E_BASE_URL="+baseURL,
		"PROXYLOOM_E2E_USERNAME="+username,
		"PROXYLOOM_E2E_PASSWORD="+password,
		"PROXYLOOM_E2E_BROWSER="+browser,
	)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	runErr := command.Run()
	text := output.String()
	if strings.Contains(text, password) {
		t.Fatal("orchestration browser output disclosed the administrator credential")
	}
	if runErr != nil {
		t.Fatalf("orchestration Playwright failed: %v\n%s", runErr, redactBrowserOutput(text, []string{password}))
	}
	t.Log(strings.TrimSpace(text))
}
