package inventory

import (
	"context"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// verifyCheck is one invariant of the stock ledger: its query returns the rows that break it,
// with the stored and the expected value as text. Each {{column}} marks where the query is
// restricted to the SKUs asked for, the grouped ledger subqueries included, so a check of one SKU
// reads that SKU's rows only.
type verifyCheck struct {
	name, query string
}

// skuFilterRe matches the {{column}} SKU restriction marks of a check.
var skuFilterRe = regexp.MustCompile(`\{\{([a-z_.]+)\}\}`)

var verifyChecks = []verifyCheck{
	{"lot balance", `
		SELECT l.id, l.lot_no AS label, COALESCE(b.qty, 0)::text AS stored, COALESCE(m.qty, 0)::text AS expected
		FROM lots l LEFT JOIN lot_balances b ON b.lot_id = l.id
		LEFT JOIN (SELECT lot_id, SUM(qty) AS qty FROM stock_moves WHERE {{sku_id}} GROUP BY lot_id) m ON m.lot_id = l.id
		WHERE {{l.sku_id}} AND COALESCE(b.qty, 0) <> COALESCE(m.qty, 0)
		ORDER BY l.id`},
	{"lot balance identity", `
		SELECT l.id, l.lot_no AS label, b.sku_id::text || '/' || COALESCE(b.color_id::text, '-') AS stored,
		  l.sku_id::text || '/' || COALESCE(l.color_id::text, '-') AS expected
		FROM lots l JOIN lot_balances b ON b.lot_id = l.id
		WHERE {{l.sku_id}} AND (b.sku_id <> l.sku_id OR b.color_id IS DISTINCT FROM l.color_id)
		ORDER BY l.id`},
	{"move color", `
		SELECT m.id, m.doc_type AS label, COALESCE(m.color_id::text, '-') AS stored, COALESCE(l.color_id::text, '-') AS expected
		FROM stock_moves m JOIN lots l ON l.id = m.lot_id
		WHERE {{m.sku_id}} AND (m.sku_id <> l.sku_id OR m.color_id IS DISTINCT FROM l.color_id)
		ORDER BY m.id`},
	{"sku on hand", `
		SELECT s.id, s.code AS label, COALESCE(ss.on_hand, 0)::text AS stored, COALESCE(m.qty, 0)::text AS expected
		FROM skus s LEFT JOIN sku_stock ss ON ss.sku_id = s.id
		LEFT JOIN (SELECT sku_id, SUM(qty) AS qty FROM stock_moves WHERE {{sku_id}} GROUP BY sku_id) m ON m.sku_id = s.id
		WHERE {{s.id}} AND COALESCE(ss.on_hand, 0) <> COALESCE(m.qty, 0)
		ORDER BY s.id`},
	{"sku on hand by lots", `
		SELECT ss.sku_id AS id, s.code AS label, ss.on_hand::text AS stored, COALESCE(b.qty, 0)::text AS expected
		FROM sku_stock ss JOIN skus s ON s.id = ss.sku_id
		LEFT JOIN (SELECT sku_id, SUM(qty) AS qty FROM lot_balances WHERE {{sku_id}} GROUP BY sku_id) b ON b.sku_id = ss.sku_id
		WHERE {{ss.sku_id}} AND ss.on_hand <> COALESCE(b.qty, 0)
		ORDER BY ss.sku_id`},
	{"sku stock value", `
		SELECT s.id, s.code AS label, COALESCE(ss.stock_value, 0)::text AS stored, COALESCE(m.value, 0)::text AS expected
		FROM skus s LEFT JOIN sku_stock ss ON ss.sku_id = s.id
		LEFT JOIN (SELECT sku_id, SUM(value_amount) AS value FROM stock_moves WHERE {{sku_id}} GROUP BY sku_id) m ON m.sku_id = s.id
		WHERE {{s.id}} AND COALESCE(ss.stock_value, 0) <> COALESCE(m.value, 0)
		ORDER BY s.id`},
	{"sku quarantine", `
		SELECT ss.sku_id AS id, s.code AS label, ss.quarantine_qty::text AS stored, COALESCE(b.qty, 0)::text AS expected
		FROM sku_stock ss JOIN skus s ON s.id = ss.sku_id
		LEFT JOIN (SELECT b.sku_id, SUM(b.qty) AS qty FROM lot_balances b JOIN lots l ON l.id = b.lot_id
		           WHERE l.is_quarantine AND {{b.sku_id}} GROUP BY b.sku_id) b ON b.sku_id = ss.sku_id
		WHERE {{ss.sku_id}} AND ss.quarantine_qty <> COALESCE(b.qty, 0)
		ORDER BY ss.sku_id`},
	{"sku sellable", `
		SELECT ss.sku_id AS id, s.code AS label, (ss.sellable + ss.quarantine_qty + ss.expired_qty)::text AS stored,
		  ss.on_hand::text AS expected
		FROM sku_stock ss JOIN skus s ON s.id = ss.sku_id
		WHERE {{ss.sku_id}} AND ss.sellable + ss.quarantine_qty + ss.expired_qty <> ss.on_hand
		ORDER BY ss.sku_id`},
	// expired_qty was rolled on some business date up to today and lots only expire further, so
	// it may lag the lots past expiry but never exceed them. The verifier runs as the read-only
	// role, which may not read the tenant's time zone; no tenant's business date is past the UTC date + 1.
	{"sku expired", `
		SELECT ss.sku_id AS id, s.code AS label, ss.expired_qty::text AS stored, COALESCE(e.qty, 0)::text AS expected
		FROM sku_stock ss JOIN skus s ON s.id = ss.sku_id
		LEFT JOIN (SELECT b.sku_id, SUM(b.qty) AS qty FROM lot_balances b JOIN lots l ON l.id = b.lot_id
		           WHERE NOT l.is_quarantine AND l.expiry_date < (now() AT TIME ZONE 'UTC')::date + 1 AND {{b.sku_id}}
		           GROUP BY b.sku_id) e ON e.sku_id = ss.sku_id
		WHERE {{ss.sku_id}} AND (ss.expired_qty < 0 OR ss.expired_qty > COALESCE(e.qty, 0))
		ORDER BY ss.sku_id`},
	{"sku reserved", `
		SELECT ss.sku_id AS id, s.code AS label, ss.reserved::text AS stored, COALESCE(r.qty, 0)::text AS expected
		FROM sku_stock ss JOIN skus s ON s.id = ss.sku_id
		LEFT JOIN (SELECT sku_id, SUM(qty - consumed_qty - released_qty) AS qty FROM reservations
		           WHERE status = 'active' AND {{sku_id}} GROUP BY sku_id) r ON r.sku_id = ss.sku_id
		WHERE {{ss.sku_id}} AND ss.reserved <> COALESCE(r.qty, 0)
		ORDER BY ss.sku_id`},
	{"negative stock", `
		SELECT ss.sku_id AS id, s.code AS label,
		  concat_ws('/', ss.on_hand, ss.sellable, ss.quarantine_qty, ss.reserved, ss.stock_value) AS stored, '>= 0' AS expected
		FROM sku_stock ss JOIN skus s ON s.id = ss.sku_id
		WHERE {{ss.sku_id}}
		  AND (ss.on_hand < 0 OR ss.sellable < 0 OR ss.quarantine_qty < 0 OR ss.reserved < 0 OR ss.stock_value < 0)
		ORDER BY ss.sku_id`},
	// Taking a receipt back out empties the stock value when it empties the stock and never takes
	// more value than there is, so only the quantity and lot of a receipt's reversal mirror it
	// exactly; its value is an outflow. Every other reversal, the quarantine leg into its twin lot
	// included, mirrors the value too: moving stock in or out of quarantine never changes its value.
	{"reversal mirror", `
		SELECT m.id, m.doc_type AS label, m.qty::text || ' ' || m.value_amount::text AS stored,
		  (-o.qty)::text || ' ' || CASE WHEN o.qty > 0 AND o.move_type <> 'quarantine_in' THEN '<= 0'
		    ELSE (-o.value_amount)::text END AS expected
		FROM stock_moves m JOIN stock_moves o ON o.id = m.reversal_of
		WHERE {{m.sku_id}} AND {{o.sku_id}} AND (m.qty <> -o.qty OR m.lot_id <> o.lot_id
		  OR (o.qty > 0 AND o.move_type <> 'quarantine_in' AND m.value_amount > 0)
		  OR ((o.qty <= 0 OR o.move_type = 'quarantine_in') AND m.value_amount <> -o.value_amount))
		ORDER BY m.id`},
}

// Verify checks the stock projections of the tenant in scope against the stock_moves ledger
// and returns one line per mismatch: lot balances and sku_stock equal the sum of their moves,
// quarantine, sellable, expired and reserved agree with the lots and the open reservations,
// nothing is negative and every reversal mirrors the quantity and lot of its move (and the value
// of any move but a receipt).
func Verify(ctx context.Context, q db.Querier) ([]string, error) {
	return verify(ctx, q, nil)
}

// VerifySkus runs Verify for some SKUs only (the stock card badge, `ledger verify --sku`).
func VerifySkus(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) ([]string, error) {
	if len(skuIDs) == 0 {
		return nil, nil
	}
	return verify(ctx, q, skuIDs)
}

func verify(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) ([]string, error) {
	type row struct {
		ID       uuid.UUID `db:"id"`
		Label    string    `db:"label"`
		Stored   string    `db:"stored"`
		Expected string    `db:"expected"`
	}
	var out []string
	for _, c := range verifyChecks {
		var rows []row
		var err error
		if skuIDs == nil {
			err = sqlx.SelectContext(ctx, q, &rows, skuFilterRe.ReplaceAllString(c.query, "TRUE"))
		} else {
			err = sqlx.SelectContext(ctx, q, &rows, skuFilterRe.ReplaceAllString(c.query, "$1 = ANY($$1)"), skuIDs)
		}
		if err != nil {
			return nil, fmt.Errorf("inventory: verify %s: %w", c.name, err)
		}
		for _, r := range rows {
			out = append(out, fmt.Sprintf("%s %s (%s): stored %s, ledger %s", c.name, r.ID, r.Label, r.Stored, r.Expected))
		}
	}
	return out, nil
}
