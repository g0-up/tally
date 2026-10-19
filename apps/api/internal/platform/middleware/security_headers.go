package middleware

import "github.com/gin-gonic/gin"

// SecurityHeaders sets the headers every API response carries: no MIME sniffing, no caching of
// tenant data by browsers or proxies, no framing. CSP and HSTS are set at the edge.
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		c.Next()
	}
}
