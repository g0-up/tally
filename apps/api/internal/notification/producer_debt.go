package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// payableWindowDays is how far ahead a supplier due date raises payable_due_soon.
const payableWindowDays = 7

// partyDue is the open obligations of one party matching a producer's due-date rule.
type partyDue struct {
	PartyID   uuid.UUID   `db:"party_id"`
	Name      string      `db:"name"`
	Count     int         `db:"n"`
	Remaining money.Money `db:"remaining"`
	Earliest  time.Time   `db:"earliest_due"`
	// DocNo is the number of the document due first.
	DocNo string `db:"doc_no"`
}

// duesBefore reads the open obligations of direction due before cutoff, by party; party narrows
// it to one party.
func duesBefore(ctx context.Context, q db.Querier, direction string, cutoff time.Time, inclusive bool, party *uuid.UUID) ([]partyDue, error) {
	op := "<"
	if inclusive {
		op = "<="
	}
	var rows []partyDue
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT o.party_id, p.name, count(*) AS n, sum(o.amount - o.settled_amount) AS remaining, min(o.due_date) AS earliest_due,
		  (array_agg(o.source_doc_no ORDER BY o.due_date, o.id))[1] AS doc_no
		FROM obligations o JOIN parties p ON p.id = o.party_id
		WHERE o.direction = $1 AND o.status = 'open' AND o.reversed_on IS NULL AND o.due_date `+op+` $2
		  AND ($3::uuid IS NULL OR o.party_id = $3)
		GROUP BY o.party_id, p.name
		ORDER BY o.party_id`, direction, cutoff, party); err != nil {
		return nil, fmt.Errorf("notification: scan %s due dates: %w", direction, err)
	}
	return rows, nil
}

// debtPath is the web route of a party's ledger in the receivables or payables screen, opened on
// the party.
func debtPath(screen string, party uuid.UUID) string {
	return "/debts/" + screen + "?party=" + party.String()
}

// ReceivableOverdue raises one receivable_overdue per customer holding open receivables past
// their due date.
type ReceivableOverdue struct{}

// Kinds implements Producer.
func (ReceivableOverdue) Kinds() []string { return []string{KindReceivableOverdue} }

// Sweep implements Producer.
func (ReceivableOverdue) Sweep(ctx context.Context, q db.Querier, at SweepTime) ([]Notice, error) {
	return overdueReceivables(ctx, q, at, nil)
}

func overdueReceivables(ctx context.Context, q db.Querier, at SweepTime, party *uuid.UUID) ([]Notice, error) {
	rows, err := duesBefore(ctx, q, "receivable", at.Today, false, party)
	if err != nil {
		return nil, err
	}
	out := make([]Notice, 0, len(rows))
	for _, r := range rows {
		days := int(at.Today.Sub(r.Earliest).Hours() / 24)
		out = append(out, Notice{
			Kind: KindReceivableOverdue, DedupeKey: key(KindReceivableOverdue, r.PartyID), Severity: SeverityWarn,
			Title:      fmt.Sprintf("%s quá hạn %s đ", r.Name, r.Remaining.FormatVND()),
			Body:       ptr(fmt.Sprintf("%d khoản quá hạn, lâu nhất %d ngày (hạn %s).", r.Count, days, r.Earliest.Format("02/01/2006"))),
			EntityType: ptr("party"), EntityID: ptr(r.PartyID), ActionPath: ptr(debtPath("receivables", r.PartyID)), DocNo: ptr(r.DocNo),
		})
	}
	return out, nil
}

// PayableDueSoon raises one payable_due_soon per supplier with open payables due within the
// next seven days, or already past due.
type PayableDueSoon struct{}

// Kinds implements Producer.
func (PayableDueSoon) Kinds() []string { return []string{KindPayableDueSoon} }

// Sweep implements Producer.
func (PayableDueSoon) Sweep(ctx context.Context, q db.Querier, at SweepTime) ([]Notice, error) {
	return payablesDueSoon(ctx, q, at, nil)
}

func payablesDueSoon(ctx context.Context, q db.Querier, at SweepTime, party *uuid.UUID) ([]Notice, error) {
	rows, err := duesBefore(ctx, q, "payable", at.Today.AddDate(0, 0, payableWindowDays), true, party)
	if err != nil {
		return nil, err
	}
	out := make([]Notice, 0, len(rows))
	for _, r := range rows {
		severity, when := SeverityInfo, "hạn sớm nhất "+r.Earliest.Format("02/01/2006")
		if r.Earliest.Before(at.Today) {
			severity, when = SeverityWarn, "đã quá hạn từ "+r.Earliest.Format("02/01/2006")
		}
		out = append(out, Notice{
			Kind: KindPayableDueSoon, DedupeKey: key(KindPayableDueSoon, r.PartyID), Severity: severity,
			Title:      fmt.Sprintf("Sắp trả %s: %s đ", r.Name, r.Remaining.FormatVND()),
			Body:       ptr(fmt.Sprintf("%d khoản, %s.", r.Count, when)),
			EntityType: ptr("party"), EntityID: ptr(r.PartyID), ActionPath: ptr(debtPath("payables", r.PartyID)), DocNo: ptr(r.DocNo),
		})
	}
	return out, nil
}
