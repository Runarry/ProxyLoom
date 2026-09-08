package server

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const managementRequestBudget = 10 * time.Second
const importCommitBudget = 120 * time.Second

// Socket write deadlines alone do not cancel a pgx query on a half-open
// connection. Give management work a context deadline so database loss returns
// a safe service-unavailable response before the transport write deadline.
func managementDeadline() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Next()
			return
		}
		budget := managementRequestBudget
		switch c.FullPath() {
		case "/api/v1/imports/:id/commit":
			budget = importCommitBudget
		case "/api/v1/jobs/:id/events":
			// Streams are finite; reconnect uses the persisted event sequence.
			budget = 110 * time.Second
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), budget)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
