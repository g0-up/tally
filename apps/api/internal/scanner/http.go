package scanner

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// RateLimit is a per-client-address token bucket: PerSecond tokens refill it up to Burst.
type RateLimit struct {
	PerSecond float64
	Burst     int
}

// RateLimits are the per-client-address limits of the scanner routes.
type RateLimits struct {
	// Claim limits POST /scan/claim; DELETE /scan/session gets a bucket of its own with the same
	// limit.
	Claim RateLimit
	// Scans limits POST /scan/scans.
	Scans RateLimit
	// Events limits opening an event stream, on each side separately.
	Events RateLimit
	// Commands limits opening a session and refreshing, approving and rejecting it.
	Commands RateLimit
}

// DefaultRateLimits are the production limits.
var DefaultRateLimits = RateLimits{
	Claim:    RateLimit{PerSecond: 10.0 / 60, Burst: 5},
	Scans:    RateLimit{PerSecond: 5, Burst: 10},
	Events:   RateLimit{PerSecond: 10.0 / 60, Burst: 5},
	Commands: RateLimit{PerSecond: 6.0 / 60, Burst: 6},
}

// Option adjusts a Handler.
type Option func(*Handler)

// WithRateLimits replaces DefaultRateLimits; tests that drive many requests from one address
// raise them.
func WithRateLimits(l RateLimits) Option { return func(h *Handler) { h.limits = l } }

// WithStreamTiming replaces the ping (15 s) and background check (10 s) intervals of the event
// streams.
func WithStreamTiming(ping, tick time.Duration) Option {
	return func(h *Handler) { h.timing = streamTiming{ping: ping, tick: tick} }
}

// Handler serves the desktop and phone routes of the scanner sessions.
type Handler struct {
	svc    *Service
	limits RateLimits
	timing streamTiming

	sessions     LoginSessions
	maintenance  middleware.MaintenanceReader
	entitlements middleware.EntitlementReader
	cookies      middleware.Cookies
}

// NewHandler returns the HTTP adapter of svc.
func NewHandler(svc *Service, opts ...Option) *Handler {
	h := &Handler{svc: svc, limits: DefaultRateLimits, timing: streamTiming{ping: pingEvery, tick: tickEvery}}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// LoginSessions re-checks the login session that opened a scanner session while the desktop's or
// the phone's event stream is open.
type LoginSessions interface {
	// SessionActive reports whether the login session of the user is still open (not signed out,
	// expired or idle, its user still active). It looks the session up by id, so a token rotated by
	// re-authentication keeps it open, and the check must not count as use of the session.
	SessionActive(ctx context.Context, tenantID, userID, sessionID uuid.UUID) (bool, error)
}

// Routes are the route groups and ports the composition root hands to Register.
type Routes struct {
	// Tenant is the signed-in tenant group (session, tenant, CSRF, maintenance, entitlement and
	// support read-only) of the desktop routes.
	Tenant *gin.RouterGroup
	// Public is the tenant-host group with an optional session and the host's CSRF token, for the
	// phone routes; the phone authenticates with its scanner cookie.
	Public *gin.RouterGroup
	// Commands returns the custom-method router of a resource under a group.
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
	// Sessions re-checks the desktop's login session on every tick of either stream.
	Sessions LoginSessions
	// Maintenance and Entitlements gate the phone's writes, which the public group does not.
	Maintenance  middleware.MaintenanceReader
	Entitlements middleware.EntitlementReader
	// Cookies names the phone's scanner cookie.
	Cookies middleware.Cookies
}

// Register adds the scanner routes.
func (h *Handler) Register(r Routes) {
	if r.Tenant == nil || r.Public == nil || r.Commands == nil || r.Sessions == nil || r.Maintenance == nil ||
		r.Entitlements == nil || r.Cookies.Scan == "" {
		panic("scanner: Register needs the tenant and public groups, the command router, the login session, maintenance " +
			"and entitlement readers and the scanner cookie name")
	}
	h.sessions, h.maintenance, h.entitlements, h.cookies = r.Sessions, r.Maintenance, r.Entitlements, r.Cookies
	commands := byIP(h.limits.Commands)

	t := r.Tenant
	t.POST("/scanner-sessions", commands, h.create)
	t.GET("/scanner-sessions/:id", h.status)
	t.DELETE("/scanner-sessions/:id", h.revoke)
	t.GET("/scanner-sessions/:id/events", byIP(h.limits.Events), h.desktopEvents)
	t.POST("/scanner-sessions/:id/scans/:scanId/result", h.resolve)
	cmd := r.Commands(t, "scanner-sessions")
	cmd.Item("refresh-code", limited(commands, h.refreshCode))
	cmd.Item("approve", limited(commands, h.approve))
	cmd.Item("reject", limited(commands, h.reject))

	p := r.Public
	p.POST("/scan/claim", byIP(h.limits.Claim), h.claim)
	p.GET("/scan/session", h.phoneSession)
	p.DELETE("/scan/session", byIP(h.limits.Claim), h.phoneRevoke)
	p.GET("/scan/events", byIP(h.limits.Events), h.phoneEvents)
	p.POST("/scan/scans", byIP(h.limits.Scans), h.scan)
}

// byIP is a fresh per-address limiter middleware.
func byIP(l RateLimit) gin.HandlerFunc {
	return middleware.RateLimitByIP(middleware.NewRateLimiter(l.PerSecond, l.Burst))
}

// limited runs handler after mw unless mw refused the request (a command verb takes one handler).
func limited(mw, handler gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		mw(c)
		if !c.IsAborted() {
			handler(c)
		}
	}
}

// owner is the desktop side of the request: the signed-in user and login session.
func owner(c *gin.Context) (Owner, error) {
	p, ok := middleware.PrincipalOf(c)
	tenantID := middleware.TenantID(c)
	if !ok || tenantID == uuid.Nil {
		return Owner{}, apperr.NewUnauthenticated()
	}
	return Owner{TenantID: tenantID, UserID: p.UserID, LoginSessionID: p.SessionID}, nil
}

// ownerAnd returns the owner and the session id of a /scanner-sessions/:id request.
func ownerAnd(c *gin.Context, id uuid.UUID, err error) (Owner, uuid.UUID, bool) {
	if err != nil {
		httpx.RenderError(c, err)
		return Owner{}, uuid.Nil, false
	}
	o, err := owner(c)
	if err != nil {
		httpx.RenderError(c, err)
		return Owner{}, uuid.Nil, false
	}
	return o, id, true
}

func (h *Handler) create(c *gin.Context) {
	o, err := owner(c)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	out, err := h.svc.Create(c.Request.Context(), o)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, out)
}

func (h *Handler) refreshCode(c *gin.Context) {
	o, id, ok := ownerAnd(c, httpx.CommandID(c), nil)
	if !ok {
		return
	}
	out, err := h.svc.RefreshCode(c.Request.Context(), o, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (h *Handler) approve(c *gin.Context) {
	o, id, ok := ownerAnd(c, httpx.CommandID(c), nil)
	if !ok {
		return
	}
	out, err := h.svc.Approve(c.Request.Context(), o, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) reject(c *gin.Context) {
	o, id, ok := ownerAnd(c, httpx.CommandID(c), nil)
	if !ok {
		return
	}
	out, err := h.svc.Reject(c.Request.Context(), o, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

func (h *Handler) status(c *gin.Context) {
	id, err := httpx.PathID(c, "id")
	o, id, ok := ownerAnd(c, id, err)
	if !ok {
		return
	}
	out, err := h.svc.Status(c.Request.Context(), o, id)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) revoke(c *gin.Context) {
	id, err := httpx.PathID(c, "id")
	o, id, ok := ownerAnd(c, id, err)
	if !ok {
		return
	}
	if err := h.svc.Revoke(c.Request.Context(), o, id); err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type resultRequest struct {
	Outcome string     `json:"outcome" validate:"required"`
	SKUID   *uuid.UUID `json:"sku_id"`
	UnitID  *uuid.UUID `json:"unit_id"`
}

func (h *Handler) resolve(c *gin.Context) {
	id, err := httpx.PathID(c, "id")
	o, id, ok := ownerAnd(c, id, err)
	if !ok {
		return
	}
	scanID, err := httpx.PathID(c, "scanId")
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	var in resultRequest
	if err := httpx.BindJSON(c, &in); err != nil {
		httpx.RenderError(c, err)
		return
	}
	out, err := h.svc.Resolve(c.Request.Context(), o, id, scanID,
		ResultInput{Outcome: Outcome(in.Outcome), SKUID: in.SKUID, UnitID: in.UnitID})
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// desktopEvents streams the events of a session to its desktop: state first, then
// claim_requested, scan, revoked, replaced and restarting. An ended session answers 204, which
// stops EventSource from reconnecting.
func (h *Handler) desktopEvents(c *gin.Context) {
	id, err := httpx.PathID(c, "id")
	o, id, ok := ownerAnd(c, id, err)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	// Read before subscribing so only the owner can replace a stream, and again after, so the
	// first event holds everything that happened before the subscription.
	if st, err := h.svc.Status(ctx, o, id); err != nil || st.State == StateEnded {
		endOrFail(c, err)
		return
	}
	key := Key{TenantID: o.TenantID, SessionID: id}
	events, cancel, err := h.svc.Hub().Subscribe(key, RoleDesktop)
	if err != nil {
		renderHubError(c, err)
		return
	}
	defer cancel()
	st, err := h.svc.Status(ctx, o, id)
	if err != nil || st.State == StateEnded {
		endOrFail(c, err)
		return
	}
	w, err := startSSE(c.Writer)
	if err != nil {
		return
	}
	pump(ctx, w, Event{Name: EventState, Data: st}, events, desktopTerminal, h.timing, func(ctx context.Context) tickResult {
		return h.desktopCheck(ctx, o, key)
	})
}

func desktopTerminal(name string) bool {
	return name == EventRevoked || name == EventReplaced || name == EventRestarting
}

// desktopCheck re-checks the login session that opened the scanner session, by its id so that a
// re-authentication (which rotates the cookie) does not count as a sign-out, and without counting
// the check as use of the login session. It revokes the scanner session once the login session is
// gone (signed out, every device signed out, expired or idle). Otherwise it keeps desktop_seen_at
// fresh, except while the tenant is in maintenance, and ends the stream once the session ended.
func (h *Handler) desktopCheck(ctx context.Context, o Owner, key Key) tickResult {
	active, err := h.sessions.SessionActive(ctx, o.TenantID, o.UserID, o.LoginSessionID)
	if err != nil {
		return tickResult{err: err}
	}
	if !active {
		// The revocation and its events complete even when the desktop leaves meanwhile: a commit
		// whose caller saw its context cancelled would leave the phone uninformed until its own
		// next check.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tickTimeout)
		defer cancel()
		if err := h.svc.RevokeForSignOut(rctx, key); err != nil {
			return tickResult{err: err}
		}
		return tickResult{events: []Event{{Name: EventRevoked, Data: revokedEvent{By: RevokedBySystem}}}, end: true}
	}
	held, err := h.maintenance.InMaintenance(ctx, o.TenantID)
	if err != nil {
		return tickResult{err: err}
	}
	st, err := h.svc.DesktopTick(ctx, o, key.SessionID, !held)
	if err != nil {
		return tickResult{err: err}
	}
	if st.State == StateEnded {
		return tickResult{events: []Event{{Name: EventState, Data: st}}, end: true}
	}
	return tickResult{}
}

// hostTenant is the tenant of a known tenant host, uuid.Nil for an unknown slug.
func hostTenant(c *gin.Context) uuid.UUID {
	host, _ := httpx.HostOf(c)
	if !host.KnownTenant() {
		return uuid.Nil
	}
	return host.TenantID
}

// secrets returns every scanner cookie of the request: a stray cookie of the same name set for a
// parent domain must not hide the real one.
func (h *Handler) secrets(c *gin.Context) []string {
	var out []string
	for _, ck := range c.Request.Cookies() {
		if ck.Name == h.cookies.Scan {
			out = append(out, ck.Value)
		}
	}
	return out
}

// writable answers the phone's writes like the tenant chain would: 423 while the tenant is in
// maintenance, 403 while its subscription is read-only. Callers check the phone's cookie or claim
// code first, so a stranger learns nothing about the tenant and costs no platform lookups.
func (h *Handler) writable(c *gin.Context, tenantID uuid.UUID) bool {
	ctx := c.Request.Context()
	held, err := h.maintenance.InMaintenance(ctx, tenantID)
	if err == nil && held {
		err = apperr.NewTenantMaintenance()
	}
	if err == nil {
		var ok bool
		if ok, err = h.entitlements.CanWrite(ctx, tenantID); err == nil && !ok {
			err = apperr.NewReadOnlyTenant()
		}
	}
	if err != nil {
		httpx.RenderError(c, err)
		return false
	}
	return true
}

func (h *Handler) setScanCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: h.cookies.Scan, Value: value, Path: "/", MaxAge: maxAge,
		Secure: h.cookies.Secure, HttpOnly: true, SameSite: http.SameSiteStrictMode,
	})
}

type claimRequest struct {
	Code string `json:"code"`
}

type claimResponse struct {
	State      State  `json:"state"`
	VerifyCode string `json:"verify_code"`
}

func (h *Handler) claim(c *gin.Context) {
	tenantID := hostTenant(c)
	if tenantID == uuid.Nil {
		httpx.RenderError(c, errClaimInvalid())
		return
	}
	var in claimRequest
	if err := httpx.BindJSON(c, &in); err != nil {
		httpx.RenderError(c, err)
		return
	}
	ctx := c.Request.Context()
	if err := h.svc.CheckClaimCode(ctx, tenantID, in.Code); err != nil {
		httpx.RenderError(c, err)
		return
	}
	if !h.writable(c, tenantID) {
		return
	}
	out, err := h.svc.Claim(ctx, tenantID, in.Code, c.Request.UserAgent())
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	h.setScanCookie(c, out.Secret, int(sessionTTL/time.Second))
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, claimResponse{State: StatePending, VerifyCode: out.VerifyCode})
}

func (h *Handler) phoneSession(c *gin.Context) {
	tenantID := hostTenant(c)
	if tenantID == uuid.Nil {
		httpx.RenderError(c, errSessionEnded())
		return
	}
	sess, err := h.svc.PhoneSession(c.Request.Context(), tenantID, h.secrets(c))
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, sess.phoneStatus())
}

// phoneRevoke lets the phone end its own session, also while the tenant is in maintenance or
// read-only: like signing out, leaving is never refused, and the cookie is always cleared.
func (h *Handler) phoneRevoke(c *gin.Context) {
	tenantID := hostTenant(c)
	if tenantID == uuid.Nil {
		h.setScanCookie(c, "", -1)
		httpx.RenderError(c, errSessionEnded())
		return
	}
	err := h.svc.RevokeByPhone(c.Request.Context(), tenantID, h.secrets(c))
	h.setScanCookie(c, "", -1)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// phoneEvents streams the events of the phone's session: state first, then approved, rejected,
// revoked, ended and restarting. An ended session answers 204.
func (h *Handler) phoneEvents(c *gin.Context) {
	tenantID := hostTenant(c)
	if tenantID == uuid.Nil {
		httpx.RenderError(c, errSessionEnded())
		return
	}
	ctx, secrets := c.Request.Context(), h.secrets(c)
	sess, err := h.svc.PhoneSession(ctx, tenantID, secrets)
	if err != nil || sess.phoneStatus().State == StateEnded {
		endOrFail(c, err)
		return
	}
	key := Key{TenantID: tenantID, SessionID: sess.ID}
	events, cancel, err := h.svc.Hub().Subscribe(key, RolePhone)
	if err != nil {
		renderHubError(c, err)
		return
	}
	defer cancel()
	if sess, err = h.svc.PhoneSession(ctx, tenantID, secrets); err != nil || sess.ID != key.SessionID ||
		sess.phoneStatus().State == StateEnded {
		endOrFail(c, err)
		return
	}
	w, err := startSSE(c.Writer)
	if err != nil {
		return
	}
	pump(ctx, w, Event{Name: EventState, Data: sess.phoneStatus()}, events, phoneTerminal, h.timing,
		func(ctx context.Context) tickResult { return h.phoneCheck(ctx, key, secrets) })
}

func phoneTerminal(name string) bool {
	switch name {
	case EventRejected, EventRevoked, EventEnded, EventReplaced, EventRestarting:
		return true
	}
	return false
}

// phoneCheck ends the phone stream with ended once its secret no longer opens the session. It
// also re-checks the login session that opened the scanner session, as desktopCheck does: a
// desktop that signs out usually closes its own stream before that stream's next check, which
// would leave the phone paired until desktopGrace ran out. A login session that is gone revokes
// the scanner session, which tells the desktop's other tabs as well.
func (h *Handler) phoneCheck(ctx context.Context, key Key, secrets []string) tickResult {
	ended := tickResult{events: []Event{{Name: EventEnded, Data: struct{}{}}}, end: true}
	sess, err := h.svc.PhoneSession(ctx, key.TenantID, secrets)
	if err != nil && !apperr.Is(err, CodeSessionEnded) {
		return tickResult{err: err}
	}
	if err != nil || sess.ID != key.SessionID || sess.phoneStatus().State == StateEnded {
		return ended
	}
	active, err := h.sessions.SessionActive(ctx, key.TenantID, sess.CreatedBy, sess.LoginSessionID)
	if err != nil {
		return tickResult{err: err}
	}
	if !active {
		// Completes even when the phone leaves meanwhile, like the desktop's revocation.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), tickTimeout)
		defer cancel()
		if err := h.svc.RevokeForSignOut(rctx, key); err != nil {
			return tickResult{err: err}
		}
		return ended
	}
	return tickResult{}
}

type scanRequest struct {
	Code string `json:"code" validate:"required"`
}

func (h *Handler) scan(c *gin.Context) {
	tenantID := hostTenant(c)
	if tenantID == uuid.Nil {
		httpx.RenderError(c, errSessionEnded())
		return
	}
	ctx, secrets := c.Request.Context(), h.secrets(c)
	if sess, err := h.svc.PhoneSession(ctx, tenantID, secrets); err != nil || sess.phoneStatus().State == StateEnded {
		if err == nil {
			err = errSessionEnded()
		}
		httpx.RenderError(c, err)
		return
	}
	if !h.writable(c, tenantID) {
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
	var in scanRequest
	if err := httpx.DecodeJSON(body, &in); err != nil {
		httpx.RenderError(c, err)
		return
	}
	out, err := h.svc.Scan(ctx, tenantID, secrets, key, in.Code)
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

// endOrFail answers a stream request that cannot start: err, or 204 for an ended session.
func endOrFail(c *gin.Context, err error) {
	if err != nil {
		httpx.RenderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// renderHubError answers 503 when the hub refuses a stream: the process is restarting or holds
// as many streams as it may.
func renderHubError(c *gin.Context, err error) {
	code, msg := CodeServerRestarting, "The server is restarting; try again shortly"
	if errors.Is(err, ErrStreamLimit) {
		code, msg = CodeStreamLimit, "Too many open scanner streams; close another one and try again"
	}
	c.AbortWithStatusJSON(http.StatusServiceUnavailable, httpx.ErrorBody{Error: httpx.ErrorPayload{Code: code, Message: msg}})
}
