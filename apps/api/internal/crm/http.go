package crm

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Handler serves the CRM endpoints of tenant hosts.
type Handler struct {
	svc *Service
}

// NewHandler returns the HTTP adapter of svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Register adds the CRM routes to the tenant-host group (behind session, host guard, CSRF,
// entitlement and support read-only).
func (h *Handler) Register(g *gin.RouterGroup) {
	if g == nil {
		panic("crm: Register needs the tenant group")
	}
	g.GET("/customers/last-season", h.lastSeason)
	g.GET("/customers/:id/timeline", h.timeline)
	g.POST("/customers/:id/activities", h.addActivity)
	g.GET("/customers/:id/consents", h.consents)
	g.POST("/customers/:id/consents", h.recordConsent)
	g.POST("/customers/:id/reorder-drafts", h.reorder)
	g.GET("/customers/:id/loyalty", h.loyalty)
	g.GET("/care-tasks", h.careTasks)
	g.POST("/care-tasks", h.createCareTask)
	g.PATCH("/care-tasks/:id", h.updateCareTask)
	g.GET("/loyalty/program", h.program)
	g.PUT("/loyalty/program", h.putProgram)
	g.POST("/loyalty/adjustments", h.adjust)
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

func bind[T any](c *gin.Context) (T, bool) {
	var v T
	if err := httpx.BindJSON(c, &v); err != nil {
		httpx.RenderError(c, err)
		return v, false
	}
	return v, true
}

func pathID(c *gin.Context, name string) (uuid.UUID, bool) {
	id, err := httpx.PathID(c, name)
	if err != nil {
		httpx.RenderError(c, err)
		return uuid.Nil, false
	}
	return id, true
}

func page(c *gin.Context) (httpx.Page, bool) {
	p, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return httpx.Page{}, false
	}
	return p, true
}

func queryID(c *gin.Context, name string) (*uuid.UUID, bool) {
	raw := c.Query(name)
	if raw == "" {
		return nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		httpx.RenderError(c, errInvalid(map[string]string{name: "invalid"}))
		return nil, false
	}
	return &id, true
}

func queryDate(c *gin.Context, name string) (*time.Time, bool) {
	raw := c.Query(name)
	if raw == "" {
		return nil, true
	}
	t, err := parseDate(raw)
	if err != nil {
		httpx.RenderError(c, errInvalid(map[string]string{name: "invalid_date"}))
		return nil, false
	}
	return &t, true
}

func render[T, D any](c *gin.Context, status int, v T, err error, fn func(T) D) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(status, fn(v))
}

func (h *Handler) timeline(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	tq := TimelineQuery{Kind: c.Query("kind"), Limit: p.FetchLimit()}
	if p.After != nil {
		at, err := time.Parse(time.RFC3339Nano, p.After.SortKey)
		if err != nil {
			httpx.RenderError(c, errCursor())
			return
		}
		tq.After = &TimelineCursor{CreatedAt: at, ID: p.After.ID}
	}
	rows, err := h.svc.Timeline(c.Request.Context(), scope, id, tq)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(a Activity) httpx.Cursor {
		return httpx.Cursor{SortKey: a.CreatedAt.UTC().Format(time.RFC3339Nano), ID: a.ID}
	})
	c.JSON(http.StatusOK, httpx.List[activityDTO]{Data: mapSlice(list.Data, toActivityDTO), NextCursor: list.NextCursor})
}

func (h *Handler) addActivity(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[activityRequest](c)
	if !ok {
		return
	}
	a, err := h.svc.AddActivity(c.Request.Context(), actor, id, ActivityInput(req))
	render(c, http.StatusCreated, a, err, toActivityDTO)
}

func (h *Handler) consents(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	cs, err := h.svc.Consents(c.Request.Context(), scope, id)
	render(c, http.StatusOK, cs, err, func(cs Consents) consentsDTO {
		return consentsDTO{Current: mapSlice(cs.Current, toConsentDTO), History: mapSlice(cs.History, toConsentDTO)}
	})
}

func (h *Handler) recordConsent(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var req consentRequest
	key, err := httpx.BindJSONKeyed(c, &req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	cs, replayed, err := h.svc.RecordConsent(c.Request.Context(), actor, key, id, ConsentInput{
		Channel: req.Channel, Purpose: req.Purpose, Granted: *req.Granted, Source: req.Source, Evidence: req.Evidence,
	})
	if err == nil {
		httpx.MarkReplay(c, replayed)
	}
	render(c, http.StatusCreated, cs, err, toConsentDTO)
}

func (h *Handler) careTasks(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	partyID, ok := queryID(c, "party_id")
	if !ok {
		return
	}
	dueBefore, ok := queryDate(c, "due_before")
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	var after *CareTaskCursor
	if p.After != nil {
		due, err := parseDate(p.After.SortKey)
		if err != nil {
			httpx.RenderError(c, errCursor())
			return
		}
		after = &CareTaskCursor{DueOn: due, ID: p.After.ID}
	}
	rows, err := h.svc.CareTasks(c.Request.Context(), scope,
		CareTaskFilter{Status: c.Query("status"), PartyID: partyID, DueBefore: dueBefore}, after, p.FetchLimit())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(t CareTask) httpx.Cursor { return httpx.Cursor{SortKey: dateOf(t.DueOn), ID: t.ID} })
	c.JSON(http.StatusOK, httpx.List[careTaskDTO]{Data: mapSlice(list.Data, toCareTaskDTO), NextCursor: list.NextCursor})
}

func (h *Handler) createCareTask(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[careTaskRequest](c)
	if !ok {
		return
	}
	t, err := h.svc.CreateCareTask(c.Request.Context(), actor, req.input())
	render(c, http.StatusCreated, t, err, toCareTaskDTO)
}

func (h *Handler) updateCareTask(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[careTaskPatchRequest](c)
	if !ok {
		return
	}
	t, err := h.svc.UpdateCareTask(c.Request.Context(), actor, id, req.input())
	render(c, http.StatusOK, t, err, toCareTaskDTO)
}

func (h *Handler) lastSeason(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	campaignID, ok := queryID(c, "campaign_id")
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	var after *LastSeasonCursor
	if p.After != nil {
		after = &LastSeasonCursor{NameKey: p.After.SortKey, ID: p.After.ID}
	}
	id, rows, err := h.svc.LastSeason(c.Request.Context(), scope, campaignID, after, p.FetchLimit())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(r LastSeasonCustomer) httpx.Cursor { return httpx.Cursor{SortKey: r.NameKey, ID: r.PartyID} })
	c.JSON(http.StatusOK, lastSeasonDTO{CampaignID: id, Data: mapSlice(list.Data, toLastSeasonDTO), NextCursor: list.NextCursor})
}

func (h *Handler) reorder(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	// The body is optional: without one the latest sale is repeated.
	var req reorderRequest
	var key *idempotency.Key
	var err error
	if c.Request.ContentLength != 0 {
		key, err = httpx.BindJSONKeyed(c, &req)
	} else {
		key, err = httpx.OptionalIdempotencyKey(c, nil)
	}
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	r, replayed, err := h.svc.Reorder(c.Request.Context(), actor, key, id, ReorderInput(req))
	if err == nil {
		httpx.MarkReplay(c, replayed)
	}
	render(c, http.StatusCreated, r, err, toReorderDTO)
}

func (h *Handler) program(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, err := h.svc.Program(c.Request.Context(), scope)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, p.Version)
	c.JSON(http.StatusOK, toProgramDTO(p))
}

func (h *Handler) putProgram(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	// A program never saved is at version 0: its first save sends If-Match "0".
	expected := 0
	if strings.TrimSpace(c.GetHeader("If-Match")) != `"0"` {
		v, err := httpx.IfMatch(c)
		if err != nil {
			httpx.RenderError(c, err)
			return
		}
		expected = v
	}
	req, ok := bind[programRequest](c)
	if !ok {
		return
	}
	p, err := h.svc.PutProgram(c.Request.Context(), actor, ProgramInput{
		Enabled: *req.Enabled, PointsPerAmount: *req.PointsPerAmount, PointValue: *req.PointValue,
	}, expected)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, p.Version)
	c.JSON(http.StatusOK, toProgramDTO(p))
}

func (h *Handler) loyalty(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	l, err := h.svc.Loyalty(c.Request.Context(), scope, id)
	render(c, http.StatusOK, l, err, func(l Loyalty) loyaltyDTO {
		return loyaltyDTO{PartyID: l.PartyID, Balance: l.Balance, Entries: mapSlice(l.Entries, toLoyaltyEntryDTO)}
	})
}

func (h *Handler) adjust(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	body, err := httpx.ReadBody(c, 0)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	key, err := httpx.IdempotencyKey(c, body)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req adjustmentRequest
	if err := httpx.DecodeJSON(body, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	res, err := h.svc.Adjust(c.Request.Context(), actor, key, AdjustmentInput{PartyID: req.PartyID, Points: *req.Points, Reason: req.Reason})
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.WritePosted(c, res.Status, res.Body, res.Replayed)
}
