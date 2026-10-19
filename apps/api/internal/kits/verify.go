package kits

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/posting"
)

// The checks share these CTEs, each summing the rows of one kind once per document.
const (
	// consumed is what each batch's consume moves took out, scrap included.
	consumed = `consumed AS (
		SELECT doc_id AS id, -SUM(qty) AS qty, -SUM(value_amount) AS value
		FROM stock_moves WHERE doc_type = 'assembly_posting' AND move_type = 'assembly_consume' GROUP BY doc_id)`
	// produced is what each batch's produce move brought into its kit lot.
	produced = `produced AS (
		SELECT doc_id AS id, SUM(qty) AS qty, SUM(value_amount) AS value
		FROM stock_moves WHERE doc_type = 'assembly_posting' AND move_type = 'assembly_produce' GROUP BY doc_id)`
	// used is what each batch put into its kits: consumption less scrap.
	used = `used AS (
		SELECT assembly_posting_id AS id, SUM(consumed_base_qty) AS consumed, SUM(consumed_base_qty - scrap_base_qty) AS qty
		FROM assembly_posting_items GROUP BY assembly_posting_id)`
	// taken is what each disassembly's consume moves took out of the kit lot.
	taken = `taken AS (
		SELECT doc_id AS id, -SUM(qty) AS qty, -SUM(value_amount) AS value
		FROM stock_moves WHERE doc_type = 'disassembly' AND move_type = 'disassembly_consume' GROUP BY doc_id)`
	// returned is what each disassembly line's return moves gave back to its component lot.
	returned = `returned AS (
		SELECT doc_line_id AS id, SUM(qty) AS qty, SUM(value_amount) AS value
		FROM stock_moves WHERE doc_type = 'disassembly' AND move_type = 'disassembly_return' GROUP BY doc_line_id)`
	// quarantined is what each disassembly line moved on to a quarantine lot.
	quarantined = `quarantined AS (
		SELECT doc_line_id AS id, SUM(qty) AS qty
		FROM stock_moves WHERE doc_type = 'disassembly' AND move_type = 'quarantine_in' GROUP BY doc_line_id)`
)

// verifyChecks are the invariants of the kits documents: each query returns the rows that break
// one, with the stored and the expected value as text.
var verifyChecks = []struct {
	name, query string
}{
	{"assembly posting balance", `
		WITH ` + consumed + `, ` + produced + `
		SELECT ap.id, o.doc_no || ' ' || l.lot_no AS label, COALESCE(p.value, 0)::text AS stored,
		       (COALESCE(c.value, 0) + ap.packing_cost)::text AS expected
		FROM assembly_postings ap JOIN assembly_orders o ON o.id = ap.assembly_order_id JOIN lots l ON l.id = ap.output_lot_id
		  LEFT JOIN consumed c ON c.id = ap.id LEFT JOIN produced p ON p.id = ap.id
		WHERE COALESCE(p.value, 0) <> COALESCE(c.value, 0) + ap.packing_cost OR COALESCE(p.qty, 0) <> ap.produced_qty
		ORDER BY ap.id`},
	{"assembly posting components cost", `
		WITH ` + consumed + `
		SELECT ap.id, o.doc_no || ' ' || l.lot_no AS label, ap.components_cost::text AS stored, COALESCE(c.value, 0)::text AS expected
		FROM assembly_postings ap JOIN assembly_orders o ON o.id = ap.assembly_order_id JOIN lots l ON l.id = ap.output_lot_id
		  LEFT JOIN consumed c ON c.id = ap.id
		WHERE ap.components_cost <> COALESCE(c.value, 0)
		ORDER BY ap.id`},
	{"assembly posting unit cost", `
		SELECT ap.id, o.doc_no || ' ' || l.lot_no AS label, ap.output_unit_cost::text AS stored,
		       round((ap.components_cost + ap.packing_cost) / ap.produced_qty, 4)::text AS expected
		FROM assembly_postings ap JOIN assembly_orders o ON o.id = ap.assembly_order_id JOIN lots l ON l.id = ap.output_lot_id
		WHERE ap.output_unit_cost <> round((ap.components_cost + ap.packing_cost) / ap.produced_qty, 4)
		ORDER BY ap.id`},
	{"assembly posting consumption", `
		WITH ` + consumed + `, ` + used + `
		SELECT ap.id, o.doc_no || ' ' || l.lot_no AS label, COALESCE(u.consumed, 0)::text AS stored,
		       COALESCE(c.qty, 0)::text AS expected
		FROM assembly_postings ap JOIN assembly_orders o ON o.id = ap.assembly_order_id JOIN lots l ON l.id = ap.output_lot_id
		  LEFT JOIN consumed c ON c.id = ap.id LEFT JOIN used u ON u.id = ap.id
		WHERE COALESCE(u.consumed, 0) <> COALESCE(c.qty, 0)
		ORDER BY ap.id`},
	{"lot genealogy", `
		WITH ` + used + `, linked AS (
		  SELECT assembly_posting_id AS id, SUM(child_base_qty) AS qty FROM lot_genealogy GROUP BY assembly_posting_id)
		SELECT ap.id, o.doc_no || ' ' || l.lot_no AS label, COALESCE(g.qty, 0)::text AS stored, COALESCE(u.qty, 0)::text AS expected
		FROM assembly_postings ap JOIN assembly_orders o ON o.id = ap.assembly_order_id JOIN lots l ON l.id = ap.output_lot_id
		  LEFT JOIN used u ON u.id = ap.id LEFT JOIN linked g ON g.id = ap.id
		WHERE COALESCE(g.qty, 0) <> COALESCE(u.qty, 0)
		ORDER BY ap.id`},
	{"kit lot expiry", `
		WITH earliest AS (
		  SELECT g.assembly_posting_id AS id, MIN(cl.expiry_date) AS expiry
		  FROM lot_genealogy g JOIN lots cl ON cl.id = g.child_lot_id GROUP BY g.assembly_posting_id)
		SELECT ap.id, o.doc_no || ' ' || l.lot_no AS label, COALESCE(l.expiry_date::text, 'none') AS stored,
		       COALESCE(e.expiry::text, 'none') AS expected
		FROM assembly_postings ap JOIN assembly_orders o ON o.id = ap.assembly_order_id JOIN lots l ON l.id = ap.output_lot_id
		  LEFT JOIN earliest e ON e.id = ap.id
		WHERE l.expiry_date IS DISTINCT FROM e.expiry
		ORDER BY ap.id`},
	{"disassembly balance", `
		WITH ` + taken + `, ` + returned + `, back AS (
		  SELECT dl.disassembly_id AS id, SUM(r.value) AS value
		  FROM disassembly_lines dl JOIN returned r ON r.id = dl.id GROUP BY dl.disassembly_id
		), expensed AS (
		  SELECT source_doc_id AS id, SUM(amount) AS value
		  FROM cogs_adjustments WHERE source_doc_type = '` + CogsSourceDisassembly + `' GROUP BY source_doc_id)
		SELECT d.id, d.doc_no AS label,
		       d.qty::text || ' kits, ' || (COALESCE(b.value, 0) + COALESCE(e.value, 0))::text || ' returned and expensed' AS stored,
		       COALESCE(t.qty, 0)::text || ' kits, ' || COALESCE(t.value, 0)::text || ' returned and expensed' AS expected
		FROM disassemblies d LEFT JOIN taken t ON t.id = d.id LEFT JOIN back b ON b.id = d.id LEFT JOIN expensed e ON e.id = d.id
		WHERE COALESCE(b.value, 0) + COALESCE(e.value, 0) <> COALESCE(t.value, 0) OR COALESCE(t.qty, 0) <> d.qty
		ORDER BY d.id`},
	{"disassembly line returned", `
		WITH ` + returned + `
		SELECT dl.id, d.doc_no || ' ' || l.lot_no AS label, (dl.return_base_qty + dl.damaged_base_qty)::text AS stored,
		       COALESCE(r.qty, 0)::text AS expected
		FROM disassembly_lines dl JOIN disassemblies d ON d.id = dl.disassembly_id JOIN lots l ON l.id = dl.source_lot_id
		  LEFT JOIN returned r ON r.id = dl.id
		WHERE dl.return_base_qty + dl.damaged_base_qty <> COALESCE(r.qty, 0)
		ORDER BY dl.id`},
	{"disassembly line damaged", `
		WITH ` + quarantined + `
		SELECT dl.id, d.doc_no || ' ' || l.lot_no AS label, dl.damaged_base_qty::text AS stored, COALESCE(x.qty, 0)::text AS expected
		FROM disassembly_lines dl JOIN disassemblies d ON d.id = dl.disassembly_id JOIN lots l ON l.id = dl.source_lot_id
		  LEFT JOIN quarantined x ON x.id = dl.id
		WHERE dl.damaged_base_qty <> COALESCE(x.qty, 0)
		ORDER BY dl.id`},
	{"assembly order completed", `
		WITH made AS (
		  SELECT assembly_order_id AS id, SUM(produced_qty) AS qty FROM assembly_postings
		  WHERE reversal_of IS NULL GROUP BY assembly_order_id)
		SELECT o.id, o.doc_no AS label, o.completed_qty::text AS stored, COALESCE(m.qty, 0)::text AS expected
		FROM assembly_orders o LEFT JOIN made m ON m.id = o.id
		WHERE o.completed_qty <> COALESCE(m.qty, 0)
		ORDER BY o.id`},
	{"assembly order disassembled", `
		WITH apart AS (
		  SELECT assembly_order_id AS id, SUM(qty) AS qty FROM disassemblies
		  WHERE reversal_of IS NULL GROUP BY assembly_order_id)
		SELECT o.id, o.doc_no AS label, o.disassembled_qty::text AS stored, COALESCE(a.qty, 0)::text AS expected
		FROM assembly_orders o LEFT JOIN apart a ON a.id = o.id
		WHERE o.disassembled_qty <> COALESCE(a.qty, 0)
		ORDER BY o.id`},
	{"assembly order component consumed", `
		WITH stored AS (
		  SELECT assembly_order_id AS order_id, sku_id, SUM(consumed_base_qty) AS qty
		  FROM assembly_order_items GROUP BY assembly_order_id, sku_id
		), put AS (
		  SELECT ap.assembly_order_id AS order_id, pi.sku_id, SUM(pi.consumed_base_qty - pi.scrap_base_qty) AS qty
		  FROM assembly_posting_items pi JOIN assembly_postings ap ON ap.id = pi.assembly_posting_id
		  WHERE ap.reversal_of IS NULL GROUP BY ap.assembly_order_id, pi.sku_id
		), back AS (
		  SELECT d.assembly_order_id AS order_id, dl.sku_id, SUM(dl.return_base_qty + dl.damaged_base_qty) AS qty
		  FROM disassembly_lines dl JOIN disassemblies d ON d.id = dl.disassembly_id
		  WHERE d.reversal_of IS NULL GROUP BY d.assembly_order_id, dl.sku_id
		)
		SELECT o.id, o.doc_no || ' ' || s.code AS label, st.qty::text AS stored,
		       (COALESCE(p.qty, 0) - COALESCE(b.qty, 0))::text AS expected, st.sku_id
		FROM stored st JOIN assembly_orders o ON o.id = st.order_id JOIN skus s ON s.id = st.sku_id
		  LEFT JOIN put p ON p.order_id = st.order_id AND p.sku_id = st.sku_id
		  LEFT JOIN back b ON b.order_id = st.order_id AND b.sku_id = st.sku_id
		WHERE st.qty <> COALESCE(p.qty, 0) - COALESCE(b.qty, 0)
		ORDER BY o.id, s.code`},
}

// Verify checks the kits documents of the tenant of q against the stock moves they posted and
// against each other: each assembly batch is balanced (its components and packing make the value
// of the kits), carries its components cost, unit cost and consumption, links its kit lot to the
// component lots its kits hold, and gives the kit lot the earliest expiry of them; each
// disassembly takes its kits out of the kit lot and accounts for their whole value, given back to
// the component lots or expensed as a COGS adjustment, its lines matching the stock it returned and
// quarantined; each order carries the kits its
// batches made and its disassemblies took apart, and its items what the batches put into kits less
// what the disassemblies gave back, and its status is what AfterProgress makes of them in the
// order they were posted. It returns one line per mismatch.
func Verify(ctx context.Context, q db.Querier) ([]string, error) {
	type row struct {
		ID       uuid.UUID `db:"id"`
		Label    string    `db:"label"`
		Stored   string    `db:"stored"`
		Expected string    `db:"expected"`
		// SkuID keys the component rows of an order for their repair.
		SkuID *uuid.UUID `db:"sku_id"`
	}
	var out []string
	for _, c := range verifyChecks {
		var rows []row
		if err := sqlx.SelectContext(ctx, q, &rows, c.query); err != nil {
			return nil, fmt.Errorf("kits: verify %s: %w", c.name, err)
		}
		for _, r := range rows {
			out = append(out, fmt.Sprintf("%s %s (%s): stored %s, expected %s", c.name, r.ID, r.Label, r.Stored, r.Expected))
		}
	}
	statuses, err := posting.VerifyStatuses(ctx, q, statusChecks)
	if err != nil {
		return nil, fmt.Errorf("kits: %w", err)
	}
	return append(out, statuses...), nil
}

// statusChecks compare document statuses with the quantities they follow.
var statusChecks = []posting.StatusCheck{
	{Name: "assembly order status", Table: "assembly_orders", Find: orderStatusMismatches},
}

// orderStatusMismatches returns the orders whose status is not what their batches and
// disassemblies make of it: each one, in the order they were posted, moves the order to what
// AfterProgress gives for the net kits after it, as the postings do. An order nothing was posted
// on keeps the status planning and reserving gave it, and a closed order stays closed.
func orderStatusMismatches(ctx context.Context, q db.Querier) ([]posting.StatusMismatch, error) {
	var orders []struct {
		ID      uuid.UUID `db:"id"`
		DocNo   string    `db:"doc_no"`
		Status  string    `db:"status"`
		Version int       `db:"version"`
		Planned money.Qty `db:"planned_qty"`
	}
	if err := sqlx.SelectContext(ctx, q, &orders, `
		SELECT o.id, o.doc_no, o.status, o.version, o.planned_qty FROM assembly_orders o
		WHERE o.tenant_id = `+posting.InTenant+` AND o.status <> $1
		  AND (EXISTS (SELECT 1 FROM assembly_postings p WHERE p.assembly_order_id = o.id AND p.reversal_of IS NULL)
		    OR EXISTS (SELECT 1 FROM disassemblies d WHERE d.assembly_order_id = o.id AND d.reversal_of IS NULL))
		ORDER BY o.id`, OrderClosed); err != nil {
		return nil, fmt.Errorf("read assembly orders: %w", err)
	}
	var out []posting.StatusMismatch
	for batch := range slices.Chunk(orders, statusBatch) {
		ids := make([]uuid.UUID, len(batch))
		for i, o := range batch {
			ids[i] = o.ID
		}
		var events []struct {
			OrderID     uuid.UUID `db:"assembly_order_id"`
			Qty         money.Qty `db:"qty"`
			Disassembly bool      `db:"disassembly"`
		}
		if err := sqlx.SelectContext(ctx, q, &events, `
			SELECT assembly_order_id, qty, disassembly FROM (
			  SELECT assembly_order_id, produced_qty AS qty, false AS disassembly, posted_at, created_at, id
			  FROM assembly_postings WHERE assembly_order_id = ANY($1) AND reversal_of IS NULL
			  UNION ALL
			  SELECT assembly_order_id, qty, true, posted_at, created_at, id
			  FROM disassemblies WHERE assembly_order_id = ANY($1) AND reversal_of IS NULL) e
			ORDER BY assembly_order_id, posted_at, created_at, id`, ids); err != nil {
			return nil, fmt.Errorf("read assembly order batches: %w", err)
		}
		status := make(map[uuid.UUID]string, len(batch))
		net := make(map[uuid.UUID]money.Qty, len(batch))
		planned := make(map[uuid.UUID]money.Qty, len(batch))
		for _, o := range batch {
			status[o.ID], planned[o.ID] = o.Status, o.Planned
		}
		for _, e := range events {
			if e.Disassembly {
				net[e.OrderID] = net[e.OrderID].Sub(e.Qty)
			} else {
				net[e.OrderID] = net[e.OrderID].Add(e.Qty)
			}
			status[e.OrderID] = (AssemblyOrderStatus{}).AfterProgress(status[e.OrderID], net[e.OrderID], planned[e.OrderID], e.Disassembly)
		}
		for _, o := range batch {
			if want := status[o.ID]; want != o.Status {
				out = append(out, posting.StatusMismatch{ID: o.ID, Version: o.Version, Label: o.DocNo, Stored: o.Status, Expected: want})
			}
		}
	}
	return out, nil
}

// statusBatch bounds the documents a status check reads events for at once.
const statusBatch = 500
