package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// ExpiryRoller moves the stock of normal lots past their expiry from sellable to expired_qty.
// Postings recompute the projection of the SKUs they touch; the roller catches the SKUs nobody
// touched since their lots expired. It only rewrites SKUs whose expired quantity differs from
// their lots, so running it again on the same day changes nothing.
type ExpiryRoller struct{}

// Roll recomputes the projections of the tenant in scope for the business date today and
// returns how many SKUs changed.
func (ExpiryRoller) Roll(ctx context.Context, tx Tx, now, today time.Time) (int, error) {
	var stale []uuid.UUID
	if err := sqlx.SelectContext(ctx, tx, &stale, `
		SELECT ss.sku_id FROM sku_stock ss
		LEFT JOIN (
		  SELECT b.sku_id, SUM(b.qty) AS qty FROM lot_balances b JOIN lots l ON l.id = b.lot_id
		  WHERE b.qty > 0 AND NOT l.is_quarantine AND l.expiry_date < $1
		  GROUP BY b.sku_id
		) e ON e.sku_id = ss.sku_id
		WHERE ss.expired_qty <> COALESCE(e.qty, 0)
		ORDER BY ss.sku_id`, today); err != nil {
		return 0, fmt.Errorf("inventory: find stale expiry: %w", err)
	}
	if len(stale) == 0 {
		return 0, nil
	}
	before, err := lockSkuStock(ctx, tx, stale, now)
	if err != nil {
		return 0, err
	}
	infos, err := readSkuInfos(ctx, tx, stale)
	if err != nil {
		return 0, err
	}
	lots, err := readLotStocks(ctx, tx, stale, nil)
	if err != nil {
		return 0, err
	}
	changed := make([]SkuStock, 0, len(stale))
	for _, id := range sortedUnique(stale) {
		s, ok := before[id]
		if !ok {
			continue
		}
		if err := recompute(&s, lots, today); err != nil {
			return 0, err
		}
		if s.ExpiredQty.Equal(before[id].ExpiredQty) {
			continue
		}
		s.UpdatedAt = now
		if err := writeSkuStock(ctx, tx, s); err != nil {
			return 0, err
		}
		s.Version++
		changed = append(changed, s)
		tx.Publish(outbox.Event{Topic: TopicStockChanged, Payload: map[string]any{
			"sku_id": id, "on_hand": s.OnHand, "sellable": s.Sellable, "reserved": s.Reserved, "available": s.Available(),
			"expired_qty": s.ExpiredQty, "reason": "expiry_roll",
		}})
	}
	if err := (StockSignalDetector{}).Check(ctx, tx, now, today, before, changed, infos); err != nil {
		return 0, err
	}
	return len(changed), nil
}
