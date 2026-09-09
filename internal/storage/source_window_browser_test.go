package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/jobs"
	"github.com/Runarry/ProxyLoom/internal/safefetch"
	"github.com/Runarry/ProxyLoom/internal/server"
)

const sourceWindowBrowserFlag = "PROXYLOOM_SOURCE_WINDOW_BROWSER_TEST"

func TestSourceWindowBrowserAcceptance(t *testing.T) {
	if os.Getenv(sourceWindowBrowserFlag) != "true" {
		t.Skip("source-window browser acceptance requires an explicit disposable PostgreSQL environment")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal("source-window repository path unavailable")
	}
	webRoot := filepath.Join(root, "proxyloom-web", "dist")
	if info, statErr := os.Stat(filepath.Join(webRoot, "index.html")); statErr != nil || !info.Mode().IsRegular() {
		t.Fatal("source-window browser acceptance requires a completed production web build")
	}

	env := newPostgres(t, true)
	setupBytes := browserRandom(t, 32)
	setup := base64.RawURLEncoding.EncodeToString(setupBytes)
	clear(setupBytes)
	pepper := browserRandom(t, 32)
	identities, err := NewIdentity(env.runtime, identity.Options{ScopeID: env.scope, SetupToken: []byte(setup), TokenPepper: pepper})
	clear(pepper)
	if err != nil {
		t.Fatal("source-window identity construction failed")
	}
	passwordBytes := browserRandom(t, 24)
	password := "Browser-Acceptance-" + base64.RawURLEncoding.EncodeToString(passwordBytes) + "!"
	clear(passwordBytes)
	username := "source-window-admin"
	if _, err := identities.Setup(context.Background(), setup, username, password, identity.Source{IP: "127.0.0.1", RequestID: "source-window-setup"}); err != nil {
		t.Fatal("source-window administrator setup failed")
	}
	setup = ""

	queue, err := NewJobs(env.runtime, env.box)
	if err != nil {
		t.Fatal("source-window job store construction failed")
	}
	importsStore, err := NewImports(env.store, queue)
	if err != nil {
		t.Fatal("source-window import store construction failed")
	}
	loopback, err := netip.ParsePrefix("127.0.0.0/8")
	if err != nil {
		t.Fatal("source-window loopback fixture prefix invalid")
	}
	sources, err := NewSources(env.store, queue, &safefetch.Client{AllowNets: []netip.Prefix{loopback}})
	if err != nil {
		t.Fatal("source-window source store construction failed")
	}
	worker, err := jobs.NewWorker(queue, jobs.WorkerConfig{
		WorkerID: jobs.NewID(), PollInterval: 10 * time.Millisecond,
		Handlers: map[jobs.Type]jobs.Handler{jobs.ImportParse: importsStore.HandleParse, jobs.SourceRefresh: sources.HandleRefresh},
	})
	if err != nil {
		t.Fatal("source-window worker construction failed")
	}
	workerContext, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workerContext) }()
	t.Cleanup(func() {
		stopWorker()
		select {
		case workerErr := <-workerDone:
			if workerErr != nil && workerErr != context.Canceled {
				t.Errorf("source-window worker shutdown failed")
			}
		case <-time.After(5 * time.Second):
			t.Errorf("source-window worker did not stop")
		}
	})

	fixture := httptest.NewServer(sourceWindowFixture())
	t.Cleanup(fixture.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("source-window API listener unavailable")
	}
	baseURL := "http://" + listener.Addr().String()
	macKey := browserRandom(t, 32)
	mac, err := apicontract.NewCursorHMAC(macKey)
	clear(macKey)
	if err != nil {
		listener.Close()
		t.Fatal("source-window cursor key construction failed")
	}
	cursor, err := apicontract.NewCursorCodec(mac)
	if err != nil {
		listener.Close()
		t.Fatal("source-window cursor construction failed")
	}
	handler, err := server.NewHandler(webRoot, server.Dependencies{
		Database: env.runtime.Ping, Secrets: func() error { return nil }, Identity: identities,
		PublicURL: baseURL, Development: true,
		Nodes:   &server.NodeDependencies{Repository: env.store, Cursor: cursor},
		Sources: sources, Imports: importsStore, Jobs: queue, JobCursor: cursor,
	}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		listener.Close()
		t.Fatal("source-window API handler construction failed")
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
			t.Errorf("source-window API shutdown failed")
		}
	})

	if !browserWaitReady(baseURL) {
		t.Fatal("source-window API did not become ready")
	}
	commandContext, cancelCommand := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelCommand()
	node := os.Getenv("PROXYLOOM_NODE_BIN")
	if node == "" {
		node = "node"
	}
	browser := os.Getenv("PROXYLOOM_E2E_BROWSER")
	if browser == "" {
		browser = "msedge"
	}
	command := exec.CommandContext(commandContext, node, "node_modules/@playwright/test/cli.js", "test", "tests/e2e/source-window.spec.ts", "--config", "playwright.config.ts")
	command.Dir = filepath.Join(root, "proxyloom-web")
	command.Env = append(os.Environ(),
		"PROXYLOOM_E2E_BASE_URL="+baseURL,
		"PROXYLOOM_E2E_USERNAME="+username,
		"PROXYLOOM_E2E_PASSWORD="+password,
		"PROXYLOOM_E2E_BROWSER="+browser,
		"PROXYLOOM_E2E_SOURCE_FIXTURE_URL="+fixture.URL,
	)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	runErr := command.Run()
	text := output.String()
	if strings.Contains(text, password) {
		t.Fatal("source-window browser output disclosed the administrator credential")
	}
	if runErr != nil {
		t.Fatalf("source-window Playwright failed: %v\n%s", runErr, redactBrowserOutput(text, []string{password, fixture.URL}))
	}
	t.Log(strings.TrimSpace(redactBrowserOutput(text, []string{fixture.URL})))
}

func browserRandom(t *testing.T, size int) []byte {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatal("source-window random fixture generation failed")
	}
	return value
}

func browserWaitReady(baseURL string) bool {
	client := &http.Client{Timeout: time.Second}
	for attempt := 0; attempt < 100; attempt++ {
		response, err := client.Get(baseURL + "/readyz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func sourceWindowFixture() http.Handler {
	mux := http.NewServeMux()
	write := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, body)
		}
	}
	mux.HandleFunc("/manual/initial", write("http://manual.example.invalid:18080?external_key=manual-alpha#Upstream%20Alpha\n"))
	mux.HandleFunc("/manual/updated", write("http://manual.example.invalid:18081?external_key=manual-alpha#Upstream%20Beta\n"))
	mux.HandleFunc("/manual/conflict", write("http://manual.example.invalid:18082?external_key=manual-alpha#Upstream%20Gamma\n"))
	mux.HandleFunc("/safe/initial", write("http://safe.example.invalid:18090?external_key=safe-alpha#Safe%20Alpha\n"))
	mux.HandleFunc("/safe/updated", write("http://safe.example.invalid:18091?external_key=safe-alpha#Safe%20Beta\n"))
	var paged strings.Builder
	for index := 0; index < 205; index++ {
		_, _ = fmt.Fprintf(&paged, "http://p%d.example.invalid:1?external_key=paged-%03d#Paged%%20%03d\n", index, index, index)
	}
	mux.HandleFunc("/paged", write(paged.String()))
	return mux
}

func redactBrowserOutput(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}
