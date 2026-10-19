package reporting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// seasonSold is a CTE "sold" of the sales lines whose primary campaign is $1: the lines attributed
// to it, and the lines of documents attributed to it as a whole unless the line itself has its
// own primary campaign. Tags never select a line.
const seasonSold = `sold AS (
		SELECT a.doc_line_id AS line_id FROM campaign_attributions a
		WHERE a.campaign_id = $1 AND a.role = 'primary' AND a.doc_type = 'sales_document' AND a.doc_line_id IS NOT NULL
		UNION
		SELECT l.id FROM campaign_attributions a JOIN sales_document_lines l ON l.sales_document_id = a.doc_id
		WHERE a.campaign_id = $1 AND a.role = 'primary' AND a.doc_type = 'sales_document' AND a.doc_line_id IS NULL
		  AND NOT EXISTS (SELECT 1 FROM campaign_attributions x WHERE x.doc_type = 'sales_document' AND x.doc_id = l.sales_document_id
			AND x.doc_line_id = l.id AND x.role = 'primary')
	)`

// seasonFacts are the facts of campaign $1 by product, as salesFacts counts them. A return line
// belongs to the campaign it is attributed to, else to the campaign of the line it returns.
const seasonFacts = `WITH ` + seasonSold + `, facts AS (
		SELECT (CASE WHEN l.line_kind = 'combo_component' THEN pl.sku_id ELSE l.sku_id END) AS sku_id,
			d.id AS doc_id, d.customer_party_id AS party_id,
			CASE WHEN l.line_kind NOT IN ('mix_pack', 'combo_component') THEN l.line_amount - l.tax_amount ELSE 0 END AS net,
			0::numeric AS returned,
			CASE WHEN l.line_kind NOT IN ('mix_pack', 'combo_component') THEN l.base_qty ELSE 0 END AS qty,
			l.cogs_amount AS cogs
		FROM sold
		JOIN sales_document_lines l ON l.id = sold.line_id
		JOIN sales_documents d ON d.id = l.sales_document_id
		LEFT JOIN sales_document_lines pl ON pl.id = l.parent_line_id
		WHERE d.status IN ('posted', 'partially_reversed') AND d.reversal_of IS NULL AND l.line_kind <> 'mix_pack'
		UNION ALL
		SELECT (CASE WHEN l.line_kind = 'combo_component' THEN pl.sku_id ELSE rl.sku_id END), NULL, NULL, -ret.net, ret.net,
			-(` + returnedQty + `), -round(rl.unit_cost * rl.base_qty, 4)
		FROM sales_return_lines rl
		JOIN sales_returns r ON r.id = rl.sales_return_id
		LEFT JOIN sales_document_lines l ON l.id = rl.sales_document_line_id
		LEFT JOIN sales_document_lines pl ON pl.id = l.parent_line_id
		LEFT JOIN sales_documents d ON d.id = r.sales_document_id
		LEFT JOIN campaign_attributions la ON la.doc_type = 'sales_return' AND la.doc_id = r.id AND la.doc_line_id = rl.id AND la.role = 'primary'
		LEFT JOIN campaign_attributions da ON da.doc_type = 'sales_return' AND da.doc_id = r.id AND da.doc_line_id IS NULL AND da.role = 'primary'
		CROSS JOIN LATERAL (SELECT ` + returnNet + ` AS net) ret
		WHERE r.status = 'posted' AND r.reversal_of IS NULL
		  AND COALESCE(la.campaign_id, da.campaign_id,
			CASE WHEN EXISTS (SELECT 1 FROM sold WHERE sold.line_id = rl.sales_document_line_id) THEN $1::uuid END) = $1
	)`

type seasonCampaign struct {
	ID           uuid.UUID        `db:"id"`
	Name         string           `db:"name"`
	StartsOn     time.Time        `db:"starts_on"`
	EndsOn       time.Time        `db:"ends_on"`
	Budget       *decimal.Decimal `db:"budget_amount"`
	TargetRev    *decimal.Decimal `db:"target_revenue"`
	TargetMargin *decimal.Decimal `db:"target_margin_percent"`
	Status       string           `db:"status"`
}

func (c seasonCampaign) days() int { return daysBetween(c.StartsOn, c.EndsOn) }

type seasonTotals struct {
	Documents int64           `db:"documents"`
	Customers int64           `db:"customers"`
	Net       decimal.Decimal `db:"net"`
	Returned  decimal.Decimal `db:"returned"`
	Qty       decimal.Decimal `db:"qty"`
	Cogs      decimal.Decimal `db:"cogs"`
}

func (t seasonTotals) margin() decimal.Decimal { return t.Net.Sub(t.Cogs) }

// seasonOpen is what a season still has outstanding now: the undelivered value of its confirmed
// sales orders and what customers still owe from its sales.
type seasonOpen struct {
	OpenOrders decimal.Decimal `db:"open_orders"`
	Receivable decimal.Decimal `db:"receivable"`
}

func seasonReport() Report {
	return Report{
		Key: ReportSeason, Title: "Báo cáo mùa vụ", Range: rangeNone,
		Filters: []Filter{
			{Name: "campaign_id", Kind: filterUUID, Required: true},
			{Name: "compare_campaign_id", Kind: filterUUID},
		},
		Columns: []Column{
			{Key: "code", Label: "Mã hàng", Kind: KindText},
			{Key: "name", Label: "Hàng", Kind: KindText},
			{Key: "qty", Label: "Số lượng bán", Kind: KindQty},
			{Key: "net_revenue", Label: "Doanh thu thuần", Kind: KindMoney},
			{Key: "cogs", Label: "Giá vốn", Kind: KindMoney},
			{Key: "gross_margin", Label: "Lãi gộp", Kind: KindMoney},
			{Key: "margin_percent", Label: "Tỷ suất lãi gộp", Kind: KindPercent},
			{Key: "share", Label: "Tỷ trọng doanh thu", Kind: KindPercent},
		},
		run: runSeason,
	}
}

func loadSeasonCampaign(ctx context.Context, q db.Querier, id uuid.UUID, field string) (seasonCampaign, error) {
	var c seasonCampaign
	err := q.QueryRowxContext(ctx, `
		SELECT id, name, starts_on, ends_on, budget_amount, target_revenue, target_margin_percent, status
		FROM campaigns WHERE id = $1`, id).StructScan(&c)
	if db.IsNoRows(err) {
		return seasonCampaign{}, errInvalid(map[string]string{field: "not_found"})
	}
	if err != nil {
		return seasonCampaign{}, fmt.Errorf("reporting: read campaign: %w", err)
	}
	return c, nil
}

func loadSeasonTotals(ctx context.Context, q db.Querier, campaignID uuid.UUID) (seasonTotals, error) {
	var t seasonTotals
	if err := q.QueryRowxContext(ctx, seasonFacts+`
		SELECT count(DISTINCT doc_id) AS documents, count(DISTINCT party_id) AS customers,
			COALESCE(sum(net), 0) AS net, COALESCE(sum(returned), 0) AS returned, COALESCE(sum(qty), 0) AS qty,
			COALESCE(sum(cogs), 0) AS cogs
		FROM facts`, campaignID).StructScan(&t); err != nil {
		return seasonTotals{}, fmt.Errorf("reporting: season totals: %w", err)
	}
	return t, nil
}

func loadSeasonOpen(ctx context.Context, q db.Querier, campaignID uuid.UUID) (seasonOpen, error) {
	var o seasonOpen
	if err := q.QueryRowxContext(ctx, `
		SELECT
			COALESCE((SELECT sum(GREATEST(i.total_amount - i.delivered_amount, 0)) FROM invoices i
				WHERE i.campaign_id = $1 AND i.status IN ('confirmed', 'partially_delivered')), 0) AS open_orders,
			COALESCE((SELECT sum(o.amount - o.settled_amount) FROM obligations o
				WHERE o.campaign_id = $1 AND o.direction = 'receivable' AND o.status = 'open'), 0) AS receivable`,
		campaignID).StructScan(&o); err != nil {
		return seasonOpen{}, fmt.Errorf("reporting: season open balances: %w", err)
	}
	return o, nil
}

func runSeason(ctx context.Context, s *Service, scope Scope, p Params) (Table, error) {
	campaignID := *p.filterUUID("campaign_id")
	var t Table
	err := s.read(ctx, scope, func(q db.Querier) error {
		c, err := loadSeasonCampaign(ctx, q, campaignID, "campaign_id")
		if err != nil {
			return err
		}
		from, to := c.StartsOn, c.EndsOn
		t.From, t.To, t.Days = &from, &to, c.days()
		t.Scope = fmt.Sprintf("Mùa %s, %d ngày (%s – %s): dòng bán gắn mùa này làm chiến dịch chính, trừ hàng trả lại của các dòng đó.",
			c.Name, c.days(), c.StartsOn.Format("02/01/2006"), c.EndsOn.Format("02/01/2006"))
		t.Notes = []string{
			"Mỗi dòng bán chỉ tính cho một mùa; nhãn phụ chỉ dùng để lọc, không cộng.",
			"Lãi gộp chưa trừ chi phí vận hành; điều chỉnh giá vốn sau khi bán không phân bổ theo mùa.",
			"Đóng mùa không khoá sổ: tồn, tiền và công nợ vẫn nằm trong sổ chung.",
			"Đơn còn phải giao và khách còn nợ từ đơn mùa là số còn lại tới hôm nay của đơn bán và chứng từ gắn mùa này.",
		}
		var rows []struct {
			ID   uuid.UUID       `db:"id"`
			Code string          `db:"code"`
			Name string          `db:"display_name"`
			Net  decimal.Decimal `db:"net"`
			Qty  decimal.Decimal `db:"qty"`
			Cogs decimal.Decimal `db:"cogs"`
		}
		if err := sqlx.SelectContext(ctx, q, &rows, seasonFacts+`
			SELECT s.id, s.code, s.display_name, sum(f.net) AS net, sum(f.qty) AS qty, sum(f.cogs) AS cogs
			FROM facts f JOIN skus s ON s.id = f.sku_id
			GROUP BY s.id, s.code, s.display_name
			ORDER BY sum(f.net) DESC, s.code, s.id`, campaignID); err != nil {
			return fmt.Errorf("reporting: season: %w", err)
		}
		totals, err := loadSeasonTotals(ctx, q, campaignID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			margin := r.Net.Sub(r.Cogs)
			t.Rows = append(t.Rows, Row{
				rowID: text(r.ID.String()), "code": text(r.Code), "name": text(r.Name), "qty": dec(r.Qty),
				"net_revenue": dec(r.Net), "cogs": dec(r.Cogs), "gross_margin": dec(margin),
				"margin_percent": percentOf(margin, r.Net), "share": percentOf(r.Net, totals.Net),
			})
		}
		t.Totals = Row{
			"qty": dec(totals.Qty), "net_revenue": dec(totals.Net), "cogs": dec(totals.Cogs),
			"gross_margin": dec(totals.margin()), "margin_percent": percentOf(totals.margin(), totals.Net),
		}
		open, err := loadSeasonOpen(ctx, q, campaignID)
		if err != nil {
			return err
		}
		t.Summary = seasonSummary(c, totals, open)
		if id := p.filterUUID("compare_campaign_id"); id != nil {
			other, err := loadSeasonCampaign(ctx, q, *id, "compare_campaign_id")
			if err != nil {
				return err
			}
			prev, err := loadSeasonTotals(ctx, q, other.ID)
			if err != nil {
				return err
			}
			prevOpen, err := loadSeasonOpen(ctx, q, other.ID)
			if err != nil {
				return err
			}
			before := seasonSummary(other, prev, prevOpen)
			for i := range t.Summary {
				t.Summary[i].Previous = before[i].Value
			}
			t.Notes = append(t.Notes, fmt.Sprintf("So sánh với mùa %s, %d ngày (%s – %s), cùng cách tính.",
				other.Name, other.days(), other.StartsOn.Format("02/01/2006"), other.EndsOn.Format("02/01/2006")))
		}
		surplus, err := seasonSurplus(ctx, q, campaignID)
		if err != nil {
			return err
		}
		t.Sections = []Section{surplus}
		return nil
	})
	return t, err
}

// seasonSummary are the plan-versus-actual figures of a season, in a fixed order so that a
// comparison season lines up with them. The open orders and receivable are balances as of now.
func seasonSummary(c seasonCampaign, t seasonTotals, open seasonOpen) []Metric {
	var achieved *string
	if c.TargetRev != nil {
		achieved = percentOf(t.Net, *c.TargetRev)
	}
	return []Metric{
		metric("net_revenue", "Doanh thu thuần", KindMoney, dec(t.Net)),
		metric("target_revenue", "Doanh thu mục tiêu", KindMoney, optDec(c.TargetRev)),
		metric("revenue_achieved", "Đạt mục tiêu doanh thu", KindPercent, achieved),
		metric("returns_amount", "Hàng trả lại", KindMoney, dec(t.Returned)),
		metric("cogs", "Giá vốn", KindMoney, dec(t.Cogs)),
		metric("gross_margin", "Lãi gộp", KindMoney, dec(t.margin())),
		metric("margin_percent", "Tỷ suất lãi gộp", KindPercent, percentOf(t.margin(), t.Net)),
		metric("target_margin_percent", "Tỷ suất lãi gộp mục tiêu", KindPercent, optDec(c.TargetMargin)),
		metric("budget", "Ngân sách", KindMoney, optDec(c.Budget)),
		metric("open_orders", "Đơn còn phải giao", KindMoney, dec(open.OpenOrders)),
		metric("receivable", "Khách còn nợ từ đơn mùa", KindMoney, dec(open.Receivable)),
		metric("documents", "Số chứng từ bán", KindInt, integer(t.Documents)),
		metric("customers", "Số khách hàng", KindInt, integer(t.Customers)),
		metric("days", "Số ngày", KindInt, integer(int64(c.days()))),
	}
}

// seasonSurplus is the stock left now of the products of a season: those it sold and those
// received for it.
func seasonSurplus(ctx context.Context, q db.Querier, campaignID uuid.UUID) (Section, error) {
	var rows []struct {
		ID     uuid.UUID       `db:"id"`
		Code   string          `db:"code"`
		Name   string          `db:"display_name"`
		OnHand decimal.Decimal `db:"on_hand"`
		Value  decimal.Decimal `db:"stock_value"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `WITH `+seasonSold+`, products AS (
			SELECT CASE WHEN l.line_kind = 'combo_component' THEN pl.sku_id ELSE l.sku_id END AS sku_id
			FROM sold JOIN sales_document_lines l ON l.id = sold.line_id
			LEFT JOIN sales_document_lines pl ON pl.id = l.parent_line_id
			WHERE l.line_kind <> 'mix_pack'
			UNION
			SELECT rl.sku_id FROM campaign_attributions a
			JOIN receipt_lines rl ON rl.receipt_id = a.doc_id AND (a.doc_line_id IS NULL OR a.doc_line_id = rl.id)
			WHERE a.campaign_id = $1 AND a.doc_type = 'receipt'
		)
		SELECT s.id, s.code, s.display_name, sum(ss.on_hand) AS on_hand, sum(ss.stock_value) AS stock_value
		FROM products pr JOIN skus s ON s.id = pr.sku_id JOIN sku_stock ss ON ss.sku_id = s.id
		GROUP BY s.id, s.code, s.display_name
		HAVING sum(ss.on_hand) > 0
		ORDER BY sum(ss.stock_value) DESC, s.code, s.id`, campaignID); err != nil {
		return Section{}, fmt.Errorf("reporting: season surplus: %w", err)
	}
	sec := Section{
		Key: "surplus_stock", Title: "Hàng mùa còn tồn",
		Columns: []Column{
			{Key: "code", Label: "Mã hàng", Kind: KindText},
			{Key: "name", Label: "Hàng", Kind: KindText},
			{Key: "on_hand", Label: "Tồn", Kind: KindQty},
			{Key: "stock_value", Label: "Giá trị tồn", Kind: KindMoney},
		},
		Rows: []Row{},
	}
	var qty, value decimal.Decimal
	for _, r := range rows {
		sec.Rows = append(sec.Rows, Row{
			rowID: text(r.ID.String()), "code": text(r.Code), "name": text(r.Name), "on_hand": dec(r.OnHand), "stock_value": dec(r.Value),
		})
		qty, value = qty.Add(r.OnHand), value.Add(r.Value)
	}
	sec.Totals = Row{"on_hand": dec(qty), "stock_value": dec(value)}
	return sec, nil
}
