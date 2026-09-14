package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Runarry/ProxyLoom/internal/operations"
	"github.com/gin-gonic/gin"
)

func TestMetricsAuthenticateAndAggregateWithoutObjectOrCredentialLabels(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x74}, 32))
	h, err := NewHandler(dir, Dependencies{Database: func(context.Context) error { return nil }, Secrets: func() error { return nil }, MetricsToken: []byte(token), MetricsSnapshot: func(context.Context) (operations.Overview, error) {
		return operations.Overview{Jobs: map[string]int64{"queued": 2}, Resources: map[string]int64{"node": 7, "PRIVATE_DYNAMIC_LABEL": 999}}, nil
	}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.router.GET("/objects/:id", func(c *gin.Context) { c.Status(204) })
	h.router.GET("/synthetic-panic", func(*gin.Context) { panic("PRIVATE_PANIC_VALUE") })
	request := func(method, path, authorization string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		r.Header.Set("Cookie", "proxyloom_session=PRIVATE_COOKIE")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	request("GET", "/objects/PRIVATE_NODE_A?token=PRIVATE_URL_TOKEN", "")
	request("GET", "/objects/PRIVATE_NODE_B", "")
	request("PRIVATE_METHOD", "/PRIVATE_UNKNOWN_PATH", "")
	request("GET", "/synthetic-panic", "")
	if r := request("GET", "/metrics", ""); r.Code != 401 {
		t.Fatal("metrics accepted session without observer token", r.Code)
	}
	if r := request("GET", "/metrics", "Bearer "+strings.Repeat("x", 43)); r.Code != 401 {
		t.Fatal("metrics accepted wrong token")
	}
	r := request("GET", "/metrics", "Bearer "+token)
	if r.Code != 200 || !strings.Contains(r.Header().Get("Content-Type"), "text/plain") {
		t.Fatal("valid metrics scrape failed", r.Code)
	}
	body := r.Body.String()
	for _, secret := range []string{"PRIVATE_", token} {
		if strings.Contains(body, secret) {
			t.Fatal("metrics leaked variable input")
		}
	}
	if !strings.Contains(body, `proxyloom_http_requests_total{route="/objects/:id",method="GET",status_class="2xx"} 2`) {
		t.Fatal("requests were not aggregated by route template")
	}
	if !strings.Contains(body, `proxyloom_http_requests_total{route="/synthetic-panic",method="GET",status_class="5xx"} 1`) {
		t.Fatal("panic failure was not observed")
	}
	if !strings.Contains(body, `proxyloom_resources{kind="node"} 7`) || !strings.Contains(body, `proxyloom_jobs{state="queued"} 2`) {
		t.Fatal("durable gauges missing")
	}
}
