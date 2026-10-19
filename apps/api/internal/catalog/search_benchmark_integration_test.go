//go:build integration

package catalog

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// benchSkuCount is the catalog size the search must stay fast at.
const benchSkuCount = 20_000

var (
	benchOnce  sync.Once
	benchScope Scope
	// benchExact is a sample of SKU codes with their barcodes, to look up exactly.
	benchExact []struct{ code, barcode string }
)

var (
	benchKinds   = []string{"Bánh quy bơ", "Bánh đậu xanh", "Kẹo dẻo", "Sữa tươi", "Sữa chua uống", "Nước ngọt", "Trà xanh", "Cà phê hòa tan", "Mì ăn liền", "Bánh gạo", "Hạt điều rang", "Nước mắm", "Dầu ăn", "Bột giặt", "Nước rửa chén", "Kẹo dừa"}
	benchBrands  = []string{"Hải Hà", "Kinh Đô", "Bibica", "Vinamilk", "TH true", "Rồng Vàng", "Tân Tân", "Oishi", "Acecook", "Nam Ngư", "Tường An", "Omo", "Sunlight", "Bến Tre", "Trung Nguyên", "Cosy"}
	benchSizes   = []string{"100g", "180ml", "250g", "330ml", "454g", "500g", "1kg", "1.5L", "2kg", "hộp thiếc", "gói lớn", "lốc 4"}
	benchFlavors = []string{"Dâu", "Cam", "Nho", "Sô-cô-la"}
)

// seedSearchCatalog writes benchSkuCount SKUs in one tenant: 12 000 single-SKU products and
// 2 000 products of 4 flavours, each SKU with a barcode, the search text built by SearchIndexer.
func seedSearchCatalog(tb testing.TB) {
	h := newHarness(tb)
	a := h.tenant(tb)
	benchScope = scopeOfActor(a)
	unit := h.unitID(tb, a, "hộp")
	now := testkit.Epoch

	var (
		pID, pName, pKey                    []string
		sID, sProduct, sCode, sName, sLabel []string
		sKey, sNorm, bID, bCode             []string
	)
	addProduct := func(name string) string {
		id := ids.New().String()
		pID, pName, pKey = append(pID, id), append(pName, name), append(pKey, nameKey(name))
		return id
	}
	addSku := func(product, name, label, key string) {
		n := len(sID)
		code, barcode := fmt.Sprintf("SP-%05d", n), fmt.Sprintf("893%010d", n)
		display := name
		if label != "" {
			display = name + " – " + label
		}
		sID, sProduct, sCode = append(sID, ids.New().String()), append(sProduct, product), append(sCode, code)
		sName, sLabel, sKey = append(sName, display), append(sLabel, label), append(sKey, key)
		sNorm = append(sNorm, SearchIndexer{}.Text(SkuSearchText{Code: code, DisplayName: display, Barcodes: []string{barcode}}))
		bID, bCode = append(bID, ids.New().String()), append(bCode, barcode)
		if n%997 == 0 {
			benchExact = append(benchExact, struct{ code, barcode string }{code, barcode})
		}
	}
	name := func(i int) string {
		return fmt.Sprintf("%s %s %s %d", benchKinds[i%len(benchKinds)], benchBrands[(i/len(benchKinds))%len(benchBrands)],
			benchSizes[i%len(benchSizes)], i)
	}
	for i := range 12_000 {
		addSku(addProduct(name(i)), name(i), "", "")
	}
	for i := 12_000; i < 14_000; i++ {
		p := addProduct(name(i))
		for _, f := range benchFlavors {
			addSku(p, name(i), f, uuid.NewSHA1(uuid.NameSpaceOID, []byte(f)).String())
		}
	}
	if len(sID) != benchSkuCount {
		tb.Fatalf("generated %d skus", len(sID))
	}

	ctx := context.Background()
	testkit.InTenant(tb, h.d, a.TenantID, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO products (id, tenant_id, type, name, name_key, status, created_at, updated_at)
			SELECT id, $1, 'goods', name, key, 'active', $2, $2
			FROM unnest($3::uuid[], $4::text[], $5::text[]) AS t(id, name, key)`,
			a.TenantID, now, pID, pName, pKey); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO skus (id, tenant_id, product_id, code, variant_key, display_name, variant_label, base_unit_id, status,
			                  search_norm, created_at, updated_at)
			SELECT id, $1, product_id, code, key, name, label, $2, 'active', norm, $3, $3
			FROM unnest($4::uuid[], $5::uuid[], $6::text[], $7::text[], $8::text[], $9::text[], $10::text[])
			     AS t(id, product_id, code, key, name, label, norm)`,
			a.TenantID, unit, now, sID, sProduct, sCode, sKey, sName, sLabel, sNorm); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO sku_units (tenant_id, sku_id, unit_id, factor, is_base, created_at, updated_at)
			SELECT $1, id, $2, 1, true, $3, $3 FROM unnest($4::uuid[]) AS t(id)`,
			a.TenantID, unit, now, sID); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `
			INSERT INTO sku_barcodes (id, tenant_id, sku_id, unit_id, code, created_at)
			SELECT id, $1, sku_id, $2, code, $3 FROM unnest($4::uuid[], $5::uuid[], $6::text[]) AS t(id, sku_id, code)`,
			a.TenantID, unit, now, bID, sID, bCode)
		return err
	})
	if _, err := testkit.SuperDB(tb).ExecContext(ctx, `ANALYZE tally.products, tally.skus, tally.sku_units, tally.sku_barcodes`); err != nil {
		tb.Fatal(err)
	}
}

// BenchmarkSearch measures the SKU picker on a 20 000-SKU catalog over a mix of picker queries
// and reports the p50, p95 and worst latency of one search.
func BenchmarkSearch(b *testing.B) {
	benchOnce.Do(func() { seedSearchCatalog(b) })
	h := newHarness(b)
	ctx := context.Background()
	queries := []string{"banh dau xanh", "sua", "keo deo dau", "nuoc ngot cosy 330ml", "tra xanh tan tan", "zzzz khong co", "hai ha"}
	for _, e := range benchExact {
		queries = append(queries, e.code, e.barcode)
	}

	// The first query of each kind pays for cold caches; warm up outside the measurement and check
	// that exact codes and barcodes rank first on the generated catalog.
	for _, q := range queries {
		if _, err := h.svc.Search(ctx, benchScope, SearchRequest{Kind: SearchSku, Q: q}); err != nil {
			b.Fatal(err)
		}
	}
	for _, e := range benchExact {
		for _, q := range []string{e.code, e.barcode} {
			items, err := h.svc.Search(ctx, benchScope, SearchRequest{Kind: SearchSku, Q: q})
			if err != nil || len(items) == 0 || items[0].Code != e.code {
				b.Fatalf("exact %s = %+v, %v", q, items, err)
			}
		}
	}
	took := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for i := range b.N {
		q := queries[i%len(queries)]
		start := time.Now()
		items, err := h.svc.Search(ctx, benchScope, SearchRequest{Kind: SearchSku, Q: q})
		took = append(took, time.Since(start))
		if err != nil {
			b.Fatal(err)
		}
		if q == "banh dau xanh" && len(items) == 0 {
			b.Fatal("no hits for a known name")
		}
	}
	b.StopTimer()
	slices.Sort(took)
	at := func(p float64) time.Duration { return took[min(len(took)-1, int(p*float64(len(took))))] }
	b.ReportMetric(float64(at(0.50).Microseconds())/1000, "p50-ms")
	b.ReportMetric(float64(at(0.95).Microseconds())/1000, "p95-ms")
	b.ReportMetric(float64(took[len(took)-1].Microseconds())/1000, "max-ms")
}
