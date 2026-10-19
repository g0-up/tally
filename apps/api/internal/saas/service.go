package saas

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/saas/provider"
)

// ClosureTokens is the owner-account side of an account closure, implemented by identity: the
// emailed one-time verification link and the emailed link to a closed store's final export. The
// issuers run within inside the account transaction and mail after commit.
type ClosureTokens interface {
	IssueClosureToken(ctx context.Context, p middleware.Principal, within func(q db.Querier) (uuid.UUID, error)) error
	ConsumeClosureToken(ctx context.Context, p middleware.Principal, token string,
		within func(q db.Querier, requestID uuid.UUID) error) error
	// IssueExportLink mails the store's owner a reusable link to the export within
	// returns, working until the time it returns; within returning uuid.Nil issues nothing. It
	// reports whether a link was issued (a store without an active owner gets none).
	IssueExportLink(ctx context.Context, tenantID uuid.UUID,
		within func(q db.Querier) (uuid.UUID, time.Time, error)) (bool, error)
	// ResolveExportLink returns the export a closure export link of the tenant opens; an
	// unknown, replaced or expired link is 404.
	ResolveExportLink(ctx context.Context, tenantID uuid.UUID, token string) (uuid.UUID, error)
}

// Deps are the collaborators of the SaaS service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	Audit *audit.Recorder
	// Flags override modules per tenant ("module:<name>"); may be nil.
	Flags FlagReader
	// Providers take payments; the manual bank transfer is always available.
	Providers []provider.Provider
	// Objects stores full data exports; without it they stay queued.
	Objects objectstore.Store
	// Closure issues and checks the closure links; required for account closure.
	Closure ClosureTokens
	Logger  *slog.Logger
}

// Service runs plans, subscriptions, service billing, account closure and the full data export.
type Service struct {
	db        *db.DB
	clock     clock.Clock
	audit     *audit.Recorder
	reader    *Reader
	publisher *outbox.Publisher
	providers map[string]provider.Provider
	objects   objectstore.Store
	closure   ClosureTokens
	logger    *slog.Logger
	lifecycle SubscriptionLifecycle
	idem      *idempotency.Guard
}

// NewService returns the service.
func NewService(d Deps) *Service {
	s := &Service{
		db: d.DB, clock: d.Clock, audit: d.Audit, reader: NewReader(d.DB, d.Clock, d.Flags),
		publisher: outbox.NewPublisher(d.Clock), providers: map[string]provider.Provider{},
		objects: d.Objects, closure: d.Closure, logger: d.Logger, idem: idempotency.NewGuard(d.Clock),
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	s.providers[provider.ManualName] = provider.NewManualTransfer("")
	for _, p := range d.Providers {
		s.providers[p.Name()] = p
	}
	return s
}

// SetClosureTokens wires the closure verification links after construction: identity is built
// after the provisioner that needs this service's trial seeder.
func (s *Service) SetClosureTokens(t ClosureTokens) { s.closure = t }

// Reader is the entitlement reader the HTTP gate, the posting guard and the limit checks share.
func (s *Service) Reader() *Reader { return s.reader }

// Plans lists the plans; activeOnly hides the retired ones (the tenant list).
func (s *Service) Plans(ctx context.Context, activeOnly bool) ([]Plan, error) {
	var out []Plan
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		out, err = listPlans(ctx, q, activeOnly)
		return err
	})
	return out, err
}

// SubscriptionView is a tenant's subscription as it holds today (the lifecycle applied at read
// time), with its plan, resolved entitlements and the add-ons in force. Subscription is nil for a
// tenant without one, and so are its add-ons, as entitlements apply them to a subscription only.
type SubscriptionView struct {
	Subscription *Subscription
	Plan         *Plan
	Entitlements Entitlements
	Addons       []ActiveAddon
}

// ActiveAddon is an add-on whose paid period holds the store's business day: the modules its
// purchase enabled from PeriodStart through PeriodEnd.
type ActiveAddon struct {
	PlanID      uuid.UUID
	PlanName    string
	Modules     []string
	PeriodStart time.Time
	PeriodEnd   time.Time
}

// Subscription returns the tenant's own subscription.
func (s *Service) Subscription(ctx context.Context, tenantID uuid.UUID) (SubscriptionView, error) {
	var out SubscriptionView
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		t, err := readTenantState(ctx, q, tenantID, false)
		if err != nil {
			return err
		}
		if out.Entitlements, err = s.reader.Resolve(ctx, q, tenantID); err != nil {
			return err
		}
		sub, found, err := readSubscription(ctx, q, tenantID, false)
		if err != nil || !found {
			return err
		}
		plan, err := readPlan(ctx, q, sub.PlanID)
		if err != nil {
			return err
		}
		today, err := t.today(s.clock.Now())
		if err != nil {
			return err
		}
		sub, _ = s.lifecycle.Advance(sub, plan, today)
		out.Subscription, out.Plan = &sub, &plan
		out.Addons, err = activeAddons(ctx, q, tenantID, today)
		return err
	})
	return out, err
}

// Entitlements returns the tenant's resolved modules and limits.
func (s *Service) Entitlements(ctx context.Context, tenantID uuid.UUID) (Entitlements, error) {
	var out Entitlements
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		var err error
		out, err = s.reader.Resolve(ctx, q, tenantID)
		return err
	})
	return out, err
}

// lockBilling serialises the billing changes of one tenant (issuing, paying, plan changes), so
// two of them never read the same subscription state.
func lockBilling(ctx context.Context, q db.Querier, tenantID uuid.UUID) error {
	if _, err := q.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('saas_billing:' || $1::text, 0))`,
		tenantID); err != nil {
		return fmt.Errorf("saas: lock billing: %w", err)
	}
	return nil
}

// systemActor is the actor of a scheduled job or a provider callback.
func systemActor(tenantID uuid.UUID, source audit.Source) audit.Actor {
	return audit.Actor{TenantID: tenantID, Type: audit.ActorSystem, Source: source}
}

// AdminActor is the actor of a platform admin acting on a tenant.
func AdminActor(tenantID, adminID uuid.UUID, requestID, ip string) audit.Actor {
	return audit.Actor{
		TenantID: tenantID, Type: audit.ActorPlatformAdmin, ID: adminID, Source: audit.SourceWeb,
		RequestID: requestID, IP: ip,
	}
}

// daysInclusive counts the days from start through end (both dates).
func daysInclusive(start, end time.Time) int64 {
	s := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	e := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	return int64(e.Sub(s).Hours()/24) + 1
}

// laterDate returns the later of two dates.
func laterDate(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// SupportSLAHours is the support response time the tenant's plan publishes, read in q; a tenant
// without a subscription gets the default term.
func (s *Service) SupportSLAHours(ctx context.Context, q db.Querier, tenantID uuid.UUID) (int, error) {
	sub, found, err := readSubscription(ctx, q, tenantID, false)
	if err != nil {
		return 0, err
	}
	if !found {
		return int(termDefaults[TermSupportSLAHours]), nil
	}
	plan, err := readPlan(ctx, q, sub.PlanID)
	if err != nil {
		return 0, err
	}
	return plan.Term(TermSupportSLAHours), nil
}
