package debt

import (
	"context"
	"fmt"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// rebuildRepairs rewrite each projection a verify check compares, keyed by the check. A sum
// outside 0..amount is a ledger error the CHECK constraints refuse; such a row keeps its stored
// value for Verify to report. An obligation's status follows its settled amount.
var rebuildRepairs = []posting.Repair{
	{Check: "obligations", Stmt: `
		UPDATE obligations o SET settled_amount = bad.expected,
		  status = CASE WHEN bad.expected = o.amount THEN '` + ObligationSettled + `' ELSE '` + ObligationOpen + `' END,
		  updated_at = $1, version = o.version + 1
		FROM bad WHERE o.id = bad.id AND o.tenant_id = ` + posting.InTenant + ` AND o.settled_amount <> bad.expected AND bad.expected BETWEEN 0 AND o.amount`},
	{Check: "credit notes", Stmt: `
		UPDATE credit_notes n SET allocated_amount = bad.expected, updated_at = $1, version = n.version + 1
		FROM bad WHERE n.id = bad.id AND n.tenant_id = ` + posting.InTenant + ` AND n.allocated_amount <> bad.expected AND bad.expected BETWEEN 0 AND n.amount`},
	{Check: "payments", Stmt: `
		UPDATE payments p SET allocated_amount = bad.expected, updated_at = $1, version = p.version + 1
		FROM bad WHERE p.id = bad.id AND p.tenant_id = ` + posting.InTenant + ` AND p.allocated_amount <> bad.expected AND bad.expected BETWEEN 0 AND p.amount`},
}

// RebuildChecks names the checks whose rows Rebuild rewrites.
func RebuildChecks() []string { return posting.Checks(rebuildRepairs) }

// Rebuild rewrites the settled and allocated amounts of the tenant in scope that Verify finds
// disagreeing with the settlement ledger. The caller holds the tenant's posting locks.
func Rebuild(ctx context.Context, q db.Querier, now time.Time) ([]posting.Rebuilt, error) {
	out, err := posting.RunRepairs(ctx, q, checkQuery, rebuildRepairs, now)
	if err != nil {
		return nil, fmt.Errorf("debt: %w", err)
	}
	return out, nil
}

// checkQuery returns the query of the named check.
func checkQuery(name string) (string, error) {
	for _, c := range verifyChecks {
		if c.name == name {
			return c.query, nil
		}
	}
	return "", fmt.Errorf("debt: no verify check %q", name)
}
