//go:build integration

package app_test

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/reporting"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// TestStockReportsOverALargeLedger times the stock reports and their export over a ledger of
// ten moves for each of TALLY_REPORT_LOAD_SKUS goods (20,000 by default, so 200,000 moves). It
// runs only with TALLY_REPORT_LOAD=1:
//
//	TALLY_REPORT_LOAD=1 go test -tags integration -count=1 -run TestStockReportsOverALargeLedger -v ./internal/app/
func TestStockReportsOverALargeLedger(t *testing.T) {
	if os.Getenv("TALLY_REPORT_LOAD") != "1" {
		t.Skip("set TALLY_REPORT_LOAD=1 to run the reporting load test")
	}
	skus := 20000
	if v := os.Getenv("TALLY_REPORT_LOAD_SKUS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("TALLY_REPORT_LOAD_SKUS = %q, want a positive number", v)
		}
		skus = n
	}
	s := newReportingStack(t, nil)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	template := st.newSku(t, a, "MAU", "10000")
	st.receive(t, a, template, nil, "1", "7000")

	// Every good is received three at a time and sold one at a time, a week apart, from 1 January:
	// ten moves, ten on hand, worth 70,000.
	seeded := time.Now()
	ctx := context.Background()
	var moves int64
	testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
		// Seeding is not what is measured: lift the interactive statement timeout for it.
		if _, err := q.ExecContext(ctx, `SELECT set_config('statement_timeout', '0', true)`); err != nil {
			return err
		}
		for _, stmt := range []string{
			`INSERT INTO skus (id, tenant_id, product_id, code, variant_key, display_name, base_unit_id, search_norm,
				created_at, updated_at)
			 SELECT gen_random_uuid(), t.tenant_id, t.product_id, 'TAI-' || lpad(i::text, 6, '0'), 'tai-' || i,
				'Hàng tải ' || i, t.base_unit_id, 'hang tai ' || i, t.created_at, t.updated_at
			 FROM skus t, generate_series(1, $2::int) i WHERE t.id = $1`,
			`INSERT INTO lots (id, tenant_id, warehouse_id, sku_id, received_on, created_at)
			 SELECT gen_random_uuid(), s.tenant_id, l.warehouse_id, s.id, DATE '2026-01-01', now()
			 FROM skus s, (SELECT warehouse_id FROM lots WHERE sku_id = $1 LIMIT 1) l
			 WHERE s.code LIKE 'TAI-%' AND $2::int > 0`,
			`INSERT INTO stock_moves (id, tenant_id, warehouse_id, sku_id, lot_id, qty, unit_cost, value_amount, move_type,
				doc_type, doc_id, business_date, posted_at, posted_by, created_at)
			 SELECT gen_random_uuid(), l.tenant_id, l.warehouse_id, l.sku_id, l.id,
				CASE WHEN k % 2 = 0 THEN 3 ELSE -1 END, 7000, CASE WHEN k % 2 = 0 THEN 21000 ELSE -7000 END,
				CASE WHEN k % 2 = 0 THEN 'receipt' ELSE 'sale' END, 'load', gen_random_uuid(), DATE '2026-01-01' + k * 7,
				now(), $1, now()
			 FROM lots l JOIN skus s ON s.id = l.sku_id, generate_series(0, 9) k
			 WHERE s.code LIKE 'TAI-%' AND $2::int > 0`,
			`INSERT INTO sku_stock (tenant_id, warehouse_id, sku_id, on_hand, sellable, avg_cost, stock_value, updated_at)
			 SELECT l.tenant_id, l.warehouse_id, l.sku_id, 10, 10, 7000, 70000, now()
			 FROM lots l JOIN skus s ON s.id = l.sku_id WHERE s.code LIKE 'TAI-%' AND $1::uuid IS NOT NULL AND $2::int > 0`,
			`INSERT INTO lot_balances (tenant_id, lot_id, sku_id, qty, updated_at)
			 SELECT l.tenant_id, l.id, l.sku_id, 10, now()
			 FROM lots l JOIN skus s ON s.id = l.sku_id WHERE s.code LIKE 'TAI-%' AND $1::uuid IS NOT NULL AND $2::int > 0`,
		} {
			if _, err := q.ExecContext(ctx, stmt, template, skus); err != nil {
				return err
			}
		}
		return q.QueryRowxContext(ctx, `SELECT count(*) FROM stock_moves`).Scan(&moves)
	})
	// Planner statistics only; the rows were written through the application role.
	if _, err := testkit.SuperDB(t).ExecContext(ctx, `ANALYZE tally.skus, tally.lots, tally.stock_moves, tally.sku_stock, tally.lot_balances`); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d goods and %d stock moves in %s", skus+1, moves, time.Since(seeded).Round(time.Millisecond))
	if moves < int64(skus)*10 {
		t.Fatalf("stock moves = %d, want at least %d", moves, skus*10)
	}

	timed := func(what string, fn func()) {
		t.Helper()
		start := time.Now()
		fn()
		t.Logf("%-48s %s", what, time.Since(start).Round(time.Millisecond))
	}
	for _, period := range []string{"from=2026-03-01&to=2026-03-15", "from=2026-01-01&to=2026-03-15"} {
		var first reportOut
		timed("nxt first page, totals included ("+period+")", func() { first = report(t, a, "nxt?"+period+"&limit=50") })
		if len(first.Rows) != 50 || first.Totals == nil || first.NextCursor == nil {
			t.Fatalf("nxt first page = %d rows, totals %v", len(first.Rows), first.Totals)
		}
		wantNum(t, "closing total", first.Totals["closing_qty"], strconv.Itoa(skus*10+1))
		cursor := *first.NextCursor
		for i := 2; i <= 5; i++ {
			var page reportOut
			timed("nxt page "+strconv.Itoa(i)+" ("+period+")", func() {
				page = report(t, a, "nxt?"+period+"&limit=50&cursor="+cursor)
			})
			if len(page.Rows) != 50 || page.Totals != nil || page.NextCursor == nil {
				t.Fatalf("nxt page %d = %d rows, totals %v", i, len(page.Rows), page.Totals)
			}
			cursor = *page.NextCursor
		}
	}
	timed("nxt searched by code", func() {
		if got := report(t, a, "nxt?from=2026-01-01&to=2026-03-15&q=hang+tai+19999"); len(got.Rows) != 1 {
			t.Errorf("searched nxt = %d rows", len(got.Rows))
		}
	})
	timed("lot stock first page, totals included", func() {
		if got := report(t, a, "lot-stock?limit=50"); len(got.Rows) != 50 || got.Totals == nil {
			t.Errorf("lot stock = %d rows, totals %v", len(got.Rows), got.Totals)
		}
	})

	rec := a.do(http.MethodPost, v1+"/export-jobs", map[string]any{"report_key": "nxt",
		"params": map[string]string{"from": "2026-01-01", "to": "2026-03-15"}})
	expect(t, rec, http.StatusAccepted, reporting.ExportQueued)
	job := testkit.DecodeJSON[exportOut](t, rec).ID.String()
	timed("nxt export of every good", func() {
		if n, err := s.domains.Reporting.RunExports(ctx, st.tenant, slog.New(slog.DiscardHandler)); err != nil || n != 1 {
			t.Fatalf("run exports = %d, %v", n, err)
		}
	})
	rec = a.do(http.MethodGet, v1+"/export-jobs/"+job+"/download", nil)
	expect(t, rec, http.StatusFound, "")
	data, ok := s.objects.object(strings.TrimPrefix(rec.Header().Get("Location"), memStoreURL))
	if !ok {
		t.Fatal("the export has no file")
	}
	// The header, every good and the totals.
	if lines := strings.Count(string(data), "\n"); lines != skus+3 {
		t.Errorf("export lines = %d, want %d", lines, skus+3)
	}
	t.Logf("export file %d bytes", len(data))
}
