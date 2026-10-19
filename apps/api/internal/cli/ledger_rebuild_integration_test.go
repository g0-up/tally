//go:build integration

package cli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// rebuiltProjections are the projection columns a rebuild rewrites, per table. avg_cost is left
// out: a rebuild derives it from the rebuilt stock value, not from the costing history.
var rebuiltProjections = []struct{ table, cols string }{
	{"sku_stock", "sku_id, on_hand, reserved, quarantine_qty, expired_qty, sellable, stock_value"},
	{"lot_balances", "lot_id, qty"},
	{"fund_accounts", "id, balance"},
	{"obligations", "id, settled_amount, status"},
	{"payments", "id, allocated_amount"},
	{"receipt_lines", "id, returned_base_qty"},
	{"assembly_orders", "id, completed_qty, status"},
	{"invoice_lines", "id, delivered_base_qty"},
	{"invoices", "id, delivered_amount, status"},
	{"sales_document_lines", "id, returned_base_qty"},
	{"sales_documents", "id, status, cogs_amount"},
}

// projectionDigest digests the projections of a tenant: their rebuilt columns, or whole rows
// (versions and timestamps included) when whole is set.
func projectionDigest(t *testing.T, super *sqlx.DB, tenantID string, whole bool) string {
	t.Helper()
	var b strings.Builder
	for _, p := range rebuiltProjections {
		row := "row(" + p.cols + ")::text"
		if whole {
			row = "t::text"
		}
		var digest string
		if err := super.Get(&digest, `SELECT md5(COALESCE(string_agg(`+row+`, ',' ORDER BY `+row+`), '')) FROM tally.`+p.table+` t WHERE tenant_id = $1`, tenantID); err != nil {
			t.Fatalf("digest %s: %v", p.table, err)
		}
		b.WriteString(p.table + "=" + digest + "\n")
	}
	return b.String()
}

// corrupt runs one tampering statement on the tenant and requires it to change exactly one row.
func corrupt(t *testing.T, super *sqlx.DB, tenantID, what, stmt string) {
	t.Helper()
	res, err := super.Exec(stmt, tenantID)
	if err != nil {
		t.Fatalf("corrupt %s: %v", what, err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("corrupt %s changed %d rows (%v), want 1", what, n, err)
	}
}

func TestLedgerRebuildCommand(t *testing.T) {
	env := loadEnv(t)
	super := testkit.SuperDB(t)
	if out, err := run(t, env, "seed", "load", "--tenants", "2", "--skus", "30", "--moves", "400", "--seed", "5", "--slug-prefix", "rebuild"); err != nil {
		t.Fatalf("seed load = %q, %v", out, err)
	}
	tenant, other := loadTenantID(t, super, "rebuild-5-1"), loadTenantID(t, super, "rebuild-5-2")
	consistent := projectionDigest(t, super, tenant, false)
	otherBefore := projectionDigest(t, super, other, true)

	if out, err := run(t, env, "ledger", "rebuild", "--tenant", tenant, "--dry-run"); err != nil ||
		!strings.Contains(out, "dry run of tenant "+tenant+": 0 differences, 0 to rebuild, 0 to fix by hand; nothing written") {
		t.Fatalf("dry run of a consistent tenant = %q, %v", out, err)
	}

	// Tamper with one row of each kind of projection; the CHECK constraints tie the sku_stock
	// quantities together, so on hand and sellable move as one.
	corrupt(t, super, tenant, "sku stock", `
		UPDATE tally.sku_stock SET on_hand = on_hand + 5, sellable = sellable + 5, stock_value = stock_value + 1000
		WHERE tenant_id = $1 AND sku_id = (SELECT sku_id FROM tally.sku_stock WHERE tenant_id = $1 AND on_hand > 0 ORDER BY sku_id LIMIT 1)`)
	corrupt(t, super, tenant, "lot balance", `
		UPDATE tally.lot_balances SET qty = qty + 3
		WHERE tenant_id = $1 AND lot_id = (SELECT lot_id FROM tally.lot_balances WHERE tenant_id = $1 AND qty > 0 ORDER BY lot_id DESC LIMIT 1)`)
	corrupt(t, super, tenant, "fund", `
		UPDATE tally.fund_accounts SET balance = balance + 777
		WHERE tenant_id = $1 AND id = (SELECT id FROM tally.fund_accounts WHERE tenant_id = $1 ORDER BY id LIMIT 1)`)
	corrupt(t, super, tenant, "obligation", `
		UPDATE tally.obligations SET settled_amount = 0, status = 'open'
		WHERE tenant_id = $1 AND id = (SELECT id FROM tally.obligations WHERE tenant_id = $1 AND settled_amount > 0 ORDER BY id LIMIT 1)`)
	corrupt(t, super, tenant, "payment", `
		UPDATE tally.payments SET allocated_amount = 0
		WHERE tenant_id = $1 AND id = (SELECT id FROM tally.payments WHERE tenant_id = $1 AND allocated_amount > 0 ORDER BY id LIMIT 1)`)
	corrupt(t, super, tenant, "receipt line", `
		UPDATE tally.receipt_lines SET returned_base_qty = 0
		WHERE tenant_id = $1 AND id = (SELECT id FROM tally.receipt_lines WHERE tenant_id = $1 AND returned_base_qty > 0 ORDER BY id LIMIT 1)`)
	corrupt(t, super, tenant, "assembly order", `
		UPDATE tally.assembly_orders SET completed_qty = completed_qty + 1
		WHERE tenant_id = $1 AND id = (SELECT id FROM tally.assembly_orders WHERE tenant_id = $1 AND completed_qty > 0 ORDER BY id LIMIT 1)`)
	corrupt(t, super, tenant, "invoice line", `
		UPDATE tally.invoice_lines SET delivered_base_qty = 0
		WHERE tenant_id = $1 AND id = (SELECT id FROM tally.invoice_lines WHERE tenant_id = $1 AND delivered_base_qty > 0 ORDER BY id LIMIT 1)`)
	corrupt(t, super, tenant, "invoice", `
		UPDATE tally.invoices SET delivered_amount = 0
		WHERE tenant_id = $1 AND id = (SELECT id FROM tally.invoices WHERE tenant_id = $1 AND delivered_amount > 0 ORDER BY id DESC LIMIT 1)`)
	// A status that does not follow from the delivered or assembled quantities. Verify derives the
	// status from the stored quantities, so the invoice is one whose quantities were left alone.
	corrupt(t, super, tenant, "invoice status", `
		UPDATE tally.invoices SET status = 'partially_delivered'
		WHERE tenant_id = $1 AND id = (
		  SELECT i.id FROM tally.invoices i WHERE i.tenant_id = $1 AND i.status = 'completed' AND i.delivered_amount > 0
		    AND NOT EXISTS (SELECT 1 FROM tally.invoice_lines l WHERE l.tenant_id = $1 AND l.invoice_id = i.id AND l.delivered_base_qty = 0)
		  ORDER BY i.id LIMIT 1)`)
	corrupt(t, super, tenant, "assembly order status", `
		UPDATE tally.assembly_orders SET status = 'partially_completed'
		WHERE tenant_id = $1 AND id = (SELECT id FROM tally.assembly_orders WHERE tenant_id = $1 AND status = 'completed' ORDER BY id DESC LIMIT 1)`)
	// A returned document goes back to posted and its returned quantity halves: the line still
	// has something returned, so each is a difference of its own.
	corrupt(t, super, tenant, "sales document status", `
		UPDATE tally.sales_documents SET status = 'posted'
		WHERE tenant_id = $1 AND id = (SELECT id FROM tally.sales_documents WHERE tenant_id = $1 AND status = 'partially_reversed' ORDER BY id LIMIT 1)`)
	corrupt(t, super, tenant, "sales document line", `
		UPDATE tally.sales_document_lines SET returned_base_qty = returned_base_qty / 2
		WHERE tenant_id = $1 AND id = (SELECT id FROM tally.sales_document_lines WHERE tenant_id = $1 AND returned_base_qty > 0 ORDER BY id LIMIT 1)`)
	corrupt(t, super, tenant, "sales document cogs", `
		UPDATE tally.sales_documents SET cogs_amount = cogs_amount + 1
		WHERE tenant_id = $1 AND id = (SELECT id FROM tally.sales_documents WHERE tenant_id = $1 AND reversal_of IS NULL ORDER BY id LIMIT 1)`)
	corrupted := projectionDigest(t, super, tenant, true)

	out, err := run(t, env, "ledger", "rebuild", "--tenant", tenant, "--dry-run")
	if err != nil {
		t.Fatalf("dry run = %q, %v", out, err)
	}
	for _, want := range []string{
		"inventory: sku on hand ", "inventory: sku stock value ", "inventory: lot balance ", "cash: fund ",
		"debt: obligations ", "debt: payments ", "procurement: receipt line returned ", "kits: assembly order completed ",
		"sales: invoice line delivered ", "sales: invoice delivered ", "sales: sales document status ",
		"sales: sales document line returned ", "sales: sales document cogs ", "sales: invoice status ",
		"kits: assembly order status ",
	} {
		if !strings.Contains(out, "rebuild tenant "+tenant+" "+want) {
			t.Errorf("dry run does not report %q to rebuild:\n%s", want, out)
		}
	}
	if !strings.Contains(out, " 0 to fix by hand; nothing written") || strings.Contains(out, "manual tenant") ||
		!strings.Contains(out, "would rebuild tenant "+tenant+" sales: invoice delivered: 1 rows") {
		t.Errorf("dry run output:\n%s", out)
	}
	if got := projectionDigest(t, super, tenant, true); got != corrupted {
		t.Errorf("dry run wrote projections:\n%s\nwant\n%s", got, corrupted)
	}

	// Without a flag the command is a dry run too.
	if out, err := run(t, env, "ledger", "rebuild", "--tenant", tenant); err != nil || !strings.Contains(out, "nothing written (take a backup, then pass --confirm") {
		t.Errorf("rebuild without a flag = %q, %v; want a dry run", out, err)
	}
	if _, err := run(t, env, "ledger", "rebuild", "--tenant", tenant, "--dry-run", "--confirm"); err == nil {
		t.Error("rebuild with --dry-run and --confirm succeeded")
	}
	if _, err := run(t, env, "ledger", "rebuild", "--tenant", ids.New().String(), "--confirm"); err == nil || !strings.Contains(err.Error(), "no tenant has the id") {
		t.Errorf("rebuild of an unknown tenant = %v", err)
	}
	if got := projectionDigest(t, super, tenant, true); got != corrupted {
		t.Error("a refused rebuild wrote projections")
	}

	countRebuilds := func() int {
		return loadCount(t, super, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND action = 'ledger.rebuild' AND source = 'cli'`, tenant)
	}

	// A document that disagrees with its own lines is not a projection the rebuild recomputes:
	// the dry run marks it manual, and a written rebuild that still finds it rolls back whole,
	// the fund it did recompute included, and records nothing.
	const wrongReturn = `
		UPDATE tally.sales_returns SET total_amount = total_amount + %[1]s, refund_due_amount = refund_due_amount + %[1]s
		WHERE tenant_id = $1 AND id = (SELECT id FROM tally.sales_returns WHERE tenant_id = $1 ORDER BY id LIMIT 1)`
	corrupt(t, super, tenant, "sales return", fmt.Sprintf(wrongReturn, "1"))
	if out, err := run(t, env, "ledger", "rebuild", "--tenant", tenant, "--dry-run"); err != nil ||
		!strings.Contains(out, "manual tenant "+tenant+" sales: sales return total ") || !strings.Contains(out, " 1 to fix by hand;") {
		t.Errorf("dry run of a document error = %q, %v", out, err)
	}
	if out, err := run(t, env, "ledger", "rebuild", "--tenant", tenant, "--confirm"); err == nil ||
		!strings.Contains(err.Error(), "rolled back, nothing written: 1 differences remain") ||
		!strings.Contains(out, "manual tenant "+tenant+" sales: sales return total ") {
		t.Errorf("rebuild over a document error = %q, %v", out, err)
	}
	if got := projectionDigest(t, super, tenant, true); got != corrupted {
		t.Errorf("a rolled back rebuild wrote projections:\n%s\nwant\n%s", got, corrupted)
	}
	if n := countRebuilds(); n != 0 {
		t.Errorf("ledger.rebuild audit events after a rolled back rebuild = %d, want 0", n)
	}
	corrupt(t, super, tenant, "sales return", fmt.Sprintf(wrongReturn, "-1"))

	out, err = run(t, env, "ledger", "rebuild", "--tenant", tenant, "--confirm")
	if err != nil {
		t.Fatalf("rebuild = %q, %v", out, err)
	}
	if !strings.Contains(out, "rebuilt tenant "+tenant+" cash: fund: 1 rows") ||
		!strings.Contains(out, "rebuilt tenant "+tenant+" sales: sales document cogs: 1 rows") ||
		!strings.Contains(out, "verified: 0 differences") {
		t.Errorf("rebuild output = %q", out)
	}
	if out, err := run(t, env, "ledger", "verify", "--tenant", tenant); err != nil || !strings.Contains(out, "0 mismatches") {
		t.Errorf("verify after the rebuild = %q, %v", out, err)
	}
	if got := projectionDigest(t, super, tenant, false); got != consistent {
		t.Errorf("rebuilt projections differ from the consistent ones:\n%s\nwant\n%s", got, consistent)
	}
	if got := projectionDigest(t, super, other, true); got != otherBefore {
		t.Errorf("rebuild changed the other tenant:\n%s\nwant\n%s", got, otherBefore)
	}
	if n := countRebuilds(); n != 1 {
		t.Errorf("ledger.rebuild audit events = %d, want 1", n)
	}
}

// The ledger commands scope a tenant by row level security, so they refuse a role that bypasses
// it before reading anything.
func TestLedgerCommandsRefuseARoleThatBypassesRLS(t *testing.T) {
	env := serviceEnv(t)
	env["DATABASE_URL"] = testkit.SuperURL(t)
	tenant := testkit.NewTenant(t, testkit.DB(t)).ID.String()
	for _, args := range [][]string{
		{"ledger", "verify"},
		{"ledger", "verify", "--tenant", tenant},
		{"ledger", "rebuild", "--tenant", tenant, "--dry-run"},
		{"ledger", "rebuild", "--tenant", tenant, "--confirm"},
	} {
		out, err := run(t, env, args...)
		if err == nil || !strings.Contains(err.Error(), "bypasses row level security; run the ledger commands as the application role") {
			t.Errorf("%v as superuser = %q, %v; want a refusal", args, out, err)
		}
	}
	env["DATABASE_URL"] = testkit.AppURL(t)
	if out, err := run(t, env, "ledger", "verify", "--tenant", tenant); err != nil || !strings.Contains(out, "0 mismatches") {
		t.Errorf("verify as the application role = %q, %v", out, err)
	}
}
