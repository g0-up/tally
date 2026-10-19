package campaign

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Handler serves the campaign endpoints of tenant hosts.
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
}

// Register adds the campaign routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Commands == nil {
		panic("campaign: Register needs the tenant group and the command router")
	}
	g := r.Tenant
	g.GET("/campaigns", h.list)
	g.POST("/campaigns", h.create)
	g.GET("/campaigns/:id", h.get)
	g.PATCH("/campaigns/:id", h.update)
	g.GET("/campaigns/:id/checklist", h.checklist)
	g.PATCH("/campaigns/:id/checklist", h.updateChecklist)
	g.GET("/campaigns/:id/demand", h.demand)
	cmds := r.Commands(g, "campaigns")
	cmds.Item("activate", h.activate)
	cmds.Item("close", h.close)
	g.POST("/campaign-attributions", h.reattribute)
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

func ifMatch(c *gin.Context) (int, bool) {
	v, err := httpx.IfMatch(c)
	if err != nil {
		httpx.RenderError(c, err)
		return 0, false
	}
	return v, true
}

func page(c *gin.Context) (httpx.Page, bool) {
	p, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return httpx.Page{}, false
	}
	return p, true
}

func queryDate(c *gin.Context, name string) (*time.Time, bool) {
	raw := c.Query(name)
	if raw == "" {
		return nil, true
	}
	var d date
	if err := d.UnmarshalJSON(strconv.AppendQuote(nil, raw)); err != nil {
		httpx.RenderError(c, errInvalid(map[string]string{name: "invalid_date"}))
		return nil, false
	}
	return d.time(), true
}

// detailResponse writes a campaign with its version as the ETag.
func detailResponse(c *gin.Context, status int, d Detail, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, d.Version)
	c.JSON(status, toDetailDTO(d))
}

func (h *Handler) list(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	rows, err := h.svc.List(c.Request.Context(), scope, Filter{Status: c.Query("status"), Kind: c.Query("kind"), Search: c.Query("q")}, p)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(rows, p, func(r Campaign) httpx.Cursor { return httpx.Cursor{SortKey: dateOf(r.StartsOn), ID: r.ID} })
	c.JSON(http.StatusOK, httpx.List[campaignDTO]{Data: mapSlice(list.Data, toCampaignDTO), NextCursor: list.NextCursor})
}

func (h *Handler) create(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[campaignRequest](c)
	if !ok {
		return
	}
	d, err := h.svc.Create(c.Request.Context(), scope, actor, req.input())
	detailResponse(c, http.StatusCreated, d, err)
}

func (h *Handler) get(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	d, err := h.svc.Get(c.Request.Context(), scope, id)
	detailResponse(c, http.StatusOK, d, err)
}

func (h *Handler) update(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	req, ok := bind[campaignPatchRequest](c)
	if !ok {
		return
	}
	d, err := h.svc.Update(c.Request.Context(), scope, actor, id, version, req.input())
	detailResponse(c, http.StatusOK, d, err)
}

func (h *Handler) activate(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	d, err := h.svc.Activate(c.Request.Context(), scope, actor, httpx.CommandID(c), version)
	detailResponse(c, http.StatusOK, d, err)
}

func (h *Handler) close(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	version, ok := ifMatch(c)
	if !ok {
		return
	}
	r, err := h.svc.Close(c.Request.Context(), scope, actor, httpx.CommandID(c), version)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	httpx.SetETag(c, r.Campaign.Version)
	c.JSON(http.StatusOK, toCloseDTO(r))
}

func (h *Handler) checklist(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	items, err := h.svc.Checklist(c.Request.Context(), scope, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toChecklistDTO(items))
}

func (h *Handler) updateChecklist(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	req, ok := bind[checklistPatchRequest](c)
	if !ok {
		return
	}
	items, err := h.svc.UpdateChecklist(c.Request.Context(), scope, actor, id, req.input())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toChecklistDTO(items))
}

func (h *Handler) demand(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	from, ok := queryDate(c, "from")
	if !ok {
		return
	}
	to, ok := queryDate(c, "to")
	if !ok {
		return
	}
	p, ok := page(c)
	if !ok {
		return
	}
	dq := DemandQuery{From: from, To: to, Limit: p.FetchLimit()}
	if p.After != nil {
		dq.After = &p.After.SortKey
	}
	d, err := h.svc.Demand(c.Request.Context(), scope, id, dq)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(d.Rows, p, func(r DemandRow) httpx.Cursor { return httpx.Cursor{SortKey: r.Code, ID: r.SkuID} })
	c.JSON(http.StatusOK, demandDTO{
		CampaignID: d.CampaignID, From: dateOf(d.From), To: dateOf(d.To),
		Data: mapSlice(list.Data, toDemandRowDTO), NextCursor: list.NextCursor,
	})
}

func (h *Handler) reattribute(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	req, ok := bind[attributionRequest](c)
	if !ok {
		return
	}
	r, err := h.svc.Reattribute(c.Request.Context(), scope, actor, ReattributeInput(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toReattributeDTO(r))
}
