package reporting

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// maxReportDays bounds the date range of a period report (a little over five years).
const maxReportDays = 1900

var skuFilters = []Filter{
	{Name: "warehouse_id", Kind: filterUUID},
	{Name: "q", Kind: filterText},
}

func nxtReport() Report {
	return Report{
		Key: ReportNXT, Title: "Nhập – xuất – tồn", Range: rangePeriod, MaxDays: maxReportDays, Paged: true,
		Filters: skuFilters,
		Columns: []Column{
			{Key: "code", Label: "Mã hàng", Kind: KindText},
			{Key: "name", Label: "Hàng", Kind: KindText},
			{Key: "unit", Label: "ĐVT", Kind: KindText},
			{Key: "opening_qty", Label: "Đầu kỳ", Kind: KindQty},
			{Key: "in_qty", Label: "Nhập", Kind: KindQty},
			{Key: "out_qty", Label: "Xuất", Kind: KindQty},
			{Key: "closing_qty", Label: "Cuối kỳ", Kind: KindQty},
			{Key: "opening_value", Label: "Giá trị đầu kỳ", Kind: KindMoney},
			{Key: "in_value", Label: "Giá trị nhập", Kind: KindMoney},
			{Key: "out_value", Label: "Giá trị xuất", Kind: KindMoney},
			{Key: "closing_value", Label: "Giá trị cuối kỳ", Kind: KindMoney},
		},
		run: runNXT,
	}
}

// likeContains is a LIKE pattern matching s anywhere, with LIKE's wildcards escaped.
func likeContains(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// searchPattern is the LIKE pattern of the q filter over skus.search_norm, or nil.
func searchPattern(p Params) *string {
	q := p.filterText("q")
	if q == nil {
		return nil
	}
	norm := textnorm.SearchNorm(*q)
	if norm == "" {
		return nil
	}
	pattern := likeContains(norm)
	return &pattern
}

// skuMoves are the movements of one SKU over a period. Quarantine moves pair a normal lot with
// its quarantine twin inside one SKU, so they count by their net only: a pair adds nothing to
// in or out, and opening + in − out = closing holds for every SKU whatever the ledger holds.
type skuMoves struct {
	OpeningQty    decimal.Decimal `db:"opening_qty"`
	OpeningValue  decimal.Decimal `db:"opening_value"`
	InQty         decimal.Decimal `db:"in_qty"`
	OutQty        decimal.Decimal `db:"out_qty"`
	InternalQty   decimal.Decimal `db:"internal_qty"`
	InValue       decimal.Decimal `db:"in_value"`
	OutValue      decimal.Decimal `db:"out_value"`
	InternalValue decimal.Decimal `db:"internal_value"`
}

// periodSums are the in, out and internal columns of skuMoves and the number of moves over
// stock_moves sm already limited to business_date >= $1; $2 is to.
const periodSums = `
	COALESCE(SUM(sm.qty) FILTER (WHERE sm.business_date <= $2 AND sm.move_type NOT IN ('quarantine_in', 'quarantine_out') AND sm.qty > 0), 0) AS in_qty,
	COALESCE(-SUM(sm.qty) FILTER (WHERE sm.business_date <= $2 AND sm.move_type NOT IN ('quarantine_in', 'quarantine_out') AND sm.qty < 0), 0) AS out_qty,
	COALESCE(SUM(sm.qty) FILTER (WHERE sm.business_date <= $2 AND sm.move_type IN ('quarantine_in', 'quarantine_out')), 0) AS internal_qty,
	COALESCE(SUM(sm.value_amount) FILTER (WHERE sm.business_date <= $2 AND sm.move_type NOT IN ('quarantine_in', 'quarantine_out')
		AND (sm.qty > 0 OR (sm.qty = 0 AND sm.value_amount > 0))), 0) AS in_value,
	COALESCE(-SUM(sm.value_amount) FILTER (WHERE sm.business_date <= $2 AND sm.move_type NOT IN ('quarantine_in', 'quarantine_out')
		AND NOT (sm.qty > 0 OR (sm.qty = 0 AND sm.value_amount > 0))), 0) AS out_value,
	COALESCE(SUM(sm.value_amount) FILTER (WHERE sm.business_date <= $2 AND sm.move_type IN ('quarantine_in', 'quarantine_out')), 0) AS internal_value,
	count(*) FILTER (WHERE sm.business_date <= $2) AS moves`

// skuFlow joins to the SKU s what sku_stock holds of it now (h) and its moves since the start of
// the period (m), in warehouse $3 or every warehouse. sku_stock is the running sum of stock_moves
// (inventory.Verify checks it), so the opening is h less every move since $1 and no statement
// reads the ledger before the period.
const skuFlow = `
	CROSS JOIN LATERAL (
		SELECT COALESCE(SUM(ss.on_hand), 0) AS qty, COALESCE(SUM(ss.stock_value), 0) AS value
		FROM sku_stock ss WHERE ss.sku_id = s.id AND ($3::uuid IS NULL OR ss.warehouse_id = $3)
	) h
	CROSS JOIN LATERAL (
		SELECT COALESCE(SUM(sm.qty), 0) AS since_qty, COALESCE(SUM(sm.value_amount), 0) AS since_value,` + periodSums + `
		FROM stock_moves sm
		WHERE sm.sku_id = s.id AND sm.business_date >= $1 AND ($3::uuid IS NULL OR sm.warehouse_id = $3)
	) m`

// nxtTotalsQuery sums the flows of every SKU of the q filter $4, the internal net of each SKU on
// the side it falls on, as flow does.
const nxtTotalsQuery = `
	WITH held AS (
		SELECT ss.sku_id, SUM(ss.on_hand) AS qty, SUM(ss.stock_value) AS value
		FROM sku_stock ss WHERE $3::uuid IS NULL OR ss.warehouse_id = $3
		GROUP BY ss.sku_id
	), moved AS (
		SELECT sm.sku_id, SUM(sm.qty) AS since_qty, SUM(sm.value_amount) AS since_value,` + periodSums + `
		FROM stock_moves sm WHERE sm.business_date >= $1 AND ($3::uuid IS NULL OR sm.warehouse_id = $3)
		GROUP BY sm.sku_id
	), flows AS (
		SELECT COALESCE(h.qty, 0) - COALESCE(m.since_qty, 0) AS opening_qty,
			COALESCE(h.value, 0) - COALESCE(m.since_value, 0) AS opening_value,
			COALESCE(m.in_qty, 0) + GREATEST(COALESCE(m.internal_qty, 0), 0) AS in_qty,
			COALESCE(m.out_qty, 0) + GREATEST(-COALESCE(m.internal_qty, 0), 0) AS out_qty,
			COALESCE(m.in_value, 0) + GREATEST(COALESCE(m.internal_value, 0), 0) AS in_value,
			COALESCE(m.out_value, 0) + GREATEST(-COALESCE(m.internal_value, 0), 0) AS out_value
		FROM held h
		FULL JOIN moved m ON m.sku_id = h.sku_id
		JOIN skus s ON s.id = COALESCE(h.sku_id, m.sku_id)
		WHERE $4::text IS NULL OR s.search_norm LIKE $4
	)
	SELECT COALESCE(SUM(opening_qty), 0) AS opening_qty, COALESCE(SUM(opening_value), 0) AS opening_value,
		COALESCE(SUM(in_qty), 0) AS in_qty, COALESCE(SUM(out_qty), 0) AS out_qty,
		COALESCE(SUM(in_value), 0) AS in_value, COALESCE(SUM(out_value), 0) AS out_value,
		0 AS internal_qty, 0 AS internal_value
	FROM flows`

// flow is the in, out and closing of a SKU, the internal net added to the side it falls on.
func (m skuMoves) flow() (inQty, outQty, closingQty, inValue, outValue, closingValue decimal.Decimal) {
	inQty, outQty = m.InQty, m.OutQty
	if m.InternalQty.IsPositive() {
		inQty = inQty.Add(m.InternalQty)
	} else {
		outQty = outQty.Sub(m.InternalQty)
	}
	inValue, outValue = m.InValue, m.OutValue
	if m.InternalValue.IsPositive() {
		inValue = inValue.Add(m.InternalValue)
	} else {
		outValue = outValue.Sub(m.InternalValue)
	}
	return inQty, outQty, m.OpeningQty.Add(inQty).Sub(outQty), inValue, outValue, m.OpeningValue.Add(inValue).Sub(outValue)
}

func (m skuMoves) cells(r Row) {
	inQty, outQty, closingQty, inValue, outValue, closingValue := m.flow()
	r["opening_qty"], r["in_qty"], r["out_qty"], r["closing_qty"] = dec(m.OpeningQty), dec(inQty), dec(outQty), dec(closingQty)
	r["opening_value"], r["in_value"], r["out_value"], r["closing_value"] = dec(m.OpeningValue), dec(inValue), dec(outValue), dec(closingValue)
}

// add sums the flow of m into t.
func (t *skuMoves) add(m skuMoves) {
	inQty, outQty, _, inValue, outValue, _ := m.flow()
	t.OpeningQty, t.OpeningValue = t.OpeningQty.Add(m.OpeningQty), t.OpeningValue.Add(m.OpeningValue)
	t.InQty, t.OutQty = t.InQty.Add(inQty), t.OutQty.Add(outQty)
	t.InValue, t.OutValue = t.InValue.Add(inValue), t.OutValue.Add(outValue)
}

type nxtRow struct {
	ID    uuid.UUID `db:"id"`
	Code  string    `db:"code"`
	Name  string    `db:"display_name"`
	Unit  string    `db:"unit"`
	Moves int64     `db:"moves"`
	skuMoves
}

func (r nxtRow) row() Row {
	row := Row{rowID: text(r.ID.String()), "code": text(r.Code), "name": text(r.Name), "unit": text(r.Unit)}
	r.cells(row)
	return row
}

// codeKeyset is the condition of the rows after the (code, id) cursor $n, $n+1 over code and id
// expressions; the code bound alone lets the plan start from the code index. A statement adds it
// only for a later page, so the first page plans without it.
func codeKeyset(code, id string, n int) string {
	a, b := "$"+strconv.Itoa(n), "$"+strconv.Itoa(n+1)
	return " AND " + code + " >= " + a + " AND (" + code + ", " + id + ") > (" + a + ", " + b + "::uuid)"
}

// runNXT reads one page of SKUs in code order with their flows, and the totals of every SKU with
// the first page; an export streams every SKU and sums the totals from its rows.
func runNXT(ctx context.Context, s *Service, scope Scope, p Params) (Table, error) {
	t := Table{
		Scope: "Theo mã hàng (không tách màu), mọi kho trừ khi chọn kho; số lượng theo đơn vị cơ bản.",
		Notes: []string{"Đầu kỳ + Nhập − Xuất = Cuối kỳ; giá trị theo giá vốn bình quân ghi trên sổ kho.",
			"Chuyển hàng vào/ra khu cách ly trong cùng mã hàng chỉ tính phần chênh lệch."},
	}
	warehouse, search := p.filterUUID("warehouse_id"), searchPattern(p)
	args := []any{p.From, p.To, warehouse, search, p.pageLimit()}
	query := `
		SELECT s.id, s.code, s.display_name, u.name AS unit, h.qty - m.since_qty AS opening_qty,
			h.value - m.since_value AS opening_value, m.in_qty, m.out_qty, m.internal_qty, m.in_value, m.out_value,
			m.internal_value, m.moves
		FROM skus s
		JOIN units u ON u.id = s.base_unit_id` + skuFlow + `
		WHERE (h.qty <> m.since_qty OR h.value <> m.since_value OR m.moves > 0)
		  AND ($4::text IS NULL OR s.search_norm LIKE $4)`
	if p.After != nil {
		query += codeKeyset("s.code", "s.id", len(args)+1)
		args = append(args, p.After.SortKey, p.After.ID)
	}
	query += `
		ORDER BY s.code, s.id
		LIMIT $5`
	err := s.read(ctx, scope, func(q db.Querier) error {
		var rows []nxtRow
		var sum skuMoves
		err := eachRow(ctx, q, query, args, func(r nxtRow) error {
			if p.sink == nil {
				rows = append(rows, r)
				return nil
			}
			sum.add(r.skuMoves)
			return p.sink(r.row())
		})
		if err != nil {
			return fmt.Errorf("reporting: nxt: %w", err)
		}
		if p.sink != nil {
			t.Totals = nxtTotals(sum)
			return nil
		}
		page, next := pageOf(rows, p.Limit, func(r nxtRow) httpx.Cursor {
			return httpx.Cursor{SortKey: r.Code, ID: r.ID}
		})
		t.Next = next
		for _, r := range page {
			t.Rows = append(t.Rows, r.row())
		}
		if !p.firstPage() {
			return nil
		}
		if err := q.QueryRowxContext(ctx, nxtTotalsQuery, p.From, p.To, warehouse, search).StructScan(&sum); err != nil {
			return fmt.Errorf("reporting: nxt totals: %w", err)
		}
		t.Totals = nxtTotals(sum)
		return nil
	})
	return t, err
}

// nxtTotals is the totals row of summed flows (their internal nets already on their sides).
func nxtTotals(m skuMoves) Row {
	r := Row{}
	m.cells(r)
	return r
}

func lotStockReport() Report {
	return Report{
		Key: ReportLotStock, Title: "Tồn kho theo lô", Range: rangeNone, Paged: true,
		Filters: append([]Filter{{Name: "expiring_within", Kind: filterInt, Min: 0, Max: 3650}}, skuFilters...),
		Columns: []Column{
			{Key: "code", Label: "Mã hàng", Kind: KindText},
			{Key: "name", Label: "Hàng", Kind: KindText},
			{Key: "warehouse", Label: "Kho", Kind: KindText},
			{Key: "lot_no", Label: "Lô", Kind: KindText},
			{Key: "color", Label: "Màu", Kind: KindText},
			{Key: "expiry_date", Label: "Hạn dùng", Kind: KindDate},
			{Key: "quarantine", Label: "Cách ly", Kind: KindText},
			{Key: "qty", Label: "Tồn", Kind: KindQty},
			{Key: "avg_cost", Label: "Giá vốn BQ", Kind: KindMoney},
			{Key: "value", Label: "Giá trị", Kind: KindMoney},
			{Key: "sku_reserved", Label: "Đặt giữ (cả mã hàng)", Kind: KindQty},
		},
		run: runLotStock,
	}
}

// lotRow is one lot of the lot stock report.
type lotRow struct {
	ID         uuid.UUID       `db:"id"`
	SkuID      uuid.UUID       `db:"sku_id"`
	Code       string          `db:"code"`
	Name       string          `db:"display_name"`
	Warehouse  string          `db:"warehouse"`
	LotNo      string          `db:"lot_no"`
	Color      *string         `db:"color"`
	Expiry     *time.Time      `db:"expiry_date"`
	Quarantine bool            `db:"is_quarantine"`
	Qty        decimal.Decimal `db:"qty"`
	AvgCost    decimal.Decimal `db:"avg_cost"`
	Reserved   decimal.Decimal `db:"reserved"`
}

// value is the lot at the moving average cost of its SKU.
func (r lotRow) value() decimal.Decimal { return r.Qty.Mul(r.AvgCost).Round(4) }

func runLotStock(ctx context.Context, s *Service, scope Scope, p Params) (Table, error) {
	t := Table{
		Scope: "Tồn hiện tại theo lô, hạn dùng và màu; giá trị = tồn × giá vốn bình quân của mã hàng tại kho.",
		Notes: []string{"Hàng đặt giữ được giữ theo mã hàng, không theo lô."},
	}
	var expiresBy *time.Time
	if _, ok := p.Filters["expiring_within"]; ok {
		d := p.Today.AddDate(0, 0, p.filterInt("expiring_within", 0))
		expiresBy = &d
	}
	warehouse, search := p.filterUUID("warehouse_id"), searchPattern(p)
	const from = `
		FROM lot_balances b
		JOIN lots l ON l.id = b.lot_id
		JOIN skus s ON s.id = l.sku_id
		JOIN warehouses w ON w.id = l.warehouse_id
		LEFT JOIN colors c ON c.id = l.color_id
		LEFT JOIN sku_stock ss ON ss.warehouse_id = l.warehouse_id AND ss.sku_id = l.sku_id
		WHERE b.qty > 0 AND ($1::uuid IS NULL OR l.warehouse_id = $1) AND ($2::text IS NULL OR s.search_norm LIKE $2)
		  AND ($3::date IS NULL OR l.expiry_date <= $3)`
	row := func(r lotRow) Row {
		color := "Chưa gán màu"
		if r.Color != nil {
			color = *r.Color
		}
		quarantine := "Không"
		if r.Quarantine {
			quarantine = "Có"
		}
		return Row{
			rowID: text(r.SkuID.String()), "code": text(r.Code), "name": text(r.Name), "warehouse": text(r.Warehouse),
			"lot_no": text(r.LotNo), "color": text(color), "expiry_date": optDay(r.Expiry), "quarantine": text(quarantine),
			"qty": dec(r.Qty), "avg_cost": dec(r.AvgCost), "value": dec(r.value()), "sku_reserved": dec(r.Reserved),
		}
	}
	args := []any{warehouse, search, expiresBy, p.pageLimit()}
	query := `
		SELECT l.id, l.sku_id, s.code, s.display_name, w.name AS warehouse, l.lot_no, c.name AS color, l.expiry_date,
		       l.is_quarantine, b.qty, COALESCE(ss.avg_cost, 0) AS avg_cost, COALESCE(ss.reserved, 0) AS reserved` + from
	if p.After != nil {
		query += codeKeyset("s.code", "l.id", len(args)+1)
		args = append(args, p.After.SortKey, p.After.ID)
	}
	query += `
		ORDER BY s.code, l.id
		LIMIT $4`
	err := s.read(ctx, scope, func(q db.Querier) error {
		var rows []lotRow
		var totalQty, totalValue decimal.Decimal
		err := eachRow(ctx, q, query, args, func(r lotRow) error {
			if p.sink == nil {
				rows = append(rows, r)
				return nil
			}
			totalQty, totalValue = totalQty.Add(r.Qty), totalValue.Add(r.value())
			return p.sink(row(r))
		})
		if err != nil {
			return fmt.Errorf("reporting: lot stock: %w", err)
		}
		if p.sink != nil {
			t.Totals = Row{"qty": dec(totalQty), "value": dec(totalValue)}
			return nil
		}
		page, next := pageOf(rows, p.Limit, func(r lotRow) httpx.Cursor { return httpx.Cursor{SortKey: r.Code, ID: r.ID} })
		t.Next = next
		for _, r := range page {
			t.Rows = append(t.Rows, row(r))
		}
		if !p.firstPage() {
			return nil
		}
		var totals struct {
			Qty   decimal.Decimal `db:"qty"`
			Value decimal.Decimal `db:"value"`
		}
		if err := q.QueryRowxContext(ctx, `
			SELECT COALESCE(SUM(b.qty), 0) AS qty, COALESCE(SUM(round(b.qty * COALESCE(ss.avg_cost, 0), 4)), 0) AS value`+from,
			warehouse, search, expiresBy).StructScan(&totals); err != nil {
			return fmt.Errorf("reporting: lot stock totals: %w", err)
		}
		t.Totals = Row{"qty": dec(totals.Qty), "value": dec(totals.Value)}
		return nil
	})
	return t, err
}
