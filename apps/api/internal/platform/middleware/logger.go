package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/g0-up/tally/apps/api/internal/platform/logging"
	"github.com/g0-up/tally/apps/api/internal/platform/metrics"
)

// Logger attaches a request logger (with request_id) to the request context, writes one access
// line per request and records request metrics. Query strings are never logged because they may
// carry tokens.
func Logger(base *slog.Logger, m *metrics.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		logger := base.With("request_id", RequestIDFrom(c.Request.Context()))
		c.Request = c.Request.WithContext(logging.WithLogger(c.Request.Context(), logger))

		c.Next()

		status := c.Writer.Status()
		elapsed := time.Since(start)
		route := c.FullPath()
		m.ObserveHTTP(c.Request.Method, route, status, elapsed)

		level := slog.LevelInfo
		switch {
		case status >= 500:
			level = slog.LevelError
		case status >= 400:
			level = slog.LevelWarn
		}
		logger.LogAttrs(c.Request.Context(), level, "http request",
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.String("route", route),
			slog.Int("status", status),
			slog.Duration("duration", elapsed),
			slog.Int("bytes", c.Writer.Size()),
			slog.String("ip", c.ClientIP()),
		)
	}
}
