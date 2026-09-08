package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/Runarry/ProxyLoom/internal/ir"
	"github.com/gin-gonic/gin"
)

const SessionCookieName = "proxyloom_session"
const MaxAuthJSONBytes = 16 << 10

type sessionContextKey struct{}

// SessionFromContext returns only a session established by RequireSession.
// Its ID is secret and must never be included in API responses or logs.
func SessionFromContext(ctx context.Context) (identity.Session, bool) {
	session, ok := ctx.Value(sessionContextKey{}).(identity.Session)
	return session, ok
}

func (a *Authentication) mount(router *gin.Engine) {
	router.POST("/api/v1/setup", a.setup)
	router.POST("/api/v1/auth/login", a.login)
	router.POST("/api/v1/auth/logout", a.RequireSession(), a.logout)
	router.GET("/api/v1/auth/me", a.RequireSession(), a.me)
	router.POST("/api/v1/auth/reauth", a.RequireSession(), a.reauthenticate)
}

// RequireSession accepts only the management cookie. The identity service is
// consulted on every request, including reads, so revocation and database loss
// take effect immediately. Mutating requests also require Origin and CSRF.
func (a *Authentication) RequireSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if foreignCredential(c.Request) {
			a.fail(c, identity.ErrUnauthenticated)
			return
		}
		token, err := sessionCookie(c.Request, true)
		if err != nil {
			a.fail(c, err)
			return
		}
		session, err := a.service.Authenticate(c.Request.Context(), token)
		if err != nil {
			a.fail(c, err)
			return
		}
		if session.User.Role != "administrator" || session.User.ScopeID.Validate() != nil {
			a.fail(c, identity.ErrForbidden)
			return
		}
		if session.ID != token || session.CSRFToken == "" {
			a.fail(c, identity.ErrUnavailable)
			return
		}
		if !session.ExpiresAt.After(time.Now()) {
			a.fail(c, identity.ErrUnauthenticated)
			return
		}
		if mutation(c.Request.Method) {
			values := c.Request.Header.Values("X-CSRF-Token")
			if !a.validOrigin(c.Request) || len(values) != 1 || len(values[0]) != len(session.CSRFToken) || subtle.ConstantTimeCompare([]byte(values[0]), []byte(session.CSRFToken)) != 1 {
				a.fail(c, identity.ErrForbidden)
				return
			}
		}
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), sessionContextKey{}, session))
		c.Next()
	}
}

// RequireScope checks an expected server-owned scope after RequireSession.
// Resource routes must obtain scope from the context, never from request JSON.
func (a *Authentication) RequireScope(scopeID ir.ID) gin.HandlerFunc {
	return func(c *gin.Context) {
		session, ok := SessionFromContext(c.Request.Context())
		if !ok {
			a.fail(c, identity.ErrUnauthenticated)
			return
		}
		if scopeID.Validate() != nil || session.User.ScopeID != scopeID {
			a.fail(c, identity.ErrForbidden)
			return
		}
		c.Next()
	}
}

// RequireRecentAuthentication requires an explicit recent password check.
// Sensitive reads must additionally use AuditSensitive, which rechecks the
// authoritative session and commits its audit record before output is released.
func (a *Authentication) RequireRecentAuthentication() gin.HandlerFunc {
	return func(c *gin.Context) {
		session, ok := SessionFromContext(c.Request.Context())
		if !ok {
			a.fail(c, identity.ErrUnauthenticated)
			return
		}
		now := time.Now()
		if session.ReauthenticatedAt.IsZero() || session.ReauthenticatedAt.After(now) || !session.ReauthenticatedAt.Add(identity.ReauthenticationLifetime).After(now) {
			a.fail(c, identity.ErrReauthenticationRequired)
			return
		}
		c.Next()
	}
}

// AuditSensitive fails closed unless the identity service has authorized and
// durably audited this allowlisted action. It never accepts an action from JSON.
func (a *Authentication) AuditSensitive(action identity.SensitiveAction) gin.HandlerFunc {
	return func(c *gin.Context) {
		session, ok := SessionFromContext(c.Request.Context())
		if !ok {
			a.fail(c, identity.ErrUnauthenticated)
			return
		}
		if !action.Valid() {
			a.fail(c, identity.ErrForbidden)
			return
		}
		source, err := a.source(c.Request)
		if err == nil {
			err = a.service.AuditSensitive(c.Request.Context(), session.ID, action, source)
		}
		if err != nil {
			a.fail(c, err)
			return
		}
		c.Next()
	}
}

func mutation(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func foreignCredential(r *http.Request) bool {
	return len(r.Header.Values("Authorization")) != 0 || r.TLS != nil && len(r.TLS.PeerCertificates) != 0
}

func sessionCookie(r *http.Request, required bool) (string, error) {
	var token string
	count := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name == SessionCookieName {
			count++
			token = cookie.Value
		}
	}
	if count > 1 {
		return "", identity.ErrUnauthenticated
	}
	decoded, valid := identity.TokenBytes(token)
	clear(decoded)
	if !valid {
		if required {
			return "", identity.ErrUnauthenticated
		}
		return "", nil
	}
	return token, nil
}

func (a *Authentication) publicWrite(c *gin.Context) (identity.Source, bool) {
	if foreignCredential(c.Request) {
		a.fail(c, identity.ErrUnauthenticated)
		return identity.Source{}, false
	}
	if !a.validOrigin(c.Request) {
		a.fail(c, identity.ErrForbidden)
		return identity.Source{}, false
	}
	source, err := a.source(c.Request)
	if err != nil {
		a.fail(c, err)
		return identity.Source{}, false
	}
	return source, true
}

func (a *Authentication) setup(c *gin.Context) {
	source, ok := a.publicWrite(c)
	if !ok {
		return
	}
	var request apicontract.SetupRequest
	if !a.readRequest(c, "SetupRequest", &request, false) {
		return
	}
	session, err := a.service.Setup(c.Request.Context(), request.SetupToken, request.Username, request.Password, source)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.sessionResponse(c, http.StatusCreated, session, true)
}

func (a *Authentication) login(c *gin.Context) {
	// Strict Origin plus JSON also protects login CSRF before a session exists.
	source, ok := a.publicWrite(c)
	if !ok {
		return
	}
	var request apicontract.LoginRequest
	if !a.readRequest(c, "LoginRequest", &request, false) {
		return
	}
	oldSession, err := sessionCookie(c.Request, false)
	if err != nil {
		a.fail(c, err)
		return
	}
	session, err := a.service.Login(c.Request.Context(), request.Username, request.Password, oldSession, source)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.sessionResponse(c, http.StatusOK, session, true)
}

func (a *Authentication) me(c *gin.Context) {
	session, _ := SessionFromContext(c.Request.Context())
	a.sessionResponse(c, http.StatusOK, session, false)
}

func (a *Authentication) reauthenticate(c *gin.Context) {
	var request apicontract.ReauthenticationRequest
	if !a.readRequest(c, "ReauthenticationRequest", &request, false) {
		return
	}
	source, err := a.source(c.Request)
	if err != nil {
		a.fail(c, err)
		return
	}
	current, _ := SessionFromContext(c.Request.Context())
	session, err := a.service.Reauthenticate(c.Request.Context(), current.ID, request.Password, source)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.sessionResponse(c, http.StatusOK, session, true)
}

func (a *Authentication) logout(c *gin.Context) {
	var request apicontract.LogoutRequest
	if !a.readRequest(c, "LogoutRequest", &request, true) {
		return
	}
	session, _ := SessionFromContext(c.Request.Context())
	if err := a.service.Logout(c.Request.Context(), session.ID); err != nil {
		a.fail(c, err)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: a.secureCookie, SameSite: http.SameSiteLaxMode, MaxAge: -1,
		Expires: time.Unix(1, 0).UTC(),
	})
	respond(c, http.StatusOK, gin.H{"request_id": apicontract.RequestID(c.Request.Context()), "data": gin.H{"acknowledged": true}})
}

func (a *Authentication) readRequest(c *gin.Context, schema string, destination any, allowEmpty bool) bool {
	if len(c.Request.Header.Values("Content-Type")) != 1 {
		a.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return false
	}
	if c.Request.ContentLength > MaxAuthJSONBytes {
		a.fail(c, apicontract.NewError(apicontract.InputLimitExceeded))
		return false
	}
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, MaxAuthJSONBytes+1))
	defer clear(data)
	if len(data) > MaxAuthJSONBytes {
		a.fail(c, apicontract.NewError(apicontract.InputLimitExceeded))
		return false
	}
	if err != nil {
		a.fail(c, apicontract.NewError(apicontract.MalformedRequest))
		return false
	}
	if len(data) == 0 && allowEmpty {
		data = []byte("{}")
	}
	bounded := c.Request.Clone(c.Request.Context())
	bounded.Body = io.NopCloser(bytes.NewReader(data))
	bounded.ContentLength = int64(len(data))
	if err := apicontract.ReadRequest(bounded, schema, destination); err != nil {
		a.fail(c, err)
		return false
	}
	return true
}

func (a *Authentication) sessionResponse(c *gin.Context, status int, session identity.Session, issueCookie bool) {
	response, err := apicontract.NewSessionResponse(apicontract.RequestID(c.Request.Context()), session)
	if err != nil {
		a.fail(c, err)
		return
	}
	if issueCookie {
		decoded, valid := identity.TokenBytes(session.ID)
		clear(decoded)
		maxAge := int(time.Until(session.ExpiresAt).Seconds())
		if !valid || maxAge < 1 {
			a.fail(c, identity.ErrUnavailable)
			return
		}
		http.SetCookie(c.Writer, &http.Cookie{
			Name: SessionCookieName, Value: session.ID, Path: "/", HttpOnly: true,
			Secure: a.secureCookie, SameSite: http.SameSiteLaxMode,
			Expires: session.ExpiresAt.UTC(), MaxAge: maxAge,
		})
	}
	respond(c, status, response)
}

func (a *Authentication) fail(c *gin.Context, err error) {
	var code apicontract.Code
	switch {
	case errors.Is(err, identity.ErrUnauthenticated):
		code = apicontract.AuthRequired
	case errors.Is(err, identity.ErrForbidden):
		code = apicontract.PermissionDenied
	case errors.Is(err, identity.ErrReauthenticationRequired):
		code = apicontract.ReauthRequired
	case errors.Is(err, identity.ErrSetupComplete):
		code = apicontract.StateConflict
	case errors.Is(err, identity.ErrRateLimited):
		code = apicontract.RateLimited
		c.Header("Retry-After", strconv.Itoa(int(identity.RateWindow/time.Second)))
	case errors.Is(err, identity.ErrInvalidInput):
		code = apicontract.ValidationFailed
	case errors.Is(err, identity.ErrUnavailable):
		code = apicontract.ServiceUnavailable
	}
	failure := apicontract.AsError(err)
	if code != "" {
		failure = apicontract.NewError(code)
	}
	respond(c, failure.HTTPStatus(), failure.Response(apicontract.RequestID(c.Request.Context())))
	c.Abort()
}
