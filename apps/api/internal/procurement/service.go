package procurement

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Deps are the collaborators of Service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	Audit *audit.Recorder
	// Posting runs receipts, landed costs, purchase returns, price adjustments and reversals.
	Posting *posting.Coordinator
	Stock   Stock
	Debt    Debt
	Pricing Pricing
	// Catalog creates the new products of purchase lines; nil refuses them.
	Catalog Catalog
	// Campaigns tags a receipt with the campaign it was bought for; nil refuses a campaign.
	Campaigns Campaigns
}

// Service implements the Nhập hàng use cases. Purchase orders and supplier documents are plain
// versioned records; everything that moves stock or debt is a posting.
type Service struct {
	db      *db.DB
	clock   clock.Clock
	audit   *audit.Recorder
	posting *posting.Coordinator
	stock   Stock
	debt    Debt
	pricing Pricing
	catalog Catalog
	// campaigns tags receipts with their purpose campaign.
	campaigns Campaigns
	docs      *docseq.Issuer
	status    PurchaseOrderStatus
	idem      *idempotency.Guard
}

// NewService returns the service.
func NewService(d Deps) *Service {
	return &Service{db: d.DB, clock: d.Clock, audit: d.Audit, posting: d.Posting, stock: d.Stock, debt: d.Debt,
		pricing: d.Pricing, catalog: d.Catalog, campaigns: d.Campaigns, docs: docseq.NewIssuer(), idem: idempotency.NewGuard(d.Clock)}
}

// Scope is the tenant a request acts on. ReadOnly is set for consented support sessions: their
// reads run in a READ ONLY transaction under the tally_readonly role.
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
}

// read runs fn in a tenant transaction suited to scope.
func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

// record appends an audit event.
func (s *Service) record(ctx context.Context, q db.Querier, actor audit.Actor, action, entity string, id uuid.UUID, before, after any) error {
	return s.audit.Record(ctx, q, actor, audit.Event{Action: action, EntityType: entity, EntityID: id, Before: before, After: after})
}

// today is the tenant's business date now. It reads the tenant's time zone in a normal tenant
// transaction: the read-only role may not read tenants.
func (s *Service) today(ctx context.Context, tenantID uuid.UUID) (time.Time, error) {
	var out time.Time
	err := s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) (err error) {
		out, err = tenantToday(ctx, q, tenantID, s.clock.Now())
		return err
	})
	return out, err
}

// postingDate is the business date of a posting: today when not given, never in the future.
func (s *Service) postingDate(ctx context.Context, tenantID uuid.UUID, given *time.Time) (time.Time, error) {
	today, err := s.today(ctx, tenantID)
	if err != nil {
		return time.Time{}, err
	}
	if given == nil {
		return today, nil
	}
	if given.After(today) {
		return time.Time{}, errInvalid(map[string]string{"business_date": "in_future"})
	}
	return *given, nil
}
