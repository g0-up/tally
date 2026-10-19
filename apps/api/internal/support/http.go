package support

import (
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Handler serves the store's support tickets and support sessions.
type Handler struct {
	svc *Service
}

// NewHandler returns the tenant HTTP adapter of svc.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Routes are the tenant route groups the composition root hands to Register.
type Routes struct {
	// Tenant is the tenant chain without the entitlement gate: a read-only store can still ask
	// for help.
	Tenant   *gin.RouterGroup
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
	// Reauth guards the grant of a support session (middleware.RequireRecentAuth).
	Reauth gin.HandlerFunc
}

// Register adds the ticket and session routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Commands == nil || r.Reauth == nil {
		panic("support: Register needs the tenant group, the command router and reauth")
	}
	g := r.Tenant
	g.GET("/support-tickets", h.tickets)
	g.POST("/support-tickets", h.createTicket)
	g.GET("/support-tickets/:id", h.ticket)
	g.POST("/support-tickets/:id/messages", h.addMessage)
	g.GET("/support-sessions", h.sessions)
	g.POST("/support-sessions", r.Reauth, h.grantSession)
	r.Commands(g, "support-sessions").Item("revoke", h.revokeSession)
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

func (h *Handler) tickets(c *gin.Context) {
	tenantID, ok := tenantOf(c)
	if !ok {
		return
	}
	list, err := h.svc.Tickets(c.Request.Context(), tenantID)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	data := make([]TicketDTO, len(list))
	for i, t := range list {
		data[i] = toTicketDTO(t)
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (h *Handler) createTicket(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	var req createTicketRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	v, err := h.svc.CreateTicket(c.Request.Context(), actor, NewTicket(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toTicketViewDTO(v))
}

func (h *Handler) ticket(c *gin.Context) {
	tenantID, ok := tenantOf(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	v, err := h.svc.Ticket(c.Request.Context(), tenantID, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTicketViewDTO(v))
}

func (h *Handler) addMessage(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req messageRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	m, err := h.svc.AddOwnerMessage(c.Request.Context(), actor, id, NewMessage(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toMessageDTO(m))
}

func (h *Handler) sessions(c *gin.Context) {
	tenantID, ok := tenantOf(c)
	if !ok {
		return
	}
	list, err := h.svc.Sessions(c.Request.Context(), tenantID)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	now := h.svc.clock.Now()
	data := make([]SessionDTO, len(list))
	for i, s := range list {
		data[i] = toSessionDTO(s, now)
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func (h *Handler) grantSession(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	var req grantSessionRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	p, _ := middleware.PrincipalOf(c)
	s, err := h.svc.GrantSession(c.Request.Context(), p, actor, NewSession(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toSessionDTO(s, h.svc.clock.Now()))
}

func (h *Handler) revokeSession(c *gin.Context) {
	actor, ok := actorFor(c)
	if !ok {
		return
	}
	p, _ := middleware.PrincipalOf(c)
	s, err := h.svc.RevokeSession(c.Request.Context(), p, actor, httpx.CommandID(c))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSessionDTO(s, h.svc.clock.Now()))
}

// AdminHandler serves the admin side: every store's tickets and the support view.
type AdminHandler struct {
	svc *Service
	// dispatch serves the support view's tenant request (the application engine).
	dispatch http.Handler
	// tenantHost returns the host name of a store's slug.
	tenantHost func(slug string) string
}

// NewAdminHandler returns the admin HTTP adapter of svc. dispatch is the application handler the
// support view re-enters on the store's host; tenantHost names that host.
func NewAdminHandler(svc *Service, dispatch http.Handler, tenantHost func(slug string) string) *AdminHandler {
	return &AdminHandler{svc: svc, dispatch: dispatch, tenantHost: tenantHost}
}

// RegisterAdmin adds the admin ticket and support-view routes to the admin group (platform
// admin session with MFA).
func (h *AdminHandler) RegisterAdmin(g *gin.RouterGroup) {
	if g == nil || h.dispatch == nil || h.tenantHost == nil {
		panic("support: RegisterAdmin needs the admin group, the dispatcher and the tenant host builder")
	}
	g.GET("/support-tickets", h.tickets)
	g.GET("/support-tickets/:id", h.ticket)
	g.PATCH("/support-tickets/:id", h.patchTicket)
	g.POST("/support-tickets/:id/messages", h.reply)
	g.GET("/support-sessions", h.sessions)
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		g.Handle(m, "/tenants/:id/support-view/:module/*rest", h.supportView)
	}
}

func adminRequest(c *gin.Context) (AdminRequest, bool) {
	p, ok := middleware.PrincipalOf(c)
	if !ok || p.Kind != middleware.PrincipalPlatformAdmin {
		httpx.RenderError(c, apperr.NewUnauthenticated())
		return AdminRequest{}, false
	}
	return AdminRequest{AdminID: p.UserID, RequestID: middleware.RequestIDFrom(c.Request.Context()), IP: c.ClientIP()}, true
}

func (h *AdminHandler) tickets(c *gin.Context) {
	f := TicketFilter{Status: c.Query("status")}
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
	list, err := h.svc.AdminTickets(c.Request.Context(), f, page)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, httpx.NewListOf(list, page, AdminTicketCursor, func(t AdminTicket) adminTicketDTO {
		return adminTicketDTO{
			TicketDTO: toTicketDTO(t.Ticket), TenantID: t.TenantID, TenantSlug: t.TenantSlug, TenantName: t.TenantName,
			LastMessageAt: utcPtr(t.LastMessage),
		}
	}))
}

func (h *AdminHandler) ticket(c *gin.Context) {
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	v, err := h.svc.AdminTicket(c.Request.Context(), id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, adminTicketViewDTO{ticketViewDTO: toTicketViewDTO(v), TenantID: v.Ticket.TenantID})
}

func (h *AdminHandler) patchTicket(c *gin.Context) {
	r, ok := adminRequest(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req patchTicketRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	t, err := h.svc.PatchTicket(c.Request.Context(), r, id, TicketPatch(req))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTicketDTO(t))
}

func (h *AdminHandler) reply(c *gin.Context) {
	r, ok := adminRequest(c)
	if !ok {
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var req adminMessageRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		httpx.RenderError(c, err)
		return
	}
	m, err := h.svc.AddAdminMessage(c.Request.Context(), r, id, req.Body)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toMessageDTO(m))
}

func (h *AdminHandler) sessions(c *gin.Context) {
	r, ok := adminRequest(c)
	if !ok {
		return
	}
	list, err := h.svc.AdminSessions(c.Request.Context(), r.AdminID)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	now := h.svc.clock.Now()
	data := make([]AdminSessionDTO, len(list))
	for i, s := range list {
		data[i] = toAdminSessionDTO(s, now)
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

// supportView re-enters the application with the store's tenant GET route, as the consented
// support session: the tenant chain then runs it in a read-only transaction under the
// tally_readonly role, and SupportReadOnly refuses anything but a read. Any other method answers
// 403 here, before a session is looked up.
func (h *AdminHandler) supportView(c *gin.Context) {
	if c.Request.Method != http.MethodGet {
		httpx.RenderError(c, apperr.NewForbidden("support_read_only", "Support sessions are read-only"))
		return
	}
	r, ok := adminRequest(c)
	if !ok {
		return
	}
	tenantID, err := httpx.PathID(c, "id")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	rest := c.Param("rest")
	clean := path.Clean("/" + strings.TrimPrefix(rest, "/"))
	if clean == "/" || clean != strings.TrimSuffix("/"+strings.TrimPrefix(rest, "/"), "/") {
		httpx.RenderError(c, errSessionInactive())
		return
	}
	open, err := h.svc.OpenAccess(c.Request.Context(), Access{
		AdminID: r.AdminID, TenantID: tenantID, Module: c.Param("module"), Path: clean, RequestID: r.RequestID, IP: r.IP,
	})
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	p := middleware.Principal{
		Kind: middleware.PrincipalSupport, SessionID: open.ID, UserID: r.AdminID, TenantID: tenantID,
	}
	req := c.Request.Clone(middleware.ContextWithPrincipal(c.Request.Context(), p))
	req.Host = h.tenantHost(open.TenantSlug)
	req.URL = &url.URL{Path: "/api/v1" + clean, RawQuery: c.Request.URL.RawQuery}
	req.RequestURI = req.URL.RequestURI()
	req.Header = c.Request.Header.Clone()
	req.Header.Del("Cookie")
	c.Abort()
	h.dispatch.ServeHTTP(c.Writer, req)
}
