package crm

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Deps are the collaborators of Service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	Audit *audit.Recorder
	// Idempotency makes manual loyalty adjustments, consents and re-order drafts safe to retry.
	Idempotency *idempotency.Guard
	// Drafts creates the draft invoice of a re-order; nil refuses re-order drafts.
	Drafts InvoiceDrafter
}

// DraftLine is one line of a re-order draft: the SKU, color and unit to sell now. The price is
// left to the current price resolver.
type DraftLine struct {
	SkuID   uuid.UUID
	ColorID *uuid.UUID
	UnitID  uuid.UUID
	Qty     money.Qty
}

// Draft is the draft invoice a re-order created.
type Draft struct {
	InvoiceID   uuid.UUID
	DocNo       string
	TotalAmount money.Money
	Version     int
}

// InvoiceDrafter creates a draft invoice for a customer at current prices, units and pack sizes,
// inside the caller's tenant transaction q. The owner reviews and confirms it.
type InvoiceDrafter interface {
	DraftInvoice(ctx context.Context, q db.Querier, actor audit.Actor, customerID uuid.UUID, lines []DraftLine, note string) (Draft, error)
}

// Service implements the customer timeline, consents, care tasks, re-order drafts and loyalty.
type Service struct {
	db     *db.DB
	clock  clock.Clock
	audit  *audit.Recorder
	idem   *idempotency.Guard
	drafts InvoiceDrafter
}

// NewService returns the service.
func NewService(d Deps) *Service {
	idem := d.Idempotency
	if idem == nil {
		idem = idempotency.NewGuard(d.Clock)
	}
	return &Service{db: d.DB, clock: d.Clock, audit: d.Audit, idem: idem, drafts: d.Drafts}
}

// Scope is the tenant a request acts on. ReadOnly is set for consented support sessions: their
// reads run in a READ ONLY transaction under the tally_readonly role.
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
}

func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

func errInvalid(fields map[string]string) error {
	return apperr.NewValidation("validation_failed", "Some fields are not valid", fields)
}

func errNotFound(what string) error {
	return apperr.NewNotFound("not_found", what+" not found")
}

func errCursor() error {
	return apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"})
}

func sqlxSelect(ctx context.Context, q db.Querier, dst any, query string, args ...any) error {
	return sqlx.SelectContext(ctx, q, dst, query, args...)
}

// readOnlyZone dates reads under the read-only role (consented support sessions), which cannot
// read the tenants table, when the transaction carries no tenant time zone (db.ReadOnlyTimezone).
const readOnlyZone = "Asia/Ho_Chi_Minh"

// todayIn is the business date at now of the tenant of q.
func todayIn(ctx context.Context, q db.Querier, now time.Time) (time.Time, error) {
	var readsTenant bool
	if err := q.QueryRowxContext(ctx, `SELECT has_table_privilege('tenants', 'SELECT')`).Scan(&readsTenant); err != nil {
		return time.Time{}, fmt.Errorf("crm: read tenant privileges: %w", err)
	}
	name, err := db.ReadOnlyTimezone(ctx, q, readOnlyZone)
	if err != nil {
		return time.Time{}, err
	}
	if readsTenant {
		if err := q.QueryRowxContext(ctx, `SELECT timezone FROM tenants WHERE id = current_setting('app.tenant_id')::uuid`).Scan(&name); err != nil {
			return time.Time{}, fmt.Errorf("crm: read tenant time zone: %w", err)
		}
	}
	loc, err := clock.LoadLocation(name)
	if err != nil {
		return time.Time{}, err
	}
	return clock.BusinessDate(now, loc), nil
}

// partyExists returns 404 when the party is not a customer of the tenant.
func partyExists(ctx context.Context, q db.Querier, partyID uuid.UUID) error {
	var ok bool
	if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM customer_profiles WHERE party_id = $1)`, partyID).Scan(&ok); err != nil {
		return fmt.Errorf("crm: read party: %w", err)
	}
	if !ok {
		return errNotFound("customer")
	}
	return nil
}
