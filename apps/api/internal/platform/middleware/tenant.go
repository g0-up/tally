package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
)

const tenantKey = "middleware.tenant_id"

// Tenant requires a tenant session and enforces the tenant-host guard: the session tenant must
// equal the tenant of the request host. A missing session, an unknown slug and a session of
// another store all get the identical 401 unauthenticated, so a misplaced cookie never works on
// another store and the response never reveals whether a slug exists.
func Tenant() gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := PrincipalOf(c)
		host, hostOK := httpx.HostOf(c)
		if !ok || !hostOK || p.TenantID == uuid.Nil || !host.KnownTenant() || p.TenantID != host.TenantID ||
			(p.Kind != PrincipalOwner && p.Kind != PrincipalSupport) {
			httpx.RenderError(c, apperr.NewUnauthenticated())
			return
		}
		c.Set(tenantKey, p.TenantID)
		c.Next()
	}
}

// TenantID returns the tenant of the request set by Tenant (uuid.Nil outside tenant routes).
func TenantID(c *gin.Context) uuid.UUID {
	if v, ok := c.Get(tenantKey); ok {
		if id, ok := v.(uuid.UUID); ok {
			return id
		}
	}
	return uuid.Nil
}

// Actor builds the audit/posting actor of a tenant request.
func Actor(c *gin.Context) (audit.Actor, error) {
	p, ok := PrincipalOf(c)
	tenantID := TenantID(c)
	if !ok || tenantID == uuid.Nil {
		return audit.Actor{}, apperr.NewUnauthenticated()
	}
	a := audit.Actor{
		TenantID:  tenantID,
		Type:      audit.ActorOwner,
		ID:        p.UserID,
		Source:    audit.SourceWeb,
		RequestID: RequestIDFrom(c.Request.Context()),
		IP:        c.ClientIP(),
	}
	if p.Kind == PrincipalSupport {
		a.Type, a.Source = audit.ActorSupport, audit.SourceSupport
	}
	return a, nil
}
