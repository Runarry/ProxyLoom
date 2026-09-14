package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Runarry/ProxyLoom/internal/operations"
	"github.com/gin-gonic/gin"
)

var durationBounds = []float64{.005, .025, .1, .5, 2, 10, 60}

type metricLabel struct{ Route, Method, Class string }
type metricObservation struct {
	Count   uint64
	Seconds float64
	Buckets [7]uint64
}
type httpMetrics struct {
	mu        sync.Mutex
	requests  map[metricLabel]metricObservation
	inflight  atomic.Int64
	tokenHash [32]byte
	enabled   bool
	snapshot  func(context.Context) (operations.Overview, error)
}

func newHTTPMetrics(token []byte, snapshot func(context.Context) (operations.Overview, error)) *httpMetrics {
	return &httpMetrics{requests: map[metricLabel]metricObservation{}, tokenHash: sha256.Sum256(token), enabled: len(token) > 0, snapshot: snapshot}
}
func (m *httpMetrics) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		m.inflight.Add(1)
		defer func() {
			m.inflight.Add(-1)
			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			method := c.Request.Method
			switch method {
			case "GET", "POST", "PATCH", "DELETE", "PUT", "HEAD", "OPTIONS":
			default:
				method = "other"
			}
			class := strconv.Itoa(c.Writer.Status()/100) + "xx"
			label := metricLabel{route, method, class}
			seconds := time.Since(start).Seconds()
			m.mu.Lock()
			v := m.requests[label]
			v.Count++
			v.Seconds += seconds
			for i, bound := range durationBounds {
				if seconds <= bound {
					v.Buckets[i]++
				}
			}
			m.requests[label] = v
			m.mu.Unlock()
		}()
		c.Next()
	}
}
func (m *httpMetrics) serve(c *gin.Context) {
	c.Set("safe_route", "metrics")
	if !m.enabled {
		c.Status(http.StatusNotFound)
		return
	}
	header := c.GetHeader("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || len(header) != len("Bearer ")+43 {
		c.Status(http.StatusUnauthorized)
		return
	}
	digest := sha256.Sum256([]byte(strings.TrimPrefix(header, "Bearer ")))
	if subtle.ConstantTimeCompare(digest[:], m.tokenHash[:]) != 1 {
		c.Status(http.StatusUnauthorized)
		return
	}
	if c.Request.URL.RawQuery != "" {
		c.Status(http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	data, err := m.snapshot(ctx)
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	m.write(c.Writer, data)
}
func (m *httpMetrics) write(w io.Writer, data operations.Overview) {
	m.mu.Lock()
	keys := make([]metricLabel, 0, len(m.requests))
	values := make(map[metricLabel]metricObservation, len(m.requests))
	for k, v := range m.requests {
		keys = append(keys, k)
		values[k] = v
	}
	m.mu.Unlock()
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.Route != b.Route {
			return a.Route < b.Route
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.Class < b.Class
	})
	fmt.Fprintln(w, "# TYPE proxyloom_http_requests_total counter\n# TYPE proxyloom_http_request_duration_seconds histogram")
	for _, key := range keys {
		v := values[key]
		labels := fmt.Sprintf("route=%q,method=%q,status_class=%q", key.Route, key.Method, key.Class)
		fmt.Fprintf(w, "proxyloom_http_requests_total{%s} %d\n", labels, v.Count)
		for i, bound := range durationBounds {
			fmt.Fprintf(w, "proxyloom_http_request_duration_seconds_bucket{%s,le=%q} %d\n", labels, strconv.FormatFloat(bound, 'g', -1, 64), v.Buckets[i])
		}
		fmt.Fprintf(w, "proxyloom_http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\nproxyloom_http_request_duration_seconds_count{%s} %d\nproxyloom_http_request_duration_seconds_sum{%s} %.9g\n", labels, v.Count, labels, v.Count, labels, v.Seconds)
	}
	fmt.Fprintln(w, "# TYPE proxyloom_http_inflight gauge\n# TYPE proxyloom_jobs gauge\n# TYPE proxyloom_resources gauge")
	fmt.Fprintf(w, "proxyloom_http_inflight %d\n", m.inflight.Load())
	for _, state := range []string{"queued", "leased", "running", "succeeded", "failed", "canceled", "timed_out"} {
		fmt.Fprintf(w, "proxyloom_jobs{state=%q} %d\n", state, data.Jobs[state])
	}
	for _, kind := range []string{"node", "chain", "source", "subscription_profile", "policy_group", "routing_profile", "dns_profile", "rule_set", "client_preset"} {
		fmt.Fprintf(w, "proxyloom_resources{kind=%q} %d\n", kind, data.Resources[kind])
	}
	fmt.Fprintln(w, "# TYPE proxyloom_budget_bytes gauge")
	fmt.Fprintf(w, "proxyloom_budget_bytes{state=\"limit\"} %d\nproxyloom_budget_bytes{state=\"reserved\"} %d\nproxyloom_budget_bytes{state=\"settled\"} %d\n", data.Budget.Limit, data.Budget.Reserved, data.Budget.Settled)
	cleanup, backup := int64(0), int64(0)
	if data.LastCleanup != nil {
		cleanup = data.LastCleanup.Unix()
	}
	if data.LastBackup != nil {
		backup = data.LastBackup.Unix()
	}
	paused := 0
	if data.CleanupPaused {
		paused = 1
	}
	fmt.Fprintf(w, "# TYPE proxyloom_cleanup_last_success_timestamp_seconds gauge\nproxyloom_cleanup_last_success_timestamp_seconds %d\n# TYPE proxyloom_backup_last_success_timestamp_seconds gauge\nproxyloom_backup_last_success_timestamp_seconds %d\n# TYPE proxyloom_cleanup_paused gauge\nproxyloom_cleanup_paused %d\n", cleanup, backup, paused)
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	fmt.Fprintf(w, "# TYPE proxyloom_go_goroutines gauge\nproxyloom_go_goroutines %d\n# TYPE proxyloom_go_heap_bytes gauge\nproxyloom_go_heap_bytes %d\n", runtime.NumGoroutine(), memory.HeapAlloc)
}
