package debt

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// verifyChecks compare a stored debt projection with its settlement ledger: each returns every
// row with both values, and Verify reports the rows where they differ.
var verifyChecks = []struct {
	name, query string
}{
	{"obligations", `
		SELECT o.id, o.source_doc_no AS label, o.settled_amount AS stored,
		       COALESCE((SELECT SUM(s.amount) FROM obligation_settlements s WHERE s.obligation_id = o.id), 0) AS expected,
		       '' AS issue
		FROM obligations o ORDER BY o.id`},
	{"credit notes", `
		SELECT n.id, n.source_doc_no AS label, n.allocated_amount AS stored,
		       COALESCE((SELECT SUM(s.amount) FROM obligation_settlements s
		                 WHERE s.settled_by_type = 'credit_note' AND s.settled_by_id = n.id), 0) AS expected,
		       '' AS issue
		FROM credit_notes n ORDER BY n.id`},
	{"payments", `
		SELECT p.id, p.doc_no AS label, p.allocated_amount AS stored,
		       CASE WHEN p.status <> 'confirmed' THEN 0
		            WHEN p.party_id IS NULL OR p.purpose = 'advance_refund' THEN p.amount
		            ELSE COALESCE((SELECT SUM(s.amount) FROM obligation_settlements s
		                           WHERE s.settled_by_type = 'payment' AND s.settled_by_id = p.id), 0)
		               + COALESCE((SELECT SUM(r.amount) FROM payments r
		                           WHERE r.refund_of = p.id AND r.purpose = 'advance_refund' AND r.status = 'confirmed'), 0)
		       END AS expected,
		       '' AS issue
		FROM payments p ORDER BY p.id`},
}

// Verify checks the debt projections of the tenant of q against the settlement ledger: every
// obligation's settled amount is Σ its settlements (and its status follows from it), every
// credit note's allocated amount is Σ its settlements, and every payment's allocated amount is
// what its money was used for — Σ its settlements plus the confirmed refunds of its advance, the
// whole amount for a payment without a party or an advance refund, nothing while it is not
// confirmed. Every reversal row mirrors the settlement it reverses exactly once. Every payment
// has the cash entries its status says, in its own fund: one for its signed amount once confirmed
// (none for an opening advance, which moves no money), that one and its mirror once reversed after
// confirmation, none otherwise. A reversed obligation is closed by exactly one obligation_reversal
// settlement of its own for its whole amount, and no other obligation has one. It returns one
// line per mismatch.
func Verify(ctx context.Context, q db.Querier) ([]string, error) {
	type row struct {
		ID       uuid.UUID   `db:"id"`
		Label    string      `db:"label"`
		Stored   money.Money `db:"stored"`
		Expected money.Money `db:"expected"`
		Issue    string      `db:"issue"`
	}
	var out []string
	for _, c := range verifyChecks {
		var rows []row
		if err := selectAll(ctx, q, &rows, c.query); err != nil {
			return nil, fmt.Errorf("debt: verify %s: %w", c.name, err)
		}
		for _, r := range rows {
			if !r.Stored.Equal(r.Expected) {
				out = append(out, fmt.Sprintf("%s %s (%s): stored %s, ledger %s", c.name, r.ID, r.Label, r.Stored, r.Expected))
			}
		}
	}
	var status []row
	if err := selectAll(ctx, q, &status, `
		SELECT o.id, o.source_doc_no AS label, o.settled_amount AS stored, o.amount AS expected, o.status AS issue
		FROM obligations o
		WHERE (o.status = 'settled') <> (o.settled_amount = o.amount) OR o.settled_amount > o.amount OR o.settled_amount < 0
		ORDER BY o.id`); err != nil {
		return nil, fmt.Errorf("debt: verify obligation status: %w", err)
	}
	for _, r := range status {
		out = append(out, fmt.Sprintf("obligation %s (%s): status %s with settled %s of %s", r.ID, r.Label, r.Issue, r.Stored, r.Expected))
	}
	var mirrors []row
	if err := selectAll(ctx, q, &mirrors, `
		SELECT r.id, '' AS label, r.amount AS stored, COALESCE(-o.amount, 0) AS expected,
		       CASE WHEN o.id IS NULL THEN 'reverses a missing settlement'
		            WHEN r.amount <> -o.amount OR r.obligation_id <> o.obligation_id
		              OR r.settled_by_type <> o.settled_by_type OR r.settled_by_id <> o.settled_by_id THEN 'does not mirror its original'
		            ELSE 'is one of several reversals of one settlement' END AS issue
		FROM obligation_settlements r LEFT JOIN obligation_settlements o ON o.id = r.reversal_of
		WHERE r.reversal_of IS NOT NULL
		  AND (o.id IS NULL OR r.amount <> -o.amount OR r.obligation_id <> o.obligation_id
		       OR r.settled_by_type <> o.settled_by_type OR r.settled_by_id <> o.settled_by_id
		       OR (SELECT COUNT(*) FROM obligation_settlements x WHERE x.reversal_of = r.reversal_of) > 1)
		ORDER BY r.id`); err != nil {
		return nil, fmt.Errorf("debt: verify settlement reversals: %w", err)
	}
	for _, m := range mirrors {
		out = append(out, fmt.Sprintf("settlement %s %s", m.ID, m.Issue))
	}
	var reversed []row
	if err := selectAll(ctx, q, &reversed, `
		SELECT o.id, o.source_doc_no AS label, COALESCE(SUM(s.amount), 0) AS stored,
		       CASE WHEN o.reversed_on IS NULL THEN 0 ELSE o.amount END AS expected,
		       CASE WHEN o.reversed_on IS NULL THEN 'has a reversal settlement but is not reversed'
		            ELSE 'is reversed but not closed by one reversal settlement of its own for its whole amount' END AS issue
		FROM obligations o LEFT JOIN obligation_settlements s ON s.obligation_id = o.id AND s.settled_by_type = 'obligation_reversal'
		GROUP BY o.id
		HAVING (o.reversed_on IS NULL AND COUNT(s.id) > 0)
		    OR (o.reversed_on IS NOT NULL AND (COUNT(s.id) <> 1 OR SUM(s.amount) <> o.amount
		        OR bool_or(s.settled_by_id <> o.id) OR bool_or(s.reversal_of IS NOT NULL)))
		ORDER BY o.id`); err != nil {
		return nil, fmt.Errorf("debt: verify obligation reversals: %w", err)
	}
	for _, r := range reversed {
		out = append(out, fmt.Sprintf("obligation %s (%s) %s: reversal settlements %s, want %s", r.ID, r.Label, r.Issue, r.Stored, r.Expected))
	}
	var entries []row
	if err := selectAll(ctx, q, &entries, `
		WITH e AS (
			SELECT p.id, p.doc_no, p.status,
			       CASE WHEN p.direction = 'out' THEN -p.amount ELSE p.amount END AS signed,
			       p.confirmed_at IS NOT NULL AND p.purpose <> 'opening_advance' AS moved,
			       COUNT(c.id) FILTER (WHERE c.reversal_of IS NULL) AS originals,
			       COUNT(c.id) FILTER (WHERE c.reversal_of IS NOT NULL) AS mirrors,
			       COUNT(c.id) FILTER (WHERE c.fund_account_id <> p.fund_account_id) AS foreign_fund,
			       COUNT(c.id) FILTER (WHERE c.reversal_of IS NOT NULL AND NOT EXISTS (
			           SELECT 1 FROM cash_entries o
			           WHERE o.id = c.reversal_of AND o.payment_id = p.id AND o.amount = -c.amount)) AS bad_mirrors,
			       COALESCE(SUM(c.amount), 0) AS net
			FROM payments p LEFT JOIN cash_entries c ON c.payment_id = p.id
			GROUP BY p.id)
		SELECT id, doc_no AS label, net AS stored,
		       CASE WHEN status = 'confirmed' AND moved THEN signed ELSE 0 END AS expected,
		       CASE WHEN foreign_fund > 0 THEN 'has cash entries in another fund'
		            WHEN bad_mirrors > 0 THEN 'has a cash entry that does not mirror one of its own'
		            ELSE 'has ' || originals || ' cash entries and ' || mirrors || ' reversals' END AS issue
		FROM e
		WHERE foreign_fund > 0 OR bad_mirrors > 0
		   OR (status = 'confirmed' AND moved AND (originals <> 1 OR mirrors <> 0 OR net <> signed))
		   OR (status = 'reversed' AND moved AND (originals <> 1 OR mirrors <> 1 OR net <> 0))
		   OR (NOT (status IN ('confirmed', 'reversed') AND moved) AND originals + mirrors > 0)
		ORDER BY id`); err != nil {
		return nil, fmt.Errorf("debt: verify payment cash entries: %w", err)
	}
	for _, e := range entries {
		out = append(out, fmt.Sprintf("payment %s (%s) %s: net %s, want %s", e.ID, e.Label, e.Issue, e.Stored, e.Expected))
	}
	return out, nil
}
