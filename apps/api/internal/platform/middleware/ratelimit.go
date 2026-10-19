package middleware

import (
	"math"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
)

const (
	limiterIdleTTL = 10 * time.Minute
	// maxLimiterBuckets bounds the memory of one limiter (a bucket is a few hundred bytes).
	maxLimiterBuckets = 100_000
)

// RateLimiter is an in-process token bucket per key (client IP, or IP+email for auth routes).
// Buckets live in a bounded LRU: idle buckets expire after limiterIdleTTL, and when the limiter
// is full the least recently used bucket is evicted, so rotating keys cannot grow memory.
type RateLimiter struct {
	limit rate.Limit
	burst int
	now   func() time.Time

	mu      sync.Mutex
	buckets *lru[*rate.Limiter]
}

// NewRateLimiter allows perSecond requests per key with the given burst.
func NewRateLimiter(perSecond float64, burst int) *RateLimiter {
	return &RateLimiter{limit: rate.Limit(perSecond), burst: burst, now: time.Now, buckets: newLRU[*rate.Limiter](maxLimiterBuckets)}
}

// Allow takes a token for key; when none is available it returns the wait until the next one.
func (r *RateLimiter) Allow(key string) (bool, time.Duration) {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buckets.dropThrough(now.Add(-limiterIdleTTL))
	var lim *rate.Limiter
	if e, ok := r.buckets.get(key); ok {
		lim = e.val
		r.buckets.touch(key, now)
	} else {
		lim = rate.NewLimiter(r.limit, r.burst)
		r.buckets.put(key, lim, now)
	}
	if lim.AllowN(now, 1) {
		return true, 0
	}
	res := lim.ReserveN(now, 1)
	wait := res.DelayFrom(now)
	res.CancelAt(now)
	return false, wait
}

// Middleware limits requests by keyOf(c); a denied request gets 429 with Retry-After.
func (r *RateLimiter) Middleware(keyOf func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ok, wait := r.Allow(keyOf(c))
		if !ok {
			secs := int(math.Ceil(wait.Seconds()))
			if secs < 1 {
				secs = 1
			}
			c.Header("Retry-After", strconv.Itoa(secs))
			httpx.RenderError(c, apperr.New(apperr.RateLimited, "rate_limited", "Too many requests; try again shortly"))
			return
		}
		c.Next()
	}
}

// RateLimitByIP limits by client address (CF-Connecting-IP from a trusted proxy, else the peer),
// see ClientKey.
func RateLimitByIP(r *RateLimiter) gin.HandlerFunc {
	return r.Middleware(func(c *gin.Context) string { return ClientKey(c.ClientIP()) })
}

// ClientKey is the rate-limit key of a client address: the IPv4 address itself, or the /64 prefix
// of an IPv6 address, because one subscriber usually holds a whole /64 and could otherwise rotate
// through it to dodge the limit. Unparsable input is returned unchanged.
func ClientKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is4() {
		return addr.String()
	}
	return netip.PrefixFrom(addr, 64).Masked().String()
}
