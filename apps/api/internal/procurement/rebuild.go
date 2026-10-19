package procurement

import (
	"context"
	"fmt"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// rebuildRepairs rewrite the projections the verify checks compare, keyed by the check and
// run in order: the matched amounts before the variance computed from them. A quantity the
// CHECK constraints refuse (a negative sum, more returned than received) is a document error and
// keeps its stored value for Verify to report. Order and supplier document lines carry no version,
// so their document's is bumped; receipts are posted documents without one.
var rebuildRepairs = []posting.Repair{
	{Check: "purchase order line received", Parent: "purchase_orders", Stmt: `
		UPDATE purchase_order_lines l SET received_base_qty = bad.expected::numeric
		FROM bad WHERE l.id = bad.id AND l.tenant_id = ` + posting.InTenant + ` AND bad.expected::numeric >= 0
		RETURNING l.purchase_order_id`},
	{Check: "purchase order line returned", Parent: "purchase_orders", Stmt: `
		UPDATE purchase_order_lines l SET returned_base_qty = bad.expected::numeric
		FROM bad WHERE l.id = bad.id AND l.tenant_id = ` + posting.InTenant + ` AND bad.expected::numeric >= 0
		RETURNING l.purchase_order_id`},
	{Check: "receipt line returned", Stmt: `
		UPDATE receipt_lines l SET returned_base_qty = bad.expected::numeric
		FROM bad WHERE l.id = bad.id AND l.tenant_id = ` + posting.InTenant + ` AND bad.expected::numeric BETWEEN 0 AND l.base_qty`},
	{Check: "receipt line landed cost", Stmt: `
		UPDATE receipt_lines l SET landed_cost_amount = bad.expected::numeric
		FROM bad WHERE l.id = bad.id AND l.tenant_id = ` + posting.InTenant},
	{Check: "receipt landed cost", Stmt: `
		UPDATE receipts r SET landed_cost_amount = bad.expected::numeric
		FROM bad WHERE r.id = bad.id AND r.tenant_id = ` + posting.InTenant},
	// The check names the supplier document only, so the links of the documents it flags are set
	// to what their receipt owes.
	{Check: "supplier document matched amount", Parent: "supplier_documents", Stmt: `, ` + owed + `
		UPDATE supplier_document_receipts l SET matched_amount = w.amount
		FROM owed w WHERE w.id = l.receipt_id AND l.matched_amount <> w.amount AND l.tenant_id = ` + posting.InTenant + `
		  AND l.supplier_document_id IN (SELECT id FROM bad)
		RETURNING l.supplier_document_id`},
	{Check: "supplier document variance", Stmt: `
		UPDATE supplier_documents d SET match_status = split_part(bad.expected, ' ', 1),
		  variance_amount = split_part(bad.expected, ' ', 2)::numeric, updated_at = $1, version = d.version + 1
		FROM bad WHERE d.id = bad.id AND d.tenant_id = ` + posting.InTenant},
}

// RebuildChecks names the checks whose rows Rebuild rewrites.
func RebuildChecks() []string {
	return append(posting.Checks(rebuildRepairs), posting.StatusCheckNames(statusChecks)...)
}

// Rebuild rewrites the procurement projections of the tenant in scope that Verify finds
// disagreeing with the documents they are kept from: received and returned quantities, landed
// cost shares and supplier document matching. The order statuses follow last, from the
// quantities just rewritten. The caller holds the tenant's posting locks.
func Rebuild(ctx context.Context, q db.Querier, now time.Time) ([]posting.Rebuilt, error) {
	out, err := posting.RunRepairs(ctx, q, checkQuery, rebuildRepairs, now)
	if err != nil {
		return nil, fmt.Errorf("procurement: %w", err)
	}
	statuses, err := posting.FixStatuses(ctx, q, statusChecks, now)
	if err != nil {
		return nil, fmt.Errorf("procurement: %w", err)
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
	return "", fmt.Errorf("procurement: no verify check %q", name)
}
