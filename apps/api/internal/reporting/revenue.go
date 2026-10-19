package reporting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
)

// Revenue groupings.
const (
	GroupProduct  = "product"
	GroupCustomer = "customer"
	GroupChannel  = "channel"
	GroupDocument = "document"
)

// filterCustomerIDs narrows the revenue to a list of customers (the customer list's "Doanh thu"
// column reads one page of customers at a time).
const filterCustomerIDs = "customer_ids"

// maxCustomerIDs bounds the customers of one revenue run.
const maxCustomerIDs = 100

// walkInKey groups the sales without a customer. It is not the nil UUID, which a cursor rejects.
const walkInKey = "ffffffff-ffff-ffff-ffff-ffffffffffff"

var channelLabels = map[string]string{
	"stock_out": "Đơn hàng", "pos": "POS", "pos_offline": "POS", "exchange": "Đổi hàng", "unlinked": "Trả hàng không chứng từ",
}

// salesGrouping is how the sales facts group: the key of a sold line and of a returned line.
type salesGrouping struct {
	saleKey, returnKey string
}

var salesGroupings = map[string]salesGrouping{
	// A combo component and its return count on the combo; a mix flavor counts on itself.
	GroupProduct: {
		saleKey:   `(CASE WHEN l.line_kind = 'combo_component' THEN pl.sku_id ELSE l.sku_id END)::text`,
		returnKey: `(CASE WHEN l.line_kind = 'combo_component' THEN pl.sku_id ELSE rl.sku_id END)::text`,
	},
	GroupCustomer: {
		saleKey:   `COALESCE(d.customer_party_id::text, '` + walkInKey + `')`,
		returnKey: `COALESCE(r.customer_party_id::text, '` + walkInKey + `')`,
	},
	GroupChannel: {
		saleKey:   `CASE WHEN d.source = 'pos_offline' THEN 'pos' ELSE d.source END`,
		returnKey: `COALESCE(CASE WHEN d.source = 'pos_offline' THEN 'pos' ELSE d.source END, 'unlinked')`,
	},
	// A return counts on the sale it returns; a return without a sale is its own document.
	GroupDocument: {
		saleKey:   `d.id::text`,
		returnKey: `COALESCE(r.sales_document_id, r.id)::text`,
	},
}

// returnNet is the revenue a return line rl takes back before tax. Return lines carry no tax: the
// share of tax comes from the sold line l, else its combo line pl, else the whole document d.
const returnNet = `rl.line_amount - round(rl.line_amount * (1 - COALESCE(
	(l.line_amount - l.tax_amount) / NULLIF(l.line_amount, 0),
	(pl.line_amount - pl.tax_amount) / NULLIF(pl.line_amount, 0),
	(d.total_amount - d.tax_amount) / NULLIF(d.total_amount, 0), 1)))`

// returnedQty is the quantity a return line rl takes back from the product of its sold line: a
// returned combo component counts as its share of combos.
const returnedQty = `CASE WHEN l.line_kind = 'combo_component' THEN rl.base_qty * pl.base_qty / NULLIF(l.base_qty, 0) ELSE rl.base_qty END`

// salesFacts is a CTE "facts" of the revenue, cost and quantity each sold or returned line adds to
// its group key, over posted documents dated $1..$2 (reversed documents and reversals are out).
// Revenue is before tax and comes from the priced lines only: a mix pack header repeats its
// flavors and a combo component is priced on its combo line. Cost comes from the lines that moved
// stock. A return takes back its revenue net of the sold line's tax share and its cost at the
// return's unit cost. With adjustments, later cost corrections of goods already sold join the
// product they concern. Invoice and order totals are never read. customers, when not empty, is the
// placeholder of a uuid[] that keeps the sales and returns of those customers only.
func salesFacts(g salesGrouping, adjustments bool, customers string) string {
	sold, returned := "", ""
	if customers != "" {
		sold = ` AND d.customer_party_id = ANY (` + customers + `::uuid[])`
		returned = ` AND r.customer_party_id = ANY (` + customers + `::uuid[])`
	}
	q := `WITH facts AS (
		SELECT ` + g.saleKey + ` AS k, d.id AS doc_id,
			CASE WHEN l.line_kind NOT IN ('mix_pack', 'combo_component') THEN l.line_amount - l.tax_amount ELSE 0 END AS net,
			CASE WHEN l.line_kind NOT IN ('mix_pack', 'combo_component') THEN l.discount_amount + l.order_discount_alloc ELSE 0 END AS discount,
			0::numeric AS returned,
			CASE WHEN l.line_kind NOT IN ('mix_pack', 'combo_component') THEN l.base_qty ELSE 0 END AS qty,
			l.cogs_amount AS cogs
		FROM sales_document_lines l
		JOIN sales_documents d ON d.id = l.sales_document_id
		LEFT JOIN sales_document_lines pl ON pl.id = l.parent_line_id
		WHERE d.business_date BETWEEN $1 AND $2 AND d.status IN ('posted', 'partially_reversed') AND d.reversal_of IS NULL
		  AND l.line_kind <> 'mix_pack'` + sold + `
		UNION ALL
		SELECT ` + g.returnKey + `, NULL, -ret.net, 0, ret.net,
			-(` + returnedQty + `),
			-round(rl.unit_cost * rl.base_qty, 4)
		FROM sales_return_lines rl
		JOIN sales_returns r ON r.id = rl.sales_return_id
		LEFT JOIN sales_document_lines l ON l.id = rl.sales_document_line_id
		LEFT JOIN sales_document_lines pl ON pl.id = l.parent_line_id
		LEFT JOIN sales_documents d ON d.id = r.sales_document_id
		CROSS JOIN LATERAL (SELECT ` + returnNet + ` AS net) ret
		WHERE r.business_date BETWEEN $1 AND $2 AND r.status = 'posted' AND r.reversal_of IS NULL` + returned
	if adjustments {
		q += `
		UNION ALL
		SELECT a.sku_id::text, NULL, 0, 0, 0, 0, a.amount
		FROM cogs_adjustments a WHERE a.business_date BETWEEN $1 AND $2`
	}
	return q + `
	), groups AS (
		SELECT k, count(DISTINCT doc_id) AS documents, sum(net) AS net, sum(discount) AS discount,
			sum(returned) AS returned, sum(qty) AS qty, sum(cogs) AS cogs
		FROM facts WHERE k IS NOT NULL GROUP BY k
	)`
}

// salesGroup is one group of the sales facts with the totals of every group, which the statement
// reading the groups computes once.
type salesGroup struct {
	Key       string          `db:"k"`
	Code      *string         `db:"code"`
	Name      *string         `db:"name"`
	Documents int64           `db:"documents"`
	Net       decimal.Decimal `db:"net"`
	Discount  decimal.Decimal `db:"discount"`
	Returned  decimal.Decimal `db:"returned"`
	Qty       decimal.Decimal `db:"qty"`
	Cogs      decimal.Decimal `db:"cogs"`
	salesTotals
}

func (g salesGroup) margin() decimal.Decimal { return g.Net.Sub(g.Cogs) }

// salesTotals are the totals of every group; documents counts each sale once.
type salesTotals struct {
	Documents int64           `db:"t_documents"`
	Net       decimal.Decimal `db:"t_net"`
	Discount  decimal.Decimal `db:"t_discount"`
	Returned  decimal.Decimal `db:"t_returned"`
	Qty       decimal.Decimal `db:"t_qty"`
	Cogs      decimal.Decimal `db:"t_cogs"`
}

// cursorAmount is the amount sort key of a cursor of a report ordered by an amount.
func cursorAmount(c *httpx.Cursor) (string, string, error) {
	if _, err := decimal.NewFromString(c.SortKey); err != nil {
		return "", "", apperr.NewValidation("invalid_cursor", "The cursor is not valid", map[string]string{"cursor": "invalid"})
	}
	return c.SortKey, c.ID.String(), nil
}

// salesNames are the code and name columns of a group g and the joins they need. Product and
// customer groups carry their code and name, a document its number and customer.
func salesNames(group string) (cols, joins string) {
	switch group {
	case GroupProduct:
		return `s.code, s.display_name AS name`, `JOIN skus s ON s.id = g.k::uuid`
	case GroupCustomer:
		return `p.phone AS code, p.name`, `LEFT JOIN parties p ON p.id = g.k::uuid`
	case GroupDocument:
		return `COALESCE(sd.doc_no, sr.doc_no) AS code, p.name`, `
			LEFT JOIN sales_documents sd ON sd.id = g.k::uuid
			LEFT JOIN sales_returns sr ON sr.id = g.k::uuid
			LEFT JOIN parties p ON p.id = COALESCE(sd.customer_party_id, sr.customer_party_id)`
	default:
		return `NULL::text AS code, NULL::text AS name`, ``
	}
}

// loadSalesGroups reads the groups by descending sort expression (over the groups CTE g), one
// page of them when paged, and hands each to emit in order. One statement reads the groups and
// the totals of every group, so the facts are aggregated once per run. An export (p.sink) reads
// every group. The totals are nil when no group is left to carry them on a later page.
func loadSalesGroups(ctx context.Context, q db.Querier, group string, adjustments bool, sortExpr string, p Params, paged bool,
	emit func(salesGroup) error) (*httpx.Cursor, *salesTotals, error) {
	cols, joins := salesNames(group)
	limit := any(nil)
	if paged {
		limit = p.pageLimit()
	}
	args := []any{p.From, p.To, limit}
	customers := ""
	if ids := p.filterUUIDs(filterCustomerIDs); len(ids) > 0 {
		args = append(args, ids)
		customers = "$4"
	}
	query := salesFacts(salesGroupings[group], adjustments, customers) + `, totals AS (
		SELECT (SELECT count(DISTINCT doc_id) FROM facts WHERE k IS NOT NULL) AS t_documents,
			COALESCE(sum(net), 0) AS t_net, COALESCE(sum(discount), 0) AS t_discount,
			COALESCE(sum(returned), 0) AS t_returned, COALESCE(sum(qty), 0) AS t_qty, COALESCE(sum(cogs), 0) AS t_cogs
		FROM groups
	)
	SELECT g.k, g.documents, g.net, g.discount, g.returned, g.qty, g.cogs, ` + cols + `, t.*
	FROM groups g CROSS JOIN totals t ` + joins + `
	WHERE TRUE`
	if p.After != nil && p.sink == nil {
		key, id, err := cursorAmount(p.After)
		if err != nil {
			return nil, nil, err
		}
		k, i := fmt.Sprintf("$%d", len(args)+1), fmt.Sprintf("$%d", len(args)+2)
		query += ` AND (` + sortExpr + ` < ` + k + `::numeric OR (` + sortExpr + ` = ` + k + `::numeric AND g.k > ` + i + `))`
		args = append(args, key, id)
	}
	query += `
	ORDER BY ` + sortExpr + ` DESC, g.k
	LIMIT $3`
	var rows []salesGroup
	var totals *salesTotals
	err := eachRow(ctx, q, query, args, func(g salesGroup) error {
		if totals == nil {
			t := g.salesTotals
			totals = &t
		}
		if p.sink != nil || !paged {
			return emit(g)
		}
		rows = append(rows, g)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("reporting: sales groups: %w", err)
	}
	if totals == nil && p.firstPage() {
		totals = &salesTotals{}
	}
	if p.sink != nil || !paged {
		return nil, totals, nil
	}
	page, next := pageOf(rows, p.Limit, func(g salesGroup) httpx.Cursor {
		key := g.Net
		if sortExpr != "g.net" {
			key = g.margin()
		}
		return httpx.Cursor{SortKey: key.String(), ID: uuid.MustParse(g.Key)}
	})
	for _, g := range page {
		if err := emit(g); err != nil {
			return nil, nil, err
		}
	}
	return next, totals, nil
}

func revenueReport() Report {
	return Report{
		Key: ReportRevenue, Title: "Doanh thu", Range: rangePeriod, MaxDays: maxReportDays, Paged: true,
		GroupBys: []string{GroupProduct, GroupCustomer, GroupChannel, GroupDocument},
		Filters:  []Filter{{Name: filterCustomerIDs, Kind: filterUUIDList, Max: maxCustomerIDs}},
		Columns: []Column{
			{Key: "code", Label: "Mã", Kind: KindText},
			{Key: "name", Label: "Tên", Kind: KindText},
			{Key: "documents", Label: "Số chứng từ bán", Kind: KindInt},
			{Key: "qty", Label: "Số lượng", Kind: KindQty},
			{Key: "sales_amount", Label: "Doanh thu bán", Kind: KindMoney},
			{Key: "discount_amount", Label: "Chiết khấu", Kind: KindMoney},
			{Key: "returns_amount", Label: "Hàng trả lại", Kind: KindMoney},
			{Key: "net_revenue", Label: "Doanh thu thuần", Kind: KindMoney},
			{Key: "share", Label: "Tỷ trọng", Kind: KindPercent},
		},
		run: runRevenue,
	}
}

// groupLabel names a group key: a product, customer or document by its row, a channel by its
// label. A document without a customer is a walk-in sale.
func groupLabel(group string, g salesGroup) (code, name *string, id *string) {
	switch group {
	case GroupDocument:
		if g.Name == nil {
			return optText(g.Code), text("Khách lẻ"), text(g.Key)
		}
	case GroupChannel:
		label := channelLabels[g.Key]
		if label == "" {
			label = g.Key
		}
		return nil, text(label), nil
	case GroupCustomer:
		if g.Key == walkInKey {
			return nil, text("Khách lẻ"), nil
		}
	}
	return optText(g.Code), optText(g.Name), text(g.Key)
}

func runRevenue(ctx context.Context, s *Service, scope Scope, p Params) (Table, error) {
	t := Table{
		Scope: "Doanh thu trước thuế của chứng từ bán đã ghi sổ trong kỳ, trừ hàng trả lại trong kỳ; không tính tổng đơn đặt hàng hay hoá đơn.",
		Notes: []string{"Combo tính trên dòng combo; thùng mix tính theo từng vị.", "Khách lẻ gộp chung một dòng."},
	}
	switch p.GroupBy {
	case GroupCustomer:
		t.Personal = []string{"code", "name"}
	case GroupDocument:
		t.Personal = []string{"name"}
		t.Notes = []string{t.Notes[0], "Hàng trả lại tính vào chứng từ bán gốc; phiếu trả không chứng từ là một dòng riêng."}
	}
	paged := p.GroupBy != GroupChannel
	err := s.read(ctx, scope, func(q db.Querier) error {
		next, totals, err := loadSalesGroups(ctx, q, p.GroupBy, false, "g.net", p, paged, func(g salesGroup) error {
			code, name, id := groupLabel(p.GroupBy, g)
			row := Row{
				"code": code, "name": name, "documents": integer(g.Documents),
				"sales_amount": dec(g.Net.Add(g.Returned)), "discount_amount": dec(g.Discount),
				"returns_amount": dec(g.Returned), "net_revenue": dec(g.Net), "share": percentOf(g.Net, g.salesTotals.Net),
			}
			if id != nil {
				row[rowID] = id
			}
			if p.GroupBy == GroupProduct {
				row["qty"] = dec(g.Qty)
			}
			return t.add(p, row)
		})
		if err != nil {
			return err
		}
		t.Next = next
		if totals == nil {
			return nil
		}
		t.Totals = Row{
			"documents": integer(totals.Documents), "sales_amount": dec(totals.Net.Add(totals.Returned)),
			"discount_amount": dec(totals.Discount), "returns_amount": dec(totals.Returned), "net_revenue": dec(totals.Net),
		}
		if p.GroupBy == GroupProduct {
			t.Totals["qty"] = dec(totals.Qty)
		}
		avg := decimal.Zero
		if totals.Documents > 0 {
			avg = totals.Net.Div(decimal.NewFromInt(totals.Documents)).Round(0)
		}
		t.Summary = []Metric{
			metric("net_revenue", "Doanh thu thuần", KindMoney, dec(totals.Net)),
			metric("returns_amount", "Hàng trả lại", KindMoney, dec(totals.Returned)),
			metric("documents", "Số chứng từ bán", KindInt, integer(totals.Documents)),
			metric("average_document", "Bình quân mỗi chứng từ", KindMoney, dec(avg)),
		}
		return nil
	})
	return t, err
}

func grossMarginReport() Report {
	return Report{
		Key: ReportGrossMargin, Title: "Lãi gộp", Range: rangePeriod, MaxDays: maxReportDays, Paged: true,
		Columns: []Column{
			{Key: "code", Label: "Mã hàng", Kind: KindText},
			{Key: "name", Label: "Hàng", Kind: KindText},
			{Key: "qty", Label: "Số lượng", Kind: KindQty},
			{Key: "net_revenue", Label: "Doanh thu thuần", Kind: KindMoney},
			{Key: "cogs", Label: "Giá vốn", Kind: KindMoney},
			{Key: "gross_margin", Label: "Lãi gộp", Kind: KindMoney},
			{Key: "margin_percent", Label: "Tỷ suất lãi gộp", Kind: KindPercent},
		},
		run: runGrossMargin,
	}
}

func runGrossMargin(ctx context.Context, s *Service, scope Scope, p Params) (Table, error) {
	t := Table{
		Scope: "Lãi gộp = doanh thu thuần − giá vốn hàng bán (gồm điều chỉnh giá vốn hàng đã bán), theo mã hàng; combo tính trên mã combo.",
		Notes: []string{"Lãi gộp chưa trừ chi phí vận hành."},
	}
	err := s.read(ctx, scope, func(q db.Querier) error {
		next, totals, err := loadSalesGroups(ctx, q, GroupProduct, true, "(g.net - g.cogs)", p, true, func(g salesGroup) error {
			return t.add(p, Row{
				rowID: text(g.Key), "code": optText(g.Code), "name": optText(g.Name), "qty": dec(g.Qty),
				"net_revenue": dec(g.Net), "cogs": dec(g.Cogs), "gross_margin": dec(g.margin()),
				"margin_percent": percentOf(g.margin(), g.Net),
			})
		})
		if err != nil {
			return err
		}
		t.Next = next
		if totals != nil {
			margin := totals.Net.Sub(totals.Cogs)
			t.Totals = Row{
				"qty": dec(totals.Qty), "net_revenue": dec(totals.Net), "cogs": dec(totals.Cogs), "gross_margin": dec(margin),
				"margin_percent": percentOf(margin, totals.Net),
			}
			t.Summary = []Metric{
				metric("net_revenue", "Doanh thu thuần", KindMoney, dec(totals.Net)),
				metric("cogs", "Giá vốn", KindMoney, dec(totals.Cogs)),
				metric("gross_margin", "Lãi gộp", KindMoney, dec(margin)),
				metric("margin_percent", "Tỷ suất lãi gộp", KindPercent, percentOf(margin, totals.Net)),
			}
		}
		pending, err := pendingRecost(ctx, q, p.To)
		if err != nil {
			return err
		}
		if pending {
			t.Provisional = true
			t.Notes = append(t.Notes, "Có đợt tính lại giá vốn chưa áp dụng ảnh hưởng kỳ này; số liệu là tạm tính.")
		}
		return nil
	})
	return t, err
}

// pendingRecost reports whether a cost recalculation awaiting confirmation or application starts
// on or before to, so that it may still change the cost of the period.
func pendingRecost(ctx context.Context, q db.Querier, to time.Time) (bool, error) {
	var pending bool
	if err := q.QueryRowxContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM cost_recalculation_runs WHERE status IN ('previewed', 'confirmed') AND from_date <= $1)`, to).
		Scan(&pending); err != nil {
		return false, fmt.Errorf("reporting: read pending cost recalculations: %w", err)
	}
	return pending, nil
}
