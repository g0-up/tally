package admin

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/saas"
)

// TenantRow is a store in the admin directory: metadata, subscription and the latest usage day.
// It is read from tenants, subscriptions, plans and tenant_usage_daily only, never from a
// sales, stock, partner or ledger table.
type TenantRow struct {
	ID                 uuid.UUID  `db:"id"`
	Slug               string     `db:"slug"`
	DisplayName        string     `db:"display_name"`
	Status             string     `db:"status"`
	IsSandbox          bool       `db:"is_sandbox"`
	SandboxOf          *uuid.UUID `db:"sandbox_of"`
	LegalHold          bool       `db:"legal_hold"`
	Maintenance        bool       `db:"maintenance"`
	CreatedAt          time.Time  `db:"created_at"`
	SubscriptionStatus *string    `db:"subscription_status"`
	PlanCode           *string    `db:"plan_code"`
	PlanName           *string    `db:"plan_name"`
	PeriodEnd          *time.Time `db:"current_period_end"`
	UsageDate          *time.Time `db:"usage_date"`
	ActiveSKUs         *int       `db:"active_skus"`
	StorageBytes       *int64     `db:"storage_bytes"`
}

// directorySelect is the one query of the directory; replaced sandboxes are left out everywhere.
const directorySelect = `
	SELECT t.id, t.slug, t.display_name, t.status, t.is_sandbox, t.sandbox_of, t.legal_hold, t.maintenance, t.created_at,
	  s.status AS subscription_status, p.code AS plan_code, p.name AS plan_name, s.current_period_end,
	  u.usage_date, u.active_skus, u.storage_bytes
	FROM tenants t
	LEFT JOIN LATERAL (
	  SELECT status, plan_id, current_period_end FROM subscriptions
	  WHERE tenant_id = t.id ORDER BY status <> 'closed' DESC, created_at DESC, id DESC LIMIT 1
	) s ON true
	LEFT JOIN plans p ON p.id = s.plan_id
	LEFT JOIN LATERAL (
	  SELECT usage_date, active_skus, storage_bytes FROM tenant_usage_daily
	  WHERE tenant_id = t.id ORDER BY usage_date DESC LIMIT 1
	) u ON true
	WHERE t.status <> 'replaced'`

// TenantFilter narrows the directory: Query matches the slug or the store name, Status the
// tenant status, Subscription the subscription status.
type TenantFilter struct {
	Query        string
	Status       string
	Subscription string
}

// Directory answers the admin tenant directory.
type Directory struct {
	db   *db.DB
	saas *saas.Service
}

// NewDirectory returns the directory.
func NewDirectory(d *db.DB, s *saas.Service) *Directory { return &Directory{db: d, saas: s} }

// Tenants lists one page of the stores, newest first; it reads page.FetchLimit rows.
func (d *Directory) Tenants(ctx context.Context, f TenantFilter, page httpx.Page) ([]TenantRow, error) {
	after, afterID, err := page.AfterTime()
	if err != nil {
		return nil, err
	}
	out := []TenantRow{}
	pattern := ""
	if q := strings.TrimSpace(f.Query); q != "" {
		pattern = "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(strings.ToLower(q)) + "%"
	}
	err = d.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, directorySelect+`
			  AND ($1 = '' OR t.slug LIKE $1 OR lower(t.display_name) LIKE $1)
			  AND ($2 = '' OR t.status = $2)
			  AND ($3 = '' OR s.status = $3)
			  AND ($4::timestamptz IS NULL OR (t.created_at, t.id) < ($4, $5))
			ORDER BY t.created_at DESC, t.id DESC LIMIT $6`, pattern, f.Status, f.Subscription, after, afterID, page.FetchLimit())
	})
	if err != nil {
		return nil, fmt.Errorf("admin: list tenants: %w", err)
	}
	return out, nil
}

// TenantDetail is one store of the directory with its resolved subscription, recent usage and
// closure.
type TenantDetail struct {
	TenantRow
	Subscription saas.SubscriptionView
	Usage        []saas.Usage
	// Closure is the store's latest closure request with its export, so an operator sees a
	// closure whose final export failed.
	Closure saas.ClosureView
}

// detailUsageDays is how much usage history the tenant page shows.
const detailUsageDays = 30

const detailUsageSelect = `
	SELECT tenant_id, usage_date, active_skus, documents_posted, storage_bytes FROM tenant_usage_daily
	WHERE tenant_id = $1 ORDER BY usage_date DESC LIMIT `

// Tenant returns one store; a replaced sandbox is not found.
func (d *Directory) Tenant(ctx context.Context, id uuid.UUID) (TenantDetail, error) {
	var out TenantDetail
	err := d.db.WithPlatformTx(ctx, func(q db.Querier) error {
		err := sqlx.GetContext(ctx, q, &out.TenantRow, directorySelect+` AND t.id = $1`, id)
		if db.IsNoRows(err) {
			return apperr.NewNotFound("not_found", "Tenant not found")
		}
		if err != nil {
			return fmt.Errorf("admin: read tenant: %w", err)
		}
		out.Usage = []saas.Usage{}
		return sqlx.SelectContext(ctx, q, &out.Usage, detailUsageSelect+fmt.Sprint(detailUsageDays), id)
	})
	if err != nil {
		return TenantDetail{}, err
	}
	if out.Subscription, err = d.saas.Subscription(ctx, id); err != nil {
		return TenantDetail{}, err
	}
	if out.Closure, err = d.saas.Closure(ctx, id); err != nil {
		return TenantDetail{}, err
	}
	return out, nil
}
