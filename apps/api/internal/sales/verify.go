package sales

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// verifyChecks are the checks of Verify, each a query returning the mismatching rows as (id, label,
// stored, expected).
var verifyChecks = []struct {
	name, query string
}{
	{"invoice line delivered", `
		WITH delivered AS (
		  SELECT id, SUM(qty) AS qty FROM (
		    SELECT sl.invoice_line_id AS id, sl.base_qty AS qty
		    FROM stock_out_lines sl JOIN stock_outs so ON so.id = sl.stock_out_id
		    WHERE so.status = 'posted' AND so.reversal_of IS NULL
		    UNION ALL
		    SELECT dl.invoice_line_id, dl.base_qty
		    FROM sales_document_lines dl JOIN sales_documents d ON d.id = dl.sales_document_id
		    WHERE d.source = 'stock_out' AND d.status <> 'reversed' AND d.reversal_of IS NULL
		      AND dl.line_kind IN ('mix_pack', 'combo') AND dl.invoice_line_id IS NOT NULL) u
		  GROUP BY id)
		SELECT l.id, i.doc_no || ' #' || l.line_no AS label, l.delivered_base_qty::text AS stored,
		       COALESCE(d.qty, 0)::text AS expected
		FROM invoice_lines l JOIN invoices i ON i.id = l.invoice_id LEFT JOIN delivered d ON d.id = l.id
		WHERE l.delivered_base_qty <> COALESCE(d.qty, 0)
		ORDER BY l.id`},
	{"invoice delivered", `
		WITH delivered AS (
		  SELECT invoice_id AS id, SUM(total_amount) AS amount
		  FROM sales_documents
		  WHERE source = 'stock_out' AND status <> 'reversed' AND reversal_of IS NULL
		  GROUP BY invoice_id)
		SELECT i.id, i.doc_no AS label, i.delivered_amount::text AS stored, COALESCE(d.amount, 0)::text AS expected
		FROM invoices i LEFT JOIN delivered d ON d.id = i.id
		WHERE i.delivered_amount <> COALESCE(d.amount, 0)
		ORDER BY i.id`},
	{"sales document line returned", `
		WITH returned AS (
		  SELECT rl.sales_document_line_id AS id, SUM(rl.base_qty) AS qty
		  FROM sales_return_lines rl JOIN sales_returns r ON r.id = rl.sales_return_id
		  WHERE r.status = 'posted' AND rl.sales_document_line_id IS NOT NULL
		  GROUP BY rl.sales_document_line_id)
		SELECT l.id, d.doc_no || ' #' || l.line_no AS label, l.returned_base_qty::text AS stored,
		       COALESCE(b.qty, 0)::text AS expected
		FROM sales_document_lines l JOIN sales_documents d ON d.id = l.sales_document_id
		  LEFT JOIN returned b ON b.id = l.id
		WHERE l.returned_base_qty <> COALESCE(b.qty, 0) OR l.returned_base_qty > l.base_qty
		ORDER BY l.id`},
	{"sales document status", `
		SELECT d.id, d.doc_no AS label, d.status AS stored, 'partially_reversed' AS expected
		FROM sales_documents d
		WHERE d.status = 'posted'
		  AND EXISTS (SELECT 1 FROM sales_document_lines l WHERE l.sales_document_id = d.id AND l.returned_base_qty > 0)
		ORDER BY d.id`},
	{"sales document cogs", `
		WITH cogs AS (
		  SELECT sales_document_id AS id, SUM(cogs_amount) AS amount
		  FROM sales_document_lines GROUP BY sales_document_id)
		SELECT d.id, d.doc_no AS label, d.cogs_amount::text AS stored, COALESCE(c.amount, 0)::text AS expected
		FROM sales_documents d LEFT JOIN cogs c ON c.id = d.id
		WHERE d.reversal_of IS NULL AND d.cogs_amount <> COALESCE(c.amount, 0)
		ORDER BY d.id`},
	{"sales document receivable", `
		SELECT d.id, d.doc_no AS label, COALESCE(o.amount::text, 'none') AS stored,
		       CASE WHEN d.payer_party_id IS NOT NULL AND d.total_amount > 0 THEN d.total_amount::text ELSE 'none' END AS expected
		FROM sales_documents d
		  LEFT JOIN obligations o ON o.direction = 'receivable' AND o.source_type = 'sales_document' AND o.source_id = d.id
		WHERE d.reversal_of IS NULL
		  AND ((d.payer_party_id IS NOT NULL AND d.total_amount > 0 AND (o.id IS NULL OR o.amount <> d.total_amount))
		    OR ((d.payer_party_id IS NULL OR d.total_amount = 0) AND o.id IS NOT NULL))
		ORDER BY d.id`},
	{"reversed sales document receivable", `
		SELECT d.id, d.doc_no AS label, 'open' AS stored, 'reversed' AS expected
		FROM sales_documents d
		  JOIN obligations o ON o.direction = 'receivable' AND o.source_type = 'sales_document' AND o.source_id = d.id
		WHERE d.status = 'reversed' AND o.reversed_on IS NULL
		ORDER BY d.id`},
	{"pos cart sales document", `
		SELECT c.id, c.doc_no AS label, COALESCE(d.pos_cart_id::text, 'none') AS stored, c.id::text AS expected
		FROM pos_carts c LEFT JOIN sales_documents d ON d.id = c.sales_document_id
		WHERE c.sales_document_id IS NOT NULL AND (d.pos_cart_id IS NULL OR d.pos_cart_id <> c.id)
		UNION ALL
		SELECT d.id, d.doc_no, COALESCE(c.sales_document_id::text, 'none'), d.id::text
		FROM sales_documents d JOIN pos_carts c ON c.id = d.pos_cart_id
		WHERE d.reversal_of IS NULL AND (c.sales_document_id IS NULL OR c.sales_document_id <> d.id)
		ORDER BY 1`},
	{"open shift", `
		SELECT s.id, s.doc_no AS label, n.open::text AS stored, '1' AS expected
		FROM shifts s CROSS JOIN (SELECT COUNT(*) AS open FROM shifts WHERE status = 'open') n
		WHERE s.status = 'open' AND n.open > 1
		ORDER BY s.id`},
	{"sales return total", `
		WITH lines AS (
		  SELECT sales_return_id AS id, SUM(line_amount) AS amount FROM sales_return_lines GROUP BY sales_return_id)
		SELECT r.id, r.doc_no AS label, r.total_amount::text AS stored, COALESCE(l.amount, 0)::text AS expected
		FROM sales_returns r LEFT JOIN lines l ON l.id = r.id
		WHERE r.total_amount <> COALESCE(l.amount, 0)
		ORDER BY r.id`},
	{"exchange amounts", `
		SELECT x.id, x.doc_no AS label,
		       x.returned_amount || '/' || x.taken_amount || '/' || x.store_refunds_amount || '/' || COALESCE(r.exchange_id::text, 'none') AS stored,
		       r.total_amount || '/' || d.total_amount || '/' || r.refund_due_amount || '/' || x.id AS expected
		FROM exchanges x JOIN sales_returns r ON r.id = x.sales_return_id JOIN sales_documents d ON d.id = x.new_sales_document_id
		WHERE x.returned_amount <> r.total_amount OR x.taken_amount <> d.total_amount
		   OR x.store_refunds_amount <> r.refund_due_amount OR r.exchange_id IS DISTINCT FROM x.id
		ORDER BY x.id`},
	{"exchange settlement", `
		SELECT x.id, x.doc_no AS label,
		       (x.returned_amount - x.debt_reduction_amount - x.store_refunds_amount + x.customer_pays_amount)::text AS stored,
		       x.taken_amount::text AS expected
		FROM exchanges x
		WHERE x.returned_amount - x.debt_reduction_amount - x.store_refunds_amount + x.customer_pays_amount <> x.taken_amount
		ORDER BY x.id`},
}

// Verify checks the sales projections of the tenant of q against the documents they are kept
// from. It returns one line per mismatch.
//   - Each invoice line has delivered what its posted stock outs took. A mix pack or combo header
//     holds no stock, so it counts what the standing sales documents of its stock outs carried.
//   - Each invoice has delivered the total of its sales documents, and an open or completed
//     invoice has the status deliveryStatus makes of its lines' delivered quantities.
//   - Each sales document line has returned what the posted returns took back on it, and a
//     document something was returned from is partially reversed.
//   - Each sales document carries Σ its lines' cost of goods.
//   - Each sales document with a payer and a total has its one receivable for that total, and a
//     reversed document's receivable is reversed too.
//   - A sold cart and its sales document point at each other.
//   - A tenant has at most one open shift.
//   - Each return's total is Σ its lines.
//   - Each exchange carries the amounts of its return and its new sale, and its return's value goes
//     to the original sale's debt, the new sale and the refund with the difference paid in
//     (returned - debt reduction - refund + customer pays = taken).
func Verify(ctx context.Context, q db.Querier) ([]string, error) {
	type row struct {
		ID       uuid.UUID `db:"id"`
		Label    string    `db:"label"`
		Stored   string    `db:"stored"`
		Expected string    `db:"expected"`
	}
	var out []string
	for _, c := range verifyChecks {
		var rows []row
		if err := sqlx.SelectContext(ctx, q, &rows, c.query); err != nil {
			return nil, fmt.Errorf("sales: verify %s: %w", c.name, err)
		}
		for _, r := range rows {
			out = append(out, fmt.Sprintf("%s %s (%s): stored %s, expected %s", c.name, r.ID, r.Label, r.Stored, r.Expected))
		}
	}
	statuses, err := posting.VerifyStatuses(ctx, q, statusChecks)
	if err != nil {
		return nil, fmt.Errorf("sales: %w", err)
	}
	return append(out, statuses...), nil
}

// statusChecks compare document statuses with the quantities they follow.
var statusChecks = []posting.StatusCheck{
	{Name: "invoice status", Table: "invoices", Find: invoiceStatusMismatches},
}

// statusBatch bounds the documents a status check reads lines for at once.
const statusBatch = 500

// invoiceStatusMismatches returns the invoices whose status is not what deliveryStatus, as a
// stock out and its reversal apply it, makes of their lines. A draft delivers nothing and a
// cancelled remainder keeps its status, so only open and completed invoices are read.
func invoiceStatusMismatches(ctx context.Context, q db.Querier) ([]posting.StatusMismatch, error) {
	var invoices []struct {
		ID      uuid.UUID `db:"id"`
		DocNo   string    `db:"doc_no"`
		Status  string    `db:"status"`
		Version int       `db:"version"`
	}
	if err := sqlx.SelectContext(ctx, q, &invoices, `
		SELECT id, doc_no, status, version FROM invoices
		WHERE tenant_id = `+posting.InTenant+` AND status IN ($1, $2, $3) ORDER BY id`,
		InvoiceConfirmed, InvoicePartiallyDelivered, InvoiceCompleted); err != nil {
		return nil, fmt.Errorf("read invoices: %w", err)
	}
	var out []posting.StatusMismatch
	for batch := range slices.Chunk(invoices, statusBatch) {
		ids := make([]uuid.UUID, len(batch))
		for i, inv := range batch {
			ids[i] = inv.ID
		}
		var lines []struct {
			InvoiceID uuid.UUID `db:"invoice_id"`
			InvoiceLine
		}
		if err := sqlx.SelectContext(ctx, q, &lines, `
			SELECT invoice_id, id, line_no, line_kind, base_qty, delivered_base_qty, cancelled_base_qty
			FROM invoice_lines WHERE invoice_id = ANY($1) ORDER BY invoice_id, line_no`, ids); err != nil {
			return nil, fmt.Errorf("read invoice lines: %w", err)
		}
		byInvoice := make(map[uuid.UUID][]InvoiceLine, len(batch))
		for _, l := range lines {
			byInvoice[l.InvoiceID] = append(byInvoice[l.InvoiceID], l.InvoiceLine)
		}
		for _, inv := range batch {
			if want := deliveryStatus(inv.Status, byInvoice[inv.ID]); want != inv.Status {
				out = append(out, posting.StatusMismatch{ID: inv.ID, Version: inv.Version, Label: inv.DocNo, Stored: inv.Status, Expected: want})
			}
		}
	}
	return out, nil
}
