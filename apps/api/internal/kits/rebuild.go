package kits

import (
	"context"
	"fmt"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// rebuildRepairs rewrite the assembly order totals the verify checks compare, keyed by the
// check. The batches and disassemblies themselves are posted documents, not projections: a
// mismatch between them and their moves keeps its rows for Verify to report. A quantity the
// CHECK constraints refuse is left the same way.
var rebuildRepairs = []posting.Repair{
	{Check: "assembly order completed", Stmt: `
		UPDATE assembly_orders o SET completed_qty = bad.expected::numeric, updated_at = $1, version = o.version + 1
		FROM bad WHERE o.id = bad.id AND o.tenant_id = ` + posting.InTenant + ` AND bad.expected::numeric >= 0`},
	{Check: "assembly order disassembled", Stmt: `
		UPDATE assembly_orders o SET disassembled_qty = bad.expected::numeric, updated_at = $1, version = o.version + 1
		FROM bad WHERE o.id = bad.id AND o.tenant_id = ` + posting.InTenant + ` AND bad.expected::numeric >= 0`},
	// The check reports one row per order and component SKU; an order holding the SKU on one item
	// only takes the whole sum. Items carry no version, so their order's is bumped.
	{Check: "assembly order component consumed", Parent: "assembly_orders", Stmt: `
		UPDATE assembly_order_items i SET consumed_base_qty = bad.expected::numeric
		FROM bad
		WHERE i.assembly_order_id = bad.id AND i.sku_id = bad.sku_id AND i.tenant_id = ` + posting.InTenant + `
		  AND bad.expected::numeric >= 0
		  AND (SELECT COUNT(*) FROM assembly_order_items x WHERE x.assembly_order_id = bad.id AND x.sku_id = bad.sku_id) = 1
		RETURNING i.assembly_order_id`},
}

// RebuildChecks names the checks whose rows Rebuild rewrites.
func RebuildChecks() []string {
	return append(posting.Checks(rebuildRepairs), posting.StatusCheckNames(statusChecks)...)
}

// Rebuild rewrites the assembly order totals of the tenant in scope that Verify finds disagreeing
// with the batches and disassemblies posted on them, then their statuses. The caller holds the
// tenant's posting locks.
func Rebuild(ctx context.Context, q db.Querier, now time.Time) ([]posting.Rebuilt, error) {
	out, err := posting.RunRepairs(ctx, q, checkQuery, rebuildRepairs, now)
	if err != nil {
		return nil, fmt.Errorf("kits: %w", err)
	}
	statuses, err := posting.FixStatuses(ctx, q, statusChecks, now)
	if err != nil {
		return nil, fmt.Errorf("kits: %w", err)
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
	return "", fmt.Errorf("kits: no verify check %q", name)
}
