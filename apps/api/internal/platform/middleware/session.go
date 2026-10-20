package middleware

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
)

// PrincipalKind says what kind of session authenticated the request.
type PrincipalKind string

// Principal kinds.
const (
	// PrincipalOwner is the store owner's own session.
	PrincipalOwner PrincipalKind = "owner"
	// PrincipalSupport is a consented, read-only platform support session inside a tenant.
	PrincipalSupport PrincipalKind = "support"
	// PrincipalPlatformAdmin is a platform admin session on the admin host.
	PrincipalPlatformAdmin PrincipalKind = "platform_admin"
)

// Principal is the authenticated identity of a request. TenantID is the only source of the
// tenant context: request bodies and the Host header never set it.
type Principal struct {
	Kind              PrincipalKind
	SessionID         uuid.UUID
	UserID            uuid.UUID
	TenantID          uuid.UUID // uuid.Nil for platform admins
	ReauthenticatedAt *time.Time
	MfaVerifiedAt     *time.Time
	IsSandbox         bool
}

// SessionResolver turns a session cookie value into a principal; found is false for unknown,
// expired or revoked tokens. Errors are infrastructure failures only.
type SessionResolver interface {
	ResolveSession(ctx context.Context, token string) (p Principal, found bool, err error)
}

// Cookies are the cookie names for one environment. Production names carry the __Host- prefix
// (host-only, Secure, Path=/); plain-http development cannot use it.
type Cookies struct {
	Session      string
	CSRF         string
	AdminSession string
	AdminCSRF    string
	// Scan is the phone's scanner session cookie.
	Scan   string
	Secure bool
}

// CookieNames returns the names for production (secure) or development.
func CookieNames(secure bool) Cookies {
	if secure {
		return Cookies{
			Session: "__Host-tally_session", CSRF: "__Host-tally_csrf",
			AdminSession: "__Host-tally_admin_session", AdminCSRF: "__Host-tally_admin_csrf",
			Scan: "__Host-tally_scan", Secure: true,
		}
	}
	return Cookies{
		Session: "tally_session", CSRF: "tally_csrf", AdminSession: "tally_admin_session", AdminCSRF: "tally_admin_csrf",
		Scan: "tally_scan",
	}
}

const principalKey = "middleware.principal"

type principalContextKey struct{}

// ContextWithPrincipal returns ctx carrying p. Session prefers it over any cookie, so in-process
// code can dispatch a request as p (the admin support view runs tenant GET handlers as the
// consented support session). Only code in this process can set it; no header or cookie can.
func ContextWithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalContextKey{}, p)
}

// Session resolves the session cookie when present and stores the principal. It never rejects:
// routes that need a session sit behind Tenant (or an admin equivalent), which answers 401.
func Session(resolver SessionResolver, cookieName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if p, ok := c.Request.Context().Value(principalContextKey{}).(Principal); ok {
			c.Set(principalKey, p)
			c.Next()
			return
		}
		token, err := c.Cookie(cookieName)
		if err != nil || token == "" {
			c.Next()
			return
		}
		p, found, err := resolver.ResolveSession(c.Request.Context(), token)
		if err != nil {
			httpx.RenderError(c, err)
			return
		}
		if found {
			c.Set(principalKey, p)
		}
		c.Next()
	}
}

// PrincipalOf returns the request principal, if a session was resolved.
func PrincipalOf(c *gin.Context) (Principal, bool) {
	v, ok := c.Get(principalKey)
	if !ok {
		return Principal{}, false
	}
	p, ok := v.(Principal)
	return p, ok
}
