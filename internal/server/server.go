// Package server exposes the health endpoints, administrator API and frontend shell.
package server

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/Runarry/ProxyLoom/internal/apicontract"
	"github.com/Runarry/ProxyLoom/internal/identity"
	"github.com/gin-gonic/gin"
)

var (
	ErrWebAssetsUnavailable = errors.New("web_assets_unavailable")
	ErrServerFailed         = errors.New("http_server_failed")
)

const ReadinessTimeout = 2 * time.Second

type Dependencies struct {
	Database func(context.Context) error
	Secrets  func() error
	// A nil Identity preserves the health/static bootstrap handler.
	Identity       identity.Service
	PublicURL      string
	Development    bool
	TrustedProxies []string
}

type Handler struct {
	router   *gin.Engine
	web      *os.Root
	boundary http.Handler
}

func init() { gin.SetMode(gin.ReleaseMode) }

func NewHandler(webDir string, dependencies Dependencies, logger *slog.Logger) (*Handler, error) {
	if dependencies.Database == nil || dependencies.Secrets == nil || logger == nil {
		return nil, errors.New("server_dependencies_invalid")
	}
	var authentication *Authentication
	if dependencies.Identity != nil {
		var err error
		authentication, err = NewAuthentication(dependencies.Identity, AuthenticationConfig{
			PublicURL: dependencies.PublicURL, Development: dependencies.Development,
			TrustedProxies: dependencies.TrustedProxies,
		})
		if err != nil {
			return nil, err
		}
	}
	root, err := os.OpenRoot(webDir)
	if err != nil {
		return nil, ErrWebAssetsUnavailable
	}
	index, err := root.Stat("index.html")
	if err != nil || !index.Mode().IsRegular() {
		root.Close()
		return nil, ErrWebAssetsUnavailable
	}
	router := gin.New()
	router.RedirectTrailingSlash = false
	router.RedirectFixedPath = false
	_ = router.SetTrustedProxies(nil)
	handler := &Handler{router: router, web: root}
	router.Use(safeAccessLog(logger), safeRecovery(logger), securityHeaders())
	live := func(c *gin.Context) { respond(c, http.StatusOK, gin.H{"status": "ok"}) }
	ready := func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), ReadinessTimeout)
		defer cancel()
		if dependencies.Secrets() != nil || dependencies.Database(ctx) != nil || ctx.Err() != nil {
			respond(c, http.StatusServiceUnavailable, gin.H{"status": "not_ready"})
			return
		}
		respond(c, http.StatusOK, gin.H{"status": "ready"})
	}
	router.GET("/healthz", live)
	router.HEAD("/healthz", live)
	router.GET("/readyz", ready)
	router.HEAD("/readyz", ready)
	if authentication != nil {
		authentication.mount(router)
	}
	router.NoRoute(handler.static)
	handler.boundary = apicontract.RequestIDs(router)
	return handler, nil
}

func (h *Handler) Close() error { return h.web.Close() }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.boundary.ServeHTTP(w, r)
}

// Routes inventories the actual registered service routes. Operational health
// endpoints and the SPA fallback are excluded; method names match the contract
// manifest. Callers receive a copy, so they cannot change routing.
func (h *Handler) Routes() map[string][]string {
	routes := make(map[string][]string)
	for _, route := range h.router.Routes() {
		if route.Path == "/healthz" || route.Path == "/readyz" {
			continue
		}
		routes[route.Path] = append(routes[route.Path], strings.ToLower(route.Method))
	}
	for _, methods := range routes {
		slices.Sort(methods)
	}
	return routes
}

func notFound(c *gin.Context) {
	respond(c, http.StatusNotFound, apicontract.NewError(apicontract.ResourceNotFound).Response(apicontract.RequestID(c.Request.Context())))
}

func respond(c *gin.Context, status int, body any) {
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Type", "application/json; charset=utf-8")
		c.Status(status)
		c.Writer.WriteHeaderNow()
		return
	}
	c.JSON(status, body)
}

func (h *Handler) static(c *gin.Context) {
	request := c.Request
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		notFound(c)
		return
	}
	name := strings.TrimPrefix(request.URL.Path, "/")
	if name == "" {
		h.serveFile(c, "index.html")
		return
	}
	if !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") {
		notFound(c)
		return
	}
	segments := strings.Split(name, "/")
	for _, part := range segments {
		if strings.HasPrefix(part, ".") {
			notFound(c)
			return
		}
	}
	switch strings.ToLower(segments[0]) {
	case "api", "internal", "s", "healthz", "readyz":
		notFound(c)
		return
	}
	if info, err := h.web.Stat(name); err == nil && info.Mode().IsRegular() {
		h.serveFile(c, name)
		return
	}
	accept := request.Header.Get("Accept")
	if path.Ext(name) != "" || strings.EqualFold(segments[0], "assets") || (accept != "" && !strings.Contains(accept, "text/html")) {
		notFound(c)
		return
	}
	h.serveFile(c, "index.html")
}

func (h *Handler) serveFile(c *gin.Context, name string) {
	file, err := h.web.Open(name)
	if err != nil {
		notFound(c)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		notFound(c)
		return
	}
	c.Set("safe_route", "static")
	// This is a rooted file handle, not a filesystem path taken from the URL.
	http.ServeContent(c.Writer, c.Request, name, info.ModTime(), file)
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		c.Next()
	}
}

func safeRecovery(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				// Panic values, request headers, and URLs can all contain secrets.
				logger.Error("http_panic", "error_code", "INTERNAL_ERROR")
				if !c.Writer.Written() {
					respond(c, http.StatusInternalServerError, apicontract.NewError(apicontract.InternalError).Response(apicontract.RequestID(c.Request.Context())))
				}
				c.Abort()
			}
		}()
		c.Next()
	}
}

func safeAccessLog(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		route := c.FullPath()
		if !safeRegisteredRoute(route) {
			route = "unmatched"
			if value, ok := c.Get("safe_route"); ok && value == "static" {
				route = "static"
			}
		}
		method := c.Request.Method
		switch method {
		case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		default:
			method = "other"
		}
		logger.Info("http_request", "request_id", apicontract.RequestID(c.Request.Context()), "route", route, "method", method, "status", c.Writer.Status(), "duration_ms", time.Since(started).Milliseconds())
	}
}

func safeRegisteredRoute(route string) bool {
	switch route {
	case "/healthz", "/readyz", "/api/v1/setup", "/api/v1/auth/login",
		"/api/v1/auth/logout", "/api/v1/auth/me", "/api/v1/auth/reauth":
		return true
	default:
		return false
	}
}

// Serve drains requests after cancellation and bounds all HTTP I/O.
func Serve(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return ErrServerFailed
	}
	server := &http.Server{
		Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second,
		IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10,
		ErrorLog: log.New(io.Discard, "", 0),
	}
	finished := make(chan error, 1)
	go func() { finished <- server.Serve(listener) }()
	logger.Info("api_started", "mode", "bootstrap")
	select {
	case err := <-finished:
		if !errors.Is(err, http.ErrServerClosed) {
			return ErrServerFailed
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return ErrServerFailed
		}
		<-finished
	}
	logger.Info("api_stopped")
	return nil
}
