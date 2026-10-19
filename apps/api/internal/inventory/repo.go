package inventory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// Column lists shared by the readers below.
const (
	lotColumns = `l.id, l.warehouse_id, l.sku_id, l.lot_no, l.expiry_date, l.color_id, l.is_quarantine,
		l.source_doc_type, l.source_doc_line_id, l.received_on, l.created_at`
	moveColumns = `id, warehouse_id, sku_id, lot_id, color_id, qty, unit_cost, value_amount, move_type, doc_type, doc_id,
		doc_line_id, business_date, posted_at, posted_by, reversal_of, created_at`
	skuStockColumns = `warehouse_id, sku_id, on_hand, quarantine_qty, expired_qty, sellable, reserved, avg_cost, stock_value,
		last_move_at, updated_at, version`
)

// prefixed qualifies every column of a column list with a table alias.
func prefixed(alias, columns string) string {
	cols := strings.Split(columns, ",")
	for i, c := range cols {
		cols[i] = alias + "." + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}

// warehouseID returns the single warehouse of the tenant in scope.
func warehouseID(ctx context.Context, q db.Querier) (uuid.UUID, error) {
	var id uuid.UUID
	err := q.QueryRowxContext(ctx, `SELECT id FROM warehouses ORDER BY created_at LIMIT 1`).Scan(&id)
	if db.IsNoRows(err) {
		return uuid.Nil, fmt.Errorf("inventory: the tenant has no warehouse")
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("inventory: read warehouse: %w", err)
	}
	return id, nil
}

// tenantSettings are the inventory settings of the tenant.
type tenantSettings struct {
	BlockExpiredLotSale bool `db:"block_expired_lot_sale"`
	ExpiryWarningDays   int  `db:"expiry_warning_days"`
}

func readSettings(ctx context.Context, q db.Querier) (tenantSettings, error) {
	s := tenantSettings{BlockExpiredLotSale: true, ExpiryWarningDays: 30}
	err := q.QueryRowxContext(ctx, `SELECT block_expired_lot_sale, expiry_warning_days FROM tenant_settings LIMIT 1`).StructScan(&s)
	if err != nil && !db.IsNoRows(err) {
		return s, fmt.Errorf("inventory: read settings: %w", err)
	}
	return s, nil
}

// tenantLocation is the time zone of the tenant (tenants is readable by the application role).
func tenantLocation(ctx context.Context, q db.Querier, tenantID uuid.UUID) (*time.Location, error) {
	var name string
	if err := q.QueryRowxContext(ctx, `SELECT timezone FROM tenants WHERE id = $1`, tenantID).Scan(&name); err != nil {
		return nil, fmt.Errorf("inventory: read tenant time zone: %w", err)
	}
	return clock.LoadLocation(name)
}

// tenantToday is the business date of now in the tenant's time zone.
func tenantToday(ctx context.Context, q db.Querier, tenantID uuid.UUID, now time.Time) (time.Time, error) {
	loc, err := tenantLocation(ctx, q, tenantID)
	if err != nil {
		return time.Time{}, err
	}
	return clock.BusinessDate(now, loc), nil
}

// readSkuInfos reads the catalog facts the ledger needs, keyed by SKU id.
func readSkuInfos(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID]skuInfo, error) {
	var rows []skuInfo
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT s.id, s.code, s.display_name, s.variant_label, s.base_unit_id, u.name AS base_unit_name, s.track_lot,
		  s.track_expiry, s.min_stock, s.max_stock, s.qty_scale, s.status, s.spec_text, p.category_id, p.type AS product_type
		FROM skus s JOIN products p ON p.id = s.product_id JOIN units u ON u.id = s.base_unit_id
		WHERE s.id = ANY($1)`, skuIDs); err != nil {
		return nil, fmt.Errorf("inventory: read skus: %w", err)
	}
	out := make(map[uuid.UUID]skuInfo, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// ensureSkuStock creates the missing sku_stock rows of skuIDs (combo SKUs hold no stock and
// get none). It runs before the rows are locked, so a first posting has a row to lock.
func ensureSkuStock(ctx context.Context, q db.Querier, skuIDs []uuid.UUID, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO sku_stock (tenant_id, warehouse_id, sku_id, updated_at)
		SELECT w.tenant_id, w.id, s.id, $2
		FROM skus s JOIN products p ON p.id = s.product_id CROSS JOIN warehouses w
		WHERE s.id = ANY($1) AND p.type <> 'combo'
		ORDER BY s.id
		ON CONFLICT ON CONSTRAINT pk_sku_stock DO NOTHING`, skuIDs, now); err != nil {
		return fmt.Errorf("inventory: create stock rows: %w", err)
	}
	return nil
}

// lockSkuStock creates and locks the sku_stock rows of skuIDs in ascending id order (the global
// lock order) and returns them by SKU.
func lockSkuStock(ctx context.Context, q db.Querier, skuIDs []uuid.UUID, now time.Time) (map[uuid.UUID]SkuStock, error) {
	ids := sortedUnique(skuIDs)
	if len(ids) == 0 {
		return map[uuid.UUID]SkuStock{}, nil
	}
	if err := ensureSkuStock(ctx, q, ids, now); err != nil {
		return nil, err
	}
	var rows []SkuStock
	if err := sqlx.SelectContext(ctx, q, &rows,
		`SELECT `+skuStockColumns+` FROM sku_stock WHERE sku_id = ANY($1) ORDER BY sku_id FOR UPDATE`, ids); err != nil {
		return nil, fmt.Errorf("inventory: lock stock rows: %w", err)
	}
	out := make(map[uuid.UUID]SkuStock, len(rows))
	for _, r := range rows {
		out[r.SkuID] = r
	}
	return out, nil
}

// readSkuStock reads sku_stock rows without locking.
func readSkuStock(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID]SkuStock, error) {
	var rows []SkuStock
	if err := sqlx.SelectContext(ctx, q, &rows, `SELECT `+skuStockColumns+` FROM sku_stock WHERE sku_id = ANY($1)`, skuIDs); err != nil {
		return nil, fmt.Errorf("inventory: read stock rows: %w", err)
	}
	out := make(map[uuid.UUID]SkuStock, len(rows))
	for _, r := range rows {
		out[r.SkuID] = r
	}
	return out, nil
}

// writeSkuStock stores the recomputed projection of one SKU.
func writeSkuStock(ctx context.Context, q db.Querier, s SkuStock) error {
	res, err := q.ExecContext(ctx, `
		UPDATE sku_stock SET on_hand = $2, quarantine_qty = $3, expired_qty = $4, sellable = $5, reserved = $6,
		  avg_cost = $7, stock_value = $8, last_move_at = $9, updated_at = $10, version = version + 1
		WHERE sku_id = $1`,
		s.SkuID, s.OnHand, s.QuarantineQty, s.ExpiredQty, s.Sellable, s.Reserved, s.AvgCost, s.StockValue, s.LastMoveAt, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("inventory: update stock row: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("inventory: stock row of sku %s missing", s.SkuID)
	}
	return nil
}

// readLotStocks returns the lots of skuIDs that hold stock, plus the lots named in extra, with
// their balances (FEFO order: expiry, received_on, lot id).
func readLotStocks(ctx context.Context, q db.Querier, skuIDs, extra []uuid.UUID) ([]LotStock, error) {
	if extra == nil {
		extra = []uuid.UUID{}
	}
	var rows []LotStock
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT `+lotColumns+`, COALESCE(b.qty, 0) AS qty
		FROM lots l LEFT JOIN lot_balances b ON b.lot_id = l.id
		WHERE (l.sku_id = ANY($1) AND b.qty > 0) OR l.id = ANY($2)
		ORDER BY l.expiry_date NULLS LAST, l.received_on, l.id`, skuIDs, extra); err != nil {
		return nil, fmt.Errorf("inventory: read lots: %w", err)
	}
	return rows, nil
}

// readLot returns one lot with its balance.
func readLot(ctx context.Context, q db.Querier, id uuid.UUID) (LotStock, error) {
	var l LotStock
	err := q.QueryRowxContext(ctx, `SELECT `+lotColumns+`, COALESCE(b.qty, 0) AS qty
		FROM lots l LEFT JOIN lot_balances b ON b.lot_id = l.id WHERE l.id = $1`, id).StructScan(&l)
	if db.IsNoRows(err) {
		return LotStock{}, errNotFound("Lot")
	}
	if err != nil {
		return LotStock{}, fmt.Errorf("inventory: read lot: %w", err)
	}
	return l, nil
}

// lotKey is the identity of a lot (ux_lots__identity).
type lotKey struct {
	WarehouseID  uuid.UUID
	SkuID        uuid.UUID
	LotNo        string
	ExpiryDate   *time.Time
	ColorID      *uuid.UUID
	IsQuarantine bool
}

// LotFactory finds or creates lots by their identity tuple. Creation is race-safe: the insert
// skips an existing identity and the select then reads whichever row won.
type LotFactory struct{}

// FindOrCreate returns the lot of k, creating it with source and received date when new.
func (LotFactory) FindOrCreate(ctx context.Context, q db.Querier, k lotKey, receivedOn, now time.Time,
	sourceDocType *string, sourceLineID *uuid.UUID) (Lot, error) {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO lots (id, tenant_id, warehouse_id, sku_id, lot_no, expiry_date, color_id, is_quarantine,
		  source_doc_type, source_doc_line_id, received_on, created_at)
		SELECT $1, w.tenant_id, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11 FROM warehouses w WHERE w.id = $2
		ON CONFLICT ON CONSTRAINT ux_lots__identity DO NOTHING`,
		ids.New(), k.WarehouseID, k.SkuID, k.LotNo, k.ExpiryDate, k.ColorID, k.IsQuarantine, sourceDocType, sourceLineID,
		receivedOn, now); err != nil {
		return Lot{}, fmt.Errorf("inventory: create lot: %w", err)
	}
	var l Lot
	if err := q.QueryRowxContext(ctx, `SELECT `+lotColumns+` FROM lots l
		WHERE l.warehouse_id = $1 AND l.sku_id = $2 AND l.lot_no = $3 AND l.expiry_date IS NOT DISTINCT FROM $4
		  AND l.color_id IS NOT DISTINCT FROM $5 AND l.is_quarantine = $6`,
		k.WarehouseID, k.SkuID, k.LotNo, k.ExpiryDate, k.ColorID, k.IsQuarantine).StructScan(&l); err != nil {
		return Lot{}, fmt.Errorf("inventory: read lot: %w", err)
	}
	return l, nil
}

// Twin returns the lot with the same identity on the other side of quarantine.
func (f LotFactory) Twin(ctx context.Context, q db.Querier, l Lot, now time.Time) (Lot, error) {
	return f.FindOrCreate(ctx, q, lotKey{WarehouseID: l.WarehouseID, SkuID: l.SkuID, LotNo: l.LotNo, ExpiryDate: l.ExpiryDate,
		ColorID: l.ColorID, IsQuarantine: !l.IsQuarantine}, l.ReceivedOn, now, l.SourceDocType, l.SourceDocLineID)
}

// insertMoves appends ledger rows.
func insertMoves(ctx context.Context, q db.Querier, tenantID uuid.UUID, moves []StockMove) error {
	for _, m := range moves {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO stock_moves (id, tenant_id, warehouse_id, sku_id, lot_id, color_id, qty, unit_cost, value_amount, move_type,
			  doc_type, doc_id, doc_line_id, business_date, posted_at, posted_by, reversal_of, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
			m.ID, tenantID, m.WarehouseID, m.SkuID, m.LotID, m.ColorID, m.Qty, m.UnitCost, m.Value, m.MoveType,
			m.DocType, m.DocID, m.DocLineID, m.BusinessDate, m.PostedAt, m.PostedBy, m.ReversalOf, m.CreatedAt); err != nil {
			if db.IsUniqueViolation(err, "ux_stock_moves__one_reversal") {
				return errAlreadyReversed()
			}
			return fmt.Errorf("inventory: insert stock move: %w", err)
		}
	}
	return nil
}

// upsertLotBalance stores the balance of a lot.
func upsertLotBalance(ctx context.Context, q db.Querier, l LotStock, now time.Time) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO lot_balances (tenant_id, lot_id, sku_id, color_id, qty, updated_at)
		SELECT tenant_id, id, sku_id, color_id, $2, $3 FROM lots WHERE id = $1
		ON CONFLICT ON CONSTRAINT pk_lot_balances DO UPDATE SET qty = EXCLUDED.qty, updated_at = EXCLUDED.updated_at`,
		l.ID, l.Qty, now); err != nil {
		return fmt.Errorf("inventory: update lot balance: %w", err)
	}
	return nil
}

// movesOfDoc returns the moves a document posted, in posting order.
func movesOfDoc(ctx context.Context, q db.Querier, docType string, docID uuid.UUID) ([]StockMove, error) {
	var rows []StockMove
	if err := sqlx.SelectContext(ctx, q, &rows, `SELECT `+moveColumns+` FROM stock_moves
		WHERE doc_type = $1 AND doc_id = $2 ORDER BY created_at, id`, docType, docID); err != nil {
		return nil, fmt.Errorf("inventory: read document moves: %w", err)
	}
	return rows, nil
}

// readMove returns one move.
func readMove(ctx context.Context, q db.Querier, id uuid.UUID) (StockMove, error) {
	var m StockMove
	err := q.QueryRowxContext(ctx, `SELECT `+moveColumns+` FROM stock_moves WHERE id = $1`, id).StructScan(&m)
	if db.IsNoRows(err) {
		return StockMove{}, errNotFound("Stock move")
	}
	if err != nil {
		return StockMove{}, fmt.Errorf("inventory: read stock move: %w", err)
	}
	return m, nil
}

// colorCodes maps color ids to their codes.
func colorCodes(ctx context.Context, q db.Querier, colorIDs []uuid.UUID) (map[uuid.UUID]string, error) {
	var rows []struct {
		ID   uuid.UUID `db:"id"`
		Code string    `db:"code"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `SELECT id, code FROM colors WHERE id = ANY($1)`, colorIDs); err != nil {
		return nil, fmt.Errorf("inventory: read colors: %w", err)
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, r := range rows {
		out[r.ID] = r.Code
	}
	return out, nil
}

// sortedUnique returns ids ascending (PostgreSQL uuid order) without duplicates or nil ids.
func sortedUnique(in []uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(in))
	for _, id := range in {
		if id != uuid.Nil {
			out = append(out, id)
		}
	}
	slices.SortFunc(out, compareUUID)
	return slices.Compact(out)
}

func compareUUID(a, b uuid.UUID) int {
	for i := range a {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}
