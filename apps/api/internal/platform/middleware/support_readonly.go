package middleware

import (
	"github.com/gin-gonic/gin"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
)

// SupportReadOnly rejects state-changing requests made under a support session (403
// support_read_only). Support reads additionally run in WithReadOnlyTenantTx.
func SupportReadOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if p, ok := PrincipalOf(c); ok && p.Kind == PrincipalSupport && !httpx.MethodIsSafe(c.Request.Method) {
			httpx.RenderError(c, apperr.NewForbidden("support_read_only", "Support sessions are read-only"))
			return
		}
		c.Next()
	}
}
