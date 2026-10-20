// Package app is the composition root of the HTTP API: it builds the gin engine, the middleware
// chain and the route groups domain modules register on, and wires the ports the platform
// middleware needs. It is the only package that knows every domain.
package app

import (
	"context"
	"log/slog"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/admin"
	"github.com/g0-up/tally/apps/api/internal/analytics"
	"github.com/g0-up/tally/apps/api/internal/campaign"
	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/crm"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/notification"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/metrics"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/urls"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/printing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/reporting"
	"github.com/g0-up/tally/apps/api/internal/saas"
	"github.com/g0-up/tally/apps/api/internal/sales"
	"github.com/g0-up/tally/apps/api/internal/scanner"
	"github.com/g0-up/tally/apps/api/internal/support"
)

// Per-client-IP limit for every API request; auth routes add a tighter limiter of their own.
const (
	apiRatePerSecond = 20
	apiRateBurst     = 100
)

// RateLimit is a per-client-IP token bucket: PerSecond tokens refill it up to Burst.
type RateLimit struct {
	PerSecond float64
	Burst     int
}

// Ports are the middleware seams implemented by domain modules. A nil port gets a closed
// default: no session ever resolves or stays active, no tenant host is known, writes and every
// module are allowed (no subscription gate) and no tenant is ever held in maintenance.
type Ports struct {
	Sessions    middleware.SessionResolver
	TenantHosts middleware.TenantHostResolver
	// Entitlements gates writes outside the posting routes; give the same reader to NewDomains
	// (WithEntitlements), which gates the postings.
	Entitlements middleware.EntitlementReader
	// Modules gates the routes of plan modules (POS, kits, campaigns, CRM, advanced reports, AI
	// Q&A) for every method.
	Modules middleware.ModuleReader
	// AdminSessions resolves the platform admin session cookie on the admin host.
	AdminSessions middleware.SessionResolver
	// Maintenance refuses every change of a tenant an operator holds in maintenance, on every
	// tenant route but the account security ones.
	Maintenance middleware.MaintenanceReader
	// LoginSessions tells whether the login session behind an open desktop scanner stream is
	// still open, by session id (a re-authentication rotates the cookie, not the session) and
	// without counting the check as use.
	LoginSessions scanner.LoginSessions
}

// Options configures New.
type Options struct {
	// PublicBaseURL is the apex URL; tenant and admin hosts derive from its host.
	PublicBaseURL *url.URL
	// TrustedProxyCIDRs lists the proxies whose CF-Connecting-IP header is trusted.
	TrustedProxyCIDRs []string
	// SecureCookies selects the __Host- cookie names (production).
	SecureCookies bool
	// Logger is the base logger (slog.Default when nil).
	Logger *slog.Logger
	// Metrics records HTTP metrics; may be nil.
	Metrics *metrics.Metrics
	// Ready reports whether dependencies are reachable (GET /readyz); nil means always ready.
	Ready func(ctx context.Context) error
	Ports Ports
	// CSRFKey keys the CSRF tokens (CSRF_KEY). Empty gets a random per-process key, which only
	// suits a single development process.
	CSRFKey []byte
	// Now is the clock of the re-authentication window (time.Now when nil).
	Now func() time.Time
	// APIRateLimit is the per-client-IP limit of every request; the zero value is the production
	// limit (20/s, burst 100). Tests that drive a long scenario from one client raise it.
	APIRateLimit RateLimit
	// Modules are the domain HTTP handlers; a nil handler is not registered.
	Modules Modules
}

// Modules are the domain handlers the composition root registers on the route groups.
type Modules struct {
	Identity *identity.Handler
	Org      *org.Handler
	Reasons  *org.ReasonHandler
	Catalog  *catalog.Handler
	Partner  *partner.Handler
	Pricing  *pricing.Handler
	Debt     *debt.Handler
	Cash     *cash.Handler

	// Inventory serves the stock list, stock card, lots, reservations, DCs, KKs and recosts.
	Inventory *inventory.Handler
	// Procurement serves POs, receipts, landed costs, supplier documents, returns and DGs.
	Procurement *procurement.Handler
	// Sales serves quotes, invoices, stock outs, sales documents, POS, returns and exchanges.
	Sales *sales.Handler
	// Kits serves kits and combos, assembly orders (LD), disassemblies (TG) and lot genealogy.
	Kits *kits.Handler
	// Campaign serves seasons and promotions, their checklist, demand and re-attribution.
	Campaign *campaign.Handler
	// CRM serves the customer timeline, consents, care tasks, re-order drafts and loyalty.
	CRM *crm.Handler
	// Notification serves the notification centre: inbox, unread count, read/resolve/snooze.
	Notification *notification.Handler
	// Reporting serves the dashboard, the reports, saved layouts and CSV exports.
	Reporting *reporting.Handler
	// Analytics serves the read-only AI Q&A and its log.
	Analytics *analytics.Handler
	// Printing serves the print data of every printable document.
	Printing *printing.Handler
	// Scanner pairs a phone with a desktop screen as a barcode scanner: the desktop routes on the
	// tenant chain, the phone routes on the tenant host with the phone's scanner cookie.
	Scanner *scanner.Handler

	// SaaS serves plans, the subscription, service invoices, the full data export, account
	// closure and the payment provider callbacks.
	SaaS *saas.Handler
	// Support serves the store's support tickets and support sessions.
	Support *support.Handler
	// SupportAdmin backs the admin support tickets and the support view; the app builds its
	// handler because the support view re-enters the application on the store's host.
	SupportAdmin *support.Service
	// Onboarding serves the onboarding checklist and the opening balance confirmation.
	Onboarding *org.OnboardingHandler
	// Feedback takes product feedback from store owners.
	Feedback *admin.FeedbackHandler
	// Status serves the public service status.
	Status *admin.StatusHandler
	// Admin serves the platform admin API on the admin host.
	Admin *admin.Handler
}

// App is the assembled HTTP application.
type App struct {
	engine    *gin.Engine
	edge      *gin.RouterGroup
	urls      urls.Builder
	cookies   middleware.Cookies
	csrf      *middleware.CSRFTokens
	now       func() time.Time
	ports     Ports
	hostCache *middleware.TenantHostCache
	commands  *httpx.CommandIndex
	logger    *slog.Logger
	ready     func(ctx context.Context) error
}

// New builds the engine and registers the platform routes.
func New(opts Options) (*App, error) {
	builder, err := urls.New(opts.PublicBaseURL)
	if err != nil {
		return nil, err
	}
	engine, err := httpx.NewEngine(httpx.EngineOptions{TrustedProxyCIDRs: opts.TrustedProxyCIDRs})
	if err != nil {
		return nil, err
	}
	a := &App{
		engine:   engine,
		urls:     builder,
		cookies:  middleware.CookieNames(opts.SecureCookies),
		csrf:     middleware.NewCSRFTokens(opts.CSRFKey),
		now:      opts.Now,
		ports:    withDefaults(opts.Ports),
		commands: &httpx.CommandIndex{},
		logger:   opts.Logger,
		ready:    opts.Ready,
	}
	if a.logger == nil {
		a.logger = slog.Default()
	}
	if a.now == nil {
		a.now = time.Now
	}
	a.hostCache = middleware.NewTenantHostCache(a.ports.TenantHosts)
	limit := opts.APIRateLimit
	if limit == (RateLimit{}) {
		limit = RateLimit{PerSecond: apiRatePerSecond, Burst: apiRateBurst}
	}
	edge := []gin.HandlerFunc{
		middleware.RequestID(),
		middleware.Logger(a.logger, opts.Metrics),
		middleware.Recover(),
		middleware.SecurityHeaders(),
		// Before HostContext: an unknown tenant slug costs a database lookup, so every request is
		// throttled before its host is resolved.
		middleware.RateLimitByIP(middleware.NewRateLimiter(limit.PerSecond, limit.Burst)),
		middleware.HostContext(builder.Host(), a.hostCache),
	}
	a.edge = engine.Group("", edge...)
	// Unknown routes go through the same chain: logged, host-checked and rate limited.
	engine.NoRoute(append(edge, func(c *gin.Context) {
		httpx.RenderError(c, apperr.NewNotFound("not_found", "Not found"))
	})...)
	a.registerRoutes()
	a.registerModules(opts.Modules)
	return a, nil
}

// Handler returns the HTTP handler.
func (a *App) Handler() *gin.Engine { return a.engine }

// URLs returns the absolute URL builder.
func (a *App) URLs() urls.Builder { return a.urls }

// Cookies returns the cookie names in use.
func (a *App) Cookies() middleware.Cookies { return a.cookies }

// CSRF returns the CSRF token issuer.
func (a *App) CSRF() *middleware.CSRFTokens { return a.csrf }

// TenantHostCache returns the slug cache (purge it when a tenant is created or purged).
func (a *App) TenantHostCache() *middleware.TenantHostCache { return a.hostCache }

// Commands returns the index of custom-method routes (used by the OpenAPI contract test).
func (a *App) Commands() *httpx.CommandIndex { return a.commands }

type noSessions struct{}

func (noSessions) ResolveSession(context.Context, string) (middleware.Principal, bool, error) {
	return middleware.Principal{}, false, nil
}

func (noSessions) SessionActive(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

type unknownTenantHosts struct{}

func (unknownTenantHosts) ResolveSlug(context.Context, string) (uuid.UUID, bool, error) {
	return uuid.Nil, false, nil
}

type writesAllowed struct{}

func (writesAllowed) CanWrite(context.Context, uuid.UUID) (bool, error) { return true, nil }

type neverHeld struct{}

func (neverHeld) InMaintenance(context.Context, uuid.UUID) (bool, error) { return false, nil }

type allModules struct{}

func (allModules) HasModule(context.Context, uuid.UUID, string) (bool, error) { return true, nil }

func withDefaults(p Ports) Ports {
	if p.Sessions == nil {
		p.Sessions = noSessions{}
	}
	if p.TenantHosts == nil {
		p.TenantHosts = unknownTenantHosts{}
	}
	if p.Entitlements == nil {
		p.Entitlements = writesAllowed{}
	}
	if p.Modules == nil {
		p.Modules = allModules{}
	}
	if p.AdminSessions == nil {
		p.AdminSessions = noSessions{}
	}
	if p.Maintenance == nil {
		p.Maintenance = neverHeld{}
	}
	if p.LoginSessions == nil {
		p.LoginSessions = noSessions{}
	}
	return p
}
