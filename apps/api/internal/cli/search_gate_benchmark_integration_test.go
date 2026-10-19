//go:build integration

package cli

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/seedload"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// searchGateQuery is one picker query of the gate and the group its latency is reported in.
type searchGateQuery struct {
	group   string
	q       string
	inStock bool
	// first is the code that must rank first, for exact code lookups.
	first string
}

// searchGateQueries mixes Vietnamese names typed with and without diacritics, the in-stock filter
// of the POS picker, a query that matches nothing (a full scan) and exact SKU and kit codes.
var searchGateQueries = func() []searchGateQuery {
	names := [][2]string{
		{"nước mắm", "nuoc mam"},
		{"dầu gội đầu", "dau goi dau"},
		{"áo thun trơn size l", "ao thun tron size l"},
		{"bình giữ nhiệt 500ml", "binh giu nhiet 500ml"},
		{"đèn led 9w", "den led 9w"},
		{"sữa tươi tiệt trùng", "sua tuoi tiet trung"},
		{"giỏ quà tết", "gio qua tet"},
		{"khăn tắm hồng hà", "khan tam hong ha"},
		{"chảo chống dính 24cm", "chao chong dinh 24cm"},
		{"đường kính trắng", "duong kinh trang"},
		{"không có zzzz", "khong co zzzz"},
	}
	var out []searchGateQuery
	for _, n := range names {
		out = append(out, searchGateQuery{group: "accent", q: n[0]}, searchGateQuery{group: "plain", q: n[1]})
	}
	for _, q := range []string{"nước mắm", "ao thun", "bút bi thiên long", "den led"} {
		out = append(out, searchGateQuery{group: "instock", q: q, inStock: true})
	}
	for _, code := range []string{"SP00001", "SP04321", "SP09876", "SP15000", "SP19500", "KIT002", "COMBO001"} {
		out = append(out, searchGateQuery{group: "code", q: code, first: code})
	}
	return out
}()

var (
	searchGateOnce   sync.Once
	searchGateTenant uuid.UUID
)

// seedSearchGate fills one tenant at the plan limit of 20 000 SKUs through `tally seed load`, so
// every product, SKU and search text is written by the catalog service, with a little stock.
func seedSearchGate(b *testing.B) {
	env := serviceEnv(b)
	env["SMTP_PORT"] = "1025"
	start := time.Now()
	out, err := run(b, env, "seed", "load", "--tenants", "1", "--skus", "20000", "--moves", "2000", "--seed", "42",
		"--slug-prefix", "search")
	if err != nil {
		b.Fatalf("seed load = %q, %v", out, err)
	}
	b.Logf("seeded a 20 000-SKU tenant in %s", time.Since(start).Round(time.Second))
	super := testkit.SuperDB(b)
	if err := super.Get(&searchGateTenant, `SELECT id FROM tally.tenants WHERE slug = $1`, "search-42-1"); err != nil {
		b.Fatal(err)
	}
	var skus int
	if err := super.Get(&skus, `SELECT count(*) FROM tally.skus WHERE tenant_id = $1`, searchGateTenant); err != nil {
		b.Fatal(err)
	}
	if skus != seedload.MaxSkus {
		b.Fatalf("seeded %d skus, want %d", skus, seedload.MaxSkus)
	}
	if _, err := super.Exec(`ANALYZE`); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkSearchAtPlanLimit measures the picker search of a tenant at the 20 000-SKU plan limit,
// built through the domain services, and reports the p50, p95 and worst latency of one search in
// total and per query group (accent, plain, instock, code). It fails when the p95 of all searches
// passes the one-second release gate. The latency is the catalog service's, without HTTP, the
// middleware and JSON, so it is a lower bound of what a client waits. It runs only with -bench;
// see the command in the security and load report.
func BenchmarkSearchAtPlanLimit(b *testing.B) {
	searchGateOnce.Do(func() { seedSearchGate(b) })
	clk := testkit.Clock()
	svc := app.NewDomains(testkit.DB(b), clk, audit.NewRecorder(clk)).Catalog
	ctx := context.Background()
	scope := catalog.Scope{TenantID: searchGateTenant, ReadOnly: true}
	search := func(q searchGateQuery) []catalog.SearchItem {
		items, err := svc.Search(ctx, scope, catalog.SearchRequest{Kind: catalog.SearchSku, Q: q.q, InStock: q.inStock})
		if err != nil {
			b.Fatalf("search %q: %v", q.q, err)
		}
		return items
	}
	// Warm every query once outside the measurement, and check that the generated catalog answers
	// names typed either way, ranks an exact code first and has stock the in-stock filter finds. The
	// seed stocks a random part of the catalog, so one in-stock query may rightly find nothing.
	inStock := 0
	for _, q := range searchGateQueries {
		items := search(q)
		switch {
		case q.first != "" && (len(items) == 0 || items[0].Code != q.first):
			b.Fatalf("exact %s = %+v", q.q, items)
		case q.group == "accent" || q.group == "plain":
			if want := q.q != "không có zzzz" && q.q != "khong co zzzz"; want != (len(items) > 0) {
				b.Fatalf("%q found %d SKUs", q.q, len(items))
			}
		case q.group == "instock":
			inStock += len(items)
		}
	}
	if inStock == 0 {
		b.Fatal("no in-stock query found a SKU, so the in-stock filter was never measured on stock")
	}

	took := make(map[string][]time.Duration)
	b.ResetTimer()
	for i := range b.N {
		q := searchGateQueries[i%len(searchGateQueries)]
		start := time.Now()
		search(q)
		d := time.Since(start)
		took[q.group] = append(took[q.group], d)
		took["all"] = append(took["all"], d)
	}
	b.StopTimer()
	for group, ds := range took {
		slices.Sort(ds)
		at := func(p float64) time.Duration { return ds[min(len(ds)-1, int(p*float64(len(ds))))] }
		b.ReportMetric(float64(at(0.50).Microseconds())/1000, group+"-p50-ms")
		b.ReportMetric(float64(at(0.95).Microseconds())/1000, group+"-p95-ms")
		b.ReportMetric(float64(ds[len(ds)-1].Microseconds())/1000, group+"-max-ms")
		if group == "all" && at(0.95) > searchGateP95 {
			b.Errorf("p95 of a search = %s, over the %s release gate", at(0.95), searchGateP95)
		}
	}
}

// searchGateP95 is the release gate of the picker search.
const searchGateP95 = time.Second
