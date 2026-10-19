package httpx

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// HostKind classifies the request host (see middleware.HostContext).
type HostKind string

// Host kinds.
const (
	HostRoot   HostKind = "root"
	HostTenant HostKind = "tenant"
	HostAdmin  HostKind = "admin"
)

// Host is the classified request host. For a tenant host Slug is set; TenantID is set only when
// the slug belongs to a tenant (unknown slugs behave like real ones until authentication).
type Host struct {
	Kind     HostKind
	Slug     string
	TenantID uuid.UUID
}

// KnownTenant reports a tenant host whose slug resolved to a tenant.
func (h Host) KnownTenant() bool { return h.Kind == HostTenant && h.TenantID != uuid.Nil }

const hostKey = "httpx.host"

// SetHost stores the classified host on the request.
func SetHost(c *gin.Context, h Host) { c.Set(hostKey, h) }

// HostOf returns the classified host; ok is false when HostContext did not run.
func HostOf(c *gin.Context) (Host, bool) {
	v, ok := c.Get(hostKey)
	if !ok {
		return Host{}, false
	}
	h, ok := v.(Host)
	return h, ok
}

// OnHosts restricts a route group to the given host kinds; any other host gets 404, as if the
// route did not exist there.
func OnHosts(kinds ...HostKind) gin.HandlerFunc {
	allowed := make(map[HostKind]bool, len(kinds))
	for _, k := range kinds {
		allowed[k] = true
	}
	return func(c *gin.Context) {
		h, ok := HostOf(c)
		if !ok || !allowed[h.Kind] {
			RenderError(c, apperr.NewNotFound("not_found", "Not found"))
			return
		}
		c.Next()
	}
}
