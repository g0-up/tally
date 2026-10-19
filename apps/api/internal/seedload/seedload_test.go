package seedload

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func validOptions() Options {
	return Options{Tenants: DefaultTenants, Skus: DefaultSkus, Moves: DefaultMoves, Seed: DefaultSeed, SlugPrefix: DefaultSlugPrefix}
}

func TestOptionsValidate(t *testing.T) {
	if err := validOptions().Validate(); err != nil {
		t.Fatalf("defaults are invalid: %v", err)
	}
	cases := []struct {
		name string
		edit func(*Options)
		want string
	}{
		{"no tenants", func(o *Options) { o.Tenants = 0 }, "--tenants"},
		{"too many tenants", func(o *Options) { o.Tenants = MaxTenants + 1 }, "--tenants"},
		{"skus above the plan limit", func(o *Options) { o.Skus = MaxSkus + 1 }, "--skus must be between 20 and 20000"},
		{"too few skus", func(o *Options) { o.Skus = MinSkus - 1 }, "--skus"},
		{"no moves", func(o *Options) { o.Moves = 0 }, "--moves"},
		{"empty prefix", func(o *Options) { o.SlugPrefix = "" }, "--slug-prefix"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := validOptions()
			tc.edit(&o)
			err := o.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() = %v, want an error naming %q", err, tc.want)
			}
		})
	}
	both := Options{Tenants: 0, Skus: 5, Moves: 1, SlugPrefix: "x"}
	err := both.Validate()
	if err == nil || !strings.Contains(err.Error(), "--tenants") || !strings.Contains(err.Error(), "--skus") {
		t.Fatalf("Validate() = %v, want every broken flag reported", err)
	}
}

func TestSlugAndOwnerEmail(t *testing.T) {
	o := Options{Seed: 42, SlugPrefix: "load"}
	if got := o.Slug(3); got != "load-42-3" {
		t.Errorf("Slug(3) = %q", got)
	}
	o.Seed = -7
	if got := o.Slug(1); got != "load--7-1" {
		t.Errorf("Slug(1) with a negative seed = %q", got)
	}
	if got := OwnerEmail("load-42-3"); got != "owner@load-42-3.load.tally.test" {
		t.Errorf("OwnerEmail = %q", got)
	}
}

func draws(g *RNG) []int {
	var out []int
	for range 50 {
		out = append(out, g.Intn(1000), g.Between(10, 20))
	}
	return append(out, g.Sample(500, 40)...)
}

func TestRNGIsDeterministicPerSeedAndTenant(t *testing.T) {
	a, b := draws(NewRNG(42, 1)), draws(NewRNG(42, 1))
	if !slices.Equal(a, b) {
		t.Fatal("the same seed and tenant gave different draws")
	}
	if slices.Equal(a, draws(NewRNG(42, 2))) {
		t.Error("two tenants of a run share their draws")
	}
	if slices.Equal(a, draws(NewRNG(43, 1))) {
		t.Error("two seeds give the same draws")
	}
}

func TestRNGBetweenAndSample(t *testing.T) {
	g := NewRNG(1, 1)
	for range 1000 {
		if v := g.Between(3, 5); v < 3 || v > 5 {
			t.Fatalf("Between(3, 5) = %d", v)
		}
	}
	if v := g.Between(7, 2); v != 7 {
		t.Errorf("Between(7, 2) = %d, want 7", v)
	}
	for _, tc := range []struct{ n, k, want int }{{100, 10, 10}, {100, 60, 60}, {5, 9, 5}, {10, 0, 0}, {0, 3, 0}} {
		s := g.Sample(tc.n, tc.k)
		if len(s) != tc.want {
			t.Errorf("Sample(%d, %d) has %d numbers, want %d", tc.n, tc.k, len(s), tc.want)
		}
		seen := map[int]bool{}
		for _, v := range s {
			if v < 0 || v >= tc.n || seen[v] {
				t.Fatalf("Sample(%d, %d) = %v: out of range or repeated", tc.n, tc.k, s)
			}
			seen[v] = true
		}
	}
}

func TestPlanCatalogIsDeterministic(t *testing.T) {
	a, err := PlanCatalog(NewRNG(7, 1), 300, 7)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PlanCatalog(NewRNG(7, 1), 300, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("the same seed planned two catalogs")
	}
	c, err := PlanCatalog(NewRNG(8, 1), 300, 7)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(a, c) {
		t.Error("two seeds planned the same catalog")
	}
}

func TestPlanCatalogInvariants(t *testing.T) {
	for _, skus := range []int{MinSkus, 60, 2500, MaxSkus} {
		const colors = 7
		c, err := PlanCatalog(NewRNG(42, 1), skus, colors)
		if err != nil {
			t.Fatalf("%d skus: %v", skus, err)
		}
		if c.Skus() != skus {
			t.Errorf("%d skus planned %d", skus, c.Skus())
		}
		if len(c.Kits) != KitCount(skus) || !c.Kits[0].Combo {
			t.Errorf("%d skus: %d kits (combo first %v), want %d", skus, len(c.Kits), c.Kits[0].Combo, KitCount(skus))
		}
		codes, names := map[string]bool{}, map[string]bool{}
		kinds := map[GoodsKind]int{}
		for i, g := range c.Goods {
			if codes[g.Code] || names[g.Name] {
				t.Fatalf("%d skus: goods %d repeats code %s or name %q", skus, i, g.Code, g.Name)
			}
			codes[g.Code], names[g.Name] = true, true
			kinds[g.Kind]++
			if i < minPlainGoods && g.Kind != GoodsPlain {
				t.Errorf("%d skus: goods %d opens the catalog but is not plain", skus, i)
			}
			if g.Retail <= 0 || g.Wholesale <= 0 || g.Wholesale > g.Retail || g.Cost <= 0 || g.Cost > g.Retail {
				t.Errorf("%d skus: %s prices retail=%d wholesale=%d cost=%d", skus, g.Code, g.Retail, g.Wholesale, g.Cost)
			}
			if g.Kind == GoodsColored {
				if len(g.Colors) < 2 || g.Slots() != len(g.Colors) {
					t.Errorf("%d skus: %s has colors %v", skus, g.Code, g.Colors)
				}
				for _, col := range g.Colors {
					if col < 0 || col >= colors {
						t.Errorf("%d skus: %s color %d out of range", skus, g.Code, col)
					}
				}
			} else if len(g.Colors) != 0 || g.Slots() != 1 {
				t.Errorf("%d skus: %s is not colored but has colors %v", skus, g.Code, g.Colors)
			}
		}
		if skus >= 2500 && (kinds[GoodsLot] == 0 || kinds[GoodsColored] == 0 || kinds[GoodsPlain] == 0) {
			t.Errorf("%d skus: kinds %v, want every kind", skus, kinds)
		}
		for _, k := range c.Kits {
			if codes[k.Code] {
				t.Fatalf("%d skus: kit code %s repeats", skus, k.Code)
			}
			codes[k.Code] = true
			if len(k.Items) < 2 || len(k.Items) > 4 || k.Retail < 1000 {
				t.Errorf("%d skus: kit %s has %d items, retail %d", skus, k.Code, len(k.Items), k.Retail)
			}
			for _, it := range k.Items {
				if c.Goods[it.Goods].Kind != GoodsPlain || it.Qty < 1 || it.Qty > 3 {
					t.Errorf("%d skus: kit %s item %+v is not 1..3 plain goods", skus, k.Code, it)
				}
			}
		}
	}
}

func TestPlanCatalogRejectsBadSizes(t *testing.T) {
	if _, err := PlanCatalog(NewRNG(1, 1), MaxSkus+1, 7); err == nil {
		t.Error("planned more SKUs than the plan limit")
	}
	if _, err := PlanCatalog(NewRNG(1, 1), MinSkus-1, 7); err == nil {
		t.Error("planned fewer SKUs than the minimum")
	}
	if _, err := PlanCatalog(NewRNG(1, 1), 100, 1); err == nil {
		t.Error("planned colored goods with a single color")
	}
}

func TestPlanParties(t *testing.T) {
	suppliers := PlanParties(NewRNG(3, 1), 10, false)
	for _, p := range suppliers {
		if p.Person || p.Name == "" || len(p.Phone) != 10 || p.Address == "" {
			t.Errorf("supplier %+v", p)
		}
	}
	customers := PlanParties(NewRNG(3, 1), 200, true)
	people := 0
	for _, p := range customers {
		if p.Person {
			people++
		}
	}
	if people < 100 || people > 180 {
		t.Errorf("%d of 200 customers are people, want about 70%%", people)
	}
	if !reflect.DeepEqual(customers, PlanParties(NewRNG(3, 1), 200, true)) {
		t.Error("the same seed planned different customers")
	}
}

func TestPartnerSizing(t *testing.T) {
	for _, tc := range []struct{ skus, kits, suppliers, customers int }{
		{MinSkus, 2, 3, 5}, {2000, 2, 5, 20}, {MaxSkus, 12, 40, 200},
	} {
		if KitCount(tc.skus) != tc.kits || SupplierCount(tc.skus) != tc.suppliers || CustomerCount(tc.skus) != tc.customers {
			t.Errorf("%d skus: kits=%d suppliers=%d customers=%d", tc.skus, KitCount(tc.skus), SupplierCount(tc.skus), CustomerCount(tc.skus))
		}
	}
}

func TestShapeOf(t *testing.T) {
	first := shapeOf(0)
	if !first.supplierReturn || !first.salesReturn || !first.payment || !first.kitBatch || !first.count {
		t.Errorf("round 0 = %+v, want every kind of document", first)
	}
	if first.receipts == 0 || first.posSales == 0 || first.invoices == 0 {
		t.Errorf("round 0 = %+v, want receipts, sales and invoices", first)
	}
	if s := shapeOf(1); s.supplierReturn || s.salesReturn || s.payment || s.kitBatch || s.count {
		t.Errorf("round 1 = %+v, want only the regular documents", s)
	}
}

func TestStock(t *testing.T) {
	c := CatalogPlan{Goods: []GoodsPlan{{Kind: GoodsPlain}, {Kind: GoodsColored, Colors: []int{0, 3, 5}}}, Kits: []KitPlan{{}}}
	s := NewStock(c)
	if _, ok := s.StockedSlot(NewRNG(1, 1), 1); ok {
		t.Fatal("empty stock has a stocked slot")
	}
	s.Add(1, 2, 10)
	s.Add(1, 0, 4)
	if s.Total(1) != 14 || s.Avail(1, 2) != 10 {
		t.Fatalf("total %d, slot 2 holds %d", s.Total(1), s.Avail(1, 2))
	}
	if err := s.Take(1, 2, 11); err == nil {
		t.Error("took more than the slot holds")
	}
	if err := s.Take(1, 2, 0); err == nil {
		t.Error("took nothing")
	}
	if err := s.Take(1, 2, 10); err != nil || s.Avail(1, 2) != 0 {
		t.Fatalf("Take = %v, slot holds %d", err, s.Avail(1, 2))
	}
	for range 20 {
		if slot, ok := s.StockedSlot(NewRNG(1, 1), 1); !ok || slot != 0 {
			t.Fatalf("StockedSlot = %d, %v, want the only stocked slot 0", slot, ok)
		}
	}
	s.AddKits(0, 3)
	if s.Kits(0) != 3 {
		t.Errorf("kits = %d", s.Kits(0))
	}
}

func TestWithGoods(t *testing.T) {
	got := withGoods([]int{9, 4, 9}, []int{1, 4, 2, 3}, 4)
	if want := []int{9, 4, 1, 2}; !slices.Equal(got, want) {
		t.Errorf("withGoods = %v, want %v", got, want)
	}
	if got := withGoods(nil, []int{5, 6}, 5); !slices.Equal(got, []int{5, 6}) {
		t.Errorf("withGoods without force = %v", got)
	}
}

func TestSummaryWrite(t *testing.T) {
	var b bytes.Buffer
	s := Summary{Slug: "load-42-1", Skus: 60, Rounds: 3, Docs: map[DocKind]int{DocReceipts: 6, DocKitBatches: 1}, Moves: 900, Elapsed: 3 * time.Second}
	if err := s.Write(&b); err != nil {
		t.Fatal(err)
	}
	want := "tenant load-42-1: skus=60 rounds=3 receipts=6 supplier_returns=0 pos_sales=0 sales_returns=0 invoices=0 " +
		"stock_outs=0 payments=0 counts=0 kit_batches=1 stock_moves=900 elapsed=3s moves_per_second=300\n"
	if b.String() != want {
		t.Errorf("summary =\n%q\nwant\n%q", b.String(), want)
	}
}
