package sales

import (
	"context"
	"fmt"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// rebuildRepairs rewrite the sales projections the verify checks compare, keyed by the check:
// what invoices delivered, what sales document lines got back, the partially reversed status
// that follows a return, and a document's cost of goods. A quantity the CHECK constraints refuse
// keeps its stored value for Verify to report. Receivables, carts, shifts, returns and exchanges
// are documents, not projections, and are left for a manual fix. Invoice lines carry no version,
// so their invoice's is bumped; sales documents are posted documents without one.
var rebuildRepairs = []posting.Repair{
	{Check: "invoice line delivered", Parent: "invoices", Stmt: `
		UPDATE invoice_lines l SET delivered_base_qty = bad.expected::numeric
		FROM bad WHERE l.id = bad.id AND l.tenant_id = ` + posting.InTenant + ` AND bad.expected::numeric >= 0
		  AND bad.expected::numeric + l.cancelled_base_qty <= l.base_qty
		  AND (l.reserved_base_qty + l.backorder_base_qty + bad.expected::numeric + l.cancelled_base_qty <= l.base_qty
		    OR l.line_kind IN ('mix_pack', 'combo'))
		RETURNING l.invoice_id`},
	{Check: "invoice delivered", Stmt: `
		UPDATE invoices i SET delivered_amount = bad.expected::numeric, updated_at = $1, version = i.version + 1
		FROM bad WHERE i.id = bad.id AND i.tenant_id = ` + posting.InTenant},
	// Returned quantities go first: the status check reads them.
	{Check: "sales document line returned", Stmt: `
		UPDATE sales_document_lines l SET returned_base_qty = bad.expected::numeric
		FROM bad WHERE l.id = bad.id AND l.tenant_id = ` + posting.InTenant + ` AND bad.expected::numeric BETWEEN 0 AND l.base_qty`},
	{Check: "sales document status", Stmt: `
		UPDATE sales_documents d SET status = bad.expected
		FROM bad WHERE d.id = bad.id AND d.tenant_id = ` + posting.InTenant},
	{Check: "sales document cogs", Stmt: `
		UPDATE sales_documents d SET cogs_amount = bad.expected::numeric
		FROM bad WHERE d.id = bad.id AND d.tenant_id = ` + posting.InTenant},
}

// RebuildChecks names the checks whose rows Rebuild rewrites.
func RebuildChecks() []string {
	return append(posting.Checks(rebuildRepairs), posting.StatusCheckNames(statusChecks)...)
}

// Rebuild rewrites the sales projections of the tenant in scope that Verify finds disagreeing
// with the documents they are kept from. The invoice statuses follow last, from the delivered
// quantities just rewritten. The caller holds the tenant's posting locks.
func Rebuild(ctx context.Context, q db.Querier, now time.Time) ([]posting.Rebuilt, error) {
	out, err := posting.RunRepairs(ctx, q, checkQuery, rebuildRepairs, now)
	if err != nil {
		return nil, fmt.Errorf("sales: %w", err)
	}
	statuses, err := posting.FixStatuses(ctx, q, statusChecks, now)
	if err != nil {
		return nil, fmt.Errorf("sales: %w", err)
	}
	return append(out, statuses...), nil
}

// checkQuery returns the query of the named check.
func checkQuery(name string) (string, error) {
	for _, c := range verifyChecks {
		if c.name == name {
			return c.query, nil
		}
	}
	return "", fmt.Errorf("sales: no verify check %q", name)
}
