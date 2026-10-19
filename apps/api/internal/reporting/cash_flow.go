package reporting

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

var cashCategoryLabels = map[string]string{
	"sales": "Bán hàng", "customer_deposit": "Khách đặt cọc", "debt_collection": "Thu nợ",
	"supplier_payment": "Trả nhà cung cấp", "supplier_advance": "Ứng trước nhà cung cấp", "refund": "Hoàn tiền",
	"landed_cost": "Chi phí mua hàng", "opening": "Số dư khai báo", "other": "Khác",
}

func cashCategoryLabel(category string) string {
	if l, ok := cashCategoryLabels[category]; ok {
		return l
	}
	return category
}

// entryInflow tells whether a cash entry e counts as money in: a reversal counts on the side of
// the entry it reverses, so reversing a receipt lowers what came in instead of adding an outflow.
const entryInflow = `(CASE WHEN e.reversal_of IS NULL THEN e.amount ELSE -e.amount END) > 0`

func cashFlowReport() Report {
	return Report{
		Key: ReportCashFlow, Title: "Dòng tiền", Range: rangePeriod, MaxDays: maxReportDays,
		Filters: []Filter{{Name: "fund_account_id", Kind: filterUUID}},
		Columns: []Column{
			{Key: "fund", Label: "Quỹ", Kind: KindText},
			{Key: "category", Label: "Loại thu chi", Kind: KindText},
			{Key: "in_amount", Label: "Thu", Kind: KindMoney},
			{Key: "out_amount", Label: "Chi", Kind: KindMoney},
			{Key: "net_amount", Label: "Chênh lệch", Kind: KindMoney},
		},
		run: runCashFlow,
	}
}

func runCashFlow(ctx context.Context, s *Service, scope Scope, p Params) (Table, error) {
	t := Table{
		Scope: "Tiền thực thu, thực chi đã xác nhận trong kỳ theo quỹ và loại; khoản chờ xác nhận chưa tính.",
		Notes: []string{"Đầu kỳ + Thu − Chi = Cuối kỳ cho từng quỹ.", "Bút toán đảo được trừ vào bên của bút toán gốc."},
	}
	fund := p.filterUUID("fund_account_id")
	err := s.read(ctx, scope, func(q db.Querier) error {
		var flows []struct {
			FundID   uuid.UUID   `db:"fund_account_id"`
			Fund     string      `db:"fund"`
			Category string      `db:"category"`
			In       money.Money `db:"in_amount"`
			Out      money.Money `db:"out_amount"`
		}
		if err := sqlx.SelectContext(ctx, q, &flows, `
			SELECT e.fund_account_id, f.name AS fund, e.category,
				COALESCE(SUM(e.amount) FILTER (WHERE `+entryInflow+`), 0) AS in_amount,
				COALESCE(-SUM(e.amount) FILTER (WHERE NOT `+entryInflow+`), 0) AS out_amount
			FROM cash_entries e JOIN fund_accounts f ON f.id = e.fund_account_id
			WHERE e.business_date BETWEEN $1 AND $2 AND ($3::uuid IS NULL OR e.fund_account_id = $3)
			GROUP BY e.fund_account_id, f.name, f.kind, e.category
			ORDER BY f.kind, f.name, e.fund_account_id, e.category`, p.From, p.To, fund); err != nil {
			return fmt.Errorf("reporting: cash flow: %w", err)
		}
		var totalIn, totalOut money.Money
		perFund := map[uuid.UUID][2]money.Money{}
		for _, f := range flows {
			t.Rows = append(t.Rows, Row{
				rowID: text(f.FundID.String()), "fund": text(f.Fund), "category": text(cashCategoryLabel(f.Category)),
				"in_amount": amount(f.In), "out_amount": amount(f.Out), "net_amount": amount(f.In.Sub(f.Out)),
			})
			totalIn, totalOut = totalIn.Add(f.In), totalOut.Add(f.Out)
			io := perFund[f.FundID]
			perFund[f.FundID] = [2]money.Money{io[0].Add(f.In), io[1].Add(f.Out)}
		}
		t.Totals = Row{"in_amount": amount(totalIn), "out_amount": amount(totalOut), "net_amount": amount(totalIn.Sub(totalOut))}

		opening, err := loadFundBalances(ctx, q, p.From.AddDate(0, 0, -1))
		if err != nil {
			return err
		}
		closing, err := loadFundBalances(ctx, q, p.To)
		if err != nil {
			return err
		}
		section := Section{
			Key: "funds", Title: "Số dư quỹ",
			Columns: []Column{
				{Key: "fund", Label: "Quỹ", Kind: KindText},
				{Key: "opening", Label: "Đầu kỳ", Kind: KindMoney},
				{Key: "in_amount", Label: "Thu", Kind: KindMoney},
				{Key: "out_amount", Label: "Chi", Kind: KindMoney},
				{Key: "closing", Label: "Cuối kỳ", Kind: KindMoney},
			},
			Rows: []Row{},
		}
		closingOf := map[uuid.UUID]money.Money{}
		for _, f := range closing {
			closingOf[f.ID] = f.Balance
		}
		var openingTotal, closingTotal money.Money
		for _, f := range opening {
			if fund != nil && f.ID != *fund {
				continue
			}
			io := perFund[f.ID]
			if !f.Active && f.Balance.IsZero() && closingOf[f.ID].IsZero() && io[0].IsZero() && io[1].IsZero() {
				continue
			}
			section.Rows = append(section.Rows, Row{
				rowID: text(f.ID.String()), "fund": text(f.Name), "opening": amount(f.Balance),
				"in_amount": amount(io[0]), "out_amount": amount(io[1]), "closing": amount(closingOf[f.ID]),
			})
			openingTotal, closingTotal = openingTotal.Add(f.Balance), closingTotal.Add(closingOf[f.ID])
		}
		section.Totals = Row{
			"opening": amount(openingTotal), "in_amount": amount(totalIn), "out_amount": amount(totalOut), "closing": amount(closingTotal),
		}
		t.Sections = []Section{section}
		t.Summary = []Metric{
			metric("opening", "Tồn quỹ đầu kỳ", KindMoney, amount(openingTotal)),
			metric("in_amount", "Tổng thu", KindMoney, amount(totalIn)),
			metric("out_amount", "Tổng chi", KindMoney, amount(totalOut)),
			metric("closing", "Tồn quỹ cuối kỳ", KindMoney, amount(closingTotal)),
		}
		return nil
	})
	return t, err
}
