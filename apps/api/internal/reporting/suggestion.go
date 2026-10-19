package reporting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Suggestion defaults.
const (
	defaultCoverDays      = 14
	defaultLookbackDays   = 30
	defaultSurplusDays    = 90
	defaultExpiringDays   = 30
	confidenceHighShare   = 0.5
	confidenceMediumShare = 0.2
)

// consumption are the stock moves that use a product up: sales net of returns, and components
// assembled into kits net of those a disassembly gave back.
const consumption = `('sale', 'sales_return', 'assembly_consume', 'disassembly_return')`

// velocity is the use of each SKU over the lookback window ($1 = first day, $2 = today): the net
// quantity used and the number of days with a use.
const velocity = `
	SELECT sm.sku_id, -SUM(sm.qty) AS used, count(DISTINCT sm.business_date) FILTER (WHERE sm.qty < 0) AS use_days
	FROM stock_moves sm
	WHERE sm.move_type IN ` + consumption + ` AND sm.business_date BETWEEN $1 AND $2
	GROUP BY sm.sku_id`

// confidenceOf grades a velocity by the share of the window's days with a use.
func confidenceOf(useDays int64, window int) string {
	share := float64(useDays) / float64(window)
	switch {
	case share >= confidenceHighShare:
		return "Cao"
	case share >= confidenceMediumShare:
		return "Trung bình"
	default:
		return "Thấp"
	}
}

func buyReport() Report {
	return Report{
		Key: ReportBuy, Title: "Gợi ý nhập hàng", Range: rangeNone,
		Filters: []Filter{
			{Name: "cover_days", Kind: filterInt, Min: 1, Max: 365},
			{Name: "lookback_days", Kind: filterInt, Min: 7, Max: 365},
			{Name: "q", Kind: filterText},
		},
		Columns: []Column{
			{Key: "code", Label: "Mã hàng", Kind: KindText},
			{Key: "name", Label: "Hàng", Kind: KindText},
			{Key: "unit", Label: "ĐVT", Kind: KindText},
			{Key: "avg_daily", Label: "Bán bình quân/ngày", Kind: KindQty},
			{Key: "available", Label: "Có thể bán", Kind: KindQty},
			{Key: "pending_po", Label: "Đang đặt mua", Kind: KindQty},
			{Key: "min_stock", Label: "Tồn tối thiểu", Kind: KindQty},
			{Key: "suggested_qty", Label: "Gợi ý nhập", Kind: KindQty},
			{Key: "method", Label: "Cách tính", Kind: KindText},
			{Key: "confidence", Label: "Độ tin cậy", Kind: KindText},
		},
		run: runBuy,
	}
}

func runBuy(ctx context.Context, s *Service, scope Scope, p Params) (Table, error) {
	cover, lookback := p.filterInt("cover_days", defaultCoverDays), p.filterInt("lookback_days", defaultLookbackDays)
	t := Table{
		Scope: fmt.Sprintf("Gợi ý = bán bình quân %d ngày qua × %d ngày cần đủ − có thể bán − đang đặt mua; chỉ là gợi ý, không tự tạo đơn mua.",
			lookback, cover),
		Notes: []string{
			"Nguồn: sổ kho (bán trừ trả lại, xuất đóng bộ), tồn hiện tại trừ hàng đặt giữ, đơn mua đã xác nhận chưa nhận đủ.",
			"Mặt hàng chưa bán trong kỳ xem xét được gợi ý theo tồn tối thiểu.",
			"Độ tin cậy theo số ngày có bán trong kỳ xem xét.",
		},
	}
	start := p.Today.AddDate(0, 0, -(lookback - 1))
	err := s.read(ctx, scope, func(q db.Querier) error {
		var rows []struct {
			ID       uuid.UUID       `db:"id"`
			Code     string          `db:"code"`
			Name     string          `db:"display_name"`
			Unit     string          `db:"unit"`
			Scale    int32           `db:"qty_scale"`
			MinStock decimal.Decimal `db:"min_stock"`
			Used     decimal.Decimal `db:"used"`
			UseDays  int64           `db:"use_days"`
			Avail    decimal.Decimal `db:"available"`
			Pending  decimal.Decimal `db:"pending"`
		}
		if err := sqlx.SelectContext(ctx, q, &rows, `
			WITH v AS (`+velocity+`),
			stock AS (SELECT sku_id, SUM(sellable - reserved) AS available FROM sku_stock GROUP BY sku_id),
			po AS (
				SELECT pl.sku_id, SUM(pl.base_qty - pl.received_base_qty) AS pending
				FROM purchase_order_lines pl JOIN purchase_orders o ON o.id = pl.purchase_order_id
				WHERE o.status IN ('confirmed', 'partially_received') AND pl.base_qty > pl.received_base_qty
				GROUP BY pl.sku_id
			)
			SELECT s.id, s.code, s.display_name, u.name AS unit, s.qty_scale, s.min_stock,
				COALESCE(v.used, 0) AS used, COALESCE(v.use_days, 0) AS use_days,
				COALESCE(stock.available, 0) AS available, COALESCE(po.pending, 0) AS pending
			FROM skus s
			JOIN products pr ON pr.id = s.product_id AND pr.type <> 'combo'
			JOIN units u ON u.id = s.base_unit_id
			LEFT JOIN v ON v.sku_id = s.id
			LEFT JOIN stock ON stock.sku_id = s.id
			LEFT JOIN po ON po.sku_id = s.id
			WHERE s.status = 'active' AND (COALESCE(v.used, 0) > 0 OR s.min_stock > 0)
			  AND ($3::text IS NULL OR s.search_norm LIKE $3)
			ORDER BY s.code, s.id`, start, p.Today, searchPattern(p)); err != nil {
			return fmt.Errorf("reporting: buy suggestions: %w", err)
		}
		days := decimal.NewFromInt(int64(lookback))
		var count int64
		for _, r := range rows {
			avg := r.Used.Div(days).Round(4)
			if avg.IsNegative() {
				avg = decimal.Zero
			}
			need := avg.Mul(decimal.NewFromInt(int64(cover)))
			method := fmt.Sprintf("Bình quân %d ngày × %d ngày", lookback, cover)
			confidence := confidenceOf(r.UseDays, lookback)
			if r.MinStock.GreaterThan(need) {
				need, method = r.MinStock, "Theo tồn tối thiểu"
				if !avg.IsPositive() {
					confidence = "Thấp"
				}
			}
			suggested := need.Sub(r.Avail).Sub(r.Pending).RoundCeil(r.Scale)
			if !suggested.IsPositive() {
				continue
			}
			count++
			t.Rows = append(t.Rows, Row{
				rowID: text(r.ID.String()), "code": text(r.Code), "name": text(r.Name), "unit": text(r.Unit),
				"avg_daily": dec(avg), "available": dec(r.Avail), "pending_po": dec(r.Pending), "min_stock": dec(r.MinStock),
				"suggested_qty": dec(suggested), "method": text(method), "confidence": text(confidence),
			})
		}
		t.Summary = []Metric{metric("items", "Số mặt hàng nên nhập", KindInt, integer(count))}
		return nil
	})
	return t, err
}

func clearanceReport() Report {
	return Report{
		Key: ReportClearance, Title: "Gợi ý xả hàng", Range: rangeNone,
		Filters: []Filter{
			{Name: "surplus_days", Kind: filterInt, Min: 7, Max: 3650},
			{Name: "lookback_days", Kind: filterInt, Min: 7, Max: 365},
			{Name: "expiring_within", Kind: filterInt, Min: 0, Max: 3650},
			{Name: "q", Kind: filterText},
		},
		Columns: []Column{
			{Key: "code", Label: "Mã hàng", Kind: KindText},
			{Key: "name", Label: "Hàng", Kind: KindText},
			{Key: "on_hand", Label: "Tồn", Kind: KindQty},
			{Key: "stock_value", Label: "Giá trị tồn", Kind: KindMoney},
			{Key: "avg_daily", Label: "Bán bình quân/ngày", Kind: KindQty},
			{Key: "days_of_cover", Label: "Đủ bán (ngày)", Kind: KindInt},
			{Key: "nearest_expiry", Label: "Hạn gần nhất", Kind: KindDate},
			{Key: "expiring_qty", Label: "Sắp hết hạn", Kind: KindQty},
			{Key: "reason", Label: "Lý do", Kind: KindText},
			{Key: "confidence", Label: "Độ tin cậy", Kind: KindText},
		},
		run: runClearance,
	}
}

func runClearance(ctx context.Context, s *Service, scope Scope, p Params) (Table, error) {
	surplus := p.filterInt("surplus_days", defaultSurplusDays)
	lookback := p.filterInt("lookback_days", defaultLookbackDays)
	expiring := p.filterInt("expiring_within", defaultExpiringDays)
	t := Table{
		Scope: fmt.Sprintf("Hàng tồn đủ bán hơn %d ngày theo tốc độ bán %d ngày qua, không bán được trong %d ngày, hoặc có lô hết hạn trong %d ngày.",
			surplus, lookback, lookback, expiring),
		Notes: []string{"Chỉ là gợi ý; giá xả do chủ cửa hàng quyết định.", "Độ tin cậy theo số ngày có bán trong kỳ xem xét."},
	}
	start := p.Today.AddDate(0, 0, -(lookback - 1))
	expiresBy := p.Today.AddDate(0, 0, expiring)
	err := s.read(ctx, scope, func(q db.Querier) error {
		var rows []struct {
			ID       uuid.UUID       `db:"id"`
			Code     string          `db:"code"`
			Name     string          `db:"display_name"`
			OnHand   decimal.Decimal `db:"on_hand"`
			Value    decimal.Decimal `db:"stock_value"`
			Used     decimal.Decimal `db:"used"`
			UseDays  int64           `db:"use_days"`
			Nearest  *time.Time      `db:"nearest_expiry"`
			Expiring decimal.Decimal `db:"expiring_qty"`
		}
		if err := sqlx.SelectContext(ctx, q, &rows, `
			WITH v AS (`+velocity+`),
			stock AS (SELECT sku_id, SUM(on_hand) AS on_hand, SUM(stock_value) AS stock_value FROM sku_stock GROUP BY sku_id),
			lots AS (
				SELECT l.sku_id, MIN(l.expiry_date) AS nearest_expiry,
					COALESCE(SUM(b.qty) FILTER (WHERE l.expiry_date <= $3), 0) AS expiring_qty
				FROM lots l JOIN lot_balances b ON b.lot_id = l.id
				WHERE b.qty > 0 AND l.expiry_date IS NOT NULL
				GROUP BY l.sku_id
			)
			SELECT s.id, s.code, s.display_name, stock.on_hand, stock.stock_value,
				COALESCE(v.used, 0) AS used, COALESCE(v.use_days, 0) AS use_days,
				lots.nearest_expiry, COALESCE(lots.expiring_qty, 0) AS expiring_qty
			FROM stock JOIN skus s ON s.id = stock.sku_id
			LEFT JOIN v ON v.sku_id = s.id
			LEFT JOIN lots ON lots.sku_id = s.id
			WHERE stock.on_hand > 0 AND ($4::text IS NULL OR s.search_norm LIKE $4)
			ORDER BY stock.stock_value DESC, s.code, s.id`, start, p.Today, expiresBy, searchPattern(p)); err != nil {
			return fmt.Errorf("reporting: clearance suggestions: %w", err)
		}
		days := decimal.NewFromInt(int64(lookback))
		var count int64
		var value decimal.Decimal
		for _, r := range rows {
			avg := r.Used.Div(days).Round(4)
			var cover *string
			var reasons []string
			switch {
			case !avg.IsPositive():
				reasons = append(reasons, fmt.Sprintf("Không bán được trong %d ngày", lookback))
			default:
				d := r.OnHand.Div(avg).Floor()
				cover = dec(d)
				if d.GreaterThan(decimal.NewFromInt(int64(surplus))) {
					reasons = append(reasons, fmt.Sprintf("Đủ bán %s ngày", d.String()))
				}
			}
			if r.Expiring.IsPositive() {
				reasons = append(reasons, "Có lô sắp hết hạn "+r.Nearest.Format("02/01/2006"))
			}
			if len(reasons) == 0 {
				continue
			}
			reason := reasons[0]
			for _, x := range reasons[1:] {
				reason += "; " + x
			}
			count++
			value = value.Add(r.Value)
			t.Rows = append(t.Rows, Row{
				rowID: text(r.ID.String()), "code": text(r.Code), "name": text(r.Name), "on_hand": dec(r.OnHand),
				"stock_value": dec(r.Value), "avg_daily": dec(avg), "days_of_cover": cover, "nearest_expiry": optDay(r.Nearest),
				"expiring_qty": dec(r.Expiring), "reason": text(reason), "confidence": text(confidenceOf(r.UseDays, lookback)),
			})
		}
		t.Totals = Row{"stock_value": dec(value)}
		t.Summary = []Metric{
			metric("items", "Số mặt hàng nên xả", KindInt, integer(count)),
			metric("stock_value", "Giá trị tồn liên quan", KindMoney, dec(value)),
		}
		return nil
	})
	return t, err
}

func cashForecastReport() Report {
	return Report{
		Key: ReportCashForecast, Title: "Dự báo dòng tiền", Range: rangeFuture, MaxDays: maxScheduleDays,
		Columns: []Column{
			{Key: "date", Label: "Ngày", Kind: KindDate},
			{Key: "receivable_due", Label: "Dự kiến thu (đến hạn)", Kind: KindMoney},
			{Key: "payable_due", Label: "Dự kiến chi (đến hạn)", Kind: KindMoney},
			{Key: "promised", Label: "Khách hẹn trả", Kind: KindMoney},
			{Key: "confirmed_in", Label: "Đã thu", Kind: KindMoney},
			{Key: "confirmed_out", Label: "Đã chi", Kind: KindMoney},
			{Key: "projected_balance", Label: "Tồn quỹ dự kiến", Kind: KindMoney},
		},
		run: runCashForecast,
	}
}

func runCashForecast(ctx context.Context, s *Service, scope Scope, p Params) (Table, error) {
	t := Table{
		Scope: "Tồn quỹ dự kiến = tồn quỹ đầu kỳ + tiền đã thu chi đến hôm nay + nợ đến hạn thu − nợ đến hạn trả các ngày sau.",
		Notes: []string{
			"Nguồn: sổ quỹ và lịch đến hạn của công nợ; tiền khách hẹn trả hiển thị riêng, không cộng vào tồn quỹ dự kiến.",
			"Khoản đã quá hạn không được giả định sẽ thu hay trả; độ tin cậy giảm dần theo khoảng cách tới hôm nay.",
		},
	}
	f, err := s.debt.Forecast(ctx, scope, p.From, p.To)
	if err != nil {
		return Table{}, err
	}
	var balance money.Money
	err = s.read(ctx, scope, func(q db.Querier) error {
		funds, err := loadFundBalances(ctx, q, p.From.AddDate(0, 0, -1))
		if err != nil {
			return err
		}
		for _, fb := range funds {
			balance = balance.Add(fb.Balance)
		}
		return nil
	})
	if err != nil {
		return Table{}, err
	}
	opening := balance
	var recv, pay, promised money.Money
	for _, d := range f.Days {
		if d.Date.After(p.Today) {
			balance = balance.Add(d.ReceivableDue).Sub(d.PayableDue)
		} else {
			balance = balance.Add(d.ConfirmedIn).Sub(d.ConfirmedOut)
		}
		recv, pay, promised = recv.Add(d.ReceivableDue), pay.Add(d.PayableDue), promised.Add(d.Promised)
		t.Rows = append(t.Rows, Row{
			"date": day(d.Date), "receivable_due": amount(d.ReceivableDue), "payable_due": amount(d.PayableDue),
			"promised": amount(d.Promised), "confirmed_in": amount(d.ConfirmedIn), "confirmed_out": amount(d.ConfirmedOut),
			"projected_balance": amount(balance),
		})
	}
	t.Totals = Row{"receivable_due": amount(recv), "payable_due": amount(pay), "promised": amount(promised)}
	t.Summary = []Metric{
		metric("opening_balance", "Tồn quỹ đầu kỳ", KindMoney, amount(opening)),
		metric("projected_balance", "Tồn quỹ dự kiến cuối kỳ", KindMoney, amount(balance)),
		metric("promised", "Khách hẹn trả (chưa tính)", KindMoney, amount(promised)),
		metric("overdue_receivable", "Phải thu quá hạn", KindMoney, amount(f.OverdueReceivable)),
		metric("overdue_payable", "Phải trả quá hạn", KindMoney, amount(f.OverduePayable)),
	}
	return t, nil
}
