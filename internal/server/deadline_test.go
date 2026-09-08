package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestManagementDeadlinesBoundQueriesAndPreserveImportBudget(t *testing.T) {
	router := gin.New()
	router.Use(managementDeadline())
	check := func(want time.Duration) gin.HandlerFunc {
		return func(c *gin.Context) {
			deadline, ok := c.Request.Context().Deadline()
			remaining := time.Until(deadline)
			if !ok || remaining > want || remaining < want-time.Second {
				t.Fatal("wrong server-owned request deadline")
			}
			c.Status(http.StatusNoContent)
		}
	}
	router.GET("/api/v1/nodes", check(managementRequestBudget))
	router.POST("/api/v1/imports/:id/commit", check(importCommitBudget))
	for _, test := range []struct{ method, path string }{{"GET", "/api/v1/nodes"}, {"POST", "/api/v1/imports/example/commit"}} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(test.method, test.path, nil))
		if w.Code != 204 {
			t.Fatal("request did not complete")
		}
	}
	// Never extend a client's earlier cancellation deadline. This models a
	// blocked DB call which must be interrupted before its HTTP handler exits.
	router.GET("/api/v1/blocked", func(c *gin.Context) { <-c.Request.Context().Done(); c.Status(http.StatusServiceUnavailable) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	w := httptest.NewRecorder()
	started := time.Now()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/blocked", nil).WithContext(ctx))
	if w.Code != 503 || time.Since(started) > time.Second {
		t.Fatal("query cancellation was not propagated")
	}
}
