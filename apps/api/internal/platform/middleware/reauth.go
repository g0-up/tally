package middleware

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
)

// ReauthWindow is how long a re-authentication unlocks sensitive actions.
const ReauthWindow = 5 * time.Minute

// RequireRecentAuth guards a sensitive action (ownership transfer, period reopen, MFA changes,
// sandbox reset, …): the session must have re-entered its password within window, otherwise the
// request gets 403 reauth_required and the client asks for the password (POST /auth/reauth)
// before retrying. now is the clock. It must run after Tenant (or the admin equivalent).
func RequireRecentAuth(window time.Duration, now func() time.Time) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := PrincipalOf(c)
		if !ok {
			httpx.RenderError(c, apperr.NewUnauthenticated())
			return
		}
		if p.ReauthenticatedAt == nil || now().Sub(*p.ReauthenticatedAt) > window || p.ReauthenticatedAt.After(now().Add(time.Minute)) {
			httpx.RenderError(c, apperr.NewForbidden("reauth_required", "Enter your password again to continue"))
			return
		}
		c.Next()
	}
}
