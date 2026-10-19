package middleware

import (
	"context"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/urls"
)

// TenantHostResolver maps a tenant slug to its tenant id; found is false for unknown slugs.
type TenantHostResolver interface {
	ResolveSlug(ctx context.Context, slug string) (tenantID uuid.UUID, found bool, err error)
}

// Negative cache settings.
const (
	negativeTTL        = 60 * time.Second
	maxNegativeEntries = 10000
)

// TenantHostCache wraps a resolver with an in-process cache. A store keeps its slug for life; the
// only address that moves to another tenant is a sandbox's on reset, and that purges it. So a
// resolved slug is cached until purged (one entry per tenant). Unknown slugs come from untrusted
// Host headers, so they are cached for 60 s in a bounded LRU: when full, the oldest unknown slug
// is evicted for the new one, which keeps repeated probes of the same names off the database.
// Purge evicts a slug (tenant purge, sandbox reset, sign-up of a negatively cached slug). Purges
// reach this process only: several replicas need a TTL or a broadcast. The edge chain rate-limits
// by client IP before HostContext, so cache misses are throttled too.
type TenantHostCache struct {
	next TenantHostResolver
	now  func() time.Time

	mu       sync.Mutex
	positive map[string]uuid.UUID
	negative *lru[struct{}] // touched when cached; expires negativeTTL later
}

var _ TenantHostResolver = (*TenantHostCache)(nil)

// NewTenantHostCache returns a cache in front of next.
func NewTenantHostCache(next TenantHostResolver) *TenantHostCache {
	return &TenantHostCache{next: next, now: time.Now, positive: map[string]uuid.UUID{}, negative: newLRU[struct{}](maxNegativeEntries)}
}

// ResolveSlug implements TenantHostResolver.
func (c *TenantHostCache) ResolveSlug(ctx context.Context, slug string) (uuid.UUID, bool, error) {
	now := c.now()
	c.mu.Lock()
	id, ok := c.positive[slug]
	neg, isNeg := c.negative.get(slug)
	unknown := isNeg && now.Before(neg.at.Add(negativeTTL))
	c.mu.Unlock()
	if ok {
		return id, true, nil
	}
	if unknown {
		return uuid.Nil, false, nil
	}

	id, found, err := c.next.ResolveSlug(ctx, slug)
	if err != nil {
		return uuid.Nil, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if found {
		c.positive[slug] = id
		c.negative.remove(slug)
		return id, true, nil
	}
	c.negative.dropThrough(now.Add(-negativeTTL))
	c.negative.put(slug, struct{}{}, now)
	return uuid.Nil, false, nil
}

// Purge evicts slug from both caches.
func (c *TenantHostCache) Purge(slug string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.positive, slug)
	c.negative.remove(slug)
}

// slugShape is the loosest slug the database accepts; other labels are never looked up.
var slugShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,38}[a-z0-9]$`)

// ClassifyHost classifies a Host header value against the root host (no port): apex and "www."
// are root, "admin." is admin, any other single extra label is a tenant slug. Ports are
// ignored. ok is false for everything else (deeper labels, foreign hosts, IP literals).
func ClassifyHost(hostHeader, rootHost string) (kind httpx.HostKind, slug string, ok bool) {
	host := strings.ToLower(strings.TrimSpace(hostHeader))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.TrimSuffix(strings.Trim(host, "[]"), ".")
	if host == "" || net.ParseIP(host) != nil {
		return "", "", false
	}
	root := strings.ToLower(rootHost)
	if host == root || host == "www."+root {
		return httpx.HostRoot, "", true
	}
	label, isSub := strings.CutSuffix(host, "."+root)
	if !isSub || label == "" || strings.Contains(label, ".") {
		return "", "", false
	}
	if label == urls.AdminLabel {
		return httpx.HostAdmin, "", true
	}
	return httpx.HostTenant, label, true
}

// HostContext classifies the request host (see ClassifyHost) and stores it for httpx.OnHosts
// and the tenant-host guard. Unclassifiable hosts get 421 misdirected_request. A tenant host
// whose slug is unknown still classifies as tenant: whether a store exists is never revealed.
func HostContext(rootHost string, resolver TenantHostResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		kind, slug, ok := ClassifyHost(c.Request.Host, rootHost)
		if !ok {
			httpx.RenderError(c, apperr.New(apperr.MisdirectedRequest, "misdirected_request", "This host is not served here"))
			return
		}
		host := httpx.Host{Kind: kind, Slug: slug}
		if kind == httpx.HostTenant && slugShape.MatchString(slug) && !strings.Contains(slug, "--") {
			id, found, err := resolver.ResolveSlug(c.Request.Context(), slug)
			if err != nil {
				httpx.RenderError(c, err)
				return
			}
			if found {
				host.TenantID = id
			}
		}
		httpx.SetHost(c, host)
		c.Next()
	}
}
