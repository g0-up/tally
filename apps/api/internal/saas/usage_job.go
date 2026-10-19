package saas

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
)

// Usage is one day of a store's usage; it counts records and never reads their content.
type Usage struct {
	TenantID        uuid.UUID `db:"tenant_id" json:"tenant_id"`
	UsageDate       time.Time `db:"usage_date" json:"usage_date"`
	ActiveSKUs      int       `db:"active_skus" json:"active_skus"`
	DocumentsPosted int       `db:"documents_posted" json:"documents_posted"`
	StorageBytes    int64     `db:"storage_bytes" json:"storage_bytes"`
}

// RecordUsage writes today's usage of every store except replaced sandboxes, each on its own
// business date: active SKUs, documents posted today and attachment storage. Running it again
// the same day refreshes the row. It returns the number of stores recorded.
func (s *Service) RecordUsage(ctx context.Context) (int, error) {
	tenants, err := s.db.ActiveTenantIDs(ctx)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, tenantID := range tenants {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		if err := s.recordTenantUsage(ctx, tenantID); err != nil {
			s.logger.ErrorContext(ctx, "usage recording failed", "tenant_id", tenantID, "error", err)
			continue
		}
		done++
	}
	return done, nil
}

func (s *Service) recordTenantUsage(ctx context.Context, tenantID uuid.UUID) error {
	return s.db.WithTenantTx(ctx, tenantID, func(q db.Querier) error {
		t, err := readTenantState(ctx, q, tenantID, false)
		if err != nil {
			return err
		}
		loc, err := clock.LoadLocation(t.Timezone)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		today := clock.BusinessDate(now, loc)
		from := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, loc)
		u := Usage{TenantID: tenantID, UsageDate: today}
		if err := sqlx.GetContext(ctx, q, &u.ActiveSKUs,
			`SELECT count(*) FROM skus WHERE tenant_id = $1 AND status = 'active'`, tenantID); err != nil {
			return fmt.Errorf("saas: count skus: %w", err)
		}
		if err := sqlx.GetContext(ctx, q, &u.DocumentsPosted, `
			SELECT count(*) FROM audit_events
			WHERE tenant_id = $1 AND action LIKE '%.post' AND created_at >= $2 AND created_at < $3`,
			tenantID, from, from.AddDate(0, 0, 1)); err != nil {
			return fmt.Errorf("saas: count postings: %w", err)
		}
		if err := sqlx.GetContext(ctx, q, &u.StorageBytes,
			`SELECT COALESCE(sum(size_bytes), 0) FROM attachments WHERE tenant_id = $1`, tenantID); err != nil {
			return fmt.Errorf("saas: sum storage: %w", err)
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO tenant_usage_daily (tenant_id, usage_date, active_skus, documents_posted, storage_bytes)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT ON CONSTRAINT pk_tenant_usage_daily DO UPDATE
			  SET active_skus = EXCLUDED.active_skus, documents_posted = EXCLUDED.documents_posted,
			      storage_bytes = EXCLUDED.storage_bytes`,
			u.TenantID, u.UsageDate, u.ActiveSKUs, u.DocumentsPosted, u.StorageBytes); err != nil {
			return fmt.Errorf("saas: write usage: %w", err)
		}
		return nil
	})
}

// UsageRow is a day of a store's usage with the store's slug and name.
type UsageRow struct {
	Usage
	TenantSlug string `db:"tenant_slug"`
	TenantName string `db:"tenant_name"`
}

// UsageCursor is the keyset cursor of a usage row: its day, then its store.
func UsageCursor(u UsageRow) httpx.Cursor {
	return httpx.Cursor{SortKey: u.UsageDate.Format(time.DateOnly), ID: u.TenantID}
}

// UsageBetween lists one page of the recorded usage of every store except replaced sandboxes from
// day from through day to, oldest day first; it reads page.FetchLimit rows.
func (s *Service) UsageBetween(ctx context.Context, from, to time.Time, page httpx.Page) ([]UsageRow, error) {
	if to.Before(from) || to.Sub(from) > 366*24*time.Hour {
		return nil, errInvalid(map[string]string{"to": "range"})
	}
	var afterDay *time.Time
	afterTenant := uuid.Nil
	if page.After != nil {
		day, err := time.Parse(time.DateOnly, page.After.SortKey)
		if err != nil {
			return nil, apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"})
		}
		afterDay, afterTenant = &day, page.After.ID
	}
	var out []UsageRow
	err := s.db.WithPlatformTx(ctx, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &out, `
			SELECT u.tenant_id, u.usage_date, u.active_skus, u.documents_posted, u.storage_bytes,
			  t.slug AS tenant_slug, t.display_name AS tenant_name
			FROM tenant_usage_daily u JOIN tenants t ON t.id = u.tenant_id
			WHERE t.status <> 'replaced' AND u.usage_date BETWEEN $1 AND $2
			  AND ($3::date IS NULL OR (u.usage_date, u.tenant_id) > ($3, $4))
			ORDER BY u.usage_date, u.tenant_id LIMIT $5`, from, to, afterDay, afterTenant, page.FetchLimit())
	})
	if err != nil {
		return nil, fmt.Errorf("saas: list usage: %w", err)
	}
	return out, nil
}

// StorageUsed returns the attachment bytes the tenant stores now; q is the tenant transaction.
func StorageUsed(ctx context.Context, q db.Querier, tenantID uuid.UUID) (int64, error) {
	var n int64
	if err := sqlx.GetContext(ctx, q, &n,
		`SELECT COALESCE(sum(size_bytes), 0) FROM attachments WHERE tenant_id = $1`, tenantID); err != nil {
		return 0, fmt.Errorf("saas: sum storage: %w", err)
	}
	return n, nil
}
