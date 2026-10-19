package pricing

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
)

// Field limits of pricing records.
const (
	maxNameLen = 100
	// MaxPriority bounds the priority of a price list (lower wins).
	MaxPriority = 10000
	// DefaultPriority is the priority of a list created without one.
	DefaultPriority = 100
	// MaxBulkItems bounds one bulk price upsert.
	MaxBulkItems = 500
	// MaxDocumentLines bounds the lines one calculation or resolution accepts.
	MaxDocumentLines = 500
	// MaxPromotionItems bounds the SKUs of one promotion.
	MaxPromotionItems = 200
	// MaxPromotionCodes bounds the codes one document may carry.
	MaxPromotionCodes = 10
)

// Deps are the collaborators of Service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	Audit *audit.Recorder
	// Exceptions consumes owner confirmations; nil uses exception.NewGuard(Clock).
	Exceptions *exception.Guard
}

// Service implements the pricing use cases. Every method runs in its own tenant transaction;
// the tenant id always comes from the authenticated session.
type Service struct {
	db         *db.DB
	clock      clock.Clock
	audit      *audit.Recorder
	resolver   PriceResolver
	engine     PromotionEngine
	calculator DocumentCalculator
	guard      MinPriceGuard
	idem       *idempotency.Guard
}

// NewService returns the service.
func NewService(d Deps) *Service {
	exceptions := d.Exceptions
	if exceptions == nil {
		exceptions = exception.NewGuard(d.Clock)
	}
	resolver := NewPriceResolver()
	engine := NewPromotionEngine()
	return &Service{
		db: d.DB, clock: d.Clock, audit: d.Audit,
		resolver:   resolver,
		engine:     engine,
		calculator: DocumentCalculator{Resolver: resolver, Engine: engine},
		guard:      MinPriceGuard{Exceptions: exceptions},
		idem:       idempotency.NewGuard(d.Clock),
	}
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

// write runs fn in a tenant transaction of the actor's tenant.
func (s *Service) write(ctx context.Context, actor audit.Actor, fn func(q db.Querier) error) error {
	return s.db.WithTenantTx(ctx, actor.TenantID, fn)
}

// record appends an audit event.
func (s *Service) record(ctx context.Context, q db.Querier, actor audit.Actor, action, entity string, id uuid.UUID, before, after any) error {
	return s.audit.Record(ctx, q, actor, audit.Event{Action: action, EntityType: entity, EntityID: id, Before: before, After: after})
}

// tenantPricing is what the calculator needs of the tenant: its time zone (business dates) and
// its tax and rounding settings.
type tenantPricing struct {
	Location         *time.Location
	PricesIncludeTax bool
	RoundingMode     string
}

// Rounding modes (tenant_settings.rounding_mode).
const (
	RoundByLine     = "line"
	RoundByDocument = "document"
)

// readOnlyZone prices reads under the read-only role (consented support sessions), which cannot
// read the tenants table, when the transaction carries no tenant time zone (db.ReadOnlyTimezone).
const readOnlyZone = "Asia/Ho_Chi_Minh"

// loadTenantPricing reads the tenant's time zone and settings; a tenant without a settings row
// uses the column defaults (prices include tax, rounding by line). Under the read-only role the
// time zone is the one the transaction carries.
func loadTenantPricing(ctx context.Context, q db.Querier, tenantID uuid.UUID) (tenantPricing, error) {
	var readsTenant bool
	if err := q.QueryRowxContext(ctx, `SELECT has_table_privilege('tenants', 'SELECT')`).Scan(&readsTenant); err != nil {
		return tenantPricing{}, fmt.Errorf("pricing: read tenant privilege: %w", err)
	}
	var row struct {
		Timezone         string `db:"timezone"`
		PricesIncludeTax bool   `db:"prices_include_tax"`
		RoundingMode     string `db:"rounding_mode"`
	}
	var err error
	if readsTenant {
		err = q.QueryRowxContext(ctx, `
			SELECT t.timezone, COALESCE(st.prices_include_tax, true) AS prices_include_tax,
			       COALESCE(st.rounding_mode, 'line') AS rounding_mode
			FROM tenants t LEFT JOIN tenant_settings st ON st.tenant_id = t.id
			WHERE t.id = $1`, tenantID).StructScan(&row)
	} else {
		if row.Timezone, err = db.ReadOnlyTimezone(ctx, q, readOnlyZone); err != nil {
			return tenantPricing{}, err
		}
		err = q.QueryRowxContext(ctx, `
			SELECT COALESCE((SELECT prices_include_tax FROM tenant_settings WHERE tenant_id = $1), true) AS prices_include_tax,
			       COALESCE((SELECT rounding_mode FROM tenant_settings WHERE tenant_id = $1), 'line') AS rounding_mode`,
			tenantID).StructScan(&row)
	}
	if db.IsNoRows(err) {
		return tenantPricing{}, errNotFound()
	}
	if err != nil {
		return tenantPricing{}, fmt.Errorf("pricing: read tenant settings: %w", err)
	}
	loc, err := clock.LoadLocation(row.Timezone)
	if err != nil {
		return tenantPricing{}, fmt.Errorf("pricing: tenant time zone: %w", err)
	}
	return tenantPricing{Location: loc, PricesIncludeTax: row.PricesIncludeTax, RoundingMode: row.RoundingMode}, nil
}

// readPriced runs fn in a read transaction suited to scope, with the tenant's pricing settings.
func (s *Service) readPriced(ctx context.Context, scope Scope, fn func(q db.Querier, tp tenantPricing) error) error {
	return s.read(ctx, scope, func(q db.Querier) error {
		tp, err := loadTenantPricing(ctx, q, scope.TenantID)
		if err != nil {
			return err
		}
		return fn(q, tp)
	})
}

// businessDate is d, or today in the tenant's time zone when d is nil.
func (s *Service) businessDate(d *time.Time, tp tenantPricing) time.Time {
	if d != nil {
		return clock.TruncateDate(*d)
	}
	return clock.BusinessDate(s.clock.Now(), tp.Location)
}

// fieldErrors collects field-level validation failures.
type fieldErrors map[string]string

func (f fieldErrors) err() error {
	if len(f) == 0 {
		return nil
	}
	return errInvalid(f)
}

// add records the first failure of a field.
func (f fieldErrors) add(field, reason string) {
	if _, ok := f[field]; !ok {
		f[field] = reason
	}
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// text trims v and checks it: required, at most maxLen characters, a single line.
func (f fieldErrors) text(field string, v *string, required bool, maxLen int) {
	*v = strings.TrimSpace(*v)
	switch {
	case *v == "":
		if required {
			f.add(field, "required")
		}
	case utf8.RuneCountInString(*v) > maxLen:
		f.add(field, "too_long")
	case !utf8.ValidString(*v) || strings.ContainsFunc(*v, isControl):
		f.add(field, "invalid_text")
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func ptr[T any](v T) *T { return &v }

func sameID(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
