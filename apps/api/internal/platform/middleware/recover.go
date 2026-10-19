package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/logging"
)

// Recover turns a handler panic into a logged 500 internal_error. http.ErrAbortHandler is
// re-raised so net/http can drop the connection as intended.
func Recover() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(p)
			}
			logging.FromContext(c.Request.Context()).Error("panic recovered",
				"method", c.Request.Method, "route", c.FullPath(),
				"panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			if c.Writer.Written() {
				c.Abort()
				return
			}
			httpx.RenderError(c, fmt.Errorf("panic: %v", p))
		}()
		c.Next()
	}
}
