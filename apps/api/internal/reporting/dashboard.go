package reporting

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// Dashboard limits.
const (
	dashboardTopParties = 5
	dashboardEntries    = 100
	dueSoonDays         = 7
)

// Dashboard is the Thu–chi – Công nợ overview of one period, with the previous period beside each
// figure and a chart series per bucket.
type Dashboard struct {
	Period   Period
	Today    time.Time
	Previous Period
	Next     Period
	Cash     CashCard
	Debt     DebtCard
	Series   []SeriesPoint
	// Entries are the cash entries of a one-day period, newest first; empty for longer periods.
	Entries []DashboardEntry
}

// CashCard is money in and out of the period, opening declarations excluded, and the balance of
// the funds at its end.
type CashCard struct {
	In, Out, Net, Balance                 money.Money
	PrevIn, PrevOut, PrevNet, PrevBalance money.Money
	Funds                                 []FundBalance
	// ByCategory splits the money in and out of the period by kind, largest first.
	ByCategory []CategoryFlow
	Link       string
}

// CategoryFlow is the money in and out of one kind of cash entry over the period.
type CategoryFlow struct {
	Category, Label string
	In, Out         money.Money
}

// FundBalance is the balance of one fund at the end of the period.
type FundBalance struct {
	ID      uuid.UUID
	Name    string
	Kind    string
	Balance money.Money
}

// DebtSide is one ledger of the debt card: the balance at the end of the period, what the period
// added (opening balances and reversed documents excluded) and what it settled.
type DebtSide struct {
	Balance, Increase, Settled             money.Money
	PrevBalance, PrevIncrease, PrevSettled money.Money
	// DueSoon falls due in the seven days from today; Overdue is already past due.
	DueSoon, Overdue money.Money
	Top              []PartyBalance
	Link             string
}

// PartyBalance is the balance of one party and the earliest due date of what is unpaid.
type PartyBalance struct {
	PartyID uuid.UUID
	Name    string
	Amount  money.Money
	Overdue money.Money
	NextDue time.Time
}

// DebtCard is what customers owe the store and what the store owes suppliers.
type DebtCard struct {
	Receivable DebtSide
	Payable    DebtSide
	DueLink    string
}

// SeriesPoint is one chart bar.
type SeriesPoint struct {
	Label    string
	From, To time.Time
	In, Out  money.Money
}

// DashboardEntry is one cash entry of the day.
type DashboardEntry struct {
	ID       uuid.UUID
	DocNo    *string
	Party    *string
	Fund     string
	Category string
	Amount   money.Money
	Reversal bool
}

// Dashboard builds the dashboard of the period of kind holding date; a zero date is today.
func (s *Service) Dashboard(ctx context.Context, scope Scope, kind string, date *time.Time) (Dashboard, error) {
	today, err := s.Today(ctx, scope)
	if err != nil {
		return Dashboard{}, err
	}
	at := today
	if date != nil {
		at = *date
	}
	period, err := NewPeriod(kind, at)
	if err != nil {
		return Dashboard{}, err
	}
	prev := period.Prev()
	out := Dashboard{Period: period, Today: today, Previous: prev, Next: period.Next()}
	balanceAt := func(p Period) time.Time {
		if p.To.After(today) {
			return today
		}
		return p.To
	}
	err = s.read(ctx, scope, func(q db.Querier) error {
		if out.Cash, err = cashCard(ctx, q, period, prev, balanceAt(period), balanceAt(prev)); err != nil {
			return err
		}
		if out.Series, err = cashSeries(ctx, q, period); err != nil {
			return err
		}
		out.Entries = []DashboardEntry{}
		if period.Kind == PeriodDay {
			if out.Entries, err = dayEntries(ctx, q, period.From); err != nil {
				return err
			}
		}
		for _, side := range []struct {
			direction string
			into      *DebtSide
		}{{Receivable, &out.Debt.Receivable}, {Payable, &out.Debt.Payable}} {
			if *side.into, err = debtMovement(ctx, q, side.direction, period, prev); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Dashboard{}, err
	}
	for _, side := range []struct {
		direction string
		into      *DebtSide
	}{{Receivable, &out.Debt.Receivable}, {Payable, &out.Debt.Payable}} {
		if err := s.debtBalances(ctx, scope, side.direction, balanceAt(period), balanceAt(prev), side.into); err != nil {
			return Dashboard{}, err
		}
	}
	f, err := s.debt.Forecast(ctx, scope, today, today.AddDate(0, 0, dueSoonDays-1))
	if err != nil {
		return Dashboard{}, err
	}
	for _, d := range f.Days {
		out.Debt.Receivable.DueSoon = out.Debt.Receivable.DueSoon.Add(d.ReceivableDue)
		out.Debt.Payable.DueSoon = out.Debt.Payable.DueSoon.Add(d.PayableDue)
	}
	out.Debt.Receivable.Overdue, out.Debt.Payable.Overdue = f.OverdueReceivable, f.OverduePayable
	from, to := period.From.Format(time.DateOnly), period.To.Format(time.DateOnly)
	out.Cash.Link = "/reports/cash-flow?from=" + from + "&to=" + to
	out.Debt.Receivable.Link = "/reports/debt-aging?direction=receivable"
	out.Debt.Payable.Link = "/reports/debt-aging?direction=payable"
	out.Debt.DueLink = "/reports/debt-schedule"
	return out, nil
}

// cashTotals sums the money in and out of a date range: a reversal counts on the side of the entry
// it reverses, and opening declarations are not movement.
const cashTotals = `
	SELECT COALESCE(SUM(e.amount) FILTER (WHERE ` + entryInflow + `), 0) AS in_amount,
		COALESCE(-SUM(e.amount) FILTER (WHERE NOT ` + entryInflow + `), 0) AS out_amount
	FROM cash_entries e
	WHERE e.category <> 'opening' AND e.business_date BETWEEN $1 AND $2`

type inOut struct {
	In  money.Money `db:"in_amount"`
	Out money.Money `db:"out_amount"`
}

func readInOut(ctx context.Context, q db.Querier, from, to time.Time) (inOut, error) {
	var io inOut
	if err := q.QueryRowxContext(ctx, cashTotals, from, to).StructScan(&io); err != nil {
		return inOut{}, fmt.Errorf("reporting: read cash totals: %w", err)
	}
	return io, nil
}

func cashCard(ctx context.Context, q db.Querier, p, prev Period, at, prevAt time.Time) (CashCard, error) {
	cur, err := readInOut(ctx, q, p.From, p.To)
	if err != nil {
		return CashCard{}, err
	}
	before, err := readInOut(ctx, q, prev.From, prev.To)
	if err != nil {
		return CashCard{}, err
	}
	funds, err := loadFundBalances(ctx, q, at)
	if err != nil {
		return CashCard{}, err
	}
	prevFunds, err := loadFundBalances(ctx, q, prevAt)
	if err != nil {
		return CashCard{}, err
	}
	byCategory, err := cashByCategory(ctx, q, p)
	if err != nil {
		return CashCard{}, err
	}
	c := CashCard{
		In: cur.In, Out: cur.Out, Net: cur.In.Sub(cur.Out),
		PrevIn: before.In, PrevOut: before.Out, PrevNet: before.In.Sub(before.Out),
		Funds: []FundBalance{}, ByCategory: byCategory,
	}
	for _, f := range funds {
		c.Balance = c.Balance.Add(f.Balance)
		if f.Active || !f.Balance.IsZero() {
			c.Funds = append(c.Funds, FundBalance{ID: f.ID, Name: f.Name, Kind: f.Kind, Balance: f.Balance})
		}
	}
	for _, f := range prevFunds {
		c.PrevBalance = c.PrevBalance.Add(f.Balance)
	}
	return c, nil
}

// cashByCategory is the money in and out of the period by kind, as cashTotals counts it.
func cashByCategory(ctx context.Context, q db.Querier, p Period) ([]CategoryFlow, error) {
	var rows []struct {
		Category string `db:"category"`
		inOut
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT e.category,
			COALESCE(SUM(e.amount) FILTER (WHERE `+entryInflow+`), 0) AS in_amount,
			COALESCE(-SUM(e.amount) FILTER (WHERE NOT `+entryInflow+`), 0) AS out_amount
		FROM cash_entries e
		WHERE e.category <> 'opening' AND e.business_date BETWEEN $1 AND $2
		GROUP BY e.category
		ORDER BY e.category`, p.From, p.To); err != nil {
		return nil, fmt.Errorf("reporting: read cash by category: %w", err)
	}
	out := make([]CategoryFlow, 0, len(rows))
	for _, r := range rows {
		if r.In.IsZero() && r.Out.IsZero() {
			continue
		}
		out = append(out, CategoryFlow{Category: r.Category, Label: cashCategoryLabel(r.Category), In: r.In, Out: r.Out})
	}
	slices.SortStableFunc(out, func(a, b CategoryFlow) int { return b.In.Add(b.Out).Cmp(a.In.Add(a.Out)) })
	return out, nil
}

func cashSeries(ctx context.Context, q db.Querier, p Period) ([]SeriesPoint, error) {
	unit := "day"
	if p.Kind == PeriodYear {
		unit = "month"
	}
	var rows []struct {
		Bucket time.Time `db:"bucket"`
		inOut
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT date_trunc('`+unit+`', e.business_date)::date AS bucket,
			COALESCE(SUM(e.amount) FILTER (WHERE `+entryInflow+`), 0) AS in_amount,
			COALESCE(-SUM(e.amount) FILTER (WHERE NOT `+entryInflow+`), 0) AS out_amount
		FROM cash_entries e
		WHERE e.category <> 'opening' AND e.business_date BETWEEN $1 AND $2
		GROUP BY 1`, p.From, p.To); err != nil {
		return nil, fmt.Errorf("reporting: read cash series: %w", err)
	}
	byBucket := make(map[time.Time]inOut, len(rows))
	for _, r := range rows {
		byBucket[r.Bucket.UTC()] = r.inOut
	}
	buckets := p.Buckets()
	out := make([]SeriesPoint, len(buckets))
	for i, b := range buckets {
		io := byBucket[b.From]
		out[i] = SeriesPoint{Label: b.Label, From: b.From, To: b.To, In: io.In, Out: io.Out}
	}
	return out, nil
}

func dayEntries(ctx context.Context, q db.Querier, date time.Time) ([]DashboardEntry, error) {
	var rows []struct {
		ID       uuid.UUID   `db:"id"`
		DocNo    *string     `db:"doc_no"`
		Party    *string     `db:"party"`
		Fund     string      `db:"fund"`
		Category string      `db:"category"`
		Amount   money.Money `db:"amount"`
		Reversal bool        `db:"reversal"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT e.id, p.doc_no, pa.name AS party, f.name AS fund, e.category, e.amount, e.reversal_of IS NOT NULL AS reversal
		FROM cash_entries e
		JOIN fund_accounts f ON f.id = e.fund_account_id
		LEFT JOIN payments p ON p.id = e.payment_id
		LEFT JOIN parties pa ON pa.id = p.party_id
		WHERE e.business_date = $1 AND e.category <> 'opening'
		ORDER BY e.posted_at DESC, e.id
		LIMIT $2`, date, dashboardEntries); err != nil {
		return nil, fmt.Errorf("reporting: read cash entries: %w", err)
	}
	out := make([]DashboardEntry, len(rows))
	for i, r := range rows {
		out[i] = DashboardEntry{
			ID: r.ID, DocNo: r.DocNo, Party: r.Party, Fund: r.Fund, Category: cashCategoryLabel(r.Category),
			Amount: r.Amount, Reversal: r.Reversal,
		}
	}
	return out, nil
}

// debtMoves sums what one ledger ($1) added and settled over a date range ($2..$3). Opening
// balances are not movement, and a reversed document takes back what it added; settlements count
// net of their own reversals.
const debtMoves = `
	SELECT
		COALESCE((SELECT SUM(o.amount) FROM obligations o
			WHERE o.direction = $1 AND o.source_type <> 'opening_balance' AND o.business_date BETWEEN $2 AND $3), 0)
		- COALESCE((SELECT SUM(s.amount) FROM obligation_settlements s JOIN obligations o ON o.id = s.obligation_id
			WHERE o.direction = $1 AND o.source_type <> 'opening_balance' AND s.settled_by_type = 'obligation_reversal'
			  AND s.business_date BETWEEN $2 AND $3), 0) AS increase,
		COALESCE((SELECT SUM(s.amount) FROM obligation_settlements s JOIN obligations o ON o.id = s.obligation_id
			WHERE o.direction = $1 AND s.settled_by_type <> 'obligation_reversal' AND s.business_date BETWEEN $2 AND $3), 0) AS settled`

func debtMovement(ctx context.Context, q db.Querier, direction string, p, prev Period) (DebtSide, error) {
	var cur, before struct {
		Increase money.Money `db:"increase"`
		Settled  money.Money `db:"settled"`
	}
	if err := q.QueryRowxContext(ctx, debtMoves, direction, p.From, p.To).StructScan(&cur); err != nil {
		return DebtSide{}, fmt.Errorf("reporting: read debt movement: %w", err)
	}
	if err := q.QueryRowxContext(ctx, debtMoves, direction, prev.From, prev.To).StructScan(&before); err != nil {
		return DebtSide{}, fmt.Errorf("reporting: read debt movement: %w", err)
	}
	return DebtSide{
		Increase: cur.Increase, Settled: cur.Settled, PrevIncrease: before.Increase, PrevSettled: before.Settled,
		Top: []PartyBalance{},
	}, nil
}

// debtBalances fills the balances of a ledger at the end of the period and of the previous one, and
// its largest balances, from the aging the debt ledger replays as of a date.
func (s *Service) debtBalances(ctx context.Context, scope Scope, direction string, at, prevAt time.Time, side *DebtSide) error {
	aging, err := s.debt.Aging(ctx, scope, direction, at)
	if err != nil {
		return err
	}
	prev, err := s.debt.Aging(ctx, scope, direction, prevAt)
	if err != nil {
		return err
	}
	side.Balance, side.PrevBalance = aging.Totals.Total(), prev.Totals.Total()
	rows := slices.Clone(aging.Rows)
	slices.SortStableFunc(rows, func(a, b AgingRow) int { return b.Buckets.Total().Cmp(a.Buckets.Total()) })
	for _, r := range rows {
		if len(side.Top) == dashboardTopParties || !r.Buckets.Total().IsPositive() {
			break
		}
		side.Top = append(side.Top, PartyBalance{
			PartyID: r.PartyID, Name: r.Name, Amount: r.Buckets.Total(), Overdue: r.Buckets.Total().Sub(r.Buckets.Current),
			NextDue: r.NextDue,
		})
	}
	return nil
}
