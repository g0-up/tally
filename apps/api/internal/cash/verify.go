package cash

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// verifyFundsQuery reads every fund with its stored balance and the balance its cash entries make.
const verifyFundsQuery = `
	SELECT f.id, f.name, f.balance,
	       f.opening_balance + COALESCE((SELECT SUM(e.amount) FROM cash_entries e WHERE e.fund_account_id = f.id), 0) AS expected
	FROM fund_accounts f ORDER BY f.id`

// Verify checks the fund projections of the tenant of q: every fund balance equals its opening
// balance plus the sum of its cash entries, and every reversal entry mirrors the entry it
// reverses exactly once. It returns one line per mismatch.
func Verify(ctx context.Context, q db.Querier) ([]string, error) {
	var funds []struct {
		ID       uuid.UUID   `db:"id"`
		Name     string      `db:"name"`
		Balance  money.Money `db:"balance"`
		Expected money.Money `db:"expected"`
	}
	if err := sqlxSelect(ctx, q, &funds, verifyFundsQuery); err != nil {
		return nil, fmt.Errorf("cash: verify funds: %w", err)
	}
	var out []string
	for _, f := range funds {
		if !f.Balance.Equal(f.Expected) {
			out = append(out, fmt.Sprintf("%s %s (%s): balance %s, opening + entries %s", fundCheck, f.ID, f.Name, f.Balance, f.Expected))
		}
	}
	var mirrors []struct {
		ID    uuid.UUID `db:"id"`
		Issue string    `db:"issue"`
	}
	if err := sqlxSelect(ctx, q, &mirrors, `
		SELECT r.id, CASE WHEN o.id IS NULL THEN 'reverses a missing entry'
		                  WHEN r.amount <> -o.amount OR r.fund_account_id <> o.fund_account_id THEN 'does not mirror its original'
		                  ELSE 'is one of several reversals of one entry' END AS issue
		FROM cash_entries r LEFT JOIN cash_entries o ON o.id = r.reversal_of
		WHERE r.reversal_of IS NOT NULL
		  AND (o.id IS NULL OR r.amount <> -o.amount OR r.fund_account_id <> o.fund_account_id
		       OR (SELECT COUNT(*) FROM cash_entries x WHERE x.reversal_of = r.reversal_of) > 1)
		ORDER BY r.id`); err != nil {
		return nil, fmt.Errorf("cash: verify reversals: %w", err)
	}
	for _, m := range mirrors {
		out = append(out, fmt.Sprintf("cash entry %s %s", m.ID, m.Issue))
	}
	return out, nil
}
