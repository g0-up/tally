package cash

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// LockFunds locks fund rows in id order; it is the posting locker of the fund_accounts kind.
func LockFunds(ctx context.Context, q db.Querier, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.QueryxContext(ctx, `SELECT id FROM fund_accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return fmt.Errorf("cash: lock funds: %w", err)
	}
	return rows.Close()
}

// LockAllFunds locks every fund of the tenant in id order. A posting that writes cash entries
// calls it before its first entry: one transaction may pay into both funds (a split payment) and
// another in the opposite order, so taking all of them up front (a tenant has two) in one order
// keeps the fund locks deadlock-free.
func LockAllFunds(ctx context.Context, q db.Querier) error {
	rows, err := q.QueryxContext(ctx, `SELECT id FROM fund_accounts ORDER BY id FOR UPDATE`)
	if err != nil {
		return fmt.Errorf("cash: lock funds: %w", err)
	}
	return rows.Close()
}
