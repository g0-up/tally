package analytics

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Handler serves the AI Q&A of tenant hosts.
type Handler struct {
	svc *Service
}

// NewHandler returns the HTTP adapter of svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register adds the Q&A routes to the tenant-host group (behind session, host guard, CSRF,
// entitlement and support read-only).
func (h *Handler) Register(g *gin.RouterGroup) {
	if g == nil {
		panic("analytics: Register needs the tenant group")
	}
	g.POST("/analytics/questions", h.ask)
	g.GET("/analytics/questions", h.list)
}

func scopeFor(c *gin.Context) (Scope, bool) {
	p, ok := middleware.PrincipalOf(c)
	tenantID := middleware.TenantID(c)
	if !ok || tenantID == uuid.Nil {
		httpx.RenderError(c, apperr.NewUnauthenticated())
		return Scope{}, false
	}
	return Scope{TenantID: tenantID, ReadOnly: p.Kind == middleware.PrincipalSupport}, true
}

type questionRequest struct {
	Question string `json:"question" validate:"required"`
}

type queryDTO struct {
	ID        uuid.UUID  `json:"id"`
	Question  string     `json:"question"`
	Answer    *string    `json:"answer"`
	Citations []Citation `json:"citations"`
	Model     string     `json:"model"`
	AskedBy   uuid.UUID  `json:"asked_by"`
	CreatedAt time.Time  `json:"created_at"`
}

func toQueryDTO(q Query) queryDTO {
	out := queryDTO(q)
	if out.Citations == nil {
		out.Citations = []Citation{}
	}
	for i := range out.Citations {
		if out.Citations[i].Params == nil {
			out.Citations[i].Params = map[string]string{}
		}
		if out.Citations[i].Refs == nil {
			out.Citations[i].Refs = []string{}
		}
	}
	return out
}

func (h *Handler) ask(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req questionRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	q, err := h.svc.Ask(c.Request.Context(), scope, actor, req.Question)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toQueryDTO(q))
}

func (h *Handler) list(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var after *QueryCursor
	if page.After != nil {
		at, err := time.Parse(time.RFC3339Nano, page.After.SortKey)
		if err != nil {
			httpx.RenderError(c, apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"}))
			return
		}
		after = &QueryCursor{CreatedAt: at, ID: page.After.ID}
	}
	rows, err := h.svc.List(c.Request.Context(), scope, after, page.Limit)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, page, cursorOf)
	data := make([]queryDTO, len(list.Data))
	for i, q := range list.Data {
		data[i] = toQueryDTO(q)
	}
	c.JSON(http.StatusOK, httpx.List[queryDTO]{Data: data, NextCursor: list.NextCursor})
}
