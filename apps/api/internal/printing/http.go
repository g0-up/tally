package printing

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Handler serves the print payloads of tenant hosts.
type Handler struct {
	svc *Service
}

// NewHandler returns the HTTP adapter of svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register adds the print route to the tenant group.
func (h *Handler) Register(g *gin.RouterGroup) {
	g.GET("/print/:docType/:id", h.print)
}

func (h *Handler) print(c *gin.Context) {
	p, ok := middleware.PrincipalOf(c)
	tenantID := middleware.TenantID(c)
	if !ok || tenantID == uuid.Nil {
		httpx.RenderError(c, apperr.NewUnauthenticated())
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var version *int
	if raw, set := c.GetQuery("version"); set {
		n, err := strconv.Atoi(raw)
		if err != nil {
			httpx.RenderError(c, errInvalid(map[string]string{"version": "invalid"}))
			return
		}
		version = &n
	}
	scope := Scope{TenantID: tenantID, ReadOnly: p.Kind == middleware.PrincipalSupport}
	out, err := h.svc.Print(c.Request.Context(), scope, c.Param("docType"), id, version)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
