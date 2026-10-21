package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/analytics"
	"github.com/g0-up/tally/apps/api/internal/campaign"
	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/crm"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/featureflag"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/notification"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/fault"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/mailer"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/platform/periodlock"
	"github.com/g0-up/tally/apps/api/internal/posting"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/printing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/reporting"
	"github.com/g0-up/tally/apps/api/internal/saas"
	"github.com/g0-up/tally/apps/api/internal/saas/provider"
	"github.com/g0-up/tally/apps/api/internal/sales"
	"github.com/g0-up/tally/apps/api/internal/scanner"
	"github.com/g0-up/tally/apps/api/internal/support"
)

// Domains are the domain services built once per process and shared by the HTTP handlers and
// the tenant provisioner's seeders.
type Domains struct {
	Catalog *catalog.Service
	Partner *partner.Service
	Pricing *pricing.Service
	// Posting is the one coordinator every money and stock posting runs through; each domain
	// registers the lockers of the rows it owns on it.
	Posting *posting.Coordinator
	Debt    *debt.Service
	Cash    *cash.Service

	// Inventory is the stock ledger; every other domain changes stock through its Ledger.
	Inventory *inventory.Service
	// Procurement posts receipts, returns and price adjustments into inventory and debt.
	Procurement *procurement.Service
	// Sales posts quotes, invoices, POS sales, returns and exchanges into inventory and debt.
	Sales *sales.Service
	// Kits keeps the kit and combo recipes, assembles and takes apart pre-packed kits and expands
	// the combos sales sells.
	Kits *kits.Service
	// Campaign keeps seasons and promotions, attributes postings to them and tracks their demand.
	Campaign *campaign.Service
	// CRM keeps the customer timeline, consents, care tasks, re-order drafts and loyalty points.
	CRM *crm.Service
	// Notification is the in-app notification centre, fed by the outbox and a periodic sweep.
	Notification *notification.Service
	// Reporting runs the dashboard and reports, keeps saved layouts and exports reports to CSV.
	Reporting *reporting.Service
	// Analytics answers questions over the reports with a language model, when enabled.
	Analytics *analytics.Service
	// Printing serves the print data of every printable document.
	Printing *printing.Service
	// SaaS runs plans, subscriptions, service billing, the full data export and account closure.
	SaaS *saas.Service
	// Flags reads the platform feature flags; the admin writer clears its cache.
	Flags *featureflag.Reader
	// Limits enforces the plan limits (nil without the subscription gate); give it to the
	// organisation service as well.
	Limits *saas.Reader
	// Scanner pairs a phone with a desktop screen as a barcode scanner; its hub lives in this
	// process, so the server closes it on shutdown.
	Scanner *scanner.Service
}

// DomainOption sets an optional collaborator of the domain services.
type DomainOption func(*domainOptions)

type domainOptions struct {
	mailer       mailer.Mailer
	entitlements middleware.EntitlementReader
	objects      objectstore.Store
	model        analytics.ModelClient
	askDeadline  time.Duration
	gate         bool
	providers    []provider.Provider
	faults       *fault.Injector
}

// WithMailer lets the debt domain email the reminders parties consented to.
func WithMailer(m mailer.Mailer) DomainOption { return func(o *domainOptions) { o.mailer = m } }

// WithEntitlements gates every new posting on the tenant's subscription. Give it the reader the
// HTTP Entitlement middleware uses (Ports.Entitlements): posting routes skip that middleware so
// a committed request can be replayed, and rely on the coordinator instead. Without it every
// tenant may post.
func WithEntitlements(r middleware.EntitlementReader) DomainOption {
	return func(o *domainOptions) { o.entitlements = r }
}

// WithObjectStore lets reporting store finished exports. Without it exports stay queued.
func WithObjectStore(s objectstore.Store) DomainOption {
	return func(o *domainOptions) { o.objects = s }
}

// WithModel lets the AI Q&A ask a language model. Without it the Q&A answers 403 feature_disabled.
func WithModel(m analytics.ModelClient) DomainOption { return func(o *domainOptions) { o.model = m } }

// WithQuestionDeadline bounds the model conversation of one AI question (45 s by default).
func WithQuestionDeadline(d time.Duration) DomainOption {
	return func(o *domainOptions) { o.askDeadline = d }
}

// WithSubscriptionGate gates postings on the tenant's subscription and enforces the plan limits
// (SKUs, storage). It overrides WithEntitlements with the subscription reader; give the same
// reader (Domains.SaaS.Reader()) to Ports.Entitlements and Ports.Modules. Without it every
// tenant may post and is unbounded.
func WithSubscriptionGate() DomainOption { return func(o *domainOptions) { o.gate = true } }

// WithPaymentProviders adds the payment providers of the service invoices; the manual bank
// transfer is always available.
func WithPaymentProviders(p ...provider.Provider) DomainOption {
	return func(o *domainOptions) { o.providers = append(o.providers, p...) }
}

// WithFaults arms the failure points of the acceptance tests (fault.Parse returns nil, the inert
// hook, outside APP_ENV=test).
func WithFaults(f *fault.Injector) DomainOption { return func(o *domainOptions) { o.faults = f } }

// NewDomains builds the domain services on one database, clock and audit recorder.
func NewDomains(d *db.DB, clk clock.Clock, rec *audit.Recorder, opts ...DomainOption) Domains {
	o := domainOptions{entitlements: writesAllowed{}}
	for _, opt := range opts {
		opt(&o)
	}
	flags := featureflag.NewReader(clk)
	sa := saas.NewService(saas.Deps{DB: d, Clock: clk, Audit: rec, Flags: flags, Providers: o.providers, Objects: o.objects})
	var limits *saas.Reader
	var catalogLimits catalog.LimitChecker
	if o.gate {
		limits = sa.Reader()
		o.entitlements, catalogLimits = limits, limits
	}
	pri := pricing.NewService(pricing.Deps{DB: d, Clock: clk, Audit: rec})
	coord, err := posting.NewCoordinator(posting.Deps{
		DB: d, Idempotency: idempotency.NewGuard(clk), Docs: docseq.NewIssuer(), Periods: periodlock.NewGuard(),
		Audit: rec, Outbox: outbox.NewPublisher(clk), Clock: clk, Entitlements: postingEntitlements{r: o.entitlements},
		Faults: o.faults,
	})
	if err != nil {
		panic(fmt.Sprintf("app: posting coordinator: %v", err)) // every dependency is set above
	}
	debt.RegisterLockers(coord)
	coord.RegisterLocker(posting.LockFundAccounts, cash.LockFunds)
	inventory.RegisterLockers(coord, clk)
	inv := inventory.NewService(inventory.Deps{
		DB: d, Clock: clk, Audit: rec, Posting: coord, Exceptions: exception.NewGuard(clk), Prices: pri,
	})
	steps := onboardingSteps{}
	dbt := debt.NewService(debt.Deps{
		DB: d, Clock: clk, Audit: rec, Posting: coord, Cash: cashBook{w: cash.NewWriter()}, Docs: docseq.NewIssuer(),
		Exceptions: exception.NewGuard(clk), Onboarding: steps, Mailer: o.mailer, Consent: crm.ConsentChecker{},
	})
	cat := catalog.NewService(catalog.Deps{
		DB: d, Clock: clk, Audit: rec, Prices: catalogPrices{w: pricing.NewPriceWriter(pri)}, Hints: pri,
		Stock: catalogStock{r: inv.StockReader()}, Limits: catalogLimits,
	})
	kt := kits.NewService(kits.Deps{
		DB: d, Clock: clk, Audit: rec, Posting: coord, Stock: kitsStock{l: inv.Ledger()},
		Reservations: kitsReservations{r: inv.Reservations()}, Prices: kitsPricing{svc: pri},
		DraftPrices: pricing.NewPriceWriter(pri), Catalog: kitsCatalog{s: cat},
	})
	cmp := campaign.NewService(campaign.Deps{DB: d, Clock: clk, Audit: rec})
	sal := sales.NewService(sales.Deps{
		DB: d, Clock: clk, Audit: rec, Posting: coord, Exceptions: exception.NewGuard(clk), Pricer: salesPricing{svc: pri},
		Stock: salesStock{l: inv.Ledger(), reader: inv.StockReader(), reservations: inv.Reservations()},
		Debt:  salesDebt{s: dbt}, Methods: salesMethods{}, Sessions: salesSessions{db: d, clock: clk},
		Combos: salesCombos{s: kt}, Campaigns: salesCampaigns{s: cmp}, Loyalty: salesLoyalty{},
	})
	pro := procurement.NewService(procurement.Deps{
		DB: d, Clock: clk, Audit: rec, Posting: coord, Stock: procurementStock{l: inv.Ledger()},
		Debt: procurementDebt{s: dbt}, Pricing: procurementPricing{svc: pri, w: pricing.NewPriceWriter(pri)},
		Catalog: procurementCatalog{s: cat}, Campaigns: cmp,
	})
	rpt := reporting.NewService(reporting.Deps{
		DB: d, Clock: clk, Audit: rec, Debt: reportingDebt{s: dbt}, Objects: o.objects,
	})
	return Domains{
		Catalog:     cat,
		Partner:     partner.NewService(partner.Deps{DB: d, Clock: clk, Audit: rec}),
		Pricing:     pri,
		Posting:     coord,
		Debt:        dbt,
		Cash:        cash.NewService(cash.Deps{DB: d, Clock: clk, Audit: rec, Onboarding: steps}),
		Inventory:   inv,
		Procurement: pro,
		Sales:       sal,
		Kits:        kt,
		Campaign:    cmp,
		CRM:         crm.NewService(crm.Deps{DB: d, Clock: clk, Audit: rec, Idempotency: idempotency.NewGuard(clk), Drafts: crmDrafts{s: sal}}),

		Notification: notification.NewService(notification.Deps{DB: d, Clock: clk}),
		Reporting:    rpt,
		Analytics: analytics.NewService(analytics.Deps{DB: d, Clock: clk, Reports: analyticsReports{s: rpt}, Model: o.model,
			Deadline: o.askDeadline}),
		Printing: printing.NewService(printing.Deps{DB: d, Clock: clk,
			Registry: printRegistry(sal, pro, inv, dbt, kt)}),
		SaaS:    sa,
		Flags:   flags,
		Limits:  limits,
		Scanner: scanner.NewService(scanner.Deps{DB: d, Clock: clk, Catalog: scannerCatalog{s: cat}}),
	}
}

// OrgLimits is the plan limit checker of the organisation service: nil without the gate.
func (d Domains) OrgLimits() org.LimitChecker {
	if d.Limits == nil {
		return nil
	}
	return d.Limits
}

// subscriptionSeeds starts a new store on the trial plan.
type subscriptionSeeds struct{ s *saas.Service }

func (s subscriptionSeeds) SeedSubscription(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	return s.s.StartTrial(ctx, q, sc.TenantID, sc.ReferralCode, sc.Now)
}

// postingEntitlements is the subscription gate as the posting coordinator sees it: a tenant that
// may not write may not post (403 subscription_read_only). A reader that is also a posting guard
// reads the subscription in the posting transaction; any other is asked on its own connection.
type postingEntitlements struct{ r middleware.EntitlementReader }

func (e postingEntitlements) CanPost(ctx context.Context, q db.Querier, tenantID uuid.UUID) error {
	if g, ok := e.r.(posting.EntitlementGuard); ok {
		return g.CanPost(ctx, q, tenantID)
	}
	ok, err := e.r.CanWrite(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("app: read entitlement: %w", err)
	}
	if !ok {
		return apperr.NewReadOnlyTenant()
	}
	return nil
}

// catalogStock is the inventory ledger as catalog search sees it: on-hand stock per color.
type catalogStock struct{ r inventory.StockReader }

func (s catalogStock) OnHandByColor(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID][]catalog.ColorQty, error) {
	stock, err := s.r.OnHandByColor(ctx, q, skuIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID][]catalog.ColorQty, len(stock))
	for skuID, colors := range stock {
		for _, c := range colors {
			out[skuID] = append(out[skuID], catalog.ColorQty{ColorID: c.ColorID, Qty: c.Qty})
		}
	}
	return out, nil
}

// scannerCatalog is the catalog as the phone scanner sees it: the barcode rules a scanned code
// must pass, and the names the phone shows once the desktop added a scanned item.
type scannerCatalog struct{ s *catalog.Service }

func (c scannerCatalog) ValidateCode(code string) error {
	return catalog.ValidateBarcode(strings.TrimSpace(code))
}

func (c scannerCatalog) ItemNames(ctx context.Context, tenantID, skuID, unitID uuid.UUID) (string, string, error) {
	d, err := c.s.Sku(ctx, catalog.Scope{TenantID: tenantID, ReadOnly: true}, skuID)
	if apperr.KindOf(err) == apperr.NotFound {
		return "", "", scanner.ErrUnknownSKU
	}
	if err != nil {
		return "", "", err
	}
	for _, u := range d.Units {
		if u.UnitID == unitID {
			return d.Sku.DisplayName, u.UnitName, nil
		}
	}
	return "", "", scanner.ErrUnknownUnit
}

// cashBook is the cash domain as the debt domain sees it: payment methods resolve to their fund
// and confirmed payments write their fund entries, all inside the debt posting.
type cashBook struct{ w cash.Writer }

func (b cashBook) Method(ctx context.Context, q db.Querier, methodID uuid.UUID) (debt.Method, error) {
	m, err := cash.MethodFund(ctx, q, methodID)
	if err != nil {
		return debt.Method{}, err
	}
	return debt.Method{
		ID: m.ID, FundAccountID: m.FundAccountID, Code: m.Code, RequiresVerification: m.RequiresVerification, Active: m.Active,
	}, nil
}

func (b cashBook) Record(ctx context.Context, q db.Querier, tenantID uuid.UUID, e debt.CashEntry) error {
	category, ok := cash.CategoryFor(e.Direction, e.Purpose, e.SourceType)
	if !ok {
		return fmt.Errorf("app: no cash category for a %s %s payment", e.Direction, e.Purpose)
	}
	// Every fund of the tenant is locked up front, in id order: a posting may pay into both.
	if err := cash.LockAllFunds(ctx, q); err != nil {
		return err
	}
	paymentID := e.PaymentID
	_, err := b.w.Write(ctx, q, tenantID, cash.EntryInput{
		FundAccountID: e.FundAccountID, Amount: e.Amount, Category: category, PaymentID: &paymentID, ShiftID: e.ShiftID,
		BusinessDate: e.BusinessDate, PostedAt: e.PostedAt, PostedBy: e.PostedBy,
	})
	return err
}

func (b cashBook) ReversePayment(ctx context.Context, q db.Querier, tenantID, paymentID uuid.UUID, businessDate, at time.Time, by uuid.UUID) error {
	if err := cash.LockAllFunds(ctx, q); err != nil {
		return err
	}
	_, err := b.w.ReversePaymentEntries(ctx, q, tenantID, paymentID, businessDate, at, by)
	return err
}

// onboardingSteps marks a step of the tenant's onboarding checklist done the first time it is
// reached, in the caller's transaction.
type onboardingSteps struct{}

func (onboardingSteps) CompleteStep(ctx context.Context, q db.Querier, tenantID uuid.UUID, step string, by uuid.UUID, at time.Time) error {
	if _, err := q.ExecContext(ctx, `
		UPDATE onboarding_steps SET completed_at = $3, completed_by = $4
		WHERE tenant_id = $1 AND step_key = $2 AND completed_at IS NULL`, tenantID, step, at, by); err != nil {
		return fmt.Errorf("app: complete onboarding step %s: %w", step, err)
	}
	return nil
}

// catalogPrices stores the base sale prices the catalog sets (product create, quick-create, unit
// table) as pricing rows with their history, in the catalog's transaction. Prices of products a purchase
// order creates are recorded as edited from the purchase order.
type catalogPrices struct{ w pricing.PriceWriter }

func (c catalogPrices) SetSalePrice(ctx context.Context, q db.Querier, actor audit.Actor, p catalog.SalePrice) error {
	unitID := p.UnitID
	source := pricing.ChangeCatalog
	if p.Source == pricing.ChangePurchaseOrder {
		source = p.Source
	}
	_, err := c.w.SetSalePrice(ctx, q, actor, pricing.SalePriceCommand{
		SkuID: p.SkuID, UnitID: &unitID, Kind: p.Kind, Price: p.Price, Source: source,
	})
	return err
}

// Seeders wires the default data of every built domain into the provisioner. Each domain keeps
// its own seed context type (domains may not import one another); the adapters below map the
// organisation's onto them.
func (d Domains) Seeders() org.Seeders {
	cat := catalogSeeds{s: catalog.NewSeeder(d.Catalog)}
	par := partnerSeeds{s: partner.NewSeeder(d.Partner)}
	pri := pricingSeeds{s: pricing.NewSeeder()}
	return org.Seeders{
		Colors:        cat,
		Units:         cat,
		TaxRates:      cat,
		AttributeSets: cat,
		PriceLists:    pri,
		Partners:      par,
		Funds:         fundSeeds{s: cash.NewSeeder()},
		Reasons:       reasonSeeds{org.ReasonDefaults{}, debtReasonSeeds{}},
		// The sample catalog comes first: later samples (documents) will reference its SKUs.
		DemoSample:   demoSamples{cat, par},
		Subscription: subscriptionSeeds{s: d.SaaS},
	}
}

// AttachmentOwners are the records a file may be uploaded for, by attachments.owner_type: product
// images, the party a signed debt offset record belongs to, the procurement documents and the
// store's support tickets.
func (d Domains) AttachmentOwners() org.AttachmentOwners {
	owners := org.AttachmentOwners{
		"product":                 ownerExists("products"),
		debt.AttachmentOwnerParty: ownerExists("parties"),
		support.AttachmentOwner:   support.TicketOfTenant,
	}
	for ownerType, exists := range procurement.AttachmentOwners() {
		owners[ownerType] = exists
	}
	return owners
}

// ownerExists reports whether a row of table has the id, in the tenant of q (RLS).
func ownerExists(table string) org.AttachmentOwner {
	query := "SELECT EXISTS (SELECT 1 FROM " + table + " WHERE id = $1)"
	return func(ctx context.Context, q db.Querier, id uuid.UUID) (bool, error) {
		var ok bool
		if err := q.QueryRowxContext(ctx, query, id).Scan(&ok); err != nil {
			return false, fmt.Errorf("app: read %s attachment owner: %w", table, err)
		}
		return ok, nil
	}
}

type fundSeeds struct{ s cash.Seeder }

func (f fundSeeds) SeedFunds(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	return f.s.SeedFunds(ctx, q, cash.SeedContext{TenantID: sc.TenantID, Now: sc.Now})
}

type debtReasonSeeds struct{}

func (debtReasonSeeds) SeedReasons(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	return debt.ReasonSeeder{}.SeedReasons(ctx, q, debt.SeedContext{TenantID: sc.TenantID, Now: sc.Now})
}

// reasonSeeds runs the reason seeders of several domains in order.
type reasonSeeds []org.ReasonSeeder

func (r reasonSeeds) SeedReasons(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	for _, s := range r {
		if err := s.SeedReasons(ctx, q, sc); err != nil {
			return err
		}
	}
	return nil
}

type pricingSeeds struct{ s *pricing.Seeder }

func (p pricingSeeds) SeedPriceLists(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	return p.s.SeedPriceLists(ctx, q, pricing.SeedContext{TenantID: sc.TenantID, Now: sc.Now})
}

type catalogSeeds struct{ s *catalog.Seeder }

func catalogContext(sc org.SeedContext) catalog.SeedContext {
	return catalog.SeedContext{TenantID: sc.TenantID, Now: sc.Now, Actor: sc.Actor}
}

func (c catalogSeeds) SeedColors(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	return c.s.SeedColors(ctx, q, catalogContext(sc))
}

func (c catalogSeeds) SeedUnits(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	return c.s.SeedUnits(ctx, q, catalogContext(sc))
}

func (c catalogSeeds) SeedTaxRates(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	return c.s.SeedTaxRates(ctx, q, catalogContext(sc))
}

func (c catalogSeeds) SeedAttributeSets(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	return c.s.SeedAttributeSets(ctx, q, catalogContext(sc))
}

func (c catalogSeeds) SeedDemoSample(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	return c.s.SeedDemoSample(ctx, q, catalogContext(sc))
}

type partnerSeeds struct{ s *partner.Seeder }

func partnerContext(sc org.SeedContext) partner.SeedContext {
	return partner.SeedContext{TenantID: sc.TenantID, Now: sc.Now, Actor: sc.Actor}
}

func (p partnerSeeds) SeedPartnerDefaults(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	return p.s.SeedPartnerDefaults(ctx, q, partnerContext(sc))
}

func (p partnerSeeds) SeedDemoSample(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	return p.s.SeedDemoSample(ctx, q, partnerContext(sc))
}

// demoSamples runs the sample seeders of several domains in order.
type demoSamples []org.DemoSampleSeeder

func (d demoSamples) SeedDemoSample(ctx context.Context, q db.Querier, sc org.SeedContext) error {
	for _, s := range d {
		if err := s.SeedDemoSample(ctx, q, sc); err != nil {
			return err
		}
	}
	return nil
}
