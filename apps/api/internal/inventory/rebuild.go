package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// lotRepairs rewrite lot_balances from the moves before the SKUs are rebuilt, as the SKU
// quantities derive from the lots.
var lotRepairs = []posting.Repair{
	// A lot whose moves sum below zero is a ledger error; it keeps its balance for Verify to report.
	{Check: "lot balance", Stmt: `
		INSERT INTO lot_balances (tenant_id, lot_id, sku_id, color_id, qty, updated_at)
		SELECT l.tenant_id, l.id, l.sku_id, l.color_id, bad.expected::numeric, $1
		FROM bad JOIN lots l ON l.id = bad.id
		WHERE l.tenant_id = ` + posting.InTenant + ` AND bad.expected::numeric >= 0
		ON CONFLICT ON CONSTRAINT pk_lot_balances DO UPDATE SET qty = EXCLUDED.qty, updated_at = EXCLUDED.updated_at`},
	{Check: "lot balance identity", Stmt: `
		UPDATE lot_balances b SET sku_id = l.sku_id, color_id = l.color_id, updated_at = $1
		FROM bad JOIN lots l ON l.id = bad.id
		WHERE b.lot_id = l.id AND b.tenant_id = ` + posting.InTenant + ` AND l.tenant_id = ` + posting.InTenant},
}

// skuStockChecks are the checks of sku_stock. Every SKU one of them flags gets its row rewritten
// whole: the CHECK constraints tie its quantities together, so they change in one update.
var skuStockChecks = []string{
	"sku on hand", "sku on hand by lots", "sku stock value", "sku quarantine", "sku sellable", "sku expired",
	"sku reserved", "negative stock",
}

// RebuildChecks names the checks whose rows Rebuild rewrites.
func RebuildChecks() []string { return append(posting.Checks(lotRepairs), skuStockChecks...) }

// checkQuery returns the query of the named check over every SKU.
func checkQuery(name string) (string, error) {
	for _, c := range verifyChecks {
		if c.name == name {
			return skuFilterRe.ReplaceAllString(c.query, "TRUE"), nil
		}
	}
	return "", fmt.Errorf("inventory: no verify check %q", name)
}

// Rebuild rewrites the stock projections of the tenant in scope that Verify finds disagreeing
// with the stock_moves ledger, to what the checks expect. Lot balances go first. Then each SKU a
// sku_stock check flags is rewritten whole: on hand, stock value and reserved as the checks sum
// them from the moves and the open reservations, quarantine, expired and sellable recomputed from
// its lots on the tenant's business date as a posting does, and the average cost from the new
// value. A row the ledger itself makes invalid (a negative sum, lots that disagree with the moves)
// keeps its stored values for Verify to report. The caller holds the tenant's posting locks.
func Rebuild(ctx context.Context, q db.Querier, tenantID uuid.UUID, now time.Time) ([]posting.Rebuilt, error) {
	out, err := posting.RunRepairs(ctx, q, checkQuery, lotRepairs, now)
	if err != nil {
		return nil, fmt.Errorf("inventory: %w", err)
	}
	skus, err := rebuildSkuStock(ctx, q, tenantID, now)
	if err != nil {
		return nil, err
	}
	return append(out, skus...), nil
}

// ledgerSums are the sku_stock quantities the checks sum from the ledger, for the SKUs whose
// stored value differs.
type ledgerSums struct {
	onHand   map[uuid.UUID]money.Qty
	value    map[uuid.UUID]money.Money
	reserved map[uuid.UUID]money.Qty
}

func rebuildSkuStock(ctx context.Context, q db.Querier, tenantID uuid.UUID, now time.Time) ([]posting.Rebuilt, error) {
	sums := ledgerSums{onHand: map[uuid.UUID]money.Qty{}, value: map[uuid.UUID]money.Money{}, reserved: map[uuid.UUID]money.Qty{}}
	flagged := make(map[string][]uuid.UUID, len(skuStockChecks))
	var all []uuid.UUID
	for _, name := range skuStockChecks {
		ids, err := readSkuCheck(ctx, q, name, &sums)
		if err != nil {
			return nil, err
		}
		flagged[name] = ids
		all = append(all, ids...)
	}
	if len(all) == 0 {
		return nil, nil
	}
	today, err := tenantToday(ctx, q, tenantID, now)
	if err != nil {
		return nil, err
	}
	stocks, err := lockSkuStock(ctx, q, all, now)
	if err != nil {
		return nil, err
	}
	lots, err := readLotStocks(ctx, q, sortedUnique(all), nil)
	if err != nil {
		return nil, err
	}
	rewritten := map[uuid.UUID]bool{}
	for _, id := range sortedUnique(all) {
		s, ok := stocks[id]
		if !ok {
			continue // a combo SKU holds no stock
		}
		if !rebuildSku(&s, sums, lots, today) {
			continue
		}
		s.UpdatedAt = now
		if err := writeSkuStock(ctx, q, s); err != nil {
			return nil, err
		}
		rewritten[id] = true
	}
	var out []posting.Rebuilt
	for _, name := range skuStockChecks {
		var n int64
		for _, id := range flagged[name] {
			if rewritten[id] {
				n++
			}
		}
		if n > 0 {
			out = append(out, posting.Rebuilt{Check: name, Rows: n})
		}
	}
	return out, nil
}

// ofTenant keeps the rows of a check over SKUs of the tenant in scope, on top of row level
// security.
const ofTenant = ` WHERE c.id IN (SELECT id FROM skus WHERE tenant_id = ` + posting.InTenant + `)`

// readSkuCheck returns the SKUs the named check flags, recording the ledger sums of the checks
// that compare a stored quantity with one.
func readSkuCheck(ctx context.Context, q db.Querier, name string, sums *ledgerSums) ([]uuid.UUID, error) {
	query, err := checkQuery(name)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	switch name {
	case "sku on hand", "sku reserved":
		var rows []struct {
			ID       uuid.UUID `db:"id"`
			Expected money.Qty `db:"expected"`
		}
		if err := sqlx.SelectContext(ctx, q, &rows, `SELECT id, expected::numeric AS expected FROM (`+query+`) c`+ofTenant); err != nil {
			return nil, fmt.Errorf("inventory: rebuild read %s: %w", name, err)
		}
		into := sums.onHand
		if name == "sku reserved" {
			into = sums.reserved
		}
		for _, r := range rows {
			into[r.ID] = r.Expected
			ids = append(ids, r.ID)
		}
	case "sku stock value":
		var rows []struct {
			ID       uuid.UUID   `db:"id"`
			Expected money.Money `db:"expected"`
		}
		if err := sqlx.SelectContext(ctx, q, &rows, `SELECT id, expected::numeric AS expected FROM (`+query+`) c`+ofTenant); err != nil {
			return nil, fmt.Errorf("inventory: rebuild read %s: %w", name, err)
		}
		for _, r := range rows {
			sums.value[r.ID] = r.Expected
			ids = append(ids, r.ID)
		}
	default:
		if err := sqlx.SelectContext(ctx, q, &ids, `SELECT id FROM (`+query+`) c`+ofTenant); err != nil {
			return nil, fmt.Errorf("inventory: rebuild read %s: %w", name, err)
		}
	}
	return ids, nil
}

// rebuildSku sets s to its ledger sums and recomputes the rest; it reports false when the ledger
// gives the row no valid value.
func rebuildSku(s *SkuStock, sums ledgerSums, lots []LotStock, today time.Time) bool {
	if v, ok := sums.onHand[s.SkuID]; ok {
		s.OnHand = v
	}
	if v, ok := sums.value[s.SkuID]; ok {
		s.StockValue = v
	}
	if v, ok := sums.reserved[s.SkuID]; ok {
		s.Reserved = v
	}
	if s.OnHand.IsNegative() || s.Reserved.IsNegative() || s.StockValue.IsNegative() {
		return false
	}
	if err := recompute(s, lots, today); err != nil {
		return false
	}
	avg, err := (MovingAverageCosting{}).Average(*s)
	if err != nil {
		return false
	}
	s.AvgCost = avg
	return true
}
