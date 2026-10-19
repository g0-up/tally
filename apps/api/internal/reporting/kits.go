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

// openAssembly are the assembly order statuses that still expect output.
const openAssembly = `('draft', 'reserved', 'in_progress', 'partially_completed')`

func kitsReport() Report {
	return Report{
		Key: ReportKits, Title: "Bộ quà – combo", Range: rangePeriod, MaxDays: maxReportDays,
		Columns: []Column{
			{Key: "code", Label: "Mã hàng", Kind: KindText},
			{Key: "name", Label: "Bộ quà / combo", Kind: KindText},
			{Key: "type", Label: "Loại", Kind: KindText},
			{Key: "assembled_qty", Label: "Đã đóng", Kind: KindQty},
			{Key: "disassembled_qty", Label: "Đã tháo", Kind: KindQty},
			{Key: "sold_qty", Label: "Đã bán", Kind: KindQty},
			{Key: "on_hand", Label: "Tồn", Kind: KindQty},
			{Key: "to_assemble", Label: "Còn phải đóng", Kind: KindQty},
			{Key: "net_revenue", Label: "Doanh thu thuần", Kind: KindMoney},
			{Key: "cogs", Label: "Giá vốn", Kind: KindMoney},
			{Key: "gross_margin", Label: "Lãi gộp", Kind: KindMoney},
		},
		run: runKits,
	}
}

func runKits(ctx context.Context, s *Service, scope Scope, p Params) (Table, error) {
	t := Table{
		Scope: "Bộ quà và combo: đóng, tháo và bán trong kỳ; tồn và lệnh còn mở tính tại hiện tại.",
		Notes: []string{"Combo không đóng sẵn: bán combo xuất thẳng từng thành phần.",
			"Lãi gộp chưa trừ chi phí vận hành; giá vốn bộ quà gồm chi phí đóng gói."},
	}
	err := s.read(ctx, scope, func(q db.Querier) error {
		var rows []struct {
			ID           uuid.UUID       `db:"id"`
			Code         string          `db:"code"`
			Name         string          `db:"display_name"`
			Type         string          `db:"type"`
			Assembled    decimal.Decimal `db:"assembled"`
			Disassembled decimal.Decimal `db:"disassembled"`
			Sold         decimal.Decimal `db:"sold"`
			OnHand       decimal.Decimal `db:"on_hand"`
			ToAssemble   decimal.Decimal `db:"to_assemble"`
			Net          decimal.Decimal `db:"net"`
			Cogs         decimal.Decimal `db:"cogs"`
		}
		if err := sqlx.SelectContext(ctx, q, &rows, salesFacts(salesGroupings[GroupProduct], true, "")+`,
			made AS (
				SELECT r.output_sku_id AS sku_id,
					SUM(CASE WHEN ap.reversal_of IS NULL THEN ap.produced_qty ELSE -ap.produced_qty END) AS qty
				FROM assembly_postings ap
				JOIN assembly_orders o ON o.id = ap.assembly_order_id JOIN kit_recipes r ON r.id = o.kit_recipe_id
				WHERE ap.business_date BETWEEN $1 AND $2 GROUP BY r.output_sku_id
			), broken AS (
				SELECT r.output_sku_id AS sku_id,
					SUM(CASE WHEN x.reversal_of IS NULL THEN x.qty ELSE -x.qty END) AS qty
				FROM disassemblies x
				JOIN assembly_orders o ON o.id = x.assembly_order_id JOIN kit_recipes r ON r.id = o.kit_recipe_id
				WHERE x.business_date BETWEEN $1 AND $2 GROUP BY r.output_sku_id
			), pending AS (
				SELECT r.output_sku_id AS sku_id, SUM(GREATEST(o.planned_qty - o.completed_qty, 0)) AS qty
				FROM assembly_orders o JOIN kit_recipes r ON r.id = o.kit_recipe_id
				WHERE o.status IN `+openAssembly+` GROUP BY r.output_sku_id
			), stock AS (
				SELECT sku_id, SUM(on_hand) AS qty FROM sku_stock GROUP BY sku_id
			)
			SELECT s.id, s.code, s.display_name, pr.type,
				COALESCE(made.qty, 0) AS assembled, COALESCE(broken.qty, 0) AS disassembled, COALESCE(g.qty, 0) AS sold,
				COALESCE(stock.qty, 0) AS on_hand, COALESCE(pending.qty, 0) AS to_assemble,
				COALESCE(g.net, 0) AS net, COALESCE(g.cogs, 0) AS cogs
			FROM skus s
			JOIN products pr ON pr.id = s.product_id AND pr.type IN ('kit', 'combo')
			LEFT JOIN groups g ON g.k = s.id::text
			LEFT JOIN made ON made.sku_id = s.id
			LEFT JOIN broken ON broken.sku_id = s.id
			LEFT JOIN pending ON pending.sku_id = s.id
			LEFT JOIN stock ON stock.sku_id = s.id
			WHERE g.k IS NOT NULL OR made.sku_id IS NOT NULL OR broken.sku_id IS NOT NULL OR pending.sku_id IS NOT NULL
			   OR COALESCE(stock.qty, 0) <> 0
			ORDER BY COALESCE(g.net, 0) DESC, s.code, s.id`, p.From, p.To); err != nil {
			return fmt.Errorf("reporting: kits: %w", err)
		}
		var total [8]decimal.Decimal
		for _, r := range rows {
			kind := "Bộ quà"
			if r.Type == "combo" {
				kind = "Combo"
			}
			margin := r.Net.Sub(r.Cogs)
			t.Rows = append(t.Rows, Row{
				rowID: text(r.ID.String()), "code": text(r.Code), "name": text(r.Name), "type": text(kind),
				"assembled_qty": dec(r.Assembled), "disassembled_qty": dec(r.Disassembled), "sold_qty": dec(r.Sold),
				"on_hand": dec(r.OnHand), "to_assemble": dec(r.ToAssemble), "net_revenue": dec(r.Net), "cogs": dec(r.Cogs),
				"gross_margin": dec(margin),
			})
			for i, v := range []decimal.Decimal{r.Assembled, r.Disassembled, r.Sold, r.OnHand, r.ToAssemble, r.Net, r.Cogs, margin} {
				total[i] = total[i].Add(v)
			}
		}
		t.Totals = Row{
			"assembled_qty": dec(total[0]), "disassembled_qty": dec(total[1]), "sold_qty": dec(total[2]), "on_hand": dec(total[3]),
			"to_assemble": dec(total[4]), "net_revenue": dec(total[5]), "cogs": dec(total[6]), "gross_margin": dec(total[7]),
		}
		t.Summary = []Metric{
			metric("assembled_qty", "Đã đóng trong kỳ", KindQty, dec(total[0])),
			metric("sold_qty", "Đã bán trong kỳ", KindQty, dec(total[2])),
			metric("to_assemble", "Còn phải đóng", KindQty, dec(total[4])),
			metric("gross_margin", "Lãi gộp", KindMoney, dec(total[7])),
		}
		orders, err := openAssemblyOrders(ctx, q)
		if err != nil {
			return err
		}
		missing, err := missingComponents(ctx, q)
		if err != nil {
			return err
		}
		t.Sections = []Section{orders, missing}
		return nil
	})
	return t, err
}

func openAssemblyOrders(ctx context.Context, q db.Querier) (Section, error) {
	var orders []struct {
		ID          uuid.UUID       `db:"id"`
		DocNo       string          `db:"doc_no"`
		Kit         string          `db:"kit"`
		Status      string          `db:"status"`
		PlannedDate *time.Time      `db:"planned_date"`
		Planned     decimal.Decimal `db:"planned_qty"`
		Completed   decimal.Decimal `db:"completed_qty"`
	}
	if err := sqlx.SelectContext(ctx, q, &orders, `
		SELECT o.id, o.doc_no, s.display_name AS kit, o.status, o.planned_date, o.planned_qty, o.completed_qty
		FROM assembly_orders o JOIN kit_recipes r ON r.id = o.kit_recipe_id JOIN skus s ON s.id = r.output_sku_id
		WHERE o.status IN `+openAssembly+`
		ORDER BY o.planned_date NULLS LAST, o.doc_no, o.id`); err != nil {
		return Section{}, fmt.Errorf("reporting: open assembly orders: %w", err)
	}
	labels := map[string]string{
		"draft": "Nháp", "reserved": "Đã giữ hàng", "in_progress": "Đang đóng", "partially_completed": "Đóng một phần",
	}
	sec := Section{
		Key: "open_orders", Title: "Lệnh đóng bộ đang mở",
		Columns: []Column{
			{Key: "doc_no", Label: "Số lệnh", Kind: KindText},
			{Key: "kit", Label: "Bộ quà", Kind: KindText},
			{Key: "status", Label: "Trạng thái", Kind: KindText},
			{Key: "planned_date", Label: "Ngày dự kiến", Kind: KindDate},
			{Key: "planned_qty", Label: "Kế hoạch", Kind: KindQty},
			{Key: "completed_qty", Label: "Đã đóng", Kind: KindQty},
			{Key: "progress", Label: "Tiến độ", Kind: KindPercent},
		},
		Rows: []Row{},
	}
	for _, o := range orders {
		sec.Rows = append(sec.Rows, Row{
			rowID: text(o.ID.String()), "doc_no": text(o.DocNo), "kit": text(o.Kit), "status": text(labels[o.Status]),
			"planned_date": optDay(o.PlannedDate), "planned_qty": dec(o.Planned), "completed_qty": dec(o.Completed),
			"progress": percentOf(o.Completed, o.Planned),
		})
	}
	return sec, nil
}

// missingComponents are the components the open assembly orders still need beyond the sellable
// stock of every warehouse.
func missingComponents(ctx context.Context, q db.Querier) (Section, error) {
	var rows []struct {
		ID        uuid.UUID       `db:"id"`
		Code      string          `db:"code"`
		Name      string          `db:"display_name"`
		Needed    decimal.Decimal `db:"needed"`
		Available decimal.Decimal `db:"available"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		WITH need AS (
			SELECT i.sku_id, SUM(GREATEST(i.planned_base_qty - i.consumed_base_qty, 0)) AS qty
			FROM assembly_order_items i JOIN assembly_orders o ON o.id = i.assembly_order_id
			WHERE o.status IN `+openAssembly+` GROUP BY i.sku_id
		)
		SELECT s.id, s.code, s.display_name, need.qty AS needed,
			COALESCE((SELECT SUM(ss.sellable) FROM sku_stock ss WHERE ss.sku_id = s.id), 0) AS available
		FROM need JOIN skus s ON s.id = need.sku_id
		WHERE need.qty > COALESCE((SELECT SUM(ss.sellable) FROM sku_stock ss WHERE ss.sku_id = s.id), 0)
		ORDER BY s.code, s.id`); err != nil {
		return Section{}, fmt.Errorf("reporting: missing components: %w", err)
	}
	sec := Section{
		Key: "missing_components", Title: "Thành phần còn thiếu",
		Columns: []Column{
			{Key: "code", Label: "Mã hàng", Kind: KindText},
			{Key: "name", Label: "Thành phần", Kind: KindText},
			{Key: "needed", Label: "Cần", Kind: KindQty},
			{Key: "available", Label: "Tồn bán được", Kind: KindQty},
			{Key: "missing", Label: "Thiếu", Kind: KindQty},
		},
		Rows: []Row{},
	}
	for _, r := range rows {
		sec.Rows = append(sec.Rows, Row{
			rowID: text(r.ID.String()), "code": text(r.Code), "name": text(r.Name), "needed": dec(r.Needed),
			"available": dec(r.Available), "missing": dec(r.Needed.Sub(r.Available)),
		})
	}
	return sec, nil
}
