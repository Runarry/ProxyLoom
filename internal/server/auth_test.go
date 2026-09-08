package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/gin-gonic/gin"
)

const testAuthPassword = "EXAMPLE_HTTP_PASSWORD"
const testAuthOrigin = "https://admin.proxyloom.invalid"

func exampleOpaque(value byte) string {
	return base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{value}, 32))
}

// This fake exercises the HTTP/service boundary. Persistence, password hashing
// and transaction/race semantics are covered with the real identity store.
type fakeIdentity struct {
	mu          sync.Mutex
	initialized bool
	next        byte
	sessions    map[string]identity.Session
	faults      map[string]error
	calls       map[string]int
	lastSource  identity.Source
	oldSession  string
	lastAction  identity.SensitiveAction
}

func newFakeIdentity() *fakeIdentity {
	return &fakeIdentity{sessions: map[string]identity.Session{}, faults: map[string]error{}, calls: map[string]int{}}
}

func (f *fakeIdentity) session(recent bool) identity.Session {
	f.next++
	now := time.Now().UTC()
	session := identity.Session{
		ID: exampleOpaque(f.next), CSRFToken: exampleOpaque(f.next + 128),
		User:      identity.User{ID: "20000000-0000-4000-8000-000000000001", ScopeID: identity.DefaultScopeID, Username: "administrator", Role: "administrator"},
		ExpiresAt: now.Add(identity.AbsoluteLifetime), LastSeenAt: now,
	}
	if recent {
		session.ReauthenticatedAt = now
	}
	f.sessions[session.ID] = session
	return session
}

func (f *fakeIdentity) call(name string, source identity.Source) error {
	f.calls[name]++
	f.lastSource = source
	return f.faults[name]
}

func (f *fakeIdentity) Setup(_ context.Context, token, username, password string, source identity.Source) (identity.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("setup", source); err != nil {
		return identity.Session{}, err
	}
	if f.initialized {
		return identity.Session{}, identity.ErrSetupComplete
	}
	if token != exampleOpaque(99) {
		return identity.Session{}, identity.ErrForbidden
	}
	if !identity.ValidUsername(username) || !identity.ValidPassword(password) {
		return identity.Session{}, identity.ErrInvalidInput
	}
	f.initialized = true
	return f.session(false), nil
}

func (f *fakeIdentity) Login(_ context.Context, username, password, previous string, source identity.Source) (identity.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("login", source); err != nil {
		return identity.Session{}, err
	}
	if username != "administrator" || password != testAuthPassword {
		return identity.Session{}, identity.ErrUnauthenticated
	}
	f.oldSession = previous
	delete(f.sessions, previous)
	return f.session(false), nil
}

func (f *fakeIdentity) Authenticate(_ context.Context, token string) (identity.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("authenticate", identity.Source{}); err != nil {
		return identity.Session{}, err
	}
	session, ok := f.sessions[token]
	if !ok {
		return identity.Session{}, identity.ErrUnauthenticated
	}
	return session, nil
}

func (f *fakeIdentity) Reauthenticate(_ context.Context, token, password string, source identity.Source) (identity.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("reauth", source); err != nil {
		return identity.Session{}, err
	}
	previous, ok := f.sessions[token]
	if !ok || password != testAuthPassword {
		return identity.Session{}, identity.ErrUnauthenticated
	}
	delete(f.sessions, token)
	session := f.session(true)
	session.ExpiresAt = previous.ExpiresAt
	f.sessions[session.ID] = session
	return session, nil
}

func (f *fakeIdentity) Logout(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("logout", identity.Source{}); err != nil {
		return err
	}
	delete(f.sessions, token)
	return nil
}

func (f *fakeIdentity) ResetPassword(context.Context, string, string) error {
	return errors.New("unused_fake_reset")
}

func (f *fakeIdentity) AuditSensitive(_ context.Context, token string, action identity.SensitiveAction, source identity.Source) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.call("audit", source); err != nil {
		return err
	}
	f.lastAction = action
	session, ok := f.sessions[token]
	if !ok {
		return identity.ErrUnauthenticated
	}
	if session.ReauthenticatedAt.IsZero() || time.Since(session.ReauthenticatedAt) >= identity.ReauthenticationLifetime {
		return identity.ErrReauthenticationRequired
	}
	return nil
}

func authDependencies(service identity.Service, origin string) Dependencies {
	dependencies := healthyDependencies()
	dependencies.Identity = service
	dependencies.PublicURL = origin
	return dependencies
}

func authRequest(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", testAuthOrigin)
	return r
}

func loginJSON() string { return `{"username":"administrator","password":"` + testAuthPassword + `"}` }

func setupJSON() string {
	return `{"setup_token":"` + exampleOpaque(99) + `","username":"administrator","password":"` + testAuthPassword + `"}`
}

func requireErrorResponse(t *testing.T, response *httptest.ResponseRecorder, status int, code apicontract.Code) {
	t.Helper()
	var failure apicontract.ErrorResponse
	if response.Code != status || json.Unmarshal(response.Body.Bytes(), &failure) != nil || failure.Error.Code != code || failure.RequestID != response.Header().Get("X-Request-ID") {
		t.Fatalf("unexpected error: status=%d body=%s", response.Code, response.Body)
	}
}

func TestHTTPSAuthenticationCookieAndCSRFLifecycle(t *testing.T) {
	service := newFakeIdentity()
	srv := httptest.NewUnstartedServer(nil)
	origin := "https://" + srv.Listener.Addr().String()
	handler, _ := testHandler(t, authDependencies(service, origin), io.Discard)
	srv.Config.Handler = handler
	srv.StartTLS()
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.Jar, _ = cookiejar.New(nil)
	do := func(method, path, body, csrf string) (*http.Response, []byte) {
		t.Helper()
		r, err := http.NewRequest(method, origin+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return response, data
	}
	readSession := func(response *http.Response, data []byte, status int) apicontract.SessionResponse {
		t.Helper()
		var session apicontract.SessionResponse
		if response.StatusCode != status || apicontract.Decode(data, "SessionResponse", &session) != nil {
			t.Fatalf("invalid session response: %d %s", response.StatusCode, data)
		}
		if session.RequestID != response.Header.Get("X-Request-ID") || session.Data.CSRFToken == "" || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("missing session boundary metadata")
		}
		return session
	}
	response, data := do(http.MethodPost, "/api/v1/setup", setupJSON(), "")
	initial := readSession(response, data, http.StatusCreated)
	cookies := response.Cookies()
	if len(cookies) != 1 {
		t.Fatal("setup did not set exactly one management cookie")
	}
	cookie := cookies[0]
	if cookie.Name != SessionCookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Domain != "" || cookie.MaxAge > int(identity.AbsoluteLifetime/time.Second) || cookie.MaxAge < int(identity.AbsoluteLifetime/time.Second)-2 {
		t.Fatal("management cookie policy is incorrect")
	}
	if bytes.Contains(data, []byte(cookie.Value)) || initial.Data.RecentAuthenticationExpiresAt != nil {
		t.Fatal("session ID leaked or setup bypassed explicit recent authentication")
	}
	response, data = do(http.MethodGet, "/api/v1/auth/me", "", "")
	if current := readSession(response, data, http.StatusOK); current.Data.CSRFToken != initial.Data.CSRFToken || len(response.Cookies()) != 0 {
		t.Fatal("me unexpectedly rotated the session")
	}
	response, _ = do(http.MethodPost, "/api/v1/setup", setupJSON(), "")
	if response.StatusCode != http.StatusConflict {
		t.Fatal("setup remained reusable")
	}
	for _, csrf := range []string{"", "EXAMPLE_WRONG_CSRF"} {
		response, _ = do(http.MethodPost, "/api/v1/auth/logout", "{}", csrf)
		if response.StatusCode != http.StatusForbidden || len(response.Cookies()) != 0 {
			t.Fatal("missing or incorrect CSRF revoked a session")
		}
	}
	// Login can rotate an existing session with Origin and JSON alone.
	response, data = do(http.MethodPost, "/api/v1/auth/login", loginJSON(), "")
	loggedIn := readSession(response, data, http.StatusOK)
	if len(response.Cookies()) != 1 || response.Cookies()[0].Value == cookie.Value || service.oldSession != cookie.Value || loggedIn.Data.CSRFToken == initial.Data.CSRFToken {
		t.Fatal("login did not rotate both cookie and CSRF")
	}
	old := authRequest(http.MethodGet, "/api/v1/auth/me", "")
	old.AddCookie(cookie)
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, old)
	requireErrorResponse(t, rejected, http.StatusUnauthorized, apicontract.AuthRequired)
	loginCookie := response.Cookies()[0]
	response, data = do(http.MethodPost, "/api/v1/auth/reauth", `{"password":"`+testAuthPassword+`"}`, loggedIn.Data.CSRFToken)
	recent := readSession(response, data, http.StatusOK)
	if recent.Data.RecentAuthenticationExpiresAt == nil || recent.Data.CSRFToken == loggedIn.Data.CSRFToken || len(response.Cookies()) != 1 || response.Cookies()[0].Value == loginCookie.Value {
		t.Fatal("reauthentication did not rotate the session and establish recent authentication")
	}
	if !recent.Data.SessionExpiresAt.Equal(loggedIn.Data.SessionExpiresAt) {
		t.Fatal("reauthentication extended the absolute session expiry")
	}
	response, _ = do(http.MethodPost, "/api/v1/auth/logout", "", recent.Data.CSRFToken)
	if response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 || response.Cookies()[0].MaxAge != -1 || !response.Cookies()[0].Secure || response.Cookies()[0].Path != "/" {
		t.Fatal("logout did not expire the management cookie")
	}
	response, _ = do(http.MethodGet, "/api/v1/auth/me", "", "")
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("logged out cookie jar remained authenticated")
	}
}

func TestAuthStrictOrigin(t *testing.T) {
	for _, path := range []string{"/api/v1/setup", "/api/v1/auth/login", "/api/v1/auth/reauth", "/api/v1/auth/logout"} {
		for _, origins := range [][]string{
			nil, {""}, {"null"}, {"https://other.invalid"}, {"http://admin.proxyloom.invalid"},
			{testAuthOrigin + ":8443"}, {testAuthOrigin + "/"}, {testAuthOrigin + "/path"},
			{testAuthOrigin + "?"}, {testAuthOrigin + "#"}, {"https://user@admin.proxyloom.invalid"},
			{testAuthOrigin + ", " + testAuthOrigin}, {testAuthOrigin + " " + testAuthOrigin},
			{testAuthOrigin, testAuthOrigin}, {" " + testAuthOrigin}, {testAuthOrigin + " "},
		} {
			service := newFakeIdentity()
			session := service.session(false)
			handler, _ := testHandler(t, authDependencies(service, testAuthOrigin), io.Discard)
			r := authRequest(http.MethodPost, path, "{}")
			r.Header["Origin"] = origins
			r.Header.Set("X-CSRF-Token", session.CSRFToken)
			r.Header.Set("X-Forwarded-Host", "admin.proxyloom.invalid")
			r.Header.Set("X-Forwarded-Proto", "https")
			r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			requireErrorResponse(t, response, http.StatusForbidden, apicontract.PermissionDenied)
			if service.calls["setup"]+service.calls["login"]+service.calls["reauth"]+service.calls["logout"] != 0 {
				t.Fatal("invalid Origin reached an identity mutation")
			}
		}
	}
}

func TestAuthRejectsCredentialSubstitutionAndAmbiguousCSRF(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(*http.Request, identity.Session)
		status int
		code   apicontract.Code
	}{
		{"no_cookie", func(r *http.Request, _ identity.Session) { r.Header.Del("Cookie") }, 401, apicontract.AuthRequired},
		{"subscription_cookie", func(r *http.Request, _ identity.Session) {
			r.Header.Set("Cookie", "subscription_token="+exampleOpaque(42))
		}, 401, apicontract.AuthRequired},
		{"subscription_as_session", func(r *http.Request, _ identity.Session) {
			r.Header.Set("Cookie", SessionCookieName+"="+exampleOpaque(42))
		}, 401, apicontract.AuthRequired},
		{"bearer_with_cookie", func(r *http.Request, _ identity.Session) {
			r.Header.Set("Authorization", "Bearer EXAMPLE_SUBSCRIPTION_TOKEN")
		}, 401, apicontract.AuthRequired},
		{"runner_certificate_with_cookie", func(r *http.Request, _ identity.Session) {
			r.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{Raw: []byte("EXAMPLE_RUNNER_CERT")}}}
		}, 401, apicontract.AuthRequired},
		{"duplicate_cookie", func(r *http.Request, s identity.Session) {
			r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: s.ID})
		}, 401, apicontract.AuthRequired},
		{"invalid_cookie", func(r *http.Request, _ identity.Session) {
			r.Header.Set("Cookie", SessionCookieName+"=EXAMPLE_INVALID_COOKIE")
		}, 401, apicontract.AuthRequired},
		{"missing_csrf", func(r *http.Request, _ identity.Session) { r.Header.Del("X-CSRF-Token") }, 403, apicontract.PermissionDenied},
		{"csrf_cookie_substitution", func(r *http.Request, s identity.Session) { r.Header.Set("X-CSRF-Token", s.ID) }, 403, apicontract.PermissionDenied},
		{"duplicate_csrf", func(r *http.Request, s identity.Session) { r.Header.Add("X-CSRF-Token", s.CSRFToken) }, 403, apicontract.PermissionDenied},
		{"comma_csrf", func(r *http.Request, s identity.Session) { r.Header.Set("X-CSRF-Token", s.CSRFToken+","+s.CSRFToken) }, 403, apicontract.PermissionDenied},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service := newFakeIdentity()
			session := service.session(false)
			var logs bytes.Buffer
			handler, _ := testHandler(t, authDependencies(service, testAuthOrigin), &logs)
			r := authRequest(http.MethodPost, "/api/v1/auth/logout", "{}")
			r.Header.Set("X-CSRF-Token", session.CSRFToken)
			r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
			scenario.change(r, session)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			requireErrorResponse(t, response, scenario.status, scenario.code)
			if service.calls["logout"] != 0 || strings.Contains(logs.String()+response.Body.String(), "EXAMPLE_") {
				t.Fatal("untrusted credential reached a mutation or leaked")
			}
		})
	}
}

func TestAuthStrictJSONAndBoundedBody(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		body   string
		change func(*http.Request)
		status int
		code   apicontract.Code
	}{
		{"scope_injection", `{"username":"administrator","password":"EXAMPLE_BODY_SECRET","scope_id":"10000000-0000-4000-8000-000000000099"}`, nil, 400, apicontract.UnknownField},
		{"actor_injection", `{"username":"administrator","password":"EXAMPLE_BODY_SECRET","user_id":"20000000-0000-4000-8000-000000000099"}`, nil, 400, apicontract.UnknownField},
		{"unknown_secret_key", `{"username":"administrator","password":"EXAMPLE_BODY_SECRET","EXAMPLE_SECRET_KEY":true}`, nil, 400, apicontract.UnknownField},
		{"duplicate_password", `{"username":"administrator","password":"EXAMPLE_BODY_SECRET","password":"EXAMPLE_SECOND_SECRET"}`, nil, 400, apicontract.DuplicateField},
		{"missing_password", `{"username":"administrator"}`, nil, 400, apicontract.MalformedRequest},
		{"null_password", `{"username":"administrator","password":null}`, nil, 400, apicontract.MalformedRequest},
		{"object_password", `{"username":"administrator","password":{"EXAMPLE_SECRET":true}}`, nil, 400, apicontract.MalformedRequest},
		{"trailing_json", loginJSON() + "{}", nil, 400, apicontract.MalformedRequest},
		{"invalid_utf8", loginJSON() + "\xff", nil, 400, apicontract.MalformedRequest},
		{"invalid_surrogate", `{"username":"administrator","password":"\uD800"}`, nil, 400, apicontract.MalformedRequest},
		{"missing_json_header", loginJSON(), func(r *http.Request) { r.Header.Del("Content-Type") }, 400, apicontract.MalformedRequest},
		{"form_content_type", loginJSON(), func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }, 400, apicontract.MalformedRequest},
		{"unsupported_charset", loginJSON(), func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=utf-16") }, 400, apicontract.MalformedRequest},
		{"duplicate_content_type", loginJSON(), func(r *http.Request) { r.Header.Add("Content-Type", "application/json") }, 400, apicontract.MalformedRequest},
		{"known_oversize", strings.Repeat(" ", MaxAuthJSONBytes+1), nil, 413, apicontract.InputLimitExceeded},
		{"unknown_size_oversize", strings.Repeat(" ", MaxAuthJSONBytes+1), func(r *http.Request) { r.ContentLength = -1 }, 413, apicontract.InputLimitExceeded},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service := newFakeIdentity()
			var logs bytes.Buffer
			handler, _ := testHandler(t, authDependencies(service, testAuthOrigin), &logs)
			r := authRequest(http.MethodPost, "/api/v1/auth/login", scenario.body)
			if scenario.change != nil {
				scenario.change(r)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			requireErrorResponse(t, response, scenario.status, scenario.code)
			if service.calls["login"] != 0 || strings.Contains(logs.String()+response.Body.String(), "EXAMPLE_") {
				t.Fatal("invalid JSON reached the service or leaked a secret")
			}
		})
	}
	service := newFakeIdentity()
	handler, _ := testHandler(t, authDependencies(service, testAuthOrigin), io.Discard)
	body := loginJSON() + strings.Repeat(" ", MaxAuthJSONBytes-len(loginJSON()))
	r := authRequest(http.MethodPost, "/api/v1/auth/login", body)
	r.ContentLength = -1
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, r)
	if response.Code != http.StatusOK || service.calls["login"] != 1 {
		t.Fatalf("valid JSON at the exact body limit was rejected: %d", response.Code)
	}
}

func TestAuthIdentityFailuresAreSanitized(t *testing.T) {
	for _, scenario := range []struct {
		failure error
		status  int
		code    apicontract.Code
	}{
		{identity.ErrUnauthenticated, 401, apicontract.AuthRequired},
		{identity.ErrForbidden, 403, apicontract.PermissionDenied},
		{identity.ErrReauthenticationRequired, 403, apicontract.ReauthRequired},
		{identity.ErrSetupComplete, 409, apicontract.StateConflict},
		{identity.ErrInvalidInput, 422, apicontract.ValidationFailed},
		{identity.ErrRateLimited, 429, apicontract.RateLimited},
		{identity.ErrUnavailable, 503, apicontract.ServiceUnavailable},
		{errors.New("EXAMPLE_DATABASE_CONNECTION_SECRET"), 500, apicontract.InternalError},
	} {
		t.Run(string(scenario.code), func(t *testing.T) {
			service := newFakeIdentity()
			service.faults["setup"] = scenario.failure
			var logs bytes.Buffer
			handler, _ := testHandler(t, authDependencies(service, testAuthOrigin), &logs)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, authRequest(http.MethodPost, "/api/v1/setup", setupJSON()))
			requireErrorResponse(t, response, scenario.status, scenario.code)
			if len(response.Result().Cookies()) != 0 || strings.Contains(logs.String()+response.Body.String(), "EXAMPLE_") {
				t.Fatal("failed authentication issued a cookie or leaked details")
			}
			if scenario.status == http.StatusTooManyRequests && response.Header().Get("Retry-After") != "60" {
				t.Fatal("rate limit omitted its retry delay")
			}
		})
	}
}

func TestAuthGuardsAndSensitiveAuditFailClosed(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		change func(*fakeIdentity, *identity.Session)
		status int
		code   apicontract.Code
	}{
		{"allowed", func(*fakeIdentity, *identity.Session) {}, 200, ""},
		{"wrong_scope", func(_ *fakeIdentity, s *identity.Session) { s.User.ScopeID = "10000000-0000-4000-8000-000000000099" }, 403, apicontract.PermissionDenied},
		{"wrong_role", func(_ *fakeIdentity, s *identity.Session) { s.User.Role = "reader" }, 403, apicontract.PermissionDenied},
		{"missing_recent", func(_ *fakeIdentity, s *identity.Session) { s.ReauthenticatedAt = time.Time{} }, 403, apicontract.ReauthRequired},
		{"expired_recent", func(_ *fakeIdentity, s *identity.Session) {
			s.ReauthenticatedAt = time.Now().Add(-identity.ReauthenticationLifetime)
		}, 403, apicontract.ReauthRequired},
		{"future_recent", func(_ *fakeIdentity, s *identity.Session) { s.ReauthenticatedAt = time.Now().Add(time.Hour) }, 403, apicontract.ReauthRequired},
		{"expired_session", func(_ *fakeIdentity, s *identity.Session) { s.ExpiresAt = time.Now().Add(-time.Second) }, 401, apicontract.AuthRequired},
		{"database_lost", func(f *fakeIdentity, _ *identity.Session) { f.faults["authenticate"] = identity.ErrUnavailable }, 503, apicontract.ServiceUnavailable},
		{"audit_lost", func(f *fakeIdentity, _ *identity.Session) { f.faults["audit"] = identity.ErrUnavailable }, 503, apicontract.ServiceUnavailable},
		{"revoked_before_audit", func(f *fakeIdentity, _ *identity.Session) { f.faults["audit"] = identity.ErrUnauthenticated }, 401, apicontract.AuthRequired},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			service := newFakeIdentity()
			session := service.session(true)
			scenario.change(service, &session)
			service.sessions[session.ID] = session
			auth, err := NewAuthentication(service, AuthenticationConfig{PublicURL: testAuthOrigin})
			if err != nil {
				t.Fatal(err)
			}
			router := gin.New()
			router.Use(securityHeaders())
			router.GET("/sensitive", auth.RequireSession(), auth.RequireScope(identity.DefaultScopeID), auth.RequireRecentAuthentication(), auth.AuditSensitive(identity.SensitiveRevealSecret), func(c *gin.Context) {
				current, ok := SessionFromContext(c.Request.Context())
				if !ok || current.User.ScopeID != identity.DefaultScopeID || service.calls["audit"] != 1 {
					t.Error("sensitive handler ran without trusted scope and completed audit")
				}
				c.String(http.StatusOK, "EXAMPLE_SENSITIVE_OUTPUT")
			})
			r := authRequest(http.MethodGet, "/sensitive", "")
			r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: session.ID})
			response := httptest.NewRecorder()
			apicontract.RequestIDs(router).ServeHTTP(response, r)
			if scenario.status == http.StatusOK {
				if response.Code != http.StatusOK || response.Body.String() != "EXAMPLE_SENSITIVE_OUTPUT" || service.lastAction != identity.SensitiveRevealSecret || !identity.ValidSource(service.lastSource) {
					t.Fatal("authorized sensitive read did not complete with an audit")
				}
			} else {
				requireErrorResponse(t, response, scenario.status, scenario.code)
				if strings.Contains(response.Body.String(), "EXAMPLE_SENSITIVE_OUTPUT") {
					t.Fatal("sensitive response escaped the failed guard")
				}
			}
		})
	}
}

func TestAuthRouteInventoryIsActualAndDoesNotMountFutureAPI(t *testing.T) {
	handler, _ := testHandler(t, authDependencies(newFakeIdentity(), testAuthOrigin), io.Discard)
	readManifest := func(name string) map[string][]string {
		t.Helper()
		data, err := os.ReadFile("../../fixtures/api/" + name)
		var manifest map[string][]string
		if err != nil || json.Unmarshal(data, &manifest) != nil {
			t.Fatalf("cannot read route manifest %s", name)
		}
		return manifest
	}
	want := readManifest("implemented-routes.json")
	// This fixture intentionally supplies only identity dependencies. Business
	// and internal transport routes are exercised with their own dependencies.
	for path := range want {
		if path != "/api/v1/setup" && !strings.HasPrefix(path, "/api/v1/auth/") {
			delete(want, path)
		}
	}
	if len(want) != 5 || !reflect.DeepEqual(handler.Routes(), want) {
		t.Fatalf("mounted routes = %#v", handler.Routes())
	}
	changed := handler.Routes()
	changed["/api/v1/setup"][0] = "delete"
	if !reflect.DeepEqual(handler.Routes(), want) {
		t.Fatal("route inventory exposed mutable routing state")
	}
	for _, path := range []string{"/api/v1/nodes", "/api/v1/settings", "/s/EXAMPLE_SUBSCRIPTION_TOKEN/xray-default", "/internal/v1/runners/heartbeat", "/api/v1/auth/login/", "/api/v1/auth/logout/"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			response := request(handler, method, path, "application/json")
			requireErrorResponse(t, response, http.StatusNotFound, apicontract.ResourceNotFound)
		}
	}
	for path, methods := range want {
		method := http.MethodGet
		if methods[0] == "get" {
			method = http.MethodPost
		}
		response := request(handler, method, path, "application/json")
		requireErrorResponse(t, response, http.StatusNotFound, apicontract.ResourceNotFound)
	}
	for path, methods := range readManifest("routes.json") {
		if _, mounted := want[path]; mounted {
			continue
		}
		for _, method := range methods {
			response := request(handler, strings.ToUpper(method), path, "application/json")
			requireErrorResponse(t, response, http.StatusNotFound, apicontract.ResourceNotFound)
		}
	}
	for _, path := range []string{"/healthz", "/readyz", "/", "/settings"} {
		if response := request(handler, http.MethodGet, path, "text/html"); response.Code != http.StatusOK {
			t.Fatalf("authentication changed bootstrap behavior for %s", path)
		}
	}
}

var _ identity.Service = (*fakeIdentity)(nil)
