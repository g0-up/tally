package org

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Handler serves the organisation endpoints of tenant hosts.
type Handler struct {
	svc *Service
}

// NewHandler returns the HTTP adapter of svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes are the route groups the composition root hands to Register.
type Routes struct {
	// Tenant is the tenant-host group behind session, host guard, CSRF, entitlement and support
	// read-only.
	Tenant *gin.RouterGroup
	// Commands returns the custom-method router of a resource under a group.
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
	// Reauth guards sensitive actions (middleware.RequireRecentAuth).
	Reauth gin.HandlerFunc
}

// Register adds the organisation routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Commands == nil || r.Reauth == nil {
		panic("org: Register needs the tenant group, the command router and the re-auth guard")
	}
	g := r.Tenant
	g.GET("/tenant", h.getTenant)
	g.PATCH("/tenant", h.patchTenant)
	g.GET("/tenant/settings", h.getSettings)
	g.PATCH("/tenant/settings", h.patchSettings)
	g.POST("/tenant/sandbox/reset", r.Reauth, h.resetSandbox)
	g.GET("/store", h.getStore)

	g.GET("/period-locks", h.listPeriodLocks)
	g.POST("/period-locks", h.lockPeriod)
	g.POST("/period-locks/reopen", r.Reauth, h.reopenPeriod)
	g.POST("/exception-confirmations", h.confirmException)

	g.GET("/audit-events", h.listAuditEvents)
	g.GET("/audit-events/:id", h.getAuditEvent)

	g.POST("/attachments", h.createAttachment)
	r.Commands(g, "attachments").Item("complete", h.completeAttachment)
	g.GET("/attachments/:id/url", h.attachmentURL)
}

// scopeOf is the tenant of the request; support sessions read in read-only transactions.
func scopeOf(c *gin.Context) (Scope, error) {
	p, ok := middleware.PrincipalOf(c)
	tenantID := middleware.TenantID(c)
	if !ok || tenantID == uuid.Nil {
		return Scope{}, apperr.NewUnauthenticated()
	}
	return Scope{TenantID: tenantID, ReadOnly: p.Kind == middleware.PrincipalSupport}, nil
}

func (h *Handler) getTenant(c *gin.Context) {
	scope, err := scopeOf(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	t, err := h.svc.Tenant(c.Request.Context(), scope.TenantID)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, t.Version)
	c.JSON(http.StatusOK, toTenantDTO(t))
}

func (h *Handler) patchTenant(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	expected, err := httpx.IfMatch(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var patch TenantPatch
	if err := httpx.BindJSON(c, &patch); err != nil {
		httpx.RenderError(c, err)
		return
	}
	t, err := h.svc.UpdateTenant(c.Request.Context(), actor, expected, patch)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, t.Version)
	c.JSON(http.StatusOK, toTenantDTO(t))
}

func (h *Handler) getSettings(c *gin.Context) {
	scope, err := scopeOf(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	st, err := h.svc.Settings(c.Request.Context(), scope)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, st.Version)
	c.JSON(http.StatusOK, toSettingsDTO(st))
}

func (h *Handler) patchSettings(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	expected, err := httpx.IfMatch(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var patch SettingsPatch
	if err := httpx.BindJSON(c, &patch); err != nil {
		httpx.RenderError(c, err)
		return
	}
	st, err := h.svc.UpdateSettings(c.Request.Context(), actor, expected, patch)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, st.Version)
	c.JSON(http.StatusOK, toSettingsDTO(st))
}

func (h *Handler) resetSandbox(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	t, err := h.svc.ResetSandbox(c.Request.Context(), actor)
	if err != nil {
		var tooSoon *ResetTooSoonError
		if errors.As(err, &tooSoon) {
			c.Header("Retry-After", strconv.Itoa(int(math.Ceil(tooSoon.RetryAfter.Seconds()))))
		}
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, t.Version)
	c.JSON(http.StatusOK, toTenantDTO(t))
}

func (h *Handler) getStore(c *gin.Context) {
	scope, err := scopeOf(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	s, w, err := h.svc.StoreAndWarehouse(c.Request.Context(), scope)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toStoreDTO(s, w))
}

func (h *Handler) listPeriodLocks(c *gin.Context) {
	scope, err := scopeOf(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	locks, err := h.svc.PeriodLocks(c.Request.Context(), scope, page)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(locks.Events, page, PeriodLockCursor)
	out := periodLocksDTO{Version: locks.Version, Data: make([]periodLockDTO, 0, len(list.Data)), NextCursor: list.NextCursor}
	out.LockedThrough = dateString(locks.LockedThrough)
	for _, ev := range list.Data {
		out.Data = append(out.Data, toPeriodLockDTO(ev))
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) lockPeriod(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req lockRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	through, err := parseDate("locked_through", req.LockedThrough)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	ev, err := h.svc.LockPeriod(c.Request.Context(), actor, through)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toPeriodLockDTO(ev))
}

// reopenPeriod binds the period_reopen confirmation to the exact body: the payload hash is
// computed over the request as sent, minus exception_confirmation_id. locked_through null
// unlocks every period.
func (h *Handler) reopenPeriod(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	body, err := httpx.ReadBody(c, 0)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req reopenRequest
	if err := httpx.DecodeJSON(body, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	hash, err := exception.PayloadHash(body)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	through, err := parseOptionalDate("locked_through", req.LockedThrough)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	ev, err := h.svc.ReopenPeriod(c.Request.Context(), actor, ReopenInput{
		LockedThrough: through, Reason: req.Reason, ConfirmationID: req.ExceptionConfirmationID, PayloadHash: hash,
	})
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toPeriodLockDTO(ev))
}

func (h *Handler) confirmException(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req confirmationRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	hash, err := parseHash("payload_hash", req.PayloadHash)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	conf, err := h.svc.ConfirmException(c.Request.Context(), actor, ConfirmInput{
		Kind: exception.Kind(req.Kind), DocType: req.DocType, DocID: req.DocID, DocVersion: req.DocVersion,
		PayloadHash: hash, Impact: req.Impact, Note: req.Note,
	})
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toConfirmationDTO(conf))
}

func (h *Handler) listAuditEvents(c *gin.Context) {
	scope, err := scopeOf(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	f, err := auditFilterOf(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	events, err := h.svc.AuditEvents(c.Request.Context(), scope, f, page)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(events, page, AuditEventCursor)
	out := httpx.List[auditEventDTO]{Data: make([]auditEventDTO, 0, len(list.Data)), NextCursor: list.NextCursor}
	for _, ev := range list.Data {
		out.Data = append(out.Data, toAuditEventDTO(ev))
	}
	c.JSON(http.StatusOK, out)
}

// auditFilterOf reads entity_type, entity_id, actor_id, action, from, to and q.
func auditFilterOf(c *gin.Context) (AuditFilter, error) {
	f := AuditFilter{EntityType: strings.TrimSpace(c.Query("entity_type")), Action: strings.TrimSpace(c.Query("action")),
		Query: strings.TrimSpace(c.Query("q"))}
	fields := map[string]string{}
	if utf8.RuneCountInString(f.Query) > AuditQueryMaxLen {
		fields["q"] = "too_long"
	}
	for name, dst := range map[string]*uuid.UUID{"entity_id": &f.EntityID, "actor_id": &f.ActorID} {
		if raw := c.Query(name); raw != "" {
			id, err := ids.Parse(raw)
			if err != nil {
				fields[name] = "invalid_uuid"
				continue
			}
			*dst = id
		}
	}
	if raw := c.Query("from"); raw != "" {
		d, err := parseDate("from", raw)
		if err != nil {
			fields["from"] = "invalid_date"
		}
		f.From = d
	}
	if raw := c.Query("to"); raw != "" {
		d, err := parseDate("to", raw)
		if err != nil {
			fields["to"] = "invalid_date"
		}
		f.To = d
	}
	if len(fields) > 0 {
		return AuditFilter{}, errInvalid(fields)
	}
	return f, nil
}

func (h *Handler) getAuditEvent(c *gin.Context) {
	scope, err := scopeOf(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	ev, err := h.svc.AuditEvent(c.Request.Context(), scope, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAuditEventDTO(ev))
}

func (h *Handler) createAttachment(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req attachmentRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	sum, err := parseHash("sha256", req.SHA256)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	up, err := h.svc.CreateAttachment(c.Request.Context(), actor, NewAttachment{
		OwnerType: req.OwnerType, OwnerID: req.OwnerID, FileName: req.FileName, ContentType: req.ContentType,
		SizeBytes: req.SizeBytes, SHA256: sum,
	})
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toUploadDTO(up))
}

func (h *Handler) completeAttachment(c *gin.Context) {
	actor, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	a, err := h.svc.CompleteAttachment(c.Request.Context(), actor, httpx.CommandID(c))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toAttachmentDTO(a))
}

func (h *Handler) attachmentURL(c *gin.Context) {
	scope, err := scopeOf(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	u, err := h.svc.AttachmentURL(c.Request.Context(), scope, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, attachmentURLDTO(u))
}
