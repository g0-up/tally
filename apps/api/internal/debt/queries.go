package debt

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// overdueExpr is the part of obligation o past due on the date bound to param: the installments
// due before that date (all of o without a schedule, once its due date passed) minus what is
// settled, installments being paid oldest first.
func overdueExpr(param string) string {
	return `GREATEST(0, COALESCE(
		(SELECT SUM(i.amount) FROM obligation_schedules i WHERE i.obligation_id = o.id AND i.due_date < ` + param + `),
		CASE WHEN EXISTS (SELECT 1 FROM obligation_schedules j WHERE j.obligation_id = o.id) THEN 0
		     WHEN o.due_date < ` + param + ` THEN o.amount ELSE 0 END) - o.settled_amount)`
}

// PartySummary is one party's position on a ledger: what is open, overdue and advanced.
type PartySummary struct {
	PartyID          uuid.UUID   `db:"party_id"`
	Name             string      `db:"name"`
	NameKey          string      `db:"name_key"`
	Phone            *string     `db:"phone"`
	Direction        string      `db:"direction"`
	Open             money.Money `db:"open"`
	Overdue          money.Money `db:"overdue"`
	OldestOverdueDue *time.Time  `db:"oldest_overdue_due"`
	OpenDocuments    int         `db:"open_documents"`
	// Advance is the unallocated confirmed money of the party in the direction that settles this
	// ledger (customer deposits for receivables, supplier advances for payables). It is shown
	// next to the balance, never netted into it.
	Advance money.Money `db:"advance"`
}

// PartyFilter narrows the party summaries (keyset by name).
type PartyFilter struct {
	Direction    string
	OverdueOnly  bool
	AfterNameKey string
	AfterID      uuid.UUID
	HasAfter     bool
	Limit        int
}

// Parties lists the parties with an open balance or an advance on a ledger, by name.
func (s *Service) Parties(ctx context.Context, scope Scope, f PartyFilter) ([]PartySummary, error) {
	if f.Direction != Receivable && f.Direction != Payable {
		return nil, errInvalid(map[string]string{"direction": "invalid"})
	}
	var out []PartySummary
	err := s.read(ctx, scope, func(q db.Querier) error {
		today, err := tenantToday(ctx, q, scope.TenantID, s.clock.Now())
		if err != nil {
			return err
		}
		return selectAll(ctx, q, &out, `
			WITH ob AS (
			  SELECT o.party_id, SUM(o.amount - o.settled_amount) AS open, SUM(`+overdueExpr("$2")+`) AS overdue,
			         MIN(CASE WHEN `+overdueExpr("$2")+` > 0 THEN `+nextDueExpr+` END) AS oldest_overdue_due,
			         COUNT(*) AS open_documents
			  FROM obligations o WHERE o.direction = $1 AND o.status = 'open' GROUP BY o.party_id
			), adv AS (
			  SELECT p.party_id, SUM(p.amount - p.allocated_amount) AS advance
			  FROM payments p WHERE p.direction = $3 AND p.status = 'confirmed' AND p.party_id IS NOT NULL
			    AND p.allocated_amount < p.amount
			  GROUP BY p.party_id
			)
			SELECT pa.id AS party_id, pa.name, pa.name_key, pa.phone, $1 AS direction,
			       COALESCE(ob.open, 0) AS open, COALESCE(ob.overdue, 0) AS overdue, ob.oldest_overdue_due,
			       COALESCE(ob.open_documents, 0) AS open_documents, COALESCE(adv.advance, 0) AS advance
			FROM parties pa LEFT JOIN ob ON ob.party_id = pa.id LEFT JOIN adv ON adv.party_id = pa.id
			WHERE (ob.party_id IS NOT NULL OR adv.party_id IS NOT NULL)
			  AND (NOT $4 OR COALESCE(ob.overdue, 0) > 0)
			  AND (NOT $5 OR (pa.name_key, pa.id) > ($6, $7::uuid))
			ORDER BY pa.name_key, pa.id LIMIT $8`,
			f.Direction, today, PaymentDirectionOf(f.Direction), f.OverdueOnly, f.HasAfter, f.AfterNameKey, f.AfterID, f.Limit)
	})
	if err != nil {
		return nil, fmt.Errorf("debt: list party balances: %w", err)
	}
	return out, nil
}

// LedgerDocument is one obligation of a party with its schedule and settlements.
type LedgerDocument struct {
	Obligation   Obligation
	NextDue      time.Time
	Overdue      money.Money
	Installments []Installment
	Settlements  []Settlement
}

// PartyLedger is the Công nợ detail of one party: every document on both ledgers, its advances
// and the payments waiting for verification.
type PartyLedger struct {
	Party      partyRef
	Receivable money.Money
	Payable    money.Money
	Documents  []LedgerDocument
	Advances   []Payment
	Pending    []Payment
}

// Ledger returns the ledger of a party. The totals are Σ of the open documents.
func (s *Service) Ledger(ctx context.Context, scope Scope, partyID uuid.UUID) (PartyLedger, error) {
	var out PartyLedger
	err := s.read(ctx, scope, func(q db.Querier) error {
		party, err := pathParty(ctx, q, partyID)
		if err != nil {
			return err
		}
		out.Party = party
		today, err := tenantToday(ctx, q, scope.TenantID, s.clock.Now())
		if err != nil {
			return err
		}
		var docs []struct {
			Candidate
			Overdue money.Money `db:"overdue"`
		}
		if err := selectAll(ctx, q, &docs, `SELECT `+obligationColumns+`, `+nextDueExpr+` AS next_due, `+overdueExpr("$2")+` AS overdue
			FROM obligations o WHERE o.party_id = $1
			ORDER BY o.status, o.business_date DESC, o.created_at DESC, o.id`, partyID, today); err != nil {
			return fmt.Errorf("debt: read party documents: %w", err)
		}
		obIDs := make([]uuid.UUID, 0, len(docs))
		for _, d := range docs {
			obIDs = append(obIDs, d.ID)
		}
		inst, err := installmentsOf(ctx, q, obIDs)
		if err != nil {
			return err
		}
		var st []Settlement
		if err := selectAll(ctx, q, &st, `SELECT `+settlementColumns+` FROM obligation_settlements s
			WHERE s.obligation_id = ANY($1) ORDER BY s.business_date, s.created_at, s.id`, obIDs); err != nil {
			return fmt.Errorf("debt: read party settlements: %w", err)
		}
		byOb := map[uuid.UUID][]Settlement{}
		for _, x := range st {
			byOb[x.ObligationID] = append(byOb[x.ObligationID], x)
		}
		out.Documents = make([]LedgerDocument, 0, len(docs))
		for _, d := range docs {
			out.Documents = append(out.Documents, LedgerDocument{
				Obligation: d.Obligation, NextDue: d.NextDue, Overdue: d.Overdue, Installments: inst[d.ID], Settlements: byOb[d.ID],
			})
			if d.Direction == Receivable {
				out.Receivable = out.Receivable.Add(d.Open())
			} else {
				out.Payable = out.Payable.Add(d.Open())
			}
		}
		if err := selectAll(ctx, q, &out.Advances, `SELECT `+paymentColumns+` FROM payments p
			WHERE p.party_id = $1 AND p.status = 'confirmed' AND p.allocated_amount < p.amount
			ORDER BY p.posted_at, p.id`, partyID); err != nil {
			return fmt.Errorf("debt: read party advances: %w", err)
		}
		if err := selectAll(ctx, q, &out.Pending, `SELECT `+paymentColumns+` FROM payments p
			WHERE p.party_id = $1 AND p.status = 'pending_verification' ORDER BY p.posted_at, p.id`, partyID); err != nil {
			return fmt.Errorf("debt: read party pending payments: %w", err)
		}
		return nil
	})
	return out, err
}

// Aging buckets by days past due.
const (
	BucketCurrent = "current"
	Bucket1To30   = "1_30"
	Bucket31To60  = "31_60"
	Bucket61To90  = "61_90"
	BucketOver90  = "over_90"
)

// AgingBuckets are the unpaid amounts by days past due.
type AgingBuckets struct {
	Current money.Money
	D1To30  money.Money
	D31To60 money.Money
	D61To90 money.Money
	Over90  money.Money
}

func (b *AgingBuckets) add(bucket string, m money.Money) {
	switch bucket {
	case BucketCurrent:
		b.Current = b.Current.Add(m)
	case Bucket1To30:
		b.D1To30 = b.D1To30.Add(m)
	case Bucket31To60:
		b.D31To60 = b.D31To60.Add(m)
	case Bucket61To90:
		b.D61To90 = b.D61To90.Add(m)
	default:
		b.Over90 = b.Over90.Add(m)
	}
}

// Total is the sum of the buckets.
func (b AgingBuckets) Total() money.Money {
	return money.SumMoney(b.Current, b.D1To30, b.D31To60, b.D61To90, b.Over90)
}

// AgingRow is one party's aging.
type AgingRow struct {
	PartyID uuid.UUID
	Name    string
	Buckets AgingBuckets
	// NextDue is the earliest due date of what the party still owes (or is owed) as of the date.
	NextDue time.Time
}

// AgingReport is the aging of a ledger as of a date.
type AgingReport struct {
	Direction string
	AsOf      time.Time
	Rows      []AgingRow
	Totals    AgingBuckets
}

// bucketOf classifies an unpaid part due on due as of asOf.
func bucketOf(due, asOf time.Time) string {
	days := int(asOf.Sub(due).Hours() / 24)
	switch {
	case days <= 0:
		return BucketCurrent
	case days <= 30:
		return Bucket1To30
	case days <= 60:
		return Bucket31To60
	case days <= 90:
		return Bucket61To90
	}
	return BucketOver90
}

// agedParts splits what is unpaid of an obligation of amount, settled by settled, over its
// installments (oldest first: settlements pay the earliest installments) and returns the unpaid
// part of each with its due date. Without installments the whole obligation is due on due.
func agedParts(amount, settled money.Money, due time.Time, inst []Installment) []struct {
	Due    time.Time
	Unpaid money.Money
} {
	type part = struct {
		Due    time.Time
		Unpaid money.Money
	}
	if len(inst) == 0 {
		if open := amount.Sub(settled); open.IsPositive() {
			return []part{{Due: due, Unpaid: open}}
		}
		return nil
	}
	sorted := append([]Installment(nil), inst...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].InstallmentNo < sorted[j].InstallmentNo })
	var out []part
	paid := settled
	for _, i := range sorted {
		covered := minMoney(paid, i.Amount)
		if covered.IsNegative() {
			covered = money.Money{}
		}
		paid = paid.Sub(covered)
		if unpaid := i.Amount.Sub(covered); unpaid.IsPositive() {
			out = append(out, part{Due: i.DueDate, Unpaid: unpaid})
		}
	}
	return out
}

// Aging replays the ledger as of a date: documents dated on or before asOf, settled by the
// settlements (and their reversals) dated on or before it. Postings dated later never change it.
func (s *Service) Aging(ctx context.Context, scope Scope, direction string, asOf time.Time) (AgingReport, error) {
	if direction != Receivable && direction != Payable {
		return AgingReport{}, errInvalid(map[string]string{"direction": "invalid"})
	}
	if asOf.IsZero() {
		return AgingReport{}, errInvalid(map[string]string{"as_of": "required"})
	}
	out := AgingReport{Direction: direction, AsOf: asOf, Rows: []AgingRow{}}
	err := s.read(ctx, scope, func(q db.Querier) error {
		var obs []struct {
			ID      uuid.UUID   `db:"id"`
			PartyID uuid.UUID   `db:"party_id"`
			Name    string      `db:"name"`
			NameKey string      `db:"name_key"`
			DueDate time.Time   `db:"due_date"`
			Amount  money.Money `db:"amount"`
			Settled money.Money `db:"settled"`
		}
		if err := selectAll(ctx, q, &obs, `
			SELECT o.id, o.party_id, pa.name, pa.name_key, o.due_date, o.amount,
			       COALESCE((SELECT SUM(s.amount) FROM obligation_settlements s
			                 WHERE s.obligation_id = o.id AND s.business_date <= $2), 0) AS settled
			FROM obligations o JOIN parties pa ON pa.id = o.party_id
			WHERE o.direction = $1 AND o.business_date <= $2
			ORDER BY pa.name_key, pa.id, o.id`, direction, asOf); err != nil {
			return fmt.Errorf("debt: read aging documents: %w", err)
		}
		obIDs := make([]uuid.UUID, 0, len(obs))
		for _, o := range obs {
			obIDs = append(obIDs, o.ID)
		}
		inst, err := installmentsOf(ctx, q, obIDs)
		if err != nil {
			return err
		}
		for _, o := range obs {
			parts := agedParts(o.Amount, o.Settled, o.DueDate, inst[o.ID])
			if len(parts) == 0 {
				continue
			}
			if n := len(out.Rows); n == 0 || out.Rows[n-1].PartyID != o.PartyID {
				out.Rows = append(out.Rows, AgingRow{PartyID: o.PartyID, Name: o.Name})
			}
			row := &out.Rows[len(out.Rows)-1]
			for _, p := range parts {
				if row.NextDue.IsZero() || p.Due.Before(row.NextDue) {
					row.NextDue = p.Due
				}
				b := bucketOf(p.Due, asOf)
				row.Buckets.add(b, p.Unpaid)
				out.Totals.add(b, p.Unpaid)
			}
		}
		return nil
	})
	return out, err
}

// ForecastDay is the expected money of one day, kept apart from the money actually confirmed.
type ForecastDay struct {
	Date time.Time
	// ReceivableDue and PayableDue are the unpaid installments (or documents) due that day.
	ReceivableDue money.Money
	PayableDue    money.Money
	// Promised is what customers promised to pay that day (collection notes).
	Promised money.Money
	// ConfirmedIn and ConfirmedOut are the money payments moved through the funds that day, on the
	// date of their cash entries as the fund book shows it (a transfer counts on the day it was
	// confirmed, a reversal takes its payment back on the day it was reversed): what really came
	// in or went out, never mixed into the expectation.
	ConfirmedIn  money.Money
	ConfirmedOut money.Money
}

// Forecast is the expected collections and payments over a period.
type Forecast struct {
	From, To time.Time
	// OverdueReceivable and OverduePayable are unpaid parts due before From.
	OverdueReceivable money.Money
	OverduePayable    money.Money
	Days              []ForecastDay
}

// maxForecastDays bounds a forecast period.
const maxForecastDays = 366

// Forecast reads the unpaid schedule of open documents, the payment promises and the payment
// cash entries of [from, to].
func (s *Service) Forecast(ctx context.Context, scope Scope, from, to time.Time) (Forecast, error) {
	fields := map[string]string{}
	if from.IsZero() {
		fields["from"] = "required"
	}
	switch {
	case to.IsZero():
		fields["to"] = "required"
	case to.Before(from):
		fields["to"] = "before_from"
	case to.Sub(from).Hours()/24 >= maxForecastDays:
		fields["to"] = "period_too_long"
	}
	if len(fields) > 0 {
		return Forecast{}, errInvalid(fields)
	}
	out := Forecast{From: from, To: to}
	err := s.read(ctx, scope, func(q db.Querier) error {
		var obs []Obligation
		if err := selectAll(ctx, q, &obs, `SELECT `+obligationColumns+` FROM obligations o WHERE o.status = 'open' ORDER BY o.id`); err != nil {
			return fmt.Errorf("debt: read open documents: %w", err)
		}
		obIDs := make([]uuid.UUID, 0, len(obs))
		for _, o := range obs {
			obIDs = append(obIDs, o.ID)
		}
		inst, err := installmentsOf(ctx, q, obIDs)
		if err != nil {
			return err
		}
		days := map[time.Time]*ForecastDay{}
		day := func(d time.Time) *ForecastDay {
			if x, ok := days[d]; ok {
				return x
			}
			x := &ForecastDay{Date: d}
			days[d] = x
			return x
		}
		for _, o := range obs {
			for _, p := range agedParts(o.Amount, o.SettledAmount, o.DueDate, inst[o.ID]) {
				switch {
				case p.Due.Before(from) && o.Direction == Receivable:
					out.OverdueReceivable = out.OverdueReceivable.Add(p.Unpaid)
				case p.Due.Before(from):
					out.OverduePayable = out.OverduePayable.Add(p.Unpaid)
				case p.Due.After(to):
				case o.Direction == Receivable:
					day(p.Due).ReceivableDue = day(p.Due).ReceivableDue.Add(p.Unpaid)
				default:
					day(p.Due).PayableDue = day(p.Due).PayableDue.Add(p.Unpaid)
				}
			}
		}
		var promises []struct {
			Date   time.Time   `db:"promised_date"`
			Amount money.Money `db:"amount"`
		}
		if err := selectAll(ctx, q, &promises, `SELECT n.promised_date, SUM(n.promised_amount) AS amount FROM collection_notes n
			WHERE n.kind = 'promise' AND n.promised_amount IS NOT NULL AND n.promised_date BETWEEN $1 AND $2
			GROUP BY n.promised_date`, from, to); err != nil {
			return fmt.Errorf("debt: read payment promises: %w", err)
		}
		for _, p := range promises {
			day(p.Date).Promised = day(p.Date).Promised.Add(p.Amount)
		}
		var confirmed []struct {
			Date      time.Time   `db:"business_date"`
			Direction string      `db:"direction"`
			Amount    money.Money `db:"amount"`
		}
		if err := selectAll(ctx, q, &confirmed, `SELECT e.business_date, p.direction,
			       SUM(CASE WHEN p.direction = 'out' THEN -e.amount ELSE e.amount END) AS amount
			FROM cash_entries e JOIN payments p ON p.id = e.payment_id
			WHERE e.business_date BETWEEN $1 AND $2
			GROUP BY e.business_date, p.direction`, from, to); err != nil {
			return fmt.Errorf("debt: read confirmed payments: %w", err)
		}
		for _, c := range confirmed {
			d := day(c.Date)
			if c.Direction == In {
				d.ConfirmedIn = d.ConfirmedIn.Add(c.Amount)
			} else {
				d.ConfirmedOut = d.ConfirmedOut.Add(c.Amount)
			}
		}
		out.Days = make([]ForecastDay, 0, len(days))
		for _, d := range days {
			out.Days = append(out.Days, *d)
		}
		sort.Slice(out.Days, func(i, j int) bool { return out.Days[i].Date.Before(out.Days[j].Date) })
		return nil
	})
	return out, err
}

// PendingPayment is a payment waiting for verification with its party name.
type PendingPayment struct {
	Payment
	PartyName  *string `db:"party_name"`
	MethodName string  `db:"method_name"`
}

// PendingVerification lists the payments waiting for verification, oldest first.
func (s *Service) PendingVerification(ctx context.Context, scope Scope) ([]PendingPayment, error) {
	var out []PendingPayment
	err := s.read(ctx, scope, func(q db.Querier) error {
		return selectAll(ctx, q, &out, `SELECT `+paymentColumns+`, pa.name AS party_name, m.name AS method_name
			FROM payments p LEFT JOIN parties pa ON pa.id = p.party_id JOIN payment_methods m ON m.id = p.payment_method_id
			WHERE p.status = 'pending_verification' ORDER BY p.posted_at, p.id`)
	})
	if err != nil {
		return nil, fmt.Errorf("debt: list pending payments: %w", err)
	}
	return out, nil
}
