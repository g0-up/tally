//go:build integration

package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// loadDocQueries count each kind of document the load generator posts, per tenant.
var loadDocQueries = []struct{ kind, query string }{
	{"receipts", `SELECT count(*) FROM tally.receipts WHERE tenant_id = $1`},
	{"supplier_returns", `SELECT count(*) FROM tally.purchase_returns WHERE tenant_id = $1`},
	{"pos_sales", `SELECT count(*) FROM tally.sales_documents WHERE tenant_id = $1 AND source = 'pos'`},
	{"sales_returns", `SELECT count(*) FROM tally.sales_returns WHERE tenant_id = $1`},
	{"invoices", `SELECT count(*) FROM tally.invoices WHERE tenant_id = $1`},
	{"stock_outs", `SELECT count(*) FROM tally.stock_outs WHERE tenant_id = $1`},
	{"payments", `SELECT count(*) FROM tally.payments WHERE tenant_id = $1 AND direction = 'in' AND party_id IS NOT NULL`},
	{"counts", `SELECT count(*) FROM tally.stock_counts WHERE tenant_id = $1 AND status = 'posted'`},
	{"kit_batches", `SELECT count(*) FROM tally.assembly_postings WHERE tenant_id = $1`},
}

func loadTenantID(t *testing.T, super *sqlx.DB, slug string) string {
	t.Helper()
	var id string
	if err := super.Get(&id, `SELECT id FROM tally.tenants WHERE slug = $1`, slug); err != nil {
		t.Fatalf("tenant %s: %v", slug, err)
	}
	return id
}

func loadCount(t *testing.T, super *sqlx.DB, query, tenantID string) int {
	t.Helper()
	var n int
	if err := super.Get(&n, query, tenantID); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// loadDigest is a digest of the stock per (SKU code, color code, lot number, quarantine) and of
// the document counts of a tenant: what the same seed must reproduce.
func loadDigest(t *testing.T, super *sqlx.DB, tenantID string) string {
	t.Helper()
	var rows []string
	err := super.Select(&rows, `
		SELECT s.code || '|' || coalesce(c.code, '') || '|' || l.lot_no || '|' || l.is_quarantine::text || '|' || b.qty::text
		FROM tally.lot_balances b
		JOIN tally.lots l ON l.tenant_id = b.tenant_id AND l.id = b.lot_id
		JOIN tally.skus s ON s.tenant_id = b.tenant_id AND s.id = b.sku_id
		LEFT JOIN tally.colors c ON c.tenant_id = b.tenant_id AND c.id = b.color_id
		WHERE b.tenant_id = $1
		ORDER BY 1`, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatalf("tenant %s holds no stock", tenantID)
	}
	h := sha256.New()
	for _, r := range rows {
		_, _ = fmt.Fprintln(h, r)
	}
	for _, q := range loadDocQueries {
		_, _ = fmt.Fprintf(h, "%s=%d\n", q.kind, loadCount(t, super, q.query, tenantID))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func loadEnv(t *testing.T) map[string]string {
	t.Helper()
	env := serviceEnv(t)
	env["SMTP_PORT"] = "1025"
	return env
}

func TestSeedLoadCommand(t *testing.T) {
	env := loadEnv(t)
	const password = "Mật-khẩu-thử-tải-2026"
	env[seedLoadPasswordEnv] = password
	super := testkit.SuperDB(t)

	start := time.Now()
	out, err := run(t, env, "seed", "load", "--tenants", "2", "--skus", "60", "--moves", "600", "--seed", "7")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("seed load = %q, %v", out, err)
	}
	t.Logf("seed load of 2 tenants took %s\n%s", elapsed, out)
	if n := strings.Count(out, "verified 1 tenants: 0 mismatches"); n != 2 {
		t.Errorf("ledger verification lines = %d, want 2:\n%s", n, out)
	}
	if strings.Contains(out, password) || strings.Contains(out, "token") || strings.Contains(out, "/verify") {
		t.Errorf("output leaks the password or the verification link:\n%s", out)
	}
	for _, slug := range []string{"load-7-1", "load-7-2"} {
		if !strings.Contains(out, "created tenant "+slug+" ") || !strings.Contains(out, "owner=owner@"+slug+".load.tally.test active") {
			t.Errorf("no creation line for %s:\n%s", slug, out)
		}
		id := loadTenantID(t, super, slug)
		if moves := loadCount(t, super, `SELECT count(*) FROM tally.stock_moves WHERE tenant_id = $1`, id); moves < 600 {
			t.Errorf("%s stock moves = %d, want >= 600", slug, moves)
		}
		if skus := loadCount(t, super, `SELECT count(*) FROM tally.skus WHERE tenant_id = $1`, id); skus != 60 {
			t.Errorf("%s skus = %d, want 60", slug, skus)
		}
		if !strings.Contains(out, "tenant "+slug+": skus=60 ") {
			t.Errorf("no summary for %s:\n%s", slug, out)
		}
		for _, q := range loadDocQueries {
			if n := loadCount(t, super, q.query, id); n == 0 {
				t.Errorf("%s has no %s", slug, q.kind)
			}
		}
		var owner string
		if err := super.Get(&owner, `SELECT status FROM tally.users WHERE tenant_id = $1 AND email = $2`, id, "owner@"+slug+".load.tally.test"); err != nil {
			t.Fatal(err)
		}
		if owner != "active" {
			t.Errorf("%s owner status = %s, want active", slug, owner)
		}
	}

	if out, err := run(t, env, "seed", "load", "--tenants", "1", "--skus", "60", "--moves", "600", "--seed", "7"); err == nil ||
		!strings.Contains(err.Error(), `"load-7-1" already exists`) {
		t.Errorf("second run over existing slugs = %q, %v; want an already-exists error", out, err)
	}
}

func TestSeedLoadIsDeterministic(t *testing.T) {
	env := loadEnv(t)
	super := testkit.SuperDB(t)
	digest := func(prefix, seed string) string {
		t.Helper()
		out, err := run(t, env, "seed", "load", "--tenants", "1", "--skus", "60", "--moves", "600", "--seed", seed, "--slug-prefix", prefix)
		if err != nil {
			t.Fatalf("seed load %s = %q, %v", prefix, out, err)
		}
		if !strings.Contains(out, "pending verification") {
			t.Errorf("owner without a password is not reported pending:\n%s", out)
		}
		return loadDigest(t, super, loadTenantID(t, super, prefix+"-"+seed+"-1"))
	}
	first, again := digest("deta", "11"), digest("detb", "11")
	if first != again {
		t.Errorf("the same seed gave different stock or document counts: %s vs %s", first, again)
	}
	if other := digest("detc", "12"); other == first {
		t.Error("a different seed gave the same stock and document counts")
	}
}

// TestSeedLoadThroughput is a medium run that reports the posting rate; it runs only when
// SEED_LOAD_THROUGHPUT is set because it takes minutes.
func TestSeedLoadThroughput(t *testing.T) {
	if os.Getenv("SEED_LOAD_THROUGHPUT") == "" {
		t.Skip("set SEED_LOAD_THROUGHPUT=1 to run the medium load")
	}
	env := loadEnv(t)
	skus, moves := os.Getenv("SEED_LOAD_SKUS"), os.Getenv("SEED_LOAD_MOVES")
	if skus == "" {
		skus = "2000"
	}
	if moves == "" {
		moves = "50000"
	}
	start := time.Now()
	out, err := run(t, env, "seed", "load", "--tenants", "1", "--skus", skus, "--moves", moves, "--seed", "42", "--slug-prefix", "rate")
	if err != nil {
		t.Fatalf("seed load = %q, %v", out, err)
	}
	t.Logf("medium seed load took %s\n%s", time.Since(start), out)
}
