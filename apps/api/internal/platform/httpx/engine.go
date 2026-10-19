// Package httpx holds the HTTP conventions shared by every handler: the gin engine factory, the
// JSON error renderer, strict JSON binding, cursor pagination, ETag/If-Match versions, the
// command router for "{id}:{verb}" paths and host-kind route guards.
package httpx

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// ClientIPHeader is the header cloudflared adds with the real client address.
const ClientIPHeader = "CF-Connecting-IP"

// EngineOptions configures NewEngine.
type EngineOptions struct {
	// TrustedProxyCIDRs lists the networks whose peers may set ClientIPHeader (the reverse proxy
	// network). Empty trusts nobody: ClientIP is always the TCP peer.
	TrustedProxyCIDRs []string
}

// NewEngine builds a gin engine without default middleware. c.ClientIP() honours
// CF-Connecting-IP only when the TCP peer is a trusted proxy; gin's TrustedPlatform shortcut is
// not used because it trusts the header from anyone.
func NewEngine(opts EngineOptions) (*gin.Engine, error) {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.RemoteIPHeaders = []string{ClientIPHeader}
	e.TrustedPlatform = ""
	e.ForwardedByClientIP = true
	if err := e.SetTrustedProxies(opts.TrustedProxyCIDRs); err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}
	// Request.Context() values (logger, request id) are visible through c.Value.
	e.ContextWithFallback = true
	e.HandleMethodNotAllowed = false
	e.RedirectTrailingSlash = false
	e.RedirectFixedPath = false
	e.NoRoute(func(c *gin.Context) {
		RenderError(c, apperr.NewNotFound("not_found", "Not found"))
	})
	return e, nil
}

// MethodIsSafe reports methods that never change state (no CSRF, idempotency or write checks).
func MethodIsSafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
