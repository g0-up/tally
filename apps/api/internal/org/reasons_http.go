package org

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// ReasonHandler serves the reason endpoints of tenant hosts.
type ReasonHandler struct {
	svc *Service
}

// NewReasonHandler returns the HTTP adapter of svc's reasons.
func NewReasonHandler(svc *Service) *ReasonHandler { return &ReasonHandler{svc: svc} }

// Register adds the reason routes.
func (h *ReasonHandler) Register(r Routes) {
	if r.Tenant == nil {
		panic("org: ReasonHandler.Register needs the tenant group")
	}
	g := r.Tenant
	g.GET("/reasons", h.list)
	g.POST("/reasons", h.create)
	g.PATCH("/reasons/:id", h.update)
	g.DELETE("/reasons/:id", h.remove)
}

type reasonDTO struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	IsSystem  bool      `json:"is_system"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

func toReasonDTO(r Reason) reasonDTO {
	return reasonDTO{ID: r.ID, Kind: r.Kind, Name: r.Name, IsSystem: r.IsSystem, Active: r.Active, CreatedAt: r.CreatedAt}
}

// reasonRequest is the body of POST /reasons and PATCH /reasons/{id}; kind is create-only.
type reasonRequest struct {
	Kind   *string `json:"kind"`
	Name   *string `json:"name"`
	Active *bool   `json:"active"`
}

func (h *ReasonHandler) list(c *gin.Context) {
	scope, err := scopeOf(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	f := ReasonFilter{Kind: c.Query("kind")}
	if raw := c.Query("active"); raw != "" {
		if f.ActiveOnly, err = strconv.ParseBool(raw); err != nil {
			httpx.RenderError(c, errInvalid(map[string]string{"active": "invalid"}))
			return
		}
	}
	reasons, err := h.svc.Reasons(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	out := httpx.List[reasonDTO]{Data: make([]reasonDTO, 0, len(reasons))}
	for _, r := range reasons {
		out.Data = append(out.Data, toReasonDTO(r))
	}
	c.JSON(http.StatusOK, out)
}

func (h *ReasonHandler) create(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req reasonRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	r, err := h.svc.CreateReason(c.Request.Context(), actor, ReasonInput(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toReasonDTO(r))
}

func (h *ReasonHandler) update(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req reasonRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	r, err := h.svc.UpdateReason(c.Request.Context(), actor, id, ReasonInput(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toReasonDTO(r))
}

func (h *ReasonHandler) remove(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	if err := h.svc.DeleteReason(c.Request.Context(), actor, id); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
