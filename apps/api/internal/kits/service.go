package kits

import (
	"context"
	"errors"
	"fmt"
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
	// Posting runs reservations, substitutions, assembly batches and disassemblies.
	Posting      *posting.Coordinator
	Stock        Stock
	Reservations Reservations
	Prices       Prices
	DraftPrices  DraftPrices
	Catalog      Catalog
}

// Service implements the kit use cases: recipes and their lifecycle, assembly orders and their
// postings, disassembly, suggestions, labels and the combo expander.
type Service struct {
	db           *db.DB
	clock        clock.Clock
	audit        *audit.Recorder
	posting      *posting.Coordinator
	stock        Stock
	reservations Reservations
	prices       Prices
	draftPrices  DraftPrices
	catalog      Catalog
	docs         *docseq.Issuer
	status       AssemblyOrderStatus
	idem         *idempotency.Guard
}

// NewService returns the service.
func NewService(d Deps) *Service {
	return &Service{db: d.DB, clock: d.Clock, audit: d.Audit, posting: d.Posting, stock: d.Stock,
		reservations: d.Reservations, prices: d.Prices, draftPrices: d.DraftPrices, catalog: d.Catalog,
		docs: docseq.NewIssuer(), idem: idempotency.NewGuard(d.Clock)}
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

func tenantToday(ctx context.Context, q db.Querier, tenantID uuid.UUID, now time.Time) (time.Time, error) {
	var name string
	if err := q.QueryRowxContext(ctx, `SELECT timezone FROM tenants WHERE id = $1`, tenantID).Scan(&name); err != nil {
		return time.Time{}, fmt.Errorf("kits: read tenant time zone: %w", err)
	}
	loc, err := clock.LoadLocation(name)
	if err != nil {
		return time.Time{}, err
	}
	return clock.BusinessDate(now, loc), nil
}

// readOnlyZone dates reads under the read-only role (consented support sessions), which cannot
// read the tenants table, when the transaction carries no tenant time zone (db.ReadOnlyTimezone).
const readOnlyZone = "Asia/Ho_Chi_Minh"

// todayIn is the business date of the tenant of the caller's transaction q.
func (s *Service) todayIn(ctx context.Context, q db.Querier) (time.Time, error) {
	var tc struct {
		Tenant      string `db:"tenant"`
		ReadsTenant bool   `db:"reads_tenant"`
	}
	if err := q.QueryRowxContext(ctx, `
		SELECT current_setting('app.tenant_id', true) AS tenant, has_table_privilege('tenants', 'SELECT') AS reads_tenant`,
	).StructScan(&tc); err != nil {
		return time.Time{}, fmt.Errorf("kits: read tenant of transaction: %w", err)
	}
	tenantID, err := uuid.Parse(tc.Tenant)
	if err != nil {
		return time.Time{}, errors.New("kits: query runs outside a tenant transaction")
	}
	now := s.clock.Now()
	if tc.ReadsTenant {
		return tenantToday(ctx, q, tenantID, now)
	}
	name, err := db.ReadOnlyTimezone(ctx, q, readOnlyZone)
	if err != nil {
		return time.Time{}, err
	}
	loc, err := clock.LoadLocation(name)
	if err != nil {
		return time.Time{}, err
	}
	return clock.BusinessDate(now, loc), nil
}
