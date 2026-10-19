package inventory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// maxSearchLen bounds the stock list search term.
const maxSearchLen = 200

// ColorStock is the stock of one color of a SKU (a chip and child row of the stock list); a nil
// ColorID is "Chưa gán màu". Value is the color's share at the SKU's average cost.
type ColorStock struct {
	SkuID      uuid.UUID  `db:"sku_id"`
	ColorID    *uuid.UUID `db:"color_id"`
	ColorCode  *string    `db:"color_code"`
	ColorName  *string    `db:"color_name"`
	ColorHex   *string    `db:"color_hex"`
	OnHand     money.Qty  `db:"on_hand"`
	Sellable   money.Qty  `db:"sellable"`
	Quarantine money.Qty  `db:"quarantine"`
	Expired    money.Qty  `db:"expired"`
	Value      money.Money
}

// StockRow is one SKU of the stock list with its projection, its price and its colors.
type StockRow struct {
	SkuID        uuid.UUID   `db:"sku_id"`
	Code         string      `db:"code"`
	DisplayName  string      `db:"display_name"`
	VariantLabel string      `db:"variant_label"`
	UnitName     string      `db:"unit_name"`
	CategoryID   *uuid.UUID  `db:"category_id"`
	Status       string      `db:"status"`
	TrackLot     bool        `db:"track_lot"`
	TrackExpiry  bool        `db:"track_expiry"`
	MinStock     money.Qty   `db:"min_stock"`
	MaxStock     *money.Qty  `db:"max_stock"`
	OnHand       money.Qty   `db:"on_hand"`
	Sellable     money.Qty   `db:"sellable"`
	Reserved     money.Qty   `db:"reserved"`
	Quarantine   money.Qty   `db:"quarantine_qty"`
	Expired      money.Qty   `db:"expired_qty"`
	AvgCost      money.Money `db:"avg_cost"`
	StockValue   money.Money `db:"stock_value"`
	Price        *money.Money
	Colors       []ColorStock
}

// Available is sellable minus reserved.
func (r StockRow) Available() money.Qty { return r.Sellable.Sub(r.Reserved) }

// Low reports a SKU below its minimum (the low-stock signal's rule).
func (r StockRow) Low() bool {
	return r.MinStock.IsPositive() && r.Available().Cmp(r.MinStock) < 0
}

// Over reports a SKU above its maximum.
func (r StockRow) Over() bool { return r.MaxStock != nil && r.OnHand.Cmp(*r.MaxStock) > 0 }

// StockFilter narrows the stock list (by SKU code). ColorID keeps the SKUs holding stock of that
// color, ColoredOnly those holding stock of any color and Uncolored those holding stock without a
// color; Uncolored excludes the other two.
type StockFilter struct {
	Q           string
	CategoryID  *uuid.UUID
	ColorID     *uuid.UUID
	ColoredOnly bool
	Uncolored   bool
	BelowMin    bool
	AfterCode   *string
	AfterID     uuid.UUID
	Limit       int
}

const stockRowSelect = `
	SELECT s.id AS sku_id, s.code, s.display_name, s.variant_label, u.name AS unit_name, p.category_id, s.status, s.track_lot,
	  s.track_expiry, s.min_stock, s.max_stock, COALESCE(ss.on_hand, 0) AS on_hand, COALESCE(ss.sellable, 0) AS sellable,
	  COALESCE(ss.reserved, 0) AS reserved, COALESCE(ss.quarantine_qty, 0) AS quarantine_qty,
	  COALESCE(ss.expired_qty, 0) AS expired_qty, COALESCE(ss.avg_cost, 0) AS avg_cost, COALESCE(ss.stock_value, 0) AS stock_value
	FROM skus s JOIN products p ON p.id = s.product_id JOIN units u ON u.id = s.base_unit_id
	LEFT JOIN sku_stock ss ON ss.sku_id = s.id`

// Stock lists the stock of the SKUs holding stock or in use (combos hold none), one row per SKU
// with its colors.
func (s *Service) Stock(ctx context.Context, scope Scope, f StockFilter) ([]StockRow, error) {
	if len(f.Q) > maxSearchLen {
		return nil, errInvalid(map[string]string{"q": "too_long"})
	}
	switch {
	case f.Uncolored && f.ColorID != nil:
		return nil, errInvalid(map[string]string{"color_id": "conflicts_with_uncolored"})
	case f.Uncolored && f.ColoredOnly:
		return nil, errInvalid(map[string]string{"colored_only": "conflicts_with_uncolored"})
	}
	words := strings.Fields(textnorm.SearchNorm(f.Q))
	patterns := make([]string, len(words))
	for i, w := range words {
		patterns[i] = "%" + w + "%"
	}
	afterCode := ""
	if f.AfterCode != nil {
		afterCode = *f.AfterCode
	}
	today, err := s.today(ctx, scope.TenantID)
	if err != nil {
		return nil, err
	}
	var out []StockRow
	err = s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, `
			WITH RECURSIVE subtree AS (
			  SELECT id FROM categories WHERE id = $2::uuid
			  UNION ALL
			  SELECT c.id FROM categories c JOIN subtree t ON c.parent_id = t.id
			)`+stockRowSelect+`
			WHERE p.type <> 'combo' AND (s.status = 'active' OR COALESCE(ss.on_hand, 0) > 0)
			  AND s.search_norm LIKE ALL ($1::text[])
			  AND ($2::uuid IS NULL OR p.category_id IN (SELECT id FROM subtree))
			  AND ($3::uuid IS NULL OR EXISTS (SELECT 1 FROM lot_balances b WHERE b.sku_id = s.id AND b.color_id = $3 AND b.qty > 0))
			  AND (NOT $4 OR EXISTS (SELECT 1 FROM lot_balances b WHERE b.sku_id = s.id AND b.color_id IS NOT NULL AND b.qty > 0))
			  AND (NOT $10 OR EXISTS (SELECT 1 FROM lot_balances b WHERE b.sku_id = s.id AND b.color_id IS NULL AND b.qty > 0))
			  AND (NOT $5 OR (s.min_stock > 0 AND COALESCE(ss.sellable, 0) - COALESCE(ss.reserved, 0) < s.min_stock))
			  AND (NOT $6 OR (s.code, s.id) > ($7, $8::uuid))
			ORDER BY s.code, s.id
			LIMIT $9`, patterns, f.CategoryID, f.ColorID, f.ColoredOnly, f.BelowMin, f.AfterCode != nil, afterCode,
			f.AfterID, f.Limit, f.Uncolored); err != nil {
			return fmt.Errorf("inventory: list stock: %w", err)
		}
		return s.decorate(ctx, q, out, today)
	})
	return out, err
}

// decorate adds the colors and the prices to stock rows.
func (s *Service) decorate(ctx context.Context, q db.Querier, rows []StockRow, today time.Time) error {
	if len(rows) == 0 {
		return nil
	}
	skuIDs := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		skuIDs[i] = r.SkuID
	}
	colors, err := colorStocks(ctx, q, skuIDs, today)
	if err != nil {
		return err
	}
	var prices map[uuid.UUID]money.Money
	if s.prices != nil {
		if prices, err = s.prices.PriceHints(ctx, q, skuIDs); err != nil {
			return err
		}
	}
	for i := range rows {
		r := &rows[i]
		r.Colors = []ColorStock{}
		for _, c := range colors[r.SkuID] {
			c.Value = r.AvgCost.MulQty(c.OnHand)
			r.Colors = append(r.Colors, c)
		}
		if p, ok := prices[r.SkuID]; ok {
			r.Price = &p
		}
	}
	return nil
}

// colorStocks groups the lot balances of skuIDs by color in one query.
func colorStocks(ctx context.Context, q db.Querier, skuIDs []uuid.UUID, today time.Time) (map[uuid.UUID][]ColorStock, error) {
	var rows []ColorStock
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT b.sku_id, b.color_id, c.code AS color_code, c.name AS color_name, c.hex AS color_hex, SUM(b.qty) AS on_hand,
		  COALESCE(SUM(b.qty) FILTER (WHERE NOT l.is_quarantine AND (l.expiry_date IS NULL OR l.expiry_date >= $2)), 0) AS sellable,
		  COALESCE(SUM(b.qty) FILTER (WHERE l.is_quarantine), 0) AS quarantine,
		  COALESCE(SUM(b.qty) FILTER (WHERE NOT l.is_quarantine AND l.expiry_date < $2), 0) AS expired
		FROM lot_balances b JOIN lots l ON l.id = b.lot_id LEFT JOIN colors c ON c.id = b.color_id
		WHERE b.sku_id = ANY($1) AND b.qty > 0
		GROUP BY b.sku_id, b.color_id, c.code, c.name, c.hex, c.sort_order
		ORDER BY b.sku_id, c.sort_order NULLS LAST, c.code NULLS LAST`, skuIDs, today); err != nil {
		return nil, fmt.Errorf("inventory: read stock by color: %w", err)
	}
	out := map[uuid.UUID][]ColorStock{}
	for _, r := range rows {
		out[r.SkuID] = append(out[r.SkuID], r)
	}
	return out, nil
}

// StockDetail is one SKU's stock with its colors and lots, and the day and warning window the
// lot states are computed on.
type StockDetail struct {
	StockRow
	Lots     []LotView
	Today    time.Time
	WarnDays int
}

// StockOf returns the stock of one SKU broken down by color and lot.
func (s *Service) StockOf(ctx context.Context, scope Scope, skuID uuid.UUID) (StockDetail, error) {
	today, err := s.today(ctx, scope.TenantID)
	if err != nil {
		return StockDetail{}, err
	}
	var out StockDetail
	err = s.read(ctx, scope, func(q db.Querier) error {
		rows := []StockRow{}
		if err := sqlx.SelectContext(ctx, q, &rows, stockRowSelect+` WHERE s.id = $1`, skuID); err != nil {
			return fmt.Errorf("inventory: read stock: %w", err)
		}
		if len(rows) == 0 {
			return errNotFound("SKU")
		}
		if err := s.decorate(ctx, q, rows, today); err != nil {
			return err
		}
		settings, err := readSettings(ctx, q)
		if err != nil {
			return err
		}
		out.StockRow, out.Today, out.WarnDays = rows[0], today, settings.ExpiryWarningDays
		out.Lots = []LotView{}
		if err := sqlx.SelectContext(ctx, q, &out.Lots, lotViewSelect+` WHERE l.sku_id = $1 AND b.qty > 0 ORDER BY `+lotOrder,
			skuID); err != nil {
			return fmt.Errorf("inventory: read lots: %w", err)
		}
		return nil
	})
	return out, err
}

// LotView is a lot with its balance and what a list shows of its SKU and color.
type LotView struct {
	LotStock
	SkuCode      string  `db:"sku_code"`
	SkuName      string  `db:"sku_name"`
	VariantLabel string  `db:"variant_label"`
	UnitName     string  `db:"unit_name"`
	ProductType  string  `db:"product_type"`
	ColorCode    *string `db:"color_code"`
	ColorName    *string `db:"color_name"`
	ColorHex     *string `db:"color_hex"`
}

// Lot states of the lot list.
const (
	LotStateAll        = "all"
	LotStateNormal     = "normal"
	LotStateExpiring   = "expiring"
	LotStateExpired    = "expired"
	LotStateQuarantine = "quarantine"
)

// StateOn is the state of the lot on day with a warning window of warnDays.
func (l LotView) StateOn(day time.Time, warnDays int) string {
	switch {
	case l.IsQuarantine:
		return LotStateQuarantine
	case l.Expired(day):
		return LotStateExpired
	case l.ExpiryDate != nil && !l.ExpiryDate.After(day.AddDate(0, 0, warnDays)):
		return LotStateExpiring
	}
	return LotStateNormal
}

// noExpiry orders lots without an expiry after every dated lot.
const noExpiry = "DATE '9999-12-31'"

const lotOrder = `COALESCE(l.expiry_date, ` + noExpiry + `), l.received_on, l.id`

const lotViewSelect = `
	SELECT ` + lotColumns + `, COALESCE(b.qty, 0) AS qty, s.code AS sku_code, s.display_name AS sku_name, s.variant_label,
	  u.name AS unit_name, p.type AS product_type, c.code AS color_code, c.name AS color_name, c.hex AS color_hex
	FROM lots l LEFT JOIN lot_balances b ON b.lot_id = l.id JOIN skus s ON s.id = l.sku_id
	JOIN products p ON p.id = s.product_id JOIN units u ON u.id = s.base_unit_id LEFT JOIN colors c ON c.id = l.color_id`

// LotFilter narrows the lot list (FEFO order). ExpiringWithin is the warning window in days;
// nil uses the tenant's setting. AfterExpiry and AfterReceived carry the cursor.
type LotFilter struct {
	SkuID          *uuid.UUID
	State          string
	KitOnly        bool
	ExpiringWithin *int
	AfterExpiry    *time.Time
	AfterReceived  time.Time
	AfterID        uuid.UUID
	Limit          int
}

// LotList is a page of lots with the day and window their states were computed on.
type LotList struct {
	Lots     []LotView
	Today    time.Time
	WarnDays int
}

// Lots lists the lots holding stock.
func (s *Service) Lots(ctx context.Context, scope Scope, f LotFilter) (LotList, error) {
	if f.State == "" {
		f.State = LotStateAll
	}
	if !slices.Contains([]string{LotStateAll, LotStateExpiring, LotStateExpired, LotStateQuarantine}, f.State) {
		return LotList{}, errInvalid(map[string]string{"state": "invalid"})
	}
	if f.ExpiringWithin != nil && (*f.ExpiringWithin < 0 || *f.ExpiringWithin > 3650) {
		return LotList{}, errInvalid(map[string]string{"expiring_within": "out_of_range"})
	}
	today, err := s.today(ctx, scope.TenantID)
	if err != nil {
		return LotList{}, err
	}
	out := LotList{Lots: []LotView{}, Today: today}
	err = s.read(ctx, scope, func(q db.Querier) error {
		settings, err := readSettings(ctx, q)
		if err != nil {
			return err
		}
		out.WarnDays = settings.ExpiryWarningDays
		if f.ExpiringWithin != nil {
			out.WarnDays = *f.ExpiringWithin
		}
		var afterExpiry time.Time
		if f.AfterExpiry != nil {
			afterExpiry = *f.AfterExpiry
		}
		if err := sqlx.SelectContext(ctx, q, &out.Lots, lotViewSelect+`
			WHERE b.qty > 0 AND ($1::uuid IS NULL OR l.sku_id = $1) AND (NOT $2 OR p.type = 'kit')
			  AND CASE $3::text
			    WHEN 'quarantine' THEN l.is_quarantine
			    WHEN 'expired' THEN NOT l.is_quarantine AND l.expiry_date < $4
			    WHEN 'expiring' THEN NOT l.is_quarantine AND l.expiry_date >= $4 AND l.expiry_date <= $5
			    ELSE true END
			  AND (NOT $6 OR (COALESCE(l.expiry_date, `+noExpiry+`), l.received_on, l.id) > ($7::date, $8::date, $9::uuid))
			ORDER BY `+lotOrder+` LIMIT $10`,
			f.SkuID, f.KitOnly, f.State, today, today.AddDate(0, 0, out.WarnDays), f.AfterExpiry != nil, afterExpiry,
			f.AfterReceived, f.AfterID, f.Limit); err != nil {
			return fmt.Errorf("inventory: list lots: %w", err)
		}
		return nil
	})
	return out, err
}

// TraceMove is a move of a lot trace or a stock card with the number of its document.
type TraceMove struct {
	StockMove
	LotNo        string     `db:"lot_no"`
	IsQuarantine bool       `db:"is_quarantine"`
	ExpiryDate   *time.Time `db:"expiry_date"`
	DocNo        *string
}

// LotTrace is a lot and every move of it and of its quarantine twin, with the day and warning
// window the lot state is computed on.
type LotTrace struct {
	Lot      LotView
	Moves    []TraceMove
	Today    time.Time
	WarnDays int
}

const traceMoveSelect = `SELECT ` + `m.id, m.warehouse_id, m.sku_id, m.lot_id, m.color_id, m.qty, m.unit_cost, m.value_amount,
	m.move_type, m.doc_type, m.doc_id, m.doc_line_id, m.business_date, m.posted_at, m.posted_by, m.reversal_of, m.created_at,
	l.lot_no, l.is_quarantine, l.expiry_date
	FROM stock_moves m JOIN lots l ON l.id = m.lot_id`

// TraceLot returns the history of a lot (in posting order, at most limit moves after the
// cursor).
func (s *Service) TraceLot(ctx context.Context, scope Scope, id uuid.UUID, after *MoveCursor, limit int) (LotTrace, error) {
	today, err := s.today(ctx, scope.TenantID)
	if err != nil {
		return LotTrace{}, err
	}
	out := LotTrace{Today: today}
	err = s.read(ctx, scope, func(q db.Querier) error {
		settings, err := readSettings(ctx, q)
		if err != nil {
			return err
		}
		out.WarnDays = settings.ExpiryWarningDays
		var lots []LotView
		if err := sqlx.SelectContext(ctx, q, &lots, lotViewSelect+` WHERE l.id = $1`, id); err != nil {
			return fmt.Errorf("inventory: read lot: %w", err)
		}
		if len(lots) == 0 {
			return errNotFound("Lot")
		}
		out.Lot = lots[0]
		l := lots[0].Lot
		c := after.orZero()
		out.Moves = []TraceMove{}
		if err := sqlx.SelectContext(ctx, q, &out.Moves, traceMoveSelect+`
			WHERE m.lot_id IN (SELECT t.id FROM lots t WHERE t.warehouse_id = $1 AND t.sku_id = $2 AND t.lot_no = $3
			    AND t.expiry_date IS NOT DISTINCT FROM $4::date AND t.color_id IS NOT DISTINCT FROM $5::uuid)
			  AND (NOT $6 OR (m.business_date, m.posted_at, m.id) > ($7::date, $8::timestamptz, $9::uuid))
			ORDER BY m.business_date, m.posted_at, m.id LIMIT $10`,
			l.WarehouseID, l.SkuID, l.LotNo, l.ExpiryDate, l.ColorID, after != nil, c.BusinessDate, c.PostedAt, c.ID,
			limit); err != nil {
			return fmt.Errorf("inventory: trace lot: %w", err)
		}
		return fillDocNos(ctx, q, out.Moves)
	})
	return out, err
}

// MoveCursor is the position of a move in posting order.
type MoveCursor struct {
	BusinessDate time.Time
	PostedAt     time.Time
	ID           uuid.UUID
}

func (c *MoveCursor) orZero() MoveCursor {
	if c == nil {
		return MoveCursor{}
	}
	return *c
}

// docNoQueries read the numbers of the documents that post stock, by stock_moves.doc_type.
var docNoQueries = map[string]string{
	"invoice":                   `SELECT id, doc_no FROM invoices WHERE id = ANY($1)`,
	"sales_document":            `SELECT id, doc_no FROM sales_documents WHERE id = ANY($1)`,
	"stock_out":                 `SELECT id, doc_no FROM stock_outs WHERE id = ANY($1)`,
	"sales_return":              `SELECT id, doc_no FROM sales_returns WHERE id = ANY($1)`,
	"exchange":                  `SELECT id, doc_no FROM exchanges WHERE id = ANY($1)`,
	"receipt":                   `SELECT id, doc_no FROM receipts WHERE id = ANY($1)`,
	"purchase_return":           `SELECT id, doc_no FROM purchase_returns WHERE id = ANY($1)`,
	"purchase_price_adjustment": `SELECT id, doc_no FROM purchase_price_adjustments WHERE id = ANY($1)`,
	"assembly_order":            `SELECT id, doc_no FROM assembly_orders WHERE id = ANY($1)`,
	// An assembly batch books its moves under its posting and is numbered by its order (LD).
	"assembly_posting": `SELECT p.id, o.doc_no FROM assembly_postings p JOIN assembly_orders o ON o.id = p.assembly_order_id
		WHERE p.id = ANY($1)`,
	"disassembly":      `SELECT id, doc_no FROM disassemblies WHERE id = ANY($1)`,
	DocStockAdjustment: `SELECT id, doc_no FROM stock_adjustments WHERE id = ANY($1)`,
}

// fillDocNos sets the document number of each move whose document type has one.
func fillDocNos(ctx context.Context, q db.Querier, moves []TraceMove) error {
	byType := map[string][]uuid.UUID{}
	for _, m := range moves {
		byType[m.DocType] = append(byType[m.DocType], m.DocID)
	}
	numbers := map[uuid.UUID]string{}
	for docType, docIDs := range byType {
		query, ok := docNoQueries[docType]
		if !ok {
			continue
		}
		var rows []struct {
			ID    uuid.UUID `db:"id"`
			DocNo string    `db:"doc_no"`
		}
		if err := sqlx.SelectContext(ctx, q, &rows, query, sortedUnique(docIDs)); err != nil {
			return fmt.Errorf("inventory: read %s numbers: %w", docType, err)
		}
		for _, r := range rows {
			numbers[r.ID] = r.DocNo
		}
	}
	for i := range moves {
		if no, ok := numbers[moves[i].DocID]; ok {
			moves[i].DocNo = &no
		}
	}
	return nil
}

// Unit modes of the stock card.
const (
	UnitModeAuto  = "auto"
	UnitModeFixed = "fixed"
)

// UnitDisplay writes a base-unit quantity for people.
type UnitDisplay interface {
	Format(q money.Qty) string
}

// cardUnit is a unit of a SKU with its factor to the base unit.
type cardUnit struct {
	ID     uuid.UUID       `db:"unit_id"`
	Name   string          `db:"name"`
	Factor decimal.Decimal `db:"factor"`
}

// AutoConvert writes a quantity in the SKU's larger units first ("Quy đổi tự động"): 180 cái
// with a bó of 10 and a thùng of 100 is "1 thùng 8 bó (180 cái)".
type AutoConvert struct {
	Base  string
	Units []cardUnit
}

// Format implements UnitDisplay.
func (a AutoConvert) Format(q money.Qty) string {
	sign, rest := "", q.Decimal()
	if rest.IsNegative() {
		sign, rest = "-", rest.Neg()
	}
	total := rest
	var parts []string
	for _, u := range a.Units {
		if u.Factor.Cmp(decimal.NewFromInt(1)) <= 0 {
			continue
		}
		n := rest.Div(u.Factor).Floor()
		if n.IsPositive() {
			parts = append(parts, n.String()+" "+u.Name)
			rest = rest.Sub(n.Mul(u.Factor))
		}
	}
	if len(parts) == 0 {
		return sign + total.String() + " " + a.Base
	}
	if !rest.IsZero() {
		parts = append(parts, rest.String()+" "+a.Base)
	}
	return sign + strings.Join(parts, " ") + " (" + sign + total.String() + " " + a.Base + ")"
}

// FixedUnit writes a quantity in one unit, rounded to the quantity scale.
type FixedUnit struct {
	Unit cardUnit
}

// Format implements UnitDisplay.
func (f FixedUnit) Format(q money.Qty) string {
	return q.Decimal().DivRound(f.Unit.Factor, money.QtyScale).String() + " " + f.Unit.Name
}

// CardFilter selects a stock card. Uncolored shows the stock without a color; From and To
// default to this month; the rows come in pages after the cursor.
type CardFilter struct {
	SkuID     uuid.UUID
	ColorID   *uuid.UUID
	Uncolored bool
	From      *time.Time
	To        *time.Time
	UnitMode  string
	UnitID    *uuid.UUID
	After     *MoveCursor
	Limit     int
}

// CardRow is a move of the stock card with the balance after it.
type CardRow struct {
	TraceMove
	In      money.Qty
	Out     money.Qty
	Balance money.Qty
}

// StockCard is the stock card of a SKU (optionally one color) over a date range: opening + in −
// out = closing. Reconciled is the "Đã khớp sổ và tồn" badge: the ledger verifier finds nothing
// on the SKU and, for a range ending today, the closing balance is the stock on hand.
type StockCard struct {
	SkuID        uuid.UUID
	SkuCode      string
	SkuName      string
	VariantLabel string
	BaseUnit     string
	ColorID      *uuid.UUID
	Uncolored    bool
	From         time.Time
	To           time.Time
	UnitMode     string
	Display      UnitDisplay
	Opening      money.Qty
	In           money.Qty
	Out          money.Qty
	Closing      money.Qty
	OnHand       money.Qty
	Reconciled   bool
	Rows         []CardRow
}

// cardMoves keeps the moves a stock card shows: quantity moves other than quarantine pairs
// (and their reversals), which move stock between twin lots of the same color and net to zero.
const cardMoves = `m.qty <> 0 AND m.move_type NOT IN ('quarantine_out', 'quarantine_in')
	AND NOT EXISTS (SELECT 1 FROM stock_moves r WHERE r.id = m.reversal_of AND r.move_type IN ('quarantine_out', 'quarantine_in'))`

// cardColor restricts moves to the card's color.
const cardColor = `m.sku_id = $1 AND ($2::uuid IS NULL OR m.color_id = $2) AND (NOT $3 OR m.color_id IS NULL)`

// StockCardOf builds a stock card.
func (s *Service) StockCardOf(ctx context.Context, scope Scope, f CardFilter) (StockCard, error) {
	if f.ColorID != nil && f.Uncolored {
		return StockCard{}, errInvalid(map[string]string{"color_id": "conflicts_with_uncolored"})
	}
	if f.UnitMode == "" {
		f.UnitMode = UnitModeAuto
	}
	switch {
	case f.UnitMode != UnitModeAuto && f.UnitMode != UnitModeFixed:
		return StockCard{}, errInvalid(map[string]string{"unit_mode": "invalid"})
	case f.UnitMode == UnitModeFixed && f.UnitID == nil:
		return StockCard{}, errInvalid(map[string]string{"unit_id": "required"})
	case f.UnitMode == UnitModeAuto && f.UnitID != nil:
		return StockCard{}, errInvalid(map[string]string{"unit_id": "not_allowed"})
	}
	today, err := s.today(ctx, scope.TenantID)
	if err != nil {
		return StockCard{}, err
	}
	card := StockCard{SkuID: f.SkuID, ColorID: f.ColorID, Uncolored: f.Uncolored, UnitMode: f.UnitMode,
		From: clock.Date(today.Year(), today.Month(), 1), To: today, Rows: []CardRow{}}
	if f.From != nil {
		card.From = *f.From
	}
	if f.To != nil {
		card.To = *f.To
	}
	if card.To.Before(card.From) {
		return StockCard{}, errInvalid(map[string]string{"to": "before_from"})
	}
	err = s.read(ctx, scope, func(q db.Querier) error {
		infos, err := readSkuInfos(ctx, q, []uuid.UUID{f.SkuID})
		if err != nil {
			return err
		}
		info, ok := infos[f.SkuID]
		if !ok {
			return errNotFound("SKU")
		}
		card.SkuCode, card.SkuName, card.VariantLabel, card.BaseUnit = info.Code, info.DisplayName, info.VariantLabel, info.BaseUnitName
		if card.Display, err = unitDisplay(ctx, q, info, f.UnitMode, f.UnitID); err != nil {
			return err
		}
		var totals struct {
			Opening money.Qty `db:"opening"`
			In      money.Qty `db:"qty_in"`
			Out     money.Qty `db:"qty_out"`
			OnHand  money.Qty `db:"on_hand"`
		}
		if err := q.QueryRowxContext(ctx, `
			SELECT
			  COALESCE((SELECT SUM(m.qty) FROM stock_moves m WHERE `+cardColor+` AND m.business_date < $4), 0) AS opening,
			  COALESCE(SUM(m.qty) FILTER (WHERE m.qty > 0), 0) AS qty_in,
			  COALESCE(-SUM(m.qty) FILTER (WHERE m.qty < 0), 0) AS qty_out,
			  COALESCE((SELECT SUM(b.qty) FROM lot_balances b WHERE b.sku_id = $1 AND ($2::uuid IS NULL OR b.color_id = $2)
			    AND (NOT $3 OR b.color_id IS NULL)), 0) AS on_hand
			FROM stock_moves m WHERE `+cardColor+` AND m.business_date BETWEEN $4 AND $5 AND `+cardMoves,
			f.SkuID, f.ColorID, f.Uncolored, card.From, card.To).StructScan(&totals); err != nil {
			return fmt.Errorf("inventory: stock card totals: %w", err)
		}
		card.Opening, card.In, card.Out, card.OnHand = totals.Opening, totals.In, totals.Out, totals.OnHand
		card.Closing = card.Opening.Add(card.In).Sub(card.Out)
		c := f.After.orZero()
		balance := card.Opening
		if f.After != nil {
			var before money.Qty
			if err := q.QueryRowxContext(ctx, `SELECT COALESCE(SUM(m.qty), 0) FROM stock_moves m WHERE `+cardColor+`
				AND m.business_date >= $4 AND (m.business_date, m.posted_at, m.id) <= ($5::date, $6::timestamptz, $7::uuid) AND `+cardMoves,
				f.SkuID, f.ColorID, f.Uncolored, card.From, c.BusinessDate, c.PostedAt, c.ID).Scan(&before); err != nil {
				return fmt.Errorf("inventory: stock card balance: %w", err)
			}
			balance = balance.Add(before)
		}
		var moves []TraceMove
		if err := sqlx.SelectContext(ctx, q, &moves, traceMoveSelect+` WHERE `+cardColor+` AND m.business_date BETWEEN $4 AND $5
			AND `+cardMoves+` AND (NOT $6 OR (m.business_date, m.posted_at, m.id) > ($7::date, $8::timestamptz, $9::uuid))
			ORDER BY m.business_date, m.posted_at, m.id LIMIT $10`,
			f.SkuID, f.ColorID, f.Uncolored, card.From, card.To, f.After != nil, c.BusinessDate, c.PostedAt, c.ID, f.Limit); err != nil {
			return fmt.Errorf("inventory: stock card rows: %w", err)
		}
		if err := fillDocNos(ctx, q, moves); err != nil {
			return err
		}
		for _, m := range moves {
			row := CardRow{TraceMove: m, In: money.ZeroQty, Out: money.ZeroQty}
			if m.Qty.IsPositive() {
				row.In = m.Qty
			} else {
				row.Out = m.Qty.Neg()
			}
			balance = balance.Add(m.Qty)
			row.Balance = balance
			card.Rows = append(card.Rows, row)
		}
		issues, err := VerifySkus(ctx, q, []uuid.UUID{f.SkuID})
		if err != nil {
			return err
		}
		card.Reconciled = len(issues) == 0 && (card.To.Before(today) || card.Closing.Equal(card.OnHand))
		return nil
	})
	return card, err
}

// unitDisplay builds the display strategy of a stock card.
func unitDisplay(ctx context.Context, q db.Querier, info skuInfo, mode string, unitID *uuid.UUID) (UnitDisplay, error) {
	var units []cardUnit
	if err := sqlx.SelectContext(ctx, q, &units, `SELECT su.unit_id, u.name, su.factor FROM sku_units su JOIN units u ON u.id = su.unit_id
		WHERE su.sku_id = $1 AND su.active ORDER BY su.factor DESC, u.name`, info.ID); err != nil {
		return nil, fmt.Errorf("inventory: read sku units: %w", err)
	}
	if mode == UnitModeAuto {
		return AutoConvert{Base: info.BaseUnitName, Units: units}, nil
	}
	if *unitID == info.BaseUnitID {
		return FixedUnit{Unit: cardUnit{ID: info.BaseUnitID, Name: info.BaseUnitName, Factor: decimal.NewFromInt(1)}}, nil
	}
	for _, u := range units {
		if u.ID == *unitID {
			return FixedUnit{Unit: u}, nil
		}
	}
	return nil, errInvalid(map[string]string{"unit_id": "not_a_unit_of_sku"})
}
