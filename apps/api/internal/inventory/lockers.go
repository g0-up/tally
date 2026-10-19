package inventory

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// RegisterLockers installs the row lockers of the inventory lock kinds on the posting
// coordinator. The sku_stock locker creates missing rows first, so the first posting of a SKU
// has a row to lock.
func RegisterLockers(c *posting.Coordinator, clk clock.Clock) {
	c.RegisterLocker(posting.LockSkuStock, func(ctx context.Context, q db.Querier, ids []uuid.UUID) error {
		_, err := lockSkuStock(ctx, q, ids, clk.Now())
		return err
	})
	c.RegisterLocker(posting.LockLots, lockRows("lots", "id"))
	c.RegisterLocker(posting.LockLotBalances, lockRows("lot_balances", "lot_id"))
	c.RegisterLocker(posting.LockReservations, lockRows("reservations", "id"))
}

// lockRows returns a locker taking FOR UPDATE locks on table rows whose column is in ids, in
// ascending order. table and column are constants of this file.
func lockRows(table, column string) posting.LockFunc {
	query := fmt.Sprintf(`SELECT 1 FROM %s WHERE %s = ANY($1) ORDER BY %s FOR UPDATE`, table, column, column)
	return func(ctx context.Context, q db.Querier, ids []uuid.UUID) error {
		if len(ids) == 0 {
			return nil
		}
		rows, err := q.QueryxContext(ctx, query, ids)
		if err != nil {
			return fmt.Errorf("inventory: lock %s: %w", table, err)
		}
		return rows.Close()
	}
}
