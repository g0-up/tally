package httpx

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

// SetETag sets the strong ETag `"<version>"` of a versioned resource.
func SetETag(c *gin.Context, version int) {
	c.Header("ETag", `"`+strconv.Itoa(version)+`"`)
}

// IfMatch returns the version the client expects from the If-Match header. A missing header is
// 428 precondition_required; a value that is not a strong version tag can never match and is
// 412 precondition_failed.
func IfMatch(c *gin.Context) (int, error) {
	raw := strings.TrimSpace(c.GetHeader("If-Match"))
	if raw == "" {
		return 0, apperr.New(apperr.PreconditionRequired, "precondition_required", "This change requires the If-Match header")
	}
	if len(raw) < 3 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return 0, apperr.NewPreconditionFailed("If-Match does not match the current version")
	}
	v, err := strconv.Atoi(raw[1 : len(raw)-1])
	if err != nil || v < 1 {
		return 0, apperr.NewPreconditionFailed("If-Match does not match the current version")
	}
	return v, nil
}
