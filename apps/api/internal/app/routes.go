package app

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/g0-up/tally/apps/api/internal/admin"
	"github.com/g0-up/tally/apps/api/internal/campaign"
	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/notification"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/reporting"
	"github.com/g0-up/tally/apps/api/internal/saas"
	"github.com/g0-up/tally/apps/api/internal/sales"
	"github.com/g0-up/tally/apps/api/internal/scanner"
	"github.com/g0-up/tally/apps/api/internal/support"
)

// Route prefixes.
const (
	APIPrefix      = "/api/v1"
	AdminAPIPrefix = "/api/admin/v1"
)

const readyTimeout = 2 * time.Second

// registerRoutes registers the platform routes. Domain modules add their routes here through
// the group builders below.
func (a *App) registerRoutes() {
	// Probes sit outside the host chain: the container healthcheck calls them on 127.0.0.1.
	probes := a.engine.Group("", middleware.Recover())
	probes.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	probes.GET("/readyz", a.readyz)

	a.API(httpx.HostRoot, httpx.HostTenant, httpx.HostAdmin).GET("/host-context", a.hostContext)
}

// registerModules registers the domain handlers on their route groups.
func (a *App) registerModules(m Modules) {
	reauth := middleware.RequireRecentAuth(middleware.ReauthWindow, a.now)
	if m.Identity != nil {
		m.Identity.Register(identity.Routes{
			Root:         a.API(httpx.HostRoot),
			TenantPublic: a.API(httpx.HostTenant),
			Account:      a.TenantSecurityAPI(),
			Commands:     a.CommandRouter,
			Reauth:       reauth,
			CSRF:         a.csrf,
			Cookies:      a.cookies,
		})
	}
	if m.Org != nil {
		m.Org.Register(org.Routes{Tenant: a.TenantAPI(), Commands: a.CommandRouter, Reauth: reauth})
	}
	if m.Reasons != nil {
		m.Reasons.Register(org.Routes{Tenant: a.TenantAPI(), Commands: a.CommandRouter, Reauth: reauth})
	}
	if m.Catalog != nil {
		m.Catalog.Register(catalog.Routes{Tenant: a.TenantAPI(), Commands: a.CommandRouter})
	}
	if m.Partner != nil {
		m.Partner.Register(partner.Routes{Tenant: a.TenantAPI(), Commands: a.CommandRouter})
	}
	if m.Pricing != nil {
		m.Pricing.Register(pricing.Routes{Tenant: a.TenantAPI(), Commands: a.CommandRouter})
	}
	if m.Cash != nil {
		m.Cash.Register(cash.Routes{Tenant: a.TenantAPI()})
	}
	if m.Debt != nil {
		m.Debt.Register(debt.Routes{
			Tenant: a.TenantAPI(), Posting: a.TenantPostingAPI(), Commands: a.CommandRouter, Reauth: reauth,
		})
	}
	if m.Inventory != nil {
		m.Inventory.Register(inventory.Routes{Tenant: a.TenantAPI(), Commands: a.CommandRouter, Reauth: reauth})
	}
	if m.Procurement != nil {
		m.Procurement.Register(procurement.Routes{
			Tenant: a.TenantAPI(), Posting: a.TenantPostingAPI(), Commands: a.CommandRouter, Reauth: reauth,
		})
	}
	if m.Sales != nil {
		m.Sales.Register(sales.Routes{
			Tenant: a.TenantAPI(), Posting: a.TenantPostingAPI(), Commands: a.CommandRouter, Reauth: reauth,
			Entitled: middleware.Entitlement(a.ports.Entitlements),
		})
	}
	if m.Kits != nil {
		m.Kits.Register(kits.Routes{Tenant: a.TenantAPI(), Posting: a.TenantPostingAPI(), Commands: a.CommandRouter})
	}
	if m.Campaign != nil {
		m.Campaign.Register(campaign.Routes{Tenant: a.TenantAPI(), Commands: a.CommandRouter})
	}
	if m.CRM != nil {
		m.CRM.Register(a.TenantAPI())
	}
	if m.Notification != nil {
		m.Notification.Register(notification.Routes{Tenant: a.TenantAPI(), Commands: a.CommandRouter})
	}
	if m.Reporting != nil {
		m.Reporting.Register(reporting.Routes{Tenant: a.TenantAPI(), Commands: a.CommandRouter})
	}
	if m.Analytics != nil {
		m.Analytics.Register(a.TenantAPI())
	}
	if m.Printing != nil {
		m.Printing.Register(a.TenantAPI())
	}
	if m.Scanner != nil {
		m.Scanner.Register(scanner.Routes{
			Tenant: a.TenantAPI(), Public: a.API(httpx.HostTenant), Commands: a.CommandRouter,
			Sessions: a.ports.LoginSessions, Maintenance: a.ports.Maintenance, Entitlements: a.ports.Entitlements,
			Cookies: a.cookies,
		})
	}
	a.registerControlModules(m, reauth)
}

// registerControlModules registers the subscription, support, onboarding, feedback, status and
// admin routes. Subscription, billing, support, onboarding and feedback stay reachable when the
// subscription lapsed (no entitlement gate): a read-only store still sees its plan, pays,
// exports, asks for help and closes its account.
func (a *App) registerControlModules(m Modules, reauth gin.HandlerFunc) {
	if m.SaaS != nil {
		m.SaaS.Register(saas.Routes{
			Tenant: a.TenantAccountAPI(), Public: a.API(httpx.HostTenant), Webhooks: a.WebhookAPI(),
			Commands: a.CommandRouter, Reauth: reauth,
		})
	}
	if m.Support != nil {
		m.Support.Register(support.Routes{Tenant: a.TenantAccountAPI(), Commands: a.CommandRouter, Reauth: reauth})
	}
	if m.Onboarding != nil {
		m.Onboarding.Register(org.OnboardingRoutes{Tenant: a.TenantAccountAPI(), Keyed: a.KeyedCommandRouter, Reauth: reauth})
	}
	if m.Feedback != nil {
		m.Feedback.Register(a.TenantAccountAPI())
	}
	if m.Status != nil {
		m.Status.Register(a.API(httpx.HostRoot, httpx.HostTenant))
	}
	if m.Admin != nil {
		m.Admin.Register(admin.Routes{
			Public: a.AdminPublicAPI(), Admin: a.AdminAPI(), Commands: a.CommandRouter, CSRF: a.csrf, Cookies: a.cookies,
		})
	}
	if m.SupportAdmin != nil {
		support.NewAdminHandler(m.SupportAdmin, a.engine, a.tenantHost).RegisterAdmin(a.AdminAPI())
	}
}

// tenantHost is the host name of a store's slug.
func (a *App) tenantHost(slug string) string {
	u, err := url.Parse(a.urls.Tenant(slug, "/"))
	if err != nil {
		return ""
	}
	return u.Host
}

// hostContextResponse tells the web app which host it runs on and hands it a CSRF token.
type hostContextResponse struct {
	Kind      httpx.HostKind `json:"kind"`
	RootURL   string         `json:"root_url"`
	AdminURL  *string        `json:"admin_url,omitempty"`
	CSRFToken string         `json:"csrf_token"`
}

// hostContext answers on every host with its kind and a fresh CSRF token bound to the current
// session (or to the anonymous state of the host), stored in the host's CSRF cookie. An unknown
// tenant slug answers like a real one.
func (a *App) hostContext(c *gin.Context) {
	host, _ := httpx.HostOf(c)
	name := a.cookies.CSRF
	out := hostContextResponse{Kind: host.Kind, RootURL: a.urls.Root("/")}
	if host.Kind == httpx.HostAdmin {
		name = a.cookies.AdminCSRF
		admin := a.urls.Admin("/")
		out.AdminURL = &admin
	}
	out.CSRFToken = middleware.SetCSRFCookie(c, a.csrf, a.cookies, name, middleware.CSRFBinding(c))
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}

// readyz answers 503 while a dependency is unreachable. The cause is logged, never returned.
func (a *App) readyz(c *gin.Context) {
	if a.ready != nil {
		ctx, cancel := context.WithTimeout(c.Request.Context(), readyTimeout)
		defer cancel()
		if err := a.ready(ctx); err != nil {
			a.logger.Warn("not ready", "error", err.Error())
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// API returns an /api/v1 group served on the given host kinds. The session is resolved when a
// cookie is present but not required (sign-up, login, host context); state-changing requests
// carry the CSRF token of that session, or of the host when signed out. The admin host uses the
// admin cookies.
func (a *App) API(hosts ...httpx.HostKind) *gin.RouterGroup {
	return a.edge.Group(APIPrefix, httpx.OnHosts(hosts...),
		byHost(middleware.Session(a.ports.Sessions, a.cookies.Session), middleware.Session(a.ports.AdminSessions, a.cookies.AdminSession)),
		byHost(middleware.CSRF(a.csrf, a.cookies.CSRF), middleware.CSRF(a.csrf, a.cookies.AdminCSRF)),
	)
}

// byHost runs admin on the admin host and site on every other host. Both call c.Next themselves.
func byHost(site, admin gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if h, _ := httpx.HostOf(c); h.Kind == httpx.HostAdmin {
			admin(c)
			return
		}
		site(c)
	}
}

// TenantAPI returns the /api/v1 group of tenant hosts behind the full tenant chain: session,
// tenant-host guard, CSRF, plan module, maintenance, entitlement and support read-only.
func (a *App) TenantAPI() *gin.RouterGroup {
	return a.tenantGroup(true, true)
}

// TenantPostingAPI returns the /api/v1 group for endpoints that run through posting.Coordinator
// (postings and payments, all with an Idempotency-Key). It is the tenant chain without the
// entitlement middleware: the coordinator replays an already completed request first and checks
// the entitlement itself (posting.EntitlementGuard) before a new request claims its key, so a
// client retrying a committed sale after the subscription turned read-only gets the stored
// response instead of 403. Every other mutation belongs in TenantAPI.
func (a *App) TenantPostingAPI() *gin.RouterGroup {
	return a.tenantGroup(false, true)
}

// TenantAccountAPI returns the /api/v1 group of tenant hosts for the store's account (plan and
// billing, support, onboarding, feedback): the tenant chain without the entitlement middleware,
// so an owner whose subscription lapsed can still pay, export, ask for help and close the store.
func (a *App) TenantAccountAPI() *gin.RouterGroup {
	return a.tenantGroup(false, true)
}

// TenantSecurityAPI returns the /api/v1 group of tenant hosts for securing the owner's own
// account (profile, sessions, MFA, re-authentication, ownership transfer): neither the
// entitlement nor the maintenance gate, so the account can be secured while the subscription
// lapsed or an operator holds the store. Its writes go to platform tables (users, sessions,
// tokens, MFA) a restore copy never writes, and to audit events under fresh keys.
func (a *App) TenantSecurityAPI() *gin.RouterGroup {
	return a.tenantGroup(false, false)
}

func (a *App) tenantGroup(entitlement, maintenance bool) *gin.RouterGroup {
	chain := []gin.HandlerFunc{
		httpx.OnHosts(httpx.HostTenant),
		middleware.Session(a.ports.Sessions, a.cookies.Session),
		middleware.Tenant(),
		middleware.CSRF(a.csrf, a.cookies.CSRF),
		middleware.ModuleGate(a.ports.Modules, routeModule),
	}
	if maintenance {
		chain = append(chain, middleware.Maintenance(a.ports.Maintenance))
	}
	if entitlement {
		chain = append(chain, middleware.Entitlement(a.ports.Entitlements))
	}
	chain = append(chain, middleware.SupportReadOnly())
	return a.edge.Group(APIPrefix, chain...)
}

// AdminAPI returns the /api/admin/v1 group of the admin host. It requires a platform admin
// session (separate cookie) and the admin CSRF token; MFA and recent-auth checks are added by
// the admin routes themselves.
func (a *App) AdminAPI() *gin.RouterGroup {
	return a.edge.Group(AdminAPIPrefix,
		httpx.OnHosts(httpx.HostAdmin),
		middleware.Session(a.ports.AdminSessions, a.cookies.AdminSession),
		requirePlatformAdmin(),
		middleware.CSRF(a.csrf, a.cookies.AdminCSRF),
	)
}

// AdminPublicAPI returns the /api/admin/v1 group of the admin host for signing in: the admin
// session is resolved when present but not required, and the admin CSRF token is checked.
func (a *App) AdminPublicAPI() *gin.RouterGroup {
	return a.edge.Group(AdminAPIPrefix,
		httpx.OnHosts(httpx.HostAdmin),
		middleware.Session(a.ports.AdminSessions, a.cookies.AdminSession),
		middleware.CSRF(a.csrf, a.cookies.AdminCSRF),
	)
}

// WebhookAPI returns the /api/v1 group of the apex host for provider callbacks: no session and
// no CSRF, every request authenticates itself with the provider signature.
func (a *App) WebhookAPI() *gin.RouterGroup {
	return a.edge.Group(APIPrefix, httpx.OnHosts(httpx.HostRoot))
}

// CommandRouter returns the custom-method router of resource under group, recorded in the
// command index.
func (a *App) CommandRouter(group *gin.RouterGroup, resource string) *httpx.CommandRouter {
	return httpx.NewCommandRouter(group, resource, a.commands)
}

// KeyedCommandRouter is CommandRouter for a resource whose item is addressed by keyName
// instead of :id.
func (a *App) KeyedCommandRouter(group *gin.RouterGroup, resource, keyName string) *httpx.CommandRouter {
	return httpx.NewKeyedCommandRouter(group, resource, keyName, a.commands)
}

func requirePlatformAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if p, ok := middleware.PrincipalOf(c); !ok || p.Kind != middleware.PrincipalPlatformAdmin {
			httpx.RenderError(c, apperr.NewUnauthenticated())
			return
		}
		c.Next()
	}
}
