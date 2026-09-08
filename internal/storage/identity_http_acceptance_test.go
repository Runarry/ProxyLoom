package storage

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/server"
	"github.com/gin-gonic/gin"
)

const (
	identityAcceptanceUsername    = "acceptance-admin"
	identityAcceptancePassword    = "SYNTHETIC_T006_PASSWORD_OLD"
	identityAcceptanceNewPassword = "SYNTHETIC_T006_PASSWORD_NEW"
)

// TestPostgresIdentityHTTPSAcceptance crosses the real TLS, HTTP, identity,
// migration and PostgreSQL boundaries. Unit tests cover malformed request
// matrices and storage races; this test proves the layers agree on one end-to-
// end administrator lifecycle and fail closed when their authority disappears.
func TestPostgresIdentityHTTPSAcceptance(t *testing.T) {
	env := newPostgres(t, true)
	setupToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x46}, 32))
	pepper := bytes.Repeat([]byte{0x7a}, 32)

	service, err := NewIdentity(env.runtime, identity.Options{
		ScopeID: env.scope, SetupToken: []byte(setupToken), TokenPepper: pepper,
	})
	if err != nil {
		t.Fatal("could not initialize the PostgreSQL identity service")
	}
	first := newIdentityAcceptanceServer(t, env, service)
	defer first.close()

	response, body := first.do(t, http.MethodPost, "/api/v1/setup",
		`{"setup_token":"`+setupToken+`","username":"`+identityAcceptanceUsername+`","password":"`+identityAcceptancePassword+`"}`, "", "")
	setup := requireAcceptanceSession(t, response, body, http.StatusCreated)
	setupCookie := requireAcceptanceCookie(t, response)
	assertAcceptanceBodySecretsAbsent(t, body, setupToken, identityAcceptancePassword, setupCookie.Value)
	if setup.Data.RecentAuthenticationExpiresAt != nil {
		t.Fatal("initial setup incorrectly established recent reauthentication")
	}

	// Reconstruct both service and handler over the same database. The setup
	// latch and session must survive process-shaped reconstruction.
	restartedService, err := NewIdentity(env.runtime, identity.Options{
		ScopeID: env.scope, SetupToken: []byte(setupToken), TokenPepper: pepper,
	})
	if err != nil {
		t.Fatal("could not reconstruct the PostgreSQL identity service")
	}
	restarted := newIdentityAcceptanceServer(t, env, restartedService)
	defer restarted.close()
	restarted.client.Jar = first.client.Jar

	response, body = restarted.do(t, http.MethodGet, "/api/v1/auth/me", "", "", "")
	persisted := requireAcceptanceSession(t, response, body, http.StatusOK)
	if persisted.Data.UserID != setup.Data.UserID || persisted.Data.CSRFToken != setup.Data.CSRFToken {
		t.Fatal("persisted administrator session changed across service reconstruction")
	}
	response, body = restarted.do(t, http.MethodPost, "/api/v1/setup",
		`{"setup_token":"`+setupToken+`","username":"second-admin","password":"SYNTHETIC_T006_SECOND_PASSWORD"}`, "", "")
	requireAcceptanceError(t, response, body, http.StatusConflict, apicontract.StateConflict)

	// Login replaces a caller's prior session. A stale pre-login cookie cannot
	// authenticate even though it remains otherwise well-formed.
	response, body = restarted.do(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+identityAcceptanceUsername+`","password":"`+identityAcceptancePassword+`"}`, "", "")
	loggedIn := requireAcceptanceSession(t, response, body, http.StatusOK)
	loginCookie := requireAcceptanceCookie(t, response)
	if loginCookie.Value == setupCookie.Value || loggedIn.Data.CSRFToken == setup.Data.CSRFToken {
		t.Fatal("login did not rotate the prior session and CSRF identity")
	}
	assertAcceptanceBodySecretsAbsent(t, body, identityAcceptancePassword, loginCookie.Value)
	response, body = restarted.doWith(t, restarted.statelessClient(), http.MethodGet, "/api/v1/auth/me", "", "", "", setupCookie)
	requireAcceptanceError(t, response, body, http.StatusUnauthorized, apicontract.AuthRequired)

	// Foreign credential types cannot augment or substitute for the management
	// cookie. These requests carry an otherwise valid cookie to prove rejection
	// is caused by crossing the credential boundary.
	response, body = restarted.do(t, http.MethodGet, "/api/v1/auth/me", "", "", "Bearer SYNTHETIC_SUBSCRIPTION_TOKEN")
	requireAcceptanceError(t, response, body, http.StatusUnauthorized, apicontract.AuthRequired)
	mtlsClient := restarted.mtlsClient(t)
	response, body = restarted.doWith(t, mtlsClient, http.MethodGet, "/api/v1/auth/me", "", "", "", nil)
	requireAcceptanceError(t, response, body, http.StatusUnauthorized, apicontract.AuthRequired)

	// A separate concurrently valid session can log out without revoking the
	// first administrator session.
	secondClient := restarted.statelessClient()
	jar, jarErr := cookiejar.New(nil)
	if jarErr != nil {
		t.Fatal("could not initialize a secondary cookie jar")
	}
	secondClient.Jar = jar
	response, body = restarted.doWith(t, secondClient, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+identityAcceptanceUsername+`","password":"`+identityAcceptancePassword+`"}`, "", "", nil)
	secondSession := requireAcceptanceSession(t, response, body, http.StatusOK)
	secondCookie := requireAcceptanceCookie(t, response)
	response, body = restarted.doWith(t, secondClient, http.MethodPost, "/api/v1/auth/logout", `{}`, secondSession.Data.CSRFToken, "", nil)
	requireAcceptanceAcknowledgement(t, response, body)
	response, body = restarted.doWith(t, restarted.statelessClient(), http.MethodGet, "/api/v1/auth/me", "", "", "", secondCookie)
	requireAcceptanceError(t, response, body, http.StatusUnauthorized, apicontract.AuthRequired)
	response, body = restarted.do(t, http.MethodGet, "/api/v1/auth/me", "", "", "")
	requireAcceptanceSession(t, response, body, http.StatusOK)

	// Sensitive output is blocked until explicit password reauthentication and
	// is released only after its PostgreSQL audit row commits.
	response, body = restarted.do(t, http.MethodGet, "/acceptance/sensitive", "", "", "")
	requireAcceptanceError(t, response, body, http.StatusForbidden, apicontract.ReauthRequired)
	response, body = restarted.do(t, http.MethodPost, "/api/v1/auth/reauth",
		`{"password":"`+identityAcceptancePassword+`"}`, loggedIn.Data.CSRFToken, "")
	reauthenticated := requireAcceptanceSession(t, response, body, http.StatusOK)
	if reauthenticated.Data.RecentAuthenticationExpiresAt == nil || !reauthenticated.Data.RecentAuthenticationExpiresAt.After(time.Now()) {
		t.Fatal("explicit reauthentication did not establish a future recent-authentication deadline")
	}
	response, body = restarted.do(t, http.MethodGet, "/acceptance/sensitive", "", "", "")
	if response.StatusCode != http.StatusOK || !bytes.Equal(bytes.TrimSpace(body), []byte(`{"data":"released"}`)) {
		t.Fatal("recently reauthenticated sensitive request was not released")
	}
	var sensitiveAudits int
	if err := env.runtime.QueryRow(env.ctx,
		"SELECT count(*) FROM public.identity_audit_events WHERE action='secret.reveal' AND outcome='success'").Scan(&sensitiveAudits); err != nil || sensitiveAudits != 1 {
		t.Fatal("sensitive output was not backed by exactly one committed success audit")
	}

	// Routes that remain contract-only must not become an SPA success or an
	// accidental authenticated API surface.
	response, body = restarted.do(t, http.MethodGet, "/api/v1/nodes", "", "", "")
	requireAcceptanceError(t, response, body, http.StatusNotFound, apicontract.ResourceNotFound)

	// A controlled reset invalidates the active cookie and old password. The new
	// password restores access without exposing any credential in storage or logs.
	if err := restartedService.ResetPassword(env.ctx, identityAcceptanceUsername, identityAcceptanceNewPassword); err != nil {
		t.Fatal("controlled password reset failed")
	}
	response, body = restarted.do(t, http.MethodGet, "/api/v1/auth/me", "", "", "")
	requireAcceptanceError(t, response, body, http.StatusUnauthorized, apicontract.AuthRequired)
	response, body = restarted.do(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+identityAcceptanceUsername+`","password":"`+identityAcceptancePassword+`"}`, "", "")
	requireAcceptanceError(t, response, body, http.StatusUnauthorized, apicontract.AuthRequired)
	response, body = restarted.do(t, http.MethodPost, "/api/v1/auth/login",
		`{"username":"`+identityAcceptanceUsername+`","password":"`+identityAcceptanceNewPassword+`"}`, "", "")
	afterReset := requireAcceptanceSession(t, response, body, http.StatusOK)
	afterResetCookie := requireAcceptanceCookie(t, response)
	assertAcceptancePersistence(t, env, afterResetCookie.Value, afterReset.Data.CSRFToken)

	allLogs := first.logs.String() + restarted.logs.String()
	assertAcceptanceBodySecretsAbsent(t, []byte(allLogs), setupToken, identityAcceptancePassword,
		identityAcceptanceNewPassword, setupCookie.Value, loginCookie.Value, secondCookie.Value,
		afterResetCookie.Value, setup.Data.CSRFToken, loggedIn.Data.CSRFToken, secondSession.Data.CSRFToken,
		afterReset.Data.CSRFToken)

	// Keep a known-valid session until the final operation. Once the runtime pool
	// is unavailable, even a read with that cookie must fail closed with 503.
	env.runtime.Close()
	response, body = restarted.do(t, http.MethodGet, "/api/v1/auth/me", "", "", "")
	requireAcceptanceError(t, response, body, http.StatusServiceUnavailable, apicontract.ServiceUnavailable)
}

type identityAcceptanceServer struct {
	tlsServer *httptest.Server
	handler   *server.Handler
	client    *http.Client
	logs      *bytes.Buffer
	url       string
}

func newIdentityAcceptanceServer(t *testing.T, env *postgresEnv, service identity.Service) *identityAcceptanceServer {
	t.Helper()
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<!doctype html>"), 0600); err != nil {
		t.Fatal("could not create acceptance web root")
	}
	tlsServer := httptest.NewUnstartedServer(nil)
	publicURL := "https://" + tlsServer.Listener.Addr().String()
	logs := &bytes.Buffer{}
	handler, err := server.NewHandler(webDir, server.Dependencies{
		Database: func(ctx context.Context) error { return env.runtime.Ping(ctx) },
		Secrets:  func() error { return nil }, Identity: service, PublicURL: publicURL,
	}, slog.New(slog.NewTextHandler(logs, nil)))
	if err != nil {
		tlsServer.Close()
		t.Fatal("could not initialize acceptance HTTP handler")
	}
	authentication, err := server.NewAuthentication(service, server.AuthenticationConfig{PublicURL: publicURL})
	if err != nil {
		_ = handler.Close()
		tlsServer.Close()
		t.Fatal("could not initialize acceptance sensitive-route guard")
	}
	sensitive := gin.New()
	sensitive.GET("/acceptance/sensitive", authentication.RequireSession(),
		authentication.RequireRecentAuthentication(), authentication.AuditSensitive(identity.SensitiveRevealSecret),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"data": "released"}) })
	mux := http.NewServeMux()
	// Test-only routes need the same outer server-owned request-ID boundary as
	// Handler.ServeHTTP; otherwise middleware errors cannot satisfy the API
	// response/header contract that the production handler enforces.
	mux.Handle("/acceptance/sensitive", apicontract.RequestIDs(sensitive))
	mux.Handle("/", handler)
	tlsServer.Config.Handler = mux
	tlsServer.TLS = &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequestClientCert}
	tlsServer.StartTLS()
	jar, err := cookiejar.New(nil)
	if err != nil {
		_ = handler.Close()
		tlsServer.Close()
		t.Fatal("could not initialize acceptance cookie jar")
	}
	client := tlsServer.Client()
	client.Jar = jar
	return &identityAcceptanceServer{tlsServer: tlsServer, handler: handler, client: client, logs: logs, url: publicURL}
}

func (s *identityAcceptanceServer) close() {
	s.tlsServer.Close()
	_ = s.handler.Close()
}

func (s *identityAcceptanceServer) statelessClient() *http.Client {
	return &http.Client{Transport: s.client.Transport}
}

func (s *identityAcceptanceServer) mtlsClient(t *testing.T) *http.Client {
	t.Helper()
	base, ok := s.client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("acceptance client did not expose a cloneable TLS transport")
	}
	transport := base.Clone()
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	transport.TLSClientConfig.Certificates = []tls.Certificate{newAcceptanceClientCertificate(t)}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Jar: s.client.Jar}
}

func newAcceptanceClientCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal("could not generate synthetic client certificate key")
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkixName("ProxyLoom synthetic Runner"),
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal("could not create synthetic client certificate")
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal("could not parse synthetic client certificate")
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private, Leaf: certificate}
}

// pkixName is kept tiny so the test certificate carries no environment identity.
func pkixName(commonName string) pkix.Name { return pkix.Name{CommonName: commonName} }

func (s *identityAcceptanceServer) do(t *testing.T, method, path, body, csrf, authorization string) (*http.Response, []byte) {
	t.Helper()
	return s.doWith(t, s.client, method, path, body, csrf, authorization, nil)
}

func (s *identityAcceptanceServer) doWith(t *testing.T, client *http.Client, method, path, body, csrf, authorization string, cookie *http.Cookie) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, s.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal("could not construct acceptance HTTP request")
	}
	if method == http.MethodPost {
		request.Header.Set("Origin", s.url)
		request.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("acceptance HTTPS request failed")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	if err != nil {
		t.Fatal("could not read acceptance HTTP response")
	}
	return response, data
}

func requireAcceptanceSession(t *testing.T, response *http.Response, body []byte, status int) apicontract.SessionResponse {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("session endpoint returned status %d, expected %d", response.StatusCode, status)
	}
	var session apicontract.SessionResponse
	if err := json.Unmarshal(body, &session); err != nil {
		t.Fatal("session endpoint did not return its typed JSON response")
	}
	if session.RequestID == "" || response.Header.Get("X-Request-ID") != session.RequestID ||
		session.Data.UserID.Validate() != nil || session.Data.Username != identityAcceptanceUsername ||
		session.Data.Role != "administrator" || len(session.Data.CSRFToken) != 43 || !session.Data.SessionExpiresAt.After(time.Now()) {
		t.Fatal("session endpoint returned inconsistent administrator metadata")
	}
	return session
}

func requireAcceptanceCookie(t *testing.T, response *http.Response) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Cookies() {
		if cookie.Name != server.SessionCookieName {
			continue
		}
		if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode ||
			cookie.Path != "/" || cookie.MaxAge < 1 || len(cookie.Value) != 43 {
			t.Fatal("management session cookie did not enforce the HTTPS cookie contract")
		}
		return cookie
	}
	t.Fatal("session response omitted the management cookie")
	return nil
}

func requireAcceptanceError(t *testing.T, response *http.Response, body []byte, status int, code apicontract.Code) {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("error endpoint returned status %d, expected %d", response.StatusCode, status)
	}
	var failure apicontract.ErrorResponse
	if err := json.Unmarshal(body, &failure); err != nil || failure.Error.Code != code ||
		failure.RequestID == "" || response.Header.Get("X-Request-ID") != failure.RequestID {
		t.Fatalf("error endpoint did not return the expected %s contract", code)
	}
}

func requireAcceptanceAcknowledgement(t *testing.T, response *http.Response, body []byte) {
	t.Helper()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("logout returned status %d, expected 200", response.StatusCode)
	}
	var acknowledgement struct {
		RequestID string `json:"request_id"`
		Data      struct {
			Acknowledged bool `json:"acknowledged"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &acknowledgement); err != nil || !acknowledgement.Data.Acknowledged ||
		acknowledgement.RequestID == "" || response.Header.Get("X-Request-ID") != acknowledgement.RequestID {
		t.Fatal("logout did not return the typed acknowledgement")
	}
}

func assertAcceptancePersistence(t *testing.T, env *postgresEnv, sessionToken, csrfToken string) {
	t.Helper()
	var passwordHash string
	if err := env.runtime.QueryRow(env.ctx,
		"SELECT password_hash FROM public.users WHERE login=$1", identityAcceptanceUsername).Scan(&passwordHash); err != nil {
		t.Fatal("could not inspect persisted administrator credential metadata")
	}
	if !strings.HasPrefix(passwordHash, "$proxyloom$v=1$argon2id$") ||
		strings.Contains(passwordHash, identityAcceptancePassword) || strings.Contains(passwordHash, identityAcceptanceNewPassword) {
		t.Fatal("administrator credential was not stored as the expected dedicated password hash")
	}
	sessionBytes, ok := identity.TokenBytes(sessionToken)
	if !ok {
		t.Fatal("issued session token was not canonical")
	}
	defer clear(sessionBytes)
	csrfBytes, ok := identity.TokenBytes(csrfToken)
	if !ok {
		t.Fatal("issued CSRF token was not canonical")
	}
	defer clear(csrfBytes)
	rows, err := env.runtime.Query(env.ctx, "SELECT id_hash FROM public.sessions")
	if err != nil {
		t.Fatal("could not inspect persisted session digests")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var digest []byte
		if err := rows.Scan(&digest); err != nil {
			t.Fatal("could not read a persisted session digest")
		}
		if len(digest) != 32 || bytes.Equal(digest, sessionBytes) || bytes.Equal(digest, csrfBytes) {
			t.Fatal("session storage retained a raw session or CSRF credential")
		}
		count++
	}
	if rows.Err() != nil || count != 1 {
		t.Fatal("password reset lifecycle left an unexpected persisted session set")
	}
}

func assertAcceptanceBodySecretsAbsent(t *testing.T, data []byte, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && bytes.Contains(data, []byte(secret)) {
			t.Fatal("an acceptance response or log retained a raw credential")
		}
	}
}
