package cash

import (
	"context"
	"fmt"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// fundCheck is the name Verify reports a fund balance mismatch under.
const fundCheck = "fund"

// rebuildRepairs sets the balance of every fund Verify finds wrong to its opening balance plus
// its cash entries.
var rebuildRepairs = []posting.Repair{{Check: fundCheck, Stmt: `
	UPDATE fund_accounts f SET balance = bad.expected, updated_at = $1, version = f.version + 1
	FROM bad WHERE f.id = bad.id AND f.tenant_id = ` + posting.InTenant + ` AND f.balance <> bad.expected`}}

// RebuildChecks names the checks whose rows Rebuild rewrites.
func RebuildChecks() []string { return posting.Checks(rebuildRepairs) }

// Rebuild rewrites the fund balances of the tenant in scope that Verify finds disagreeing with
// their cash entries. The caller holds the tenant's posting locks.
func Rebuild(ctx context.Context, q db.Querier, now time.Time) ([]posting.Rebuilt, error) {
	out, err := posting.RunRepairs(ctx, q, checkQuery, rebuildRepairs, now)
	if err != nil {
		return nil, fmt.Errorf("cash: %w", err)
	}
	return out, nil
}

// checkQuery returns the query of the named check.
func checkQuery(name string) (string, error) {
	if name == fundCheck {
		return verifyFundsQuery, nil
	}
	return "", fmt.Errorf("cash: no verify check %q", name)
}
