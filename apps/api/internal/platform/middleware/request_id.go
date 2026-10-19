// Package middleware holds the gin middleware chain. The order is fixed by the app composition
// root: RequestID → Logger → Recover → HostContext → SecurityHeaders → RateLimit → Session →
// Tenant (+ tenant-host guard) → CSRF → Entitlement → SupportReadOnly → handler. Session, tenant
// host and entitlement lookups go through small interfaces implemented by domain packages.
package middleware

import (
	"context"
	"regexp"

	"github.com/gin-gonic/gin"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// RequestIDHeader carries the request id in both directions.
const RequestIDHeader = "X-Request-Id"

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

type requestIDKey struct{}

// RequestID reuses a well-formed incoming X-Request-Id (set by the edge) or generates one, echoes
// it in the response and stores it on the request context.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDHeader)
		if !requestIDPattern.MatchString(id) {
			id = ids.New().String()
		}
		c.Header(RequestIDHeader, id)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), requestIDKey{}, id))
		c.Next()
	}
}

// RequestIDFrom returns the request id stored by RequestID ("" outside a request).
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
