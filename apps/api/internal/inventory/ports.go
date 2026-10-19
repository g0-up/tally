package inventory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// PriceHints is the pricing domain as the stock list sees it: the base sale price per SKU in
// its base unit (a SKU without a price is left out of the map).
type PriceHints interface {
	PriceHints(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID]money.Money, error)
}

// ColorQty is the on-hand quantity of one color of a SKU; ColorID nil is "Chưa gán màu".
type ColorQty struct {
	ColorID *uuid.UUID
	Qty     money.Qty
}

// StockReader answers stock questions of other domains inside their transaction (catalog
// search shows per-color stock with this). Build it with (*Service).StockReader. It reads only
// through the caller's querier and never opens a transaction of its own: a second connection
// taken while the caller holds one deadlocks the pool once every request holds its first.
type StockReader struct {
	clock clock.Clock
}

// StockReader returns the stock reader of the ledger.
func (s *Service) StockReader() StockReader { return StockReader{clock: s.clock} }

// readOnlyZone dates reads under the read-only role (consented support sessions), which cannot
// read the tenants table, when the transaction carries no tenant time zone (db.ReadOnlyTimezone).
const readOnlyZone = "Asia/Ho_Chi_Minh"

// OnHandByColor returns the sellable quantity per color of skuIDs: quarantined lots and lots past
// their expiry are not on the shelf to sell and are left out (the expiry day itself still sells).
// Colors are ordered by color sort order, the uncolored stock last; a SKU without sellable stock
// is absent from the map.
func (r StockReader) OnHandByColor(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID][]ColorQty, error) {
	out := map[uuid.UUID][]ColorQty{}
	if len(skuIDs) == 0 {
		return out, nil
	}
	if r.clock == nil {
		return nil, errors.New("inventory: stock reader is not built by the ledger service")
	}
	today, err := r.today(ctx, q)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		SkuID   uuid.UUID  `db:"sku_id"`
		ColorID *uuid.UUID `db:"color_id"`
		Qty     money.Qty  `db:"qty"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT b.sku_id, b.color_id, SUM(b.qty) AS qty
		FROM lot_balances b JOIN lots l ON l.id = b.lot_id LEFT JOIN colors c ON c.id = b.color_id
		WHERE b.sku_id = ANY($1) AND b.qty > 0 AND NOT l.is_quarantine
		  AND (l.expiry_date IS NULL OR l.expiry_date >= $2)
		GROUP BY b.sku_id, b.color_id, c.sort_order, c.code
		ORDER BY b.sku_id, c.sort_order NULLS LAST, c.code`, skuIDs, today); err != nil {
		return nil, fmt.Errorf("inventory: read stock by color: %w", err)
	}
	for _, row := range rows {
		out[row.SkuID] = append(out[row.SkuID], ColorQty{ColorID: row.ColorID, Qty: row.Qty})
	}
	return out, nil
}

// today is the business date of the caller's tenant, read in the caller's transaction.
func (r StockReader) today(ctx context.Context, q db.Querier) (time.Time, error) {
	var tc struct {
		Tenant      string `db:"tenant"`
		ReadsTenant bool   `db:"reads_tenant"`
	}
	if err := q.QueryRowxContext(ctx, `
		SELECT current_setting('app.tenant_id', true) AS tenant, has_table_privilege('tenants', 'SELECT') AS reads_tenant`,
	).StructScan(&tc); err != nil {
		return time.Time{}, fmt.Errorf("inventory: read tenant of stock query: %w", err)
	}
	tenantID, err := uuid.Parse(tc.Tenant)
	if err != nil {
		return time.Time{}, fmt.Errorf("inventory: stock query runs outside a tenant transaction: %w", err)
	}
	now := r.clock.Now()
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
