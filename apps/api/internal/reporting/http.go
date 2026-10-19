package reporting

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Handler serves the reports, saved layouts and exports of tenant hosts.
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

// suggestionReports maps the kinds of /reports/suggestions/{kind} to their reports.
var suggestionReports = map[string]string{"buy": ReportBuy, "clearance": ReportClearance}

// pagingParams are the query parameters of paging, not of the report.
var pagingParams = map[string]bool{"cursor": true, "limit": true}

// Register adds the reporting routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Commands == nil {
		panic("reporting: Register needs the tenant group and the command router")
	}
	r.Tenant.GET("/reports", h.catalogue)
	r.Tenant.GET("/reports/dashboard", h.dashboard)
	r.Tenant.GET("/reports/suggestions/:kind", h.suggestions)
	r.Tenant.GET("/reports/:reportKey", h.report)
	r.Tenant.GET("/saved-reports", h.listSaved)
	r.Tenant.POST("/saved-reports", h.createSaved)
	r.Tenant.PATCH("/saved-reports/:id", h.updateSaved)
	r.Tenant.DELETE("/saved-reports/:id", h.deleteSaved)
	r.Tenant.GET("/export-jobs", h.listExports)
	r.Tenant.POST("/export-jobs", h.createExport)
	r.Tenant.GET("/export-jobs/:id", h.getExport)
	r.Tenant.GET("/export-jobs/:id/download", h.download)
	cmds := r.Commands(r.Tenant, "export-jobs")
	cmds.Item("cancel", h.cancelExport)
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

// reportQuery is the query string as report parameters: the first value of each, paging left out.
func reportQuery(c *gin.Context) map[string]string {
	raw := map[string]string{}
	for k, v := range c.Request.URL.Query() {
		if len(v) > 0 && !pagingParams[k] {
			raw[k] = v[0]
		}
	}
	return raw
}

func (h *Handler) catalogue(c *gin.Context) {
	if _, ok := scopeFor(c); !ok {
		return
	}
	reports := h.svc.Registry().Reports()
	data := make([]reportDTO, len(reports))
	for i, r := range reports {
		data[i] = toReportDTO(r)
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (h *Handler) dashboard(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	var date *time.Time
	if raw := c.Query("date"); raw != "" {
		d, err := time.Parse(time.DateOnly, raw)
		if err != nil {
			httpx.RenderError(c, errInvalid(map[string]string{"date": "invalid_date"}))
			return
		}
		date = &d
	}
	d, err := h.svc.Dashboard(c.Request.Context(), scope, c.DefaultQuery("period", PeriodMonth), date)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toDashboardDTO(d))
}

func (h *Handler) suggestions(c *gin.Context) {
	key, ok := suggestionReports[c.Param("kind")]
	if !ok {
		httpx.RenderError(c, apperr.NewNotFound(CodeUnknownReport, "report not found"))
		return
	}
	h.run(c, key)
}

// report runs the report a path names; the path may spell the key with hyphens (cash-forecast).
func (h *Handler) report(c *gin.Context) {
	h.run(c, strings.ReplaceAll(c.Param("reportKey"), "-", "_"))
}

func (h *Handler) run(c *gin.Context, name string) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	t, err := h.svc.Run(c.Request.Context(), scope, name, reportQuery(c), Page{After: page.After, Limit: page.Limit})
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTableDTO(t))
}

func (h *Handler) listSaved(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	rows, err := h.svc.ListSaved(c.Request.Context(), scope, c.Query("report_key"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	data := make([]savedReportDTO, len(rows))
	for i, r := range rows {
		data[i] = toSavedDTO(r)
	}
	c.JSON(http.StatusOK, httpx.List[savedReportDTO]{Data: data})
}

func (h *Handler) createSaved(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	var req savedReportRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	r, err := h.svc.CreateSaved(c.Request.Context(), scope, actor, SavedReportInput(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toSavedDTO(r))
}

func (h *Handler) updateSaved(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req savedReportPatchRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	r, err := h.svc.UpdateSaved(c.Request.Context(), scope, actor, id, SavedReportPatch(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSavedDTO(r))
}

func (h *Handler) deleteSaved(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	if err := h.svc.DeleteSaved(c.Request.Context(), scope, actor, id); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) createExport(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	var req exportRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	if req.Params == nil {
		req.Params = map[string]string{}
	}
	j, err := h.svc.CreateExport(c.Request.Context(), scope, actor, req.ReportKey, req.Params)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, toExportDTO(j, h.svc.clock.Now()))
}

func (h *Handler) listExports(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	f := ExportFilter{RequestedBy: actor.ID, Limit: page.FetchLimit()}
	if page.After != nil {
		at, err := time.Parse(time.RFC3339Nano, page.After.SortKey)
		if err != nil {
			httpx.RenderError(c, apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"}))
			return
		}
		f.AfterCreatedAt, f.AfterID = &at, page.After.ID
	}
	jobs, err := h.svc.ListExports(c.Request.Context(), scope, f)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list := httpx.NewList(jobs, page, func(j ExportJob) httpx.Cursor {
		return httpx.Cursor{SortKey: j.CreatedAt.Format(time.RFC3339Nano), ID: j.ID}
	})
	now := h.svc.clock.Now()
	data := make([]exportJobDTO, len(list.Data))
	for i, j := range list.Data {
		data[i] = toExportDTO(j, now)
	}
	c.JSON(http.StatusOK, httpx.List[exportJobDTO]{Data: data, NextCursor: list.NextCursor})
}

func (h *Handler) getExport(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	j, err := h.svc.GetExport(c.Request.Context(), scope, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toExportDTO(j, h.svc.clock.Now()))
}

func (h *Handler) cancelExport(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	j, err := h.svc.CancelExport(c.Request.Context(), scope, actor, httpx.CommandID(c))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toExportDTO(j, h.svc.clock.Now()))
}

func (h *Handler) download(c *gin.Context) {
	scope, ok := scopeFor(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	url, err := h.svc.DownloadURL(c.Request.Context(), scope, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, url)
}
