package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/logging"
)

// ErrorBody is the JSON envelope of every error response.
type ErrorBody struct {
	Error ErrorPayload `json:"error"`
}

// ErrorPayload carries the machine-readable code and a safe message.
type ErrorPayload struct {
	Code          string            `json:"code"`
	Message       string            `json:"message"`
	Fields        map[string]string `json:"fields,omitempty"`
	ExceptionKind string            `json:"exception_kind,omitempty"`
}

// StatusOf maps an error kind to its HTTP status.
func StatusOf(kind apperr.Kind) int {
	switch kind {
	case apperr.Validation:
		return http.StatusUnprocessableEntity
	case apperr.NotFound:
		return http.StatusNotFound
	case apperr.Conflict, apperr.ExceptionRequired:
		return http.StatusConflict
	case apperr.PreconditionFailed:
		return http.StatusPreconditionFailed
	case apperr.PreconditionRequired:
		return http.StatusPreconditionRequired
	case apperr.PeriodLocked, apperr.TenantMaintenance:
		return http.StatusLocked
	case apperr.ReadOnlyTenant, apperr.Forbidden:
		return http.StatusForbidden
	case apperr.Unauthenticated:
		return http.StatusUnauthorized
	case apperr.RateLimited:
		return http.StatusTooManyRequests
	case apperr.MisdirectedRequest:
		return http.StatusMisdirectedRequest
	case apperr.Gone:
		return http.StatusGone
	case apperr.Timeout:
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}

// internalBody is the only body an unexpected error ever produces.
var internalBody = ErrorBody{Error: ErrorPayload{Code: "internal_error", Message: "Something went wrong; try again later"}}

// RenderError writes err as the JSON error envelope and aborts the chain. Errors that are not
// *apperr.Error (or are of kind Internal) are logged with the request logger, which carries the
// request id, and rendered as "internal_error" without detail.
func RenderError(c *gin.Context, err error) {
	e, ok := apperr.As(err)
	if !ok || e.Kind == apperr.Internal {
		logging.FromContext(c.Request.Context()).Error("request failed",
			"method", c.Request.Method, "route", c.FullPath(), "error", err.Error())
		c.AbortWithStatusJSON(http.StatusInternalServerError, internalBody)
		return
	}
	c.AbortWithStatusJSON(StatusOf(e.Kind), ErrorBody{Error: ErrorPayload{
		Code:          e.Code,
		Message:       e.Message,
		Fields:        e.Fields,
		ExceptionKind: e.ExceptionKind,
	}})
}
