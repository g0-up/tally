package identity

import (
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

// Per-client-address limit of the credential endpoints (sign-up, login, MFA, reset, re-auth),
// on top of the per-account login throttle.
const (
	authRatePerSecond = 10.0 / 60
	authRateBurst     = 10
	// The live store address check runs while the visitor types (debounced), so it gets a
	// separate, more generous per-address budget: it cannot drain the credential budget, and a
	// script cannot enumerate store addresses at full speed.
	slugCheckPerSecond = 1.0
	slugCheckBurst     = 30
	// maxSlugQueryLen bounds the slug-check query; longer input is never a valid address.
	maxSlugQueryLen = 200
)

// Handler serves the account endpoints of the root and tenant hosts.
type Handler struct {
	svc         *Service
	limiter     *middleware.RateLimiter
	slugLimiter *middleware.RateLimiter
	tokens      *middleware.CSRFTokens
	cookies     middleware.Cookies
}

// NewHandler returns the HTTP adapter of svc.
func NewHandler(svc *Service) *Handler {
	return &Handler{
		svc:         svc,
		limiter:     middleware.NewRateLimiter(authRatePerSecond, authRateBurst),
		slugLimiter: middleware.NewRateLimiter(slugCheckPerSecond, slugCheckBurst),
	}
}

// Routes are the route groups the composition root hands to Register.
type Routes struct {
	// Root is the /api/v1 group of the root host (session optional, CSRF checked).
	Root *gin.RouterGroup
	// TenantPublic is the /api/v1 group of tenant hosts for anonymous account flows (session
	// optional, CSRF checked).
	TenantPublic *gin.RouterGroup
	// Account is the /api/v1 group of tenant hosts behind session, host guard, CSRF and support
	// read-only, without the entitlement gate: an owner whose subscription lapsed can still
	// manage the account.
	Account *gin.RouterGroup
	// Commands returns the custom-method router of a resource under a group.
	Commands func(group *gin.RouterGroup, resource string) *httpx.CommandRouter
	// Reauth guards sensitive actions (middleware.RequireRecentAuth).
	Reauth gin.HandlerFunc
	// CSRF issues the tokens of new sessions; Cookies names the cookies.
	CSRF    *middleware.CSRFTokens
	Cookies middleware.Cookies
}

// Register adds the identity routes.
func (h *Handler) Register(r Routes) {
	if r.Root == nil || r.TenantPublic == nil || r.Account == nil || r.Commands == nil || r.Reauth == nil || r.CSRF == nil {
		panic("identity: Register needs the root, tenant-public and account groups, the command router, the re-auth guard and the CSRF issuer")
	}
	h.tokens, h.cookies = r.CSRF, r.Cookies
	limit := middleware.RateLimitByIP(h.limiter)

	root := r.Root
	root.POST("/auth/signup", limit, h.signup)
	root.GET("/signup/slug-check", middleware.RateLimitByIP(h.slugLimiter), h.slugCheck)
	root.POST("/store-lookup", limit, h.storeLookup)

	pub := r.TenantPublic
	pub.POST("/auth/verify", limit, h.verify)
	pub.POST("/auth/login", limit, h.login)
	pub.POST("/auth/logout", h.logout)
	pub.POST("/auth/password-reset", limit, h.requestReset)
	pub.POST("/auth/password-reset/confirm", limit, h.confirmReset)
	pub.POST("/auth/mfa/totp/verify", limit, h.verifyTOTP)
	pub.POST("/owner-transfer/accept", limit, h.acceptTransfer)

	acc := r.Account
	acc.POST("/auth/reauth", limit, h.reauth)
	acc.POST("/auth/password", r.Reauth, h.changePassword)
	acc.POST("/auth/mfa/totp", r.Reauth, h.enrolTOTP)
	acc.DELETE("/auth/mfa/totp", r.Reauth, h.disableTOTP)
	acc.GET("/me", h.me)
	acc.GET("/sessions", h.sessions)
	acc.DELETE("/sessions/:id", h.revokeSession)
	r.Commands(acc, "sessions").Collection("revoke-all", h.revokeAll)
	acc.POST("/owner-transfer", r.Reauth, h.requestTransfer)
}

// client describes the caller for audit and session records.
func client(c *gin.Context) Client {
	return Client{IP: c.ClientIP(), UserAgent: c.Request.UserAgent(), RequestID: middleware.RequestIDFrom(c.Request.Context())}
}

// hostTenant is the tenant of a known tenant host, uuid.Nil for an unknown slug.
func hostTenant(c *gin.Context) uuid.UUID {
	h, _ := httpx.HostOf(c)
	if !h.KnownTenant() {
		return uuid.Nil
	}
	return h.TenantID
}

// owner returns the principal of an owner session on its own tenant host; support sessions
// cannot act on the account.
func owner(c *gin.Context) (middleware.Principal, error) {
	p, ok := middleware.PrincipalOf(c)
	if !ok || middleware.TenantID(c) == uuid.Nil {
		return middleware.Principal{}, apperr.NewUnauthenticated()
	}
	if p.Kind != middleware.PrincipalOwner {
		return middleware.Principal{}, apperr.NewForbidden("forbidden", "Only the store owner can do this")
	}
	return p, nil
}

// ownerOnHost returns the owner session of the request host, if any, for the tenant-public
// routes that behave differently when signed in.
func ownerOnHost(c *gin.Context) (middleware.Principal, bool) {
	p, ok := middleware.PrincipalOf(c)
	host := hostTenant(c)
	if !ok || p.Kind != middleware.PrincipalOwner || host == uuid.Nil || p.TenantID != host {
		return middleware.Principal{}, false
	}
	return p, true
}

// renderError writes err; a throttled login also gets Retry-After.
func renderError(c *gin.Context, err error) {
	var throttled *ThrottledError
	if errors.As(err, &throttled) {
		secs := int(math.Ceil(throttled.RetryAfter.Seconds()))
		if secs < 1 {
			secs = 1
		}
		c.Header("Retry-After", strconv.Itoa(secs))
	}
	httpx.RenderError(c, err)
}

// startSession sets the session cookie and a CSRF cookie bound to the new session.
func (h *Handler) startSession(c *gin.Context, s IssuedSession) authResponse {
	middleware.SetSessionCookie(c, h.cookies, h.cookies.Session, s.Token, 0)
	token := middleware.SetCSRFCookie(c, h.tokens, h.cookies, h.cookies.CSRF, middleware.SessionBinding(middleware.PrincipalOwner, s.ID))
	expires := s.ExpiresAt
	return authResponse{Status: authSignedIn, CSRFToken: &token, ExpiresAt: &expires}
}

// endSession clears the session cookie and issues an anonymous CSRF token for the host.
func (h *Handler) endSession(c *gin.Context) string {
	middleware.ClearCookie(c, h.cookies, h.cookies.Session, true)
	host, _ := httpx.HostOf(c)
	return middleware.SetCSRFCookie(c, h.tokens, h.cookies, h.cookies.CSRF, middleware.AnonymousBinding(host))
}

func (h *Handler) signup(c *gin.Context) {
	var req signupRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		renderError(c, err)
		return
	}
	res, err := h.svc.Signup(c.Request.Context(), SignupInput{
		Email: req.Email, Phone: req.Phone, Password: req.Password, FullName: req.FullName, StoreName: req.StoreName, Slug: req.Slug,
		ReferralCode: req.ReferralCode, Client: client(c),
	})
	if err != nil {
		renderError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, signupResponse(res))
}

func (h *Handler) slugCheck(c *gin.Context) {
	slug := c.Query("slug")
	if slug == "" || len(slug) > maxSlugQueryLen {
		renderError(c, errInvalid(map[string]string{"slug": "invalid"}))
		return
	}
	res, err := h.svc.CheckSlug(c.Request.Context(), slug)
	if err != nil {
		renderError(c, err)
		return
	}
	c.JSON(http.StatusOK, slugCheckResponse{Available: res.Available, Reason: optional(res.Reason), Suggestion: optional(res.Suggestion)})
}

func (h *Handler) storeLookup(c *gin.Context) {
	var req emailRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		renderError(c, err)
		return
	}
	if err := h.svc.StoreLookup(c.Request.Context(), req.Email); err != nil {
		renderError(c, err)
		return
	}
	c.Status(http.StatusAccepted)
}

func (h *Handler) verify(c *gin.Context) {
	var req verifyRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		renderError(c, err)
		return
	}
	if err := h.svc.VerifyEmail(c.Request.Context(), VerifyInput{
		HostTenant: hostTenant(c), Token: req.Token, Password: req.Password, Client: client(c),
	}); err != nil {
		renderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) login(c *gin.Context) {
	var req loginRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		renderError(c, err)
		return
	}
	res, err := h.svc.Login(c.Request.Context(), LoginInput{
		HostTenant: hostTenant(c), Login: req.Login, Password: req.Password, Client: client(c),
	})
	if err != nil {
		renderError(c, err)
		return
	}
	if res.Session == nil {
		c.JSON(http.StatusOK, authResponse{Status: authMFARequired, Challenge: &res.MFAChallenge})
		return
	}
	c.JSON(http.StatusOK, h.startSession(c, *res.Session))
}

// logout ends the session of the host when there is one; it always clears the cookies, so a
// stale cookie never keeps the browser half signed in.
func (h *Handler) logout(c *gin.Context) {
	if p, ok := ownerOnHost(c); ok {
		if err := h.svc.Logout(c.Request.Context(), p); err != nil {
			renderError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, csrfResponse{CSRFToken: h.endSession(c)})
}

func (h *Handler) requestReset(c *gin.Context) {
	var req emailRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		renderError(c, err)
		return
	}
	if err := h.svc.RequestPasswordReset(c.Request.Context(), hostTenant(c), req.Email); err != nil {
		renderError(c, err)
		return
	}
	c.Status(http.StatusAccepted)
}

func (h *Handler) confirmReset(c *gin.Context) {
	var req tokenPasswordRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		renderError(c, err)
		return
	}
	if err := h.svc.ResetPassword(c.Request.Context(), ResetInput{
		HostTenant: hostTenant(c), Token: req.Token, Password: req.Password, Client: client(c),
	}); err != nil {
		renderError(c, err)
		return
	}
	c.JSON(http.StatusOK, csrfResponse{CSRFToken: h.endSession(c)})
}

// verifyTOTP answers the MFA step of a login when the body carries a challenge, and otherwise
// confirms the TOTP enrolment of the signed-in owner.
func (h *Handler) verifyTOTP(c *gin.Context) {
	var req mfaVerifyRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		renderError(c, err)
		return
	}
	if req.Challenge != "" {
		s, err := h.svc.VerifyChallenge(c.Request.Context(), ChallengeInput{
			HostTenant: hostTenant(c), Challenge: req.Challenge, Code: req.Code, RecoveryCode: req.RecoveryCode, Client: client(c),
		})
		if err != nil {
			renderError(c, err)
			return
		}
		c.JSON(http.StatusOK, h.startSession(c, s))
		return
	}
	p, ok := ownerOnHost(c)
	if !ok {
		renderError(c, apperr.NewUnauthenticated())
		return
	}
	if req.Code == "" || req.RecoveryCode != "" {
		renderError(c, errInvalid(map[string]string{"code": "required"}))
		return
	}
	codes, err := h.svc.ConfirmTOTP(c.Request.Context(), p, req.Code, client(c))
	if err != nil {
		renderError(c, err)
		return
	}
	c.JSON(http.StatusOK, recoveryCodesResponse{RecoveryCodes: codes})
}

func (h *Handler) acceptTransfer(c *gin.Context) {
	var req tokenPasswordRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		renderError(c, err)
		return
	}
	if err := h.svc.AcceptOwnerTransfer(c.Request.Context(), AcceptInput{
		HostTenant: hostTenant(c), Token: req.Token, Password: req.Password, Client: client(c),
	}); err != nil {
		renderError(c, err)
		return
	}
	c.JSON(http.StatusOK, csrfResponse{CSRFToken: h.endSession(c)})
}

func (h *Handler) reauth(c *gin.Context) {
	p, err := owner(c)
	if err != nil {
		renderError(c, err)
		return
	}
	var req passwordRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		renderError(c, err)
		return
	}
	s, err := h.svc.Reauth(c.Request.Context(), p, req.Password, client(c))
	if err != nil {
		renderError(c, err)
		return
	}
	// The token rotates but the session id (the CSRF binding) does not.
	middleware.SetSessionCookie(c, h.cookies, h.cookies.Session, s.Token, 0)
	c.JSON(http.StatusOK, reauthResponse{ReauthenticatedUntil: h.svc.clock.Now().Add(middleware.ReauthWindow).UTC()})
}

func (h *Handler) changePassword(c *gin.Context) {
	p, err := owner(c)
	if err != nil {
		renderError(c, err)
		return
	}
	var req passwordRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		renderError(c, err)
		return
	}
	n, err := h.svc.ChangePassword(c.Request.Context(), p, req.Password, client(c))
	if err != nil {
		renderError(c, err)
		return
	}
	c.JSON(http.StatusOK, revokedResponse{Revoked: n})
}

func (h *Handler) enrolTOTP(c *gin.Context) {
	p, err := owner(c)
	if err != nil {
		renderError(c, err)
		return
	}
	e, err := h.svc.EnrolTOTP(c.Request.Context(), p)
	if err != nil {
		renderError(c, err)
		return
	}
	c.JSON(http.StatusOK, enrolmentResponse{Secret: e.Secret, OTPAuthURI: e.URI})
}

func (h *Handler) disableTOTP(c *gin.Context) {
	p, err := owner(c)
	if err != nil {
		renderError(c, err)
		return
	}
	if err := h.svc.DisableTOTP(c.Request.Context(), p, client(c)); err != nil {
		renderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) me(c *gin.Context) {
	p, err := owner(c)
	if err != nil {
		renderError(c, err)
		return
	}
	m, err := h.svc.Me(c.Request.Context(), p)
	if err != nil {
		renderError(c, err)
		return
	}
	c.JSON(http.StatusOK, toMeResponse(m))
}

func (h *Handler) sessions(c *gin.Context) {
	p, err := owner(c)
	if err != nil {
		renderError(c, err)
		return
	}
	list, err := h.svc.Sessions(c.Request.Context(), p)
	if err != nil {
		renderError(c, err)
		return
	}
	out := sessionListResponse{Data: make([]sessionDTO, 0, len(list))}
	for _, s := range list {
		out.Data = append(out.Data, toSessionDTO(s, p.SessionID))
	}
	c.JSON(http.StatusOK, out)
}

func (h *Handler) revokeSession(c *gin.Context) {
	p, err := owner(c)
	if err != nil {
		renderError(c, err)
		return
	}
	id, err := httpx.PathID(c, "id")
	if err != nil {
		renderError(c, err)
		return
	}
	if err := h.svc.RevokeSession(c.Request.Context(), p, id, client(c)); err != nil {
		renderError(c, err)
		return
	}
	if id == p.SessionID {
		h.endSession(c)
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) revokeAll(c *gin.Context) {
	p, err := owner(c)
	if err != nil {
		renderError(c, err)
		return
	}
	n, err := h.svc.RevokeOtherSessions(c.Request.Context(), p, client(c))
	if err != nil {
		renderError(c, err)
		return
	}
	c.JSON(http.StatusOK, revokedResponse{Revoked: n})
}

func (h *Handler) requestTransfer(c *gin.Context) {
	p, err := owner(c)
	if err != nil {
		renderError(c, err)
		return
	}
	var req transferRequest
	if err := httpx.BindJSON(c, &req); err != nil {
		renderError(c, err)
		return
	}
	if err := h.svc.RequestOwnerTransfer(c.Request.Context(), p, TransferInput{
		Email: req.Email, FullName: req.FullName, Client: client(c),
	}); err != nil {
		renderError(c, err)
		return
	}
	c.Status(http.StatusAccepted)
}
