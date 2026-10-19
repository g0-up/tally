package notification

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Handler serves the notification centre of tenant hosts.
type Handler struct {
	svc *Service
}

// NewHandler returns the HTTP adapter of svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes are the route groups the composition root hands to Register.
type Routes struct {
	Tenant   *gin.RouterGroup
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
}

// Register adds the notification routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Commands == nil {
		panic("notification: Register needs the tenant group and the command router")
	}
	r.Tenant.GET("/notifications", h.list)
	r.Tenant.GET("/notifications/unread-count", h.unreadCount)
	cmds := r.Commands(r.Tenant, "notifications")
	cmds.Item("read", h.markRead)
	cmds.Item("resolve", h.resolve)
	cmds.Item("snooze", h.snooze)
}

type notificationDTO struct {
	ID           uuid.UUID  `json:"id"`
	Kind         string     `json:"kind"`
	DedupeKey    string     `json:"dedupe_key"`
	Severity     string     `json:"severity"`
	Title        string     `json:"title"`
	Body         *string    `json:"body"`
	EntityType   *string    `json:"entity_type"`
	EntityID     *uuid.UUID `json:"entity_id"`
	ColorID      *uuid.UUID `json:"color_id"`
	ActionPath   *string    `json:"action_path"`
	DocNo        *string    `json:"doc_no"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ReadAt       *time.Time `json:"read_at"`
	ResolvedAt   *time.Time `json:"resolved_at"`
	ResolvedBy   *string    `json:"resolved_by"`
	SnoozedUntil *time.Time `json:"snoozed_until"`
}

func toDTO(n Notification) notificationDTO {
	return notificationDTO(n)
}

type unreadCountDTO struct {
	Count int `json:"count"`
}

type snoozeRequest struct {
	Until *time.Time `json:"until" validate:"required"`
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

func actorFor(c *gin.Context) (audit.Actor, bool) {
	a, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return audit.Actor{}, false
	}
	return a, true
}

func (h *Handler) list(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var after *Cursor
	if p.After != nil {
		at, err := time.Parse(time.RFC3339Nano, p.After.SortKey)
		if err != nil {
			httpx.RenderError(c, apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"}))
			return
		}
		after = &Cursor{CreatedAt: at, ID: p.After.ID}
	}
	rows, err := h.svc.List(c.Request.Context(), scope, c.Query("state"), after, p.FetchLimit())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(n Notification) httpx.Cursor {
		return httpx.Cursor{SortKey: n.CreatedAt.UTC().Format(time.RFC3339Nano), ID: n.ID}
	})
	data := make([]notificationDTO, len(list.Data))
	for i, n := range list.Data {
		data[i] = toDTO(n)
	}
	c.JSON(http.StatusOK, httpx.List[notificationDTO]{Data: data, NextCursor: list.NextCursor})
}

func (h *Handler) unreadCount(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	n, err := h.svc.UnreadCount(c.Request.Context(), scope)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, unreadCountDTO{Count: n})
}

func (h *Handler) markRead(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	n, err := h.svc.MarkRead(c.Request.Context(), actor, httpx.CommandID(c))
	respond(c, n, err)
}

func (h *Handler) resolve(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	n, err := h.svc.Resolve(c.Request.Context(), actor, httpx.CommandID(c))
	respond(c, n, err)
}

func (h *Handler) snooze(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	var req snoozeRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	n, err := h.svc.Snooze(c.Request.Context(), actor, httpx.CommandID(c), *req.Until)
	respond(c, n, err)
}

func respond(c *gin.Context, n Notification, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toDTO(n))
}
