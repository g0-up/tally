package debt

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// SeedContext is what the seeder needs from the tenant provisioning (mapped from the
// organisation's seed context by the composition root).
type SeedContext struct {
	TenantID uuid.UUID
	Now      time.Time
}

// defaultReasons are the built-in reasons a DN needs, so the opening-balance wizard and a
// write-off work in a new store before the owner adds their own.
var defaultReasons = []struct{ kind, name string }{
	{reasonDebtAdjustment, "Số dư đầu kỳ"},
	{reasonDebtAdjustment, "Điều chỉnh công nợ"},
	{reasonDebtAdjustment, "Thỏa thuận khi đối chiếu"},
	{reasonWriteOff, "Xóa nợ khó đòi"},
}

// ReasonSeeder creates the built-in debt adjustment and write-off reasons.
type ReasonSeeder struct{}

// SeedReasons creates the reasons and leaves existing ones (same kind and name) alone, so it is
// safe to run again.
func (ReasonSeeder) SeedReasons(ctx context.Context, q db.Querier, sc SeedContext) error {
	for _, d := range defaultReasons {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO reasons (id, tenant_id, kind, name, name_key, is_system, active, created_at)
			VALUES ($1, $2, $3, $4, $5, true, true, $6)
			ON CONFLICT ON CONSTRAINT ux_reasons__kind_name_key DO NOTHING`,
			ids.New(), sc.TenantID, d.kind, d.name, textnorm.Key(d.name), sc.Now); err != nil {
			return fmt.Errorf("debt: seed reason %s: %w", d.name, err)
		}
	}
	return nil
}
