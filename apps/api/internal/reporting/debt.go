package reporting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// maxScheduleDays bounds a debt schedule or cash forecast, as the debt ledger does.
const maxScheduleDays = 366

var directionFilter = Filter{Name: "direction", Kind: filterEnum, Values: []string{Receivable, Payable}}

// directionOf is the debt direction of a request, receivable unless asked otherwise.
func directionOf(p Params) string {
	if d := p.filterText("direction"); d != nil {
		return *d
	}
	return Receivable
}

// paymentDirection is the payment direction that settles or prepays a debt direction.
func paymentDirection(direction string) string {
	if direction == Payable {
		return "out"
	}
	return "in"
}

func debtAgingReport() Report {
	return Report{
		Key: ReportDebtAging, Title: "Tuổi nợ", Range: rangeAsOf,
		Filters: []Filter{directionFilter},
		Columns: []Column{
			{Key: "party", Label: "Đối tác", Kind: KindText},
			{Key: "current", Label: "Trong hạn", Kind: KindMoney},
			{Key: "d1_30", Label: "1–30 ngày", Kind: KindMoney},
			{Key: "d31_60", Label: "31–60 ngày", Kind: KindMoney},
			{Key: "over_60", Label: "Trên 60 ngày", Kind: KindMoney},
			{Key: "total", Label: "Tổng nợ", Kind: KindMoney},
		},
		run: runDebtAging,
	}
}

func agingCells(b AgingBuckets) Row {
	return Row{
		"current": amount(b.Current), "d1_30": amount(b.D1To30), "d31_60": amount(b.D31To60),
		"over_60": amount(b.Over60()), "total": amount(b.Total()),
	}
}

func runDebtAging(ctx context.Context, s *Service, scope Scope, p Params) (Table, error) {
	direction := directionOf(p)
	t := Table{
		Scope:    "Nợ phải thu của khách hàng theo số ngày quá hạn tại ngày xem.",
		Notes:    []string{"Tiền ứng trước chưa cấn trừ hiển thị riêng, không trừ vào tuổi nợ.", "Tiền ứng trước là số hiện tại."},
		Personal: []string{"party"},
	}
	if direction == Payable {
		t.Scope = "Nợ phải trả nhà cung cấp theo số ngày quá hạn tại ngày xem."
	}
	aging, err := s.debt.Aging(ctx, scope, direction, p.AsOf)
	if err != nil {
		return Table{}, err
	}
	for _, r := range aging.Rows {
		row := agingCells(r.Buckets)
		row[rowID], row["party"] = text(r.PartyID.String()), text(r.Name)
		t.Rows = append(t.Rows, row)
	}
	t.Totals = agingCells(aging.Totals)
	var advances []struct {
		PartyID uuid.UUID   `db:"party_id"`
		Name    string      `db:"name"`
		Advance money.Money `db:"advance"`
	}
	err = s.read(ctx, scope, func(q db.Querier) error {
		return sqlx.SelectContext(ctx, q, &advances, `
			SELECT pa.id AS party_id, pa.name, SUM(p.amount - p.allocated_amount) AS advance
			FROM payments p JOIN parties pa ON pa.id = p.party_id
			WHERE p.direction = $1 AND p.status = 'confirmed' AND p.allocated_amount < p.amount
			GROUP BY pa.id, pa.name
			ORDER BY pa.name, pa.id`, paymentDirection(direction))
	})
	if err != nil {
		return Table{}, fmt.Errorf("reporting: read advances: %w", err)
	}
	section := Section{
		Key: "advances", Title: "Tiền ứng trước chưa cấn trừ",
		Columns: []Column{{Key: "party", Label: "Đối tác", Kind: KindText}, {Key: "advance", Label: "Còn lại", Kind: KindMoney}},
		Rows:    []Row{},
	}
	var totalAdvance money.Money
	for _, a := range advances {
		section.Rows = append(section.Rows, Row{rowID: text(a.PartyID.String()), "party": text(a.Name), "advance": amount(a.Advance)})
		totalAdvance = totalAdvance.Add(a.Advance)
	}
	section.Totals = Row{"advance": amount(totalAdvance)}
	t.Sections = []Section{section}
	overdue := aging.Totals.Total().Sub(aging.Totals.Current)
	t.Summary = []Metric{
		metric("total", "Tổng nợ", KindMoney, amount(aging.Totals.Total())),
		metric("overdue", "Quá hạn", KindMoney, amount(overdue)),
		metric("over_60", "Quá hạn trên 60 ngày", KindMoney, amount(aging.Totals.Over60())),
		metric("advance", "Tiền ứng trước", KindMoney, amount(totalAdvance)),
	}
	return t, nil
}

func debtScheduleReport() Report {
	return Report{
		Key: ReportDebtSchedule, Title: "Lịch thu – trả nợ", Range: rangeFuture, MaxDays: maxScheduleDays,
		Columns: []Column{
			{Key: "date", Label: "Ngày", Kind: KindDate},
			{Key: "receivable_due", Label: "Đến hạn thu", Kind: KindMoney},
			{Key: "promised", Label: "Khách hẹn trả", Kind: KindMoney},
			{Key: "payable_due", Label: "Đến hạn trả", Kind: KindMoney},
			{Key: "confirmed_in", Label: "Đã thu", Kind: KindMoney},
			{Key: "confirmed_out", Label: "Đã chi", Kind: KindMoney},
		},
		run: runDebtSchedule,
	}
}

func runDebtSchedule(ctx context.Context, s *Service, scope Scope, p Params) (Table, error) {
	t := Table{
		Scope: "Khoản nợ đến hạn theo ngày; tiền khách hẹn trả là dự kiến, không cộng vào tiền đã thu.",
		Notes: []string{"Khoản quá hạn trước ngày bắt đầu hiển thị ở phần tổng quan."},
	}
	f, err := s.debt.Forecast(ctx, scope, p.From, p.To)
	if err != nil {
		return Table{}, err
	}
	var recv, pay, promised, in, out money.Money
	for _, d := range f.Days {
		t.Rows = append(t.Rows, Row{
			"date": day(d.Date), "receivable_due": amount(d.ReceivableDue), "promised": amount(d.Promised),
			"payable_due": amount(d.PayableDue), "confirmed_in": amount(d.ConfirmedIn), "confirmed_out": amount(d.ConfirmedOut),
		})
		recv, pay, promised = recv.Add(d.ReceivableDue), pay.Add(d.PayableDue), promised.Add(d.Promised)
		in, out = in.Add(d.ConfirmedIn), out.Add(d.ConfirmedOut)
	}
	t.Totals = Row{
		"receivable_due": amount(recv), "promised": amount(promised), "payable_due": amount(pay),
		"confirmed_in": amount(in), "confirmed_out": amount(out),
	}
	t.Summary = []Metric{
		metric("overdue_receivable", "Phải thu quá hạn", KindMoney, amount(f.OverdueReceivable)),
		metric("overdue_payable", "Phải trả quá hạn", KindMoney, amount(f.OverduePayable)),
		metric("receivable_due", "Đến hạn thu trong kỳ", KindMoney, amount(recv)),
		metric("payable_due", "Đến hạn trả trong kỳ", KindMoney, amount(pay)),
		metric("promised", "Khách hẹn trả", KindMoney, amount(promised)),
	}
	return t, nil
}

// fundBalances are the balance of each fund at the end of a date: its opening balance and every
// cash entry dated up to it.
const fundBalances = `
	SELECT f.id, f.name, f.kind, f.active,
		f.opening_balance + COALESCE((SELECT SUM(e.amount) FROM cash_entries e
			WHERE e.fund_account_id = f.id AND e.business_date <= $1), 0) AS balance
	FROM fund_accounts f`

type fundBalance struct {
	ID      uuid.UUID   `db:"id"`
	Name    string      `db:"name"`
	Kind    string      `db:"kind"`
	Active  bool        `db:"active"`
	Balance money.Money `db:"balance"`
}

func loadFundBalances(ctx context.Context, q db.Querier, asOf time.Time) ([]fundBalance, error) {
	var out []fundBalance
	if err := sqlx.SelectContext(ctx, q, &out, fundBalances+` ORDER BY f.kind, f.name, f.id`, asOf); err != nil {
		return nil, fmt.Errorf("reporting: read fund balances: %w", err)
	}
	return out, nil
}
