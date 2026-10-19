package pricing

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// SeedContext is what the pricing seeder needs of a tenant being provisioned. It runs inside the
// provisioning tenant transaction and is idempotent: a retried provisioning runs it again.
type SeedContext struct {
	TenantID uuid.UUID
	Now      time.Time
}

// Seeder creates the base price lists of a new tenant.
type Seeder struct{}

// NewSeeder returns the seeder.
func NewSeeder() *Seeder { return &Seeder{} }

// SeedPriceLists creates the retail ("Giá bán lẻ") and wholesale ("Giá bán sỉ") lists.
func (*Seeder) SeedPriceLists(ctx context.Context, q db.Querier, sc SeedContext) error {
	for _, kind := range []string{KindRetail, KindWholesale} {
		if err := insertBaseList(ctx, q, sc.TenantID, kind, sc.Now); err != nil {
			return err
		}
		if _, ok, err := baseList(ctx, q, kind); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("pricing: seed %s price list: %w", kind, errBaseListMissing())
		}
	}
	return nil
}
