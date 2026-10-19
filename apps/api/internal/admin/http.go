package admin

import (
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/featureflag"
	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/saas"
)

// Sign-in throttle per client address: ten attempts a minute.
const (
	loginPerSecond = 10.0 / 60
	loginBurst     = 10
)

// Handler serves the platform admin API on the admin host.
type Handler struct {
	auth      *AuthService
	directory *Directory
	saas      *saas.Service
	flags     *featureflag.Writer
	incidents *Incidents
	partners  *Partners
	feedback  *FeedbackBox
	cookies   middleware.Cookies
	csrf      *middleware.CSRFTokens
	logger    *slog.Logger
	limiter   *middleware.RateLimiter
}

// HandlerDeps are the services behind the admin API.
type HandlerDeps struct {
	Auth      *AuthService
	Directory *Directory
	SaaS      *saas.Service
	Flags     *featureflag.Writer
	Incidents *Incidents
	Partners  *Partners
	Feedback  *FeedbackBox
	Logger    *slog.Logger
}

// NewHandler returns the admin HTTP adapter.
func NewHandler(d HandlerDeps) *Handler {
	logger := d.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		auth: d.Auth, directory: d.Directory, saas: d.SaaS, flags: d.Flags, incidents: d.Incidents,
		partners: d.Partners, feedback: d.Feedback, logger: logger,
		limiter: middleware.NewRateLimiter(loginPerSecond, loginBurst),
	}
}

// Routes are the route groups the composition root hands to Register.
type Routes struct {
	// Public is the admin group without a required session (sign-in), with the admin CSRF.
	Public *gin.RouterGroup
	// Admin requires a platform admin session.
	Admin    *gin.RouterGroup
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
	CSRF     *middleware.CSRFTokens
	Cookies  middleware.Cookies
}

// Register adds the admin routes.
func (h *Handler) Register(r Routes) {
	if r.Public == nil || r.Admin == nil || r.Commands == nil || r.CSRF == nil {
		panic("admin: Register needs the public and admin groups, the command router and the CSRF tokens")
	}
	h.csrf, h.cookies = r.CSRF, r.Cookies
	throttle := middleware.RateLimitByIP(h.limiter)
	r.Public.POST("/auth/login", throttle, h.login)
	r.Public.POST("/auth/totp", throttle, h.totp)

	g := r.Admin
	g.POST("/auth/logout", h.logout)
	g.GET("/me", h.me)
	g.GET("/admins", h.admins)
	g.GET("/tenants", h.tenants)
	g.GET("/tenants/:id", h.tenant)
	g.POST(`/tenants/:id/subscription\:change`, h.changePlan)
	g.POST(`/tenants/:id/subscription\:extend`, h.extend)
	g.POST(`/tenants/:id/subscription\:set-status`, h.setStatus)
	g.POST(`/tenants/:id/account-closure\:retry-export`, h.retryClosureExport)
	g.GET("/plans", h.plans)
	g.POST("/plans", h.createPlan)
	g.PATCH("/plans/:id", h.updatePlan)
	g.GET("/saas-invoices", h.invoices)
	g.POST("/saas-invoices", h.issueInvoice)
	inv := r.Commands(g, "saas-invoices")
	inv.Item("mark-paid", h.markPaid)
	inv.Item("void", h.voidInvoice)
	g.GET("/feature-flags", h.flagList)
	g.PUT("/feature-flags", h.setFlag)
	g.GET("/feature-flags/:key/changes", h.flagChanges)
	g.DELETE("/tenants/:id/feature-flags/:key", h.removeFlagOverride)
	g.GET("/incidents", h.incidentList)
	g.POST("/incidents", h.createIncident)
	g.PATCH("/incidents/:id", h.updateIncident)
	g.GET("/partners", h.partnerList)
	g.POST("/partners", h.createPartner)
	g.GET("/partners/:id/referrals", h.referrals)
	g.GET("/feedback", h.feedbackList)
	g.GET("/usage", h.usage)
}

func adminOf(c *gin.Context) (middleware.Principal, bool) {
	p, ok := middleware.PrincipalOf(c)
	if !ok || p.Kind != middleware.PrincipalPlatformAdmin {
		httpx.RenderError(c, apperr.NewUnauthenticated())
		return middleware.Principal{}, false
	}
	return p, true
}

// actorFor is the audit actor of an admin action on tenantID; the action lands in the store's log.
func actorFor(c *gin.Context, p middleware.Principal, tenantID uuid.UUID) audit.Actor {
	return saas.AdminActor(tenantID, p.UserID, middleware.RequestIDFrom(c.Request.Context()), c.ClientIP())
}

// logAction records an admin change of a platform row, which has no tenant audit log.
func (h *Handler) logAction(c *gin.Context, p middleware.Principal, action string, attrs ...any) {
	h.logger.InfoContext(c.Request.Context(), "admin action",
		append([]any{"action", action, "admin_id", p.UserID, "request_id", middleware.RequestIDFrom(c.Request.Context())}, attrs...)...)
}

func (h *Handler) login(c *gin.Context) {
	var req loginRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	challenge, err := h.auth.Login(c.Request.Context(), req.Email, req.Password, c.ClientIP())
	if err != nil {
		renderAuthError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, authResponse{Status: "mfa_required", Challenge: &challenge})
}

func (h *Handler) totp(c *gin.Context) {
	var req totpRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	s, err := h.auth.VerifyTOTP(c.Request.Context(), req.Challenge, req.Code, c.ClientIP())
	if err != nil {
		renderAuthError(c, err)
		return
	}
	middleware.SetSessionCookie(c, h.cookies, h.cookies.AdminSession, s.Token, 0)
	token := middleware.SetCSRFCookie(c, h.csrf, h.cookies, h.cookies.AdminCSRF, middleware.SessionBinding(middleware.PrincipalPlatformAdmin, s.ID))
	expires := s.ExpiresAt.UTC()
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, authResponse{Status: "signed_in", CSRFToken: &token, ExpiresAt: &expires})
}

// renderAuthError writes err; a throttled or locked sign-in step also gets Retry-After.
func renderAuthError(c *gin.Context, err error) {
	var throttled *throttledError
	if errors.As(err, &throttled) {
		c.Header("Retry-After", strconv.Itoa(max(1, int(math.Ceil(throttled.RetryAfter.Seconds())))))
	}
	httpx.RenderError(c, err)
}

func (h *Handler) logout(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	if err := h.auth.Logout(c.Request.Context(), p); err != nil {
		httpx.RenderError(c, err)
		return
	}
	middleware.ClearCookie(c, h.cookies, h.cookies.AdminSession, true)
	host, _ := httpx.HostOf(c)
	token := middleware.SetCSRFCookie(c, h.csrf, h.cookies, h.cookies.AdminCSRF, middleware.AnonymousBinding(host))
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, authResponse{Status: "signed_out", CSRFToken: &token})
}

// me answers the signed-in admin, its session expiry and the CSRF token of the session: the one
// in the CSRF cookie while it is valid for this session (so other tabs keep theirs), else a new
// one written to the cookie.
func (h *Handler) me(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	who, err := h.auth.Me(c.Request.Context(), p)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	binding := middleware.SessionBinding(middleware.PrincipalPlatformAdmin, p.SessionID)
	token, err := c.Cookie(h.cookies.AdminCSRF)
	if err != nil || !h.csrf.Valid(binding, token) {
		token = middleware.SetCSRFCookie(c, h.csrf, h.cookies, h.cookies.AdminCSRF, binding)
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, meResponse{
		Admin:     adminRefDTO{ID: who.AdminID, FullName: who.FullName, Email: who.Email},
		CSRFToken: token, ExpiresAt: who.ExpiresAt,
	})
}

// admins lists the platform admins by name, the directory a ticket is assigned from.
func (h *Handler) admins(c *gin.Context) {
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list, err := h.auth.Admins(c.Request.Context(), page)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewListOf(list, page, DirectoryCursor, func(a DirectoryEntry) adminEntryDTO {
		return adminEntryDTO{adminRefDTO: adminRefDTO{ID: a.ID, FullName: a.FullName, Email: a.Email}, Active: a.Active}
	}))
}

func (h *Handler) tenants(c *gin.Context) {
	f := TenantFilter{Query: c.Query("q"), Status: c.Query("status"), Subscription: c.Query("subscription_status")}
	if len(f.Query) > 100 {
		httpx.RenderError(c, errInvalid(map[string]string{"q": "too_long"}))
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	rows, err := h.directory.Tenants(c.Request.Context(), f, page)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewListOf(rows, page, func(r TenantRow) httpx.Cursor {
		return httpx.TimeCursor(r.CreatedAt, r.ID)
	}, toTenantDTO))
}

func (h *Handler) tenant(c *gin.Context) {
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	d, err := h.directory.Tenant(c.Request.Context(), id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	out := tenantDetailDTO{
		tenantDTO: toTenantDTO(d.TenantRow), Subscription: saas.ToSubscriptionViewDTO(d.Subscription), Usage: toUsageDTOs(d.Usage),
	}
	if d.Closure.Request != nil {
		closure := saas.ToClosureViewDTO(d.Closure)
		out.Closure = &closure
	}
	c.JSON(http.StatusOK, out)
}

// subscriptionAction binds the request of a subscription verb on /tenants/{id} and renders the
// resulting subscription.
func subscriptionAction[T any](c *gin.Context, run func(p middleware.Principal, tenantID uuid.UUID, req T) (saas.Subscription, error)) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	tenantID, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req T
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	sub, err := run(p, tenantID, req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, saas.ToSubscriptionDTO(sub))
}

func (h *Handler) changePlan(c *gin.Context) {
	subscriptionAction(c, func(p middleware.Principal, tenantID uuid.UUID, req changePlanRequest) (saas.Subscription, error) {
		return h.saas.AdminChangePlan(c.Request.Context(), actorFor(c, p, tenantID), tenantID, req.PlanID)
	})
}

func (h *Handler) extend(c *gin.Context) {
	subscriptionAction(c, func(p middleware.Principal, tenantID uuid.UUID, req extendRequest) (saas.Subscription, error) {
		return h.saas.Extend(c.Request.Context(), actorFor(c, p, tenantID), tenantID, req.Days)
	})
}

func (h *Handler) setStatus(c *gin.Context) {
	subscriptionAction(c, func(p middleware.Principal, tenantID uuid.UUID, req setStatusRequest) (saas.Subscription, error) {
		return h.saas.SetStatus(c.Request.Context(), actorFor(c, p, tenantID), tenantID, req.Status, req.LegalHold)
	})
}

// retryClosureExport starts a new round of a stopped closure's final export, or mails the owner
// of a closed store a new link to its final export.
func (h *Handler) retryClosureExport(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	tenantID, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	v, err := h.saas.RetryClosureExport(c.Request.Context(), actorFor(c, p, tenantID), tenantID)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, saas.ToClosureViewDTO(v))
}

func (h *Handler) plans(c *gin.Context) {
	plans, err := h.saas.Plans(c.Request.Context(), false)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	data := make([]saas.PlanDTO, len(plans))
	for i, p := range plans {
		data[i] = saas.ToPlanDTO(p)
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (h *Handler) createPlan(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	var req planRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	plan, err := h.saas.CreatePlan(c.Request.Context(), p.UserID, req.input())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	h.logAction(c, p, "plan.created", "plan_id", plan.ID, "code", plan.Code)
	c.JSON(http.StatusCreated, saas.ToPlanDTO(plan))
}

func (h *Handler) updatePlan(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req planRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	plan, err := h.saas.UpdatePlan(c.Request.Context(), p.UserID, id, req.input())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	h.logAction(c, p, "plan.updated", "plan_id", plan.ID, "code", plan.Code)
	c.JSON(http.StatusOK, saas.ToPlanDTO(plan))
}

func (h *Handler) invoices(c *gin.Context) {
	f := saas.InvoiceFilter{Status: c.Query("status")}
	if raw := c.Query("tenant_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			httpx.RenderError(c, errInvalid(map[string]string{"tenant_id": "invalid"}))
			return
		}
		f.TenantID = &id
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list, err := h.saas.AdminInvoices(c.Request.Context(), f, page)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewListOf(list, page, func(inv saas.AdminInvoice) httpx.Cursor {
		return httpx.TimeCursor(inv.IssuedAt, inv.ID)
	}, func(inv saas.AdminInvoice) adminInvoiceDTO {
		return adminInvoiceDTO{
			InvoiceDTO: saas.ToInvoiceDTO(inv.Invoice), TenantID: inv.TenantID, TenantSlug: inv.TenantSlug, TenantName: inv.TenantName,
		}
	}))
}

func (h *Handler) issueInvoice(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	var req issueInvoiceRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	inv, err := h.saas.IssueInvoice(c.Request.Context(), actorFor(c, p, req.TenantID), req.TenantID, req.PlanID, req.Amount)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, saas.ToInvoiceDTO(inv))
}

func (h *Handler) markPaid(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	var req markPaidRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	inv, err := h.saas.MarkPaid(c.Request.Context(), actorFor(c, p, uuid.Nil), httpx.CommandID(c), req.Reference)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, saas.ToInvoiceDTO(inv))
}

func (h *Handler) voidInvoice(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	inv, err := h.saas.VoidInvoice(c.Request.Context(), actorFor(c, p, uuid.Nil), httpx.CommandID(c))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, saas.ToInvoiceDTO(inv))
}

func (h *Handler) flagList(c *gin.Context) {
	list, err := h.flags.List(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, flagListResponse{Data: list, KnownKeys: saas.FlagKeys()})
}

// removeFlagOverride drops a store's override of a flag, so the store follows the global value.
func (h *Handler) removeFlagOverride(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	tenantID, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	key := c.Param("key")
	if err := h.flags.Unset(c.Request.Context(), actorFor(c, p, tenantID), key); err != nil {
		httpx.RenderError(c, err)
		return
	}
	h.logAction(c, p, "feature_flag.override_removed", "key", key, "tenant_id", tenantID)
	c.Status(http.StatusNoContent)
}

func (h *Handler) setFlag(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	var req setFlagRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	// The actor's store is the override's; Set ignores it for the global value.
	f, err := h.flags.Set(c.Request.Context(), actorFor(c, p, uuid.Nil), req.Key, req.TenantID, *req.Enabled)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	h.logAction(c, p, "feature_flag.set", "key", f.Key, "tenant_id", f.TenantID, "enabled", f.Enabled)
	c.JSON(http.StatusOK, f)
}

func (h *Handler) flagChanges(c *gin.Context) {
	key := c.Param("key")
	if !featureflag.ValidKey(key) {
		httpx.RenderError(c, apperr.NewNotFound("not_found", "Not found"))
		return
	}
	list, err := h.flags.Changes(c.Request.Context(), key)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list})
}

func (h *Handler) incidentList(c *gin.Context) {
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list, err := h.incidents.List(c.Request.Context(), page, c.Query("status"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewListOf(list, page, func(i Incident) httpx.Cursor {
		return httpx.TimeCursor(i.StartedAt, i.ID)
	}, toIncidentDTO))
}

func (h *Handler) createIncident(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	var req incidentRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	i, err := h.incidents.Create(c.Request.Context(), NewIncident(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	h.logAction(c, p, "incident.created", "incident_id", i.ID, "impact", i.Impact)
	c.JSON(http.StatusCreated, toIncidentDTO(i))
}

func (h *Handler) updateIncident(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req incidentPatchRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	i, err := h.incidents.Update(c.Request.Context(), id, IncidentPatch(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	h.logAction(c, p, "incident.updated", "incident_id", i.ID, "status", i.Status)
	c.JSON(http.StatusOK, toIncidentDTO(i))
}

func (h *Handler) partnerList(c *gin.Context) {
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list, err := h.partners.List(c.Request.Context(), page)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewListOf(list, page, func(p Partner) httpx.Cursor {
		return httpx.TimeCursor(p.CreatedAt, p.ID)
	}, toPartnerDTO))
}

func (h *Handler) createPartner(c *gin.Context) {
	p, ok := adminOf(c)
	if !ok {
		return
	}
	var req partnerRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	partner, err := h.partners.Create(c.Request.Context(), NewPartner(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	h.logAction(c, p, "partner.created", "partner_id", partner.ID)
	c.JSON(http.StatusCreated, toPartnerDTO(partner))
}

func (h *Handler) referrals(c *gin.Context) {
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	partner, list, err := h.partners.Referrals(c.Request.Context(), id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	data := make([]referralDTO, len(list))
	for i, r := range list {
		data[i] = referralDTO{
			TenantID: r.TenantID, Slug: r.Slug, DisplayName: r.DisplayName, ReferredAt: r.ReferredAt.UTC(),
			SubscriptionStatus: r.SubscriptionStatus, PaidInvoices: r.PaidInvoices, Commission: r.Commission,
		}
	}
	c.JSON(http.StatusOK, gin.H{"partner": toPartnerDTO(partner), "data": data})
}

func (h *Handler) feedbackList(c *gin.Context) {
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list, err := h.feedback.List(c.Request.Context(), c.Query("kind"), page)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewListOf(list, page, func(f Feedback) httpx.Cursor {
		return httpx.TimeCursor(f.CreatedAt, f.ID)
	}, func(f Feedback) feedbackDTO { return feedbackDTO(f) }))
}

// maxUsageDays caps one usage query.
const maxUsageDays = 366

func (h *Handler) usage(c *gin.Context) {
	from, errFrom := time.Parse(time.DateOnly, c.Query("from"))
	to, errTo := time.Parse(time.DateOnly, c.Query("to"))
	fields := map[string]string{}
	if errFrom != nil {
		fields["from"] = "invalid"
	}
	if errTo != nil {
		fields["to"] = "invalid"
	}
	if len(fields) == 0 && (to.Before(from) || to.Sub(from) > maxUsageDays*24*time.Hour) {
		fields["to"] = "range"
	}
	if len(fields) > 0 {
		httpx.RenderError(c, errInvalid(fields))
		return
	}
	page, err := httpx.ParsePage(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	list, err := h.saas.UsageBetween(c.Request.Context(), from, to, page)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewListOf(list, page, saas.UsageCursor, func(u saas.UsageRow) usageRowDTO {
		return usageRowDTO{usageDTO: toUsageDTO(u.Usage), TenantName: u.TenantName, Slug: u.TenantSlug}
	}))
}

// FeedbackHandler serves the store owner's product feedback.
type FeedbackHandler struct {
	box *FeedbackBox
}

// NewFeedbackHandler returns the tenant feedback adapter.
func NewFeedbackHandler(box *FeedbackBox) *FeedbackHandler { return &FeedbackHandler{box: box} }

// Register adds POST /feedback to the tenant account group.
func (h *FeedbackHandler) Register(g *gin.RouterGroup) {
	g.POST("/feedback", h.submit)
}

func (h *FeedbackHandler) submit(c *gin.Context) {
	p, ok := middleware.PrincipalOf(c)
	tenantID := middleware.TenantID(c)
	if !ok || p.Kind != middleware.PrincipalOwner || tenantID == uuid.Nil {
		httpx.RenderError(c, apperr.NewUnauthenticated())
		return
	}
	var req feedbackRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	id, err := h.box.Submit(c.Request.Context(), tenantID, FeedbackInput(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

// StatusHandler serves the public service status.
type StatusHandler struct {
	incidents *Incidents
}

// NewStatusHandler returns the status adapter.
func NewStatusHandler(i *Incidents) *StatusHandler { return &StatusHandler{incidents: i} }

// Register adds GET /status to a public group.
func (h *StatusHandler) Register(g *gin.RouterGroup) {
	g.GET("/status", h.status)
}

func (h *StatusHandler) status(c *gin.Context) {
	s, err := h.incidents.Status(c.Request.Context())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Header("Cache-Control", "public, max-age=30")
	c.JSON(http.StatusOK, statusDTO{Indicator: s.Indicator, Incidents: toIncidentDTOs(s.Incidents)})
}
