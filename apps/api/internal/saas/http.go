package saas

import (
	"bytes"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// webhookBodyLimit caps a payment callback body.
const webhookBodyLimit = 64 << 10

// Per-client-address limit of the closure export link, which needs no session.
const (
	closureLinkPerSecond = 10.0 / 60
	closureLinkBurst     = 10
)

// Handler serves the store's own subscription, service invoices, full data export and account
// closure, and the payment provider callbacks.
type Handler struct {
	svc   *Service
	links *middleware.RateLimiter
}

// NewHandler returns the HTTP adapter of svc.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, links: middleware.NewRateLimiter(closureLinkPerSecond, closureLinkBurst)}
}

// Routes are the route groups the composition root hands to Register.
type Routes struct {
	// Tenant is the tenant chain without the entitlement gate: a read-only store still sees its
	// subscription, pays, exports and closes.
	Tenant *gin.RouterGroup
	// Public is the /api/v1 group of tenant hosts with an optional session, for the emailed link
	// to a closed store's final export (its owner can no longer sign in).
	Public *gin.RouterGroup
	// Webhooks is the apex group without session or CSRF for provider callbacks.
	Webhooks *gin.RouterGroup
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
	// Reauth guards sensitive actions (middleware.RequireRecentAuth).
	Reauth gin.HandlerFunc
}

// Register adds the subscription, billing, export and closure routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Public == nil || r.Webhooks == nil || r.Commands == nil || r.Reauth == nil {
		panic("saas: Register needs the tenant, public and webhook groups, the command router and reauth")
	}
	g := r.Tenant
	g.GET("/plans", h.plans)
	g.GET("/subscription", h.subscription)
	g.GET("/entitlements", h.entitlements)
	sub := r.Commands(g, "subscription")
	sub.Collection("preview-change", h.previewChange)
	sub.Collection("change", guarded(r.Reauth, h.changePlan))
	g.GET("/saas-invoices", h.invoices)
	g.GET("/saas-invoices/:id", h.invoice)
	r.Commands(g, "saas-invoices").Item("pay", h.pay)
	g.POST("/data-exports", r.Reauth, h.requestDataExport)
	g.GET("/account-closure", h.closure)
	g.POST("/account-closure", r.Reauth, h.requestClosure)
	closure := r.Commands(g, "account-closure")
	closure.Collection("verify", guarded(r.Reauth, h.verifyClosure))
	closure.Collection("cancel", h.cancelClosure)
	r.Public.GET("/account-closure/export", middleware.RateLimitByIP(h.links), h.closureExport)
	r.Webhooks.POST("/webhooks/saas-payments/:provider", h.webhook)
}

// guarded runs reauth before h.
func guarded(reauth, h gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		reauth(c)
		if c.IsAborted() {
			return
		}
		h(c)
	}
}

func tenantOf(c *gin.Context) (uuid.UUID, bool) {
	tenantID := middleware.TenantID(c)
	if _, ok := middleware.PrincipalOf(c); !ok || tenantID == uuid.Nil {
		httpx.RenderError(c, apperr.NewUnauthenticated())
		return uuid.Nil, false
	}
	return tenantID, true
}

func actorFor(c *gin.Context) (audit.Actor, bool) {
	a, err := middleware.Actor(c)
	if err != nil {
		httpx.RenderError(c, err)
		return audit.Actor{}, false
	}
	return a, true
}

func (h *Handler) plans(c *gin.Context) {
	if _, ok := tenantOf(c); !ok {
		return
	}
	plans, err := h.svc.Plans(c.Request.Context(), true)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	data := make([]PlanDTO, len(plans))
	for i, p := range plans {
		data[i] = ToPlanDTO(p)
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (h *Handler) subscription(c *gin.Context) {
	tenantID, ok := tenantOf(c)
	if !ok {
		return
	}
	v, err := h.svc.Subscription(c.Request.Context(), tenantID)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, ToSubscriptionViewDTO(v))
}

func (h *Handler) entitlements(c *gin.Context) {
	tenantID, ok := tenantOf(c)
	if !ok {
		return
	}
	e, err := h.svc.Entitlements(c.Request.Context(), tenantID)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, e)
}

func (h *Handler) previewChange(c *gin.Context) {
	tenantID, ok := tenantOf(c)
	if !ok {
		return
	}
	var req changePlanRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	q, err := h.svc.PreviewChange(c.Request.Context(), tenantID, req.PlanID)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toQuoteDTO(q))
}

func (h *Handler) changePlan(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	var req changePlanRequest
	key, err := httpx.BindJSONKeyed(c, &req)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	inv, replayed, err := h.svc.ChangePlan(c.Request.Context(), actor, key, req.PlanID, req.AcceptTerms)
	httpx.MarkReplay(c, replayed)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, ToInvoiceDTO(inv))
}

func (h *Handler) invoices(c *gin.Context) {
	tenantID, ok := tenantOf(c)
	if !ok {
		return
	}
	list, err := h.svc.Invoices(c.Request.Context(), tenantID)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	data := make([]InvoiceDTO, len(list))
	for i, inv := range list {
		data[i] = ToInvoiceDTO(inv)
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (h *Handler) invoice(c *gin.Context) {
	tenantID, ok := tenantOf(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	inv, err := h.svc.Invoice(c.Request.Context(), tenantID, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, ToInvoiceDTO(inv))
}

func (h *Handler) pay(c *gin.Context) {
	tenantID, ok := tenantOf(c)
	if !ok {
		return
	}
	body, err := httpx.ReadBody(c, 0)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req payRequest
	if len(bytes.TrimSpace(body)) > 0 {
		if err := httpx.DecodeJSON(body, &req); err != nil {
			httpx.RenderError(c, err)
			return
		}
	}
	out, err := h.svc.Pay(c.Request.Context(), tenantID, httpx.CommandID(c), req.Provider)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, payInstructionsDTO{Invoice: ToInvoiceDTO(out.Invoice), Provider: out.Provider, Instructions: out.Instructions})
}

func (h *Handler) requestDataExport(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	key, err := httpx.OptionalCommandKey(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	out, replayed, err := h.svc.RequestDataExport(c.Request.Context(), actor, key)
	httpx.MarkReplay(c, replayed)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, out)
}

func (h *Handler) closure(c *gin.Context) {
	tenantID, ok := tenantOf(c)
	if !ok {
		return
	}
	v, err := h.svc.Closure(c.Request.Context(), tenantID)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, ToClosureViewDTO(v))
}

// closureExport redirects the emailed link of a closed store's final export to a short-lived
// download address. The token travels in the query, which the request log leaves out.
func (h *Handler) closureExport(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	host, _ := httpx.HostOf(c)
	if !host.KnownTenant() {
		httpx.RenderError(c, errClosureLinkNotFound())
		return
	}
	url, err := h.svc.ClosureExportURL(c.Request.Context(), host.TenantID, c.Query("token"))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Redirect(http.StatusFound, url)
}

func (h *Handler) requestClosure(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	p, _ := middleware.PrincipalOf(c)
	r, err := h.svc.RequestClosure(c.Request.Context(), p, actor)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, toClosureRequestDTO(r))
}

func (h *Handler) verifyClosure(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	var req verifyClosureRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	p, _ := middleware.PrincipalOf(c)
	r, err := h.svc.VerifyClosure(c.Request.Context(), p, actor, req.Token)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toClosureRequestDTO(r))
}

func (h *Handler) cancelClosure(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	r, err := h.svc.CancelClosure(c.Request.Context(), actor)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toClosureRequestDTO(r))
}

// webhook applies a provider callback; a repeated event answers 200 with duplicate true, so the
// provider stops retrying.
func (h *Handler) webhook(c *gin.Context) {
	body, err := httpx.ReadBody(c, webhookBodyLimit)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	duplicate, err := h.svc.HandleWebhook(c.Request.Context(), c.Param("provider"), c.Request.Header, body)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"duplicate": duplicate})
}
