package procurement

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// Each check sums the child rows once per parent in a CTE and joins the sums, rather than
// summing them again for every parent row.
const (
	// returned is what the posted returns took back from each receipt line, in base units and
	// at the unit cost they left at.
	returned = `returned AS (
		SELECT xl.receipt_line_id AS id, SUM(xl.base_qty) AS qty, SUM(xl.base_qty * xl.unit_cost) AS value
		FROM purchase_return_lines xl JOIN purchase_returns x ON x.id = xl.purchase_return_id
		WHERE x.status = 'posted' GROUP BY xl.receipt_line_id)`
	// landed is the landed cost allocated to each receipt line and how many allocations made it.
	landed = `landed AS (
		SELECT receipt_line_id AS id, SUM(amount) AS amount, COUNT(*) AS n
		FROM landed_cost_allocation_lines GROUP BY receipt_line_id)`
	// priced is what the posted price adjustments put on each receipt line, and how many price
	// adjustments (reversed ones too) rounded its unit cost again.
	priced = `priced AS (
		SELECT p.receipt_line_id AS id,
		       SUM(p.on_hand_amount + p.sold_amount) FILTER (WHERE a.status = 'posted' AND a.reversal_of IS NULL) AS amount,
		       COUNT(*) AS n
		FROM purchase_price_adjustment_lines p JOIN purchase_price_adjustments a ON a.id = p.purchase_price_adjustment_id
		GROUP BY p.receipt_line_id)`
	// owed is what the store owes for each receipt: its total moved by its price adjustments.
	owed = `owed AS (
		SELECT r.id, r.total_amount + COALESCE(SUM(a.payable_delta), 0) AS amount
		FROM receipts r LEFT JOIN purchase_price_adjustments a
		  ON a.receipt_id = r.id AND a.status = 'posted' AND a.reversal_of IS NULL
		GROUP BY r.id, r.total_amount)`
)

// verifyChecks are the invariants of the procurement projections: each query returns the rows
// that break one, with the stored and the expected value as text.
var verifyChecks = []struct {
	name, query string
}{
	{"purchase order line received", `
		WITH received AS (
		  SELECT rl.purchase_order_line_id AS id, SUM(rl.base_qty) AS qty
		  FROM receipt_lines rl JOIN receipts r ON r.id = rl.receipt_id
		  WHERE rl.purchase_order_line_id IS NOT NULL AND rl.condition <> 'wrong_item'
		    AND r.status = 'posted' AND r.reversal_of IS NULL
		  GROUP BY rl.purchase_order_line_id)
		SELECT l.id, o.doc_no || ' #' || l.line_no AS label, l.received_base_qty::text AS stored,
		       COALESCE(g.qty, 0)::text AS expected
		FROM purchase_order_lines l JOIN purchase_orders o ON o.id = l.purchase_order_id
		  LEFT JOIN received g ON g.id = l.id
		WHERE l.received_base_qty <> COALESCE(g.qty, 0)
		ORDER BY l.id`},
	{"purchase order line returned", `
		WITH ` + returned + `, ordered AS (
		  SELECT rl.purchase_order_line_id AS id, SUM(b.qty) AS qty
		  FROM returned b JOIN receipt_lines rl ON rl.id = b.id
		  WHERE rl.purchase_order_line_id IS NOT NULL GROUP BY rl.purchase_order_line_id)
		SELECT l.id, o.doc_no || ' #' || l.line_no AS label, l.returned_base_qty::text AS stored,
		       COALESCE(b.qty, 0)::text AS expected
		FROM purchase_order_lines l JOIN purchase_orders o ON o.id = l.purchase_order_id
		  LEFT JOIN ordered b ON b.id = l.id
		WHERE l.returned_base_qty <> COALESCE(b.qty, 0)
		ORDER BY l.id`},
	{"receipt line returned", `
		WITH ` + returned + `
		SELECT l.id, r.doc_no || ' #' || l.line_no AS label, l.returned_base_qty::text AS stored,
		       COALESCE(b.qty, 0)::text AS expected
		FROM receipt_lines l JOIN receipts r ON r.id = l.receipt_id LEFT JOIN returned b ON b.id = l.id
		WHERE l.returned_base_qty <> COALESCE(b.qty, 0) OR l.returned_base_qty > l.base_qty
		ORDER BY l.id`},
	{"receipt line landed cost", `
		WITH ` + landed + `
		SELECT l.id, r.doc_no || ' #' || l.line_no AS label, l.landed_cost_amount::text AS stored,
		       COALESCE(c.amount, 0)::text AS expected
		FROM receipt_lines l JOIN receipts r ON r.id = l.receipt_id LEFT JOIN landed c ON c.id = l.id
		WHERE l.landed_cost_amount <> COALESCE(c.amount, 0)
		ORDER BY l.id`},
	{"receipt landed cost", `
		WITH allocated AS (
		  SELECT receipt_id AS id, SUM(amount) AS amount FROM landed_cost_allocations GROUP BY receipt_id)
		SELECT r.id, r.doc_no AS label, r.landed_cost_amount::text AS stored, COALESCE(c.amount, 0)::text AS expected
		FROM receipts r LEFT JOIN allocated c ON c.id = r.id
		WHERE r.landed_cost_amount <> COALESCE(c.amount, 0)
		ORDER BY r.id`},
	{"landed cost allocation", `
		WITH spread AS (
		  SELECT landed_cost_allocation_id AS id, SUM(amount) AS amount
		  FROM landed_cost_allocation_lines GROUP BY landed_cost_allocation_id)
		SELECT a.id, r.doc_no AS label, a.amount::text AS stored, COALESCE(c.amount, 0)::text AS expected
		FROM landed_cost_allocations a JOIN receipts r ON r.id = a.receipt_id LEFT JOIN spread c ON c.id = a.id
		WHERE a.amount <> COALESCE(c.amount, 0)
		ORDER BY a.id`},
	{"receipt payable", `
		SELECT r.id, r.doc_no AS label, COALESCE(o.amount::text, 'none') AS stored, r.total_amount::text AS expected
		FROM receipts r LEFT JOIN obligations o ON o.direction = 'payable' AND o.source_type = 'receipt' AND o.source_id = r.id
		WHERE r.reversal_of IS NULL
		  AND ((r.total_amount > 0 AND (o.id IS NULL OR o.amount <> r.total_amount))
		    OR (r.total_amount = 0 AND o.id IS NOT NULL))
		ORDER BY r.id`},
	{"reversed receipt payable", `
		SELECT r.id, r.doc_no AS label, 'open' AS stored, 'reversed' AS expected
		FROM receipts r JOIN obligations o ON o.direction = 'payable' AND o.source_type = 'receipt' AND o.source_id = r.id
		WHERE r.status = 'reversed' AND o.reversed_on IS NULL
		ORDER BY r.id`},
	{"supplier document receipt", `
		SELECT d.id, d.document_no || ' / ' || r.doc_no AS label,
		       CASE WHEN r.reversal_of IS NULL THEN r.status ELSE 'reversal' END AS stored, 'posted' AS expected
		FROM supplier_document_receipts l JOIN supplier_documents d ON d.id = l.supplier_document_id
		  JOIN receipts r ON r.id = l.receipt_id
		WHERE r.status <> 'posted' OR r.reversal_of IS NOT NULL
		ORDER BY d.id, r.id`},
	{"supplier document matched amount", `
		WITH ` + owed + `
		SELECT d.id, d.document_no || ' / ' || r.doc_no AS label, l.matched_amount::text AS stored, w.amount::text AS expected
		FROM supplier_document_receipts l JOIN supplier_documents d ON d.id = l.supplier_document_id
		  JOIN receipts r ON r.id = l.receipt_id JOIN owed w ON w.id = r.id
		WHERE l.matched_amount <> w.amount
		ORDER BY d.id, r.id`},
	// Both sides print the variance without trailing zeros, so a mismatch reads as one.
	{"supplier document variance", `
		WITH matches AS (
		  SELECT supplier_document_id AS id, SUM(matched_amount) AS amount
		  FROM supplier_document_receipts GROUP BY supplier_document_id),
		want AS (
		  SELECT d.id,
		         CASE WHEN m.id IS NULL THEN 'unmatched' WHEN m.amount = d.total_amount THEN 'matched' ELSE 'variance' END AS status,
		         CASE WHEN m.id IS NULL THEN 0 ELSE d.total_amount - m.amount END AS variance
		  FROM supplier_documents d LEFT JOIN matches m ON m.id = d.id)
		SELECT d.id, d.document_no AS label, d.match_status || ' ' || trim_scale(d.variance_amount)::text AS stored,
		       w.status || ' ' || trim_scale(w.variance)::text AS expected
		FROM supplier_documents d JOIN want w ON w.id = d.id
		WHERE d.match_status <> w.status OR d.variance_amount <> w.variance
		ORDER BY d.id`},
	// kept is the value a receipt line still carries: its amount, landed cost and price
	// adjustments, less what its returns took out at the unit cost they left at. unit_cost ×
	// kept units equals it up to the rounding of the unit cost to 4 decimals at receipt and at
	// each landed cost or price adjustment (moves); a return takes out exactly unit_cost × qty
	// and keeps the error the same.
	{"receipt line unit cost", `
		WITH ` + returned + `, ` + landed + `, ` + priced + `,
		kept AS (
		  SELECT l.id, l.line_amount + l.landed_cost_amount + COALESCE(p.amount, 0) - COALESCE(b.value, 0) AS value,
		         COALESCE(c.n, 0) + COALESCE(p.n, 0) AS moves
		  FROM receipt_lines l LEFT JOIN returned b ON b.id = l.id LEFT JOIN landed c ON c.id = l.id
		    LEFT JOIN priced p ON p.id = l.id)
		SELECT l.id, r.doc_no || ' #' || l.line_no AS label, l.unit_cost::text AS stored,
		       CASE WHEN l.base_qty > l.returned_base_qty THEN round(v.value / (l.base_qty - l.returned_base_qty), 4)::text
		            ELSE 'nothing kept, ' || v.value::text || ' left' END AS expected
		FROM receipt_lines l JOIN receipts r ON r.id = l.receipt_id JOIN kept v ON v.id = l.id
		WHERE abs(l.unit_cost * (l.base_qty - l.returned_base_qty) - v.value) > 0.00005 * l.base_qty * (1 + v.moves)
		ORDER BY l.id`},
}

// Verify checks the procurement projections of the tenant of q against the documents they are
// kept from: each PO line has received what the posted receipts brought in on it and returned
// what the posted returns took back; each receipt line has returned what the returns of it took
// and carries the landed cost allocated to it; each receipt carries Σ its allocations and each
// allocation is Σ its lines; each posted receipt owing money has its one payable for its total,
// and a reversed receipt's payable is reversed too. A supplier document is matched to posted
// receipts only, each at its total moved by its price adjustments, and its status and variance
// follow from them; each receipt line's unit cost carries what the line still holds over the
// units it keeps. A receiving or completed order has the status AfterReceipts makes of its
// received quantities. It returns one line per mismatch.
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
			return nil, fmt.Errorf("procurement: verify %s: %w", c.name, err)
		}
		for _, r := range rows {
			out = append(out, fmt.Sprintf("%s %s (%s): stored %s, expected %s", c.name, r.ID, r.Label, r.Stored, r.Expected))
		}
	}
	statuses, err := posting.VerifyStatuses(ctx, q, statusChecks)
	if err != nil {
		return nil, fmt.Errorf("procurement: %w", err)
	}
	return append(out, statuses...), nil
}

// statusChecks compare document statuses with the quantities they follow.
var statusChecks = []posting.StatusCheck{
	{Name: "purchase order status", Table: "purchase_orders", Find: orderStatusMismatches},
}

// statusBatch bounds the documents a status check reads lines for at once.
const statusBatch = 500

// orderStatusMismatches returns the orders whose status is not what AfterReceipts, as a receipt
// posting applies it, makes of their received quantities. A draft takes no receipts and a
// closed-short order keeps its status, so only receiving and completed orders are read.
func orderStatusMismatches(ctx context.Context, q db.Querier) ([]posting.StatusMismatch, error) {
	var orders []struct {
		ID      uuid.UUID `db:"id"`
		DocNo   string    `db:"doc_no"`
		Status  string    `db:"status"`
		Version int       `db:"version"`
	}
	if err := sqlx.SelectContext(ctx, q, &orders, `
		SELECT id, doc_no, status, version FROM purchase_orders
		WHERE tenant_id = `+posting.InTenant+` AND status IN ($1, $2, $3) ORDER BY id`,
		StatusConfirmed, StatusPartiallyReceived, StatusCompleted); err != nil {
		return nil, fmt.Errorf("read purchase orders: %w", err)
	}
	var out []posting.StatusMismatch
	for batch := range slices.Chunk(orders, statusBatch) {
		ids := make([]uuid.UUID, len(batch))
		for i, o := range batch {
			ids[i] = o.ID
		}
		lines, err := orderLines(ctx, q, ids...)
		if err != nil {
			return nil, err
		}
		byOrder := make(map[uuid.UUID][]PurchaseOrderLine, len(batch))
		for _, l := range lines {
			byOrder[l.PurchaseOrderID] = append(byOrder[l.PurchaseOrderID], l)
		}
		for _, o := range batch {
			if want := (PurchaseOrderStatus{}).AfterReceipts(o.Status, byOrder[o.ID]); want != o.Status {
				out = append(out, posting.StatusMismatch{ID: o.ID, Version: o.Version, Label: o.DocNo, Stored: o.Status, Expected: want})
			}
		}
	}
	return out, nil
}
