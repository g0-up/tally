//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/printing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/sales"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// newKitsStack is the sales stack with the kits routes: kits are assembled from received goods
// and sold, and combos are expanded at the counter.
func newKitsStack(t *testing.T) stack {
	t.Helper()
	clk := testkit.Clock()
	capture := &captureMailer{}
	deps := identityDeps(t, capture, clk)
	var hosts *middleware.TenantHostCache
	deps.TenantCreated = func(slug string) {
		if hosts != nil {
			hosts.Purge(slug)
		}
	}
	idSvc, err := identity.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	// Presigning an upload needs no connection; no test here reaches the object store.
	objects, err := objectstore.NewS3(objectstore.S3Config{
		Endpoint: &url.URL{Scheme: "http", Host: "127.0.0.1:9000"}, Region: "us-east-1", Bucket: "tally-dev",
		AccessKey: "tally-dev", SecretKey: "tally-dev-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	entitlements := statusEntitlements{db: testkit.SuperDB(t)}
	domains := app.NewDomains(deps.DB, clk, deps.Audit, app.WithEntitlements(entitlements))
	orgSvc := org.NewService(org.Deps{
		DB: deps.DB, Clock: clk, Audit: deps.Audit, Exceptions: exception.NewGuard(clk),
		Provisioner: org.NewProvisioner(org.NewSlugPolicy(nil), deps.Audit, clk, domains.Seeders()), Objects: objects,
		AttachmentOwners: domains.AttachmentOwners(),
	})
	a, err := app.New(app.Options{
		PublicBaseURL: testBaseURL,
		Logger:        slog.New(slog.DiscardHandler),
		Ports:         app.Ports{Sessions: idSvc, TenantHosts: idSvc, Entitlements: entitlements},
		CSRFKey:       []byte("0123456789abcdef0123456789abcdef"),
		Now:           clk.Now,
		APIRateLimit:  testAPIRateLimit,
		Modules: app.Modules{
			Identity: identity.NewHandler(idSvc), Org: org.NewHandler(orgSvc), Reasons: org.NewReasonHandler(orgSvc),
			Catalog: catalog.NewHandler(domains.Catalog), Partner: partner.NewHandler(domains.Partner),
			Pricing: pricing.NewHandler(domains.Pricing), Inventory: inventory.NewHandler(domains.Inventory),
			Debt: debt.NewHandler(domains.Debt), Cash: cash.NewHandler(domains.Cash),
			Procurement: procurement.NewHandler(domains.Procurement), Sales: sales.NewHandler(domains.Sales),
			Printing: printing.NewHandler(domains.Printing),
			Kits:     kits.NewHandler(domains.Kits),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts = a.TenantHostCache()
	return stack{handler: a.Handler(), identity: idSvc, mail: capture}
}

type kitOut struct {
	ProductID uuid.UUID `json:"product_id"`
	Status    string    `json:"status"`
	SkuID     uuid.UUID `json:"sku_id"`
	Version   int       `json:"version"`
	Latest    struct {
		Status string `json:"status"`
		Items  []struct {
			ID    uuid.UUID  `json:"id"`
			SkuID *uuid.UUID `json:"sku_id"`
		} `json:"items"`
	} `json:"latest"`
}

type assemblyOrderOut struct {
	ID              uuid.UUID `json:"id"`
	Status          string    `json:"status"`
	PlannedQty      string    `json:"planned_qty"`
	CompletedQty    string    `json:"completed_qty"`
	DisassembledQty string    `json:"disassembled_qty"`
	Version         int       `json:"version"`
	Items           []struct {
		ID          uuid.UUID  `json:"id"`
		SkuID       uuid.UUID  `json:"sku_id"`
		ColorID     *uuid.UUID `json:"color_id"`
		PlannedQty  string     `json:"planned_qty"`
		ConsumedQty string     `json:"consumed_qty"`
		ReservedQty string     `json:"reserved_qty"`
	} `json:"items"`
}

type assemblyPostingOut struct {
	ID             uuid.UUID `json:"id"`
	ProducedQty    string    `json:"produced_qty"`
	OutputLotID    uuid.UUID `json:"output_lot_id"`
	OutputExpiry   *string   `json:"output_expiry"`
	ComponentsCost string    `json:"components_cost"`
	PackingCost    string    `json:"packing_cost"`
	OutputUnitCost string    `json:"output_unit_cost"`
	Items          []struct {
		SkuID       uuid.UUID  `json:"sku_id"`
		LotID       uuid.UUID  `json:"lot_id"`
		ColorID     *uuid.UUID `json:"color_id"`
		ConsumedQty string     `json:"consumed_qty"`
		ScrapQty    string     `json:"scrap_qty"`
	} `json:"items"`
}

type assemblyBatchOut struct {
	Posting assemblyPostingOut `json:"posting"`
	Order   assemblyOrderOut   `json:"order"`
}

type disassemblyOut struct {
	ID    uuid.UUID `json:"id"`
	Qty   string    `json:"qty"`
	Lines []struct {
		ID          uuid.UUID  `json:"id"`
		SkuID       uuid.UUID  `json:"sku_id"`
		SourceLotID uuid.UUID  `json:"source_lot_id"`
		ColorID     *uuid.UUID `json:"color_id"`
		ReturnQty   string     `json:"return_qty"`
		DamagedQty  string     `json:"damaged_qty"`
	} `json:"lines"`
}

// kitStore is a sales store with the goods a gift basket is made of.
type kitStore struct {
	salesStore
	candy, tea, ribbon, basket uuid.UUID
	damageReason               uuid.UUID
}

// buildKitStore receives candy and tea in lots that expire, and a ribbon and a basket in red and
// purple: enough for every subtest of a store to build its own kits.
func buildKitStore(t *testing.T, b *browser) kitStore {
	t.Helper()
	st := kitStore{salesStore: buildSalesStore(t, b)}
	st.candy = st.newExpiringSku(t, b, "KEO-SUA", "20000")
	st.tea = st.newExpiringSku(t, b, "TRA-SEN", "50000")
	st.ribbon = st.newSku(t, b, "RUY-BANG", "5000")
	st.basket = st.newSku(t, b, "GIO-MAY", "30000")
	red, purple := st.colors["do"], st.colors["tim"]
	st.receiveLot(t, b, st.candy, "KS-01", "2027-01-31", "30", "12000")
	st.receiveLot(t, b, st.tea, "TS-01", "2026-12-31", "10", "30000")
	st.receive(t, b, st.ribbon, &red, "20", "2000")
	st.receive(t, b, st.ribbon, &purple, "20", "2000")
	st.receive(t, b, st.basket, &red, "10", "15000")
	st.receive(t, b, st.basket, &purple, "10", "15000")
	st.damageReason = send[withID](t, b, http.MethodPost, "/reasons", map[string]any{"kind": "damage", "name": "Hư khi tháo giỏ"}).ID
	return st
}

// newExpiringSku creates a goods SKU tracked by lot and expiry.
func (st kitStore) newExpiringSku(t *testing.T, b *browser, code, price string) uuid.UUID {
	t.Helper()
	sku := send[productRef](t, b, http.MethodPost, "/products", map[string]any{
		"type": "goods", "name": "Hàng " + code,
		"sku": map[string]any{"code": code, "base_unit_id": st.units["cái"], "track_lot": true, "track_expiry": true},
	}).Skus[0].ID
	send[map[string]any](t, b, http.MethodPost, "/skus/"+sku.String()+"/sale-price",
		map[string]any{"kind": "retail", "price": price, "source": "catalog"})
	return sku
}

func (st kitStore) receiveLot(t *testing.T, b *browser, sku uuid.UUID, lotNo, expiry, qty, unitCost string) {
	t.Helper()
	postReceipt(t, b, map[string]any{"supplier_party_id": st.supplier, "lines": []map[string]any{
		{"sku_id": sku, "qty": qty, "unit_price": unitCost, "lot_no": lotNo, "expiry_date": expiry}}})
}

// giftBasket is the recipe of the gift basket: 2 candy, 1 tea, a red ribbon and a basket whose
// color the assembler chooses, packed for 10000.
func (st kitStore) giftBasket(code string) map[string]any {
	red := st.colors["do"]
	return map[string]any{"type": "kit", "name": "Giỏ quà Tết " + code, "sku_code": code, "base_unit_id": st.units["cái"],
		"retail_price": "200000", "recipe": map[string]any{"packing_cost": "10000", "items": []map[string]any{
			{"kind": "goods", "sku_id": st.candy, "qty": "2"},
			{"kind": "goods", "sku_id": st.tea, "qty": "1"},
			{"kind": "accessory", "sku_id": st.ribbon, "qty": "1", "color_id": red},
			{"kind": "basket", "sku_id": st.basket, "qty": "1"},
		}}}
}

// createKit creates a kit or combo and publishes it.
func createKit(t *testing.T, b *browser, body map[string]any) kitOut {
	t.Helper()
	rec := b.do(http.MethodPost, v1+"/kits", body)
	expect(t, rec, http.StatusCreated, kits.RecipeDraft)
	kit := testkit.DecodeJSON[kitOut](t, rec)
	return publishKit(t, b, kit)
}

func publishKit(t *testing.T, b *browser, kit kitOut) kitOut {
	t.Helper()
	rec := b.doWith(http.MethodPost, v1+"/kits/"+kit.ProductID.String()+":publish", nil, ifMatchOf(kit.Version))
	expect(t, rec, http.StatusOK, kits.ProductActive)
	return testkit.DecodeJSON[kitOut](t, rec)
}

// assemble plans an order of planned kits with the basket in color, reserves its components and
// posts a batch of all of them.
func (st kitStore) assemble(t *testing.T, b *browser, kit kitOut, planned string, color uuid.UUID) (assemblyOrderOut, assemblyPostingOut) {
	t.Helper()
	rec := b.do(http.MethodPost, v1+"/assembly-orders", map[string]any{"kit_id": kit.ProductID, "planned_qty": planned,
		"colors": []map[string]any{{"sku_id": st.basket, "color_id": color}}})
	expect(t, rec, http.StatusCreated, kits.OrderDraft)
	order := testkit.DecodeJSON[assemblyOrderOut](t, rec)
	rec = b.doWith(http.MethodPost, v1+"/assembly-orders/"+order.ID.String()+":reserve", nil, keyed(nil))
	expect(t, rec, http.StatusOK, kits.OrderReserved)
	rec = b.doWith(http.MethodPost, v1+"/assembly-orders/"+order.ID.String()+"/postings", map[string]any{"produced_qty": planned},
		keyed(nil))
	expect(t, rec, http.StatusCreated, "")
	batch := testkit.DecodeJSON[assemblyBatchOut](t, rec)
	return batch.Order, batch.Posting
}

// verifyKitLedgers runs the kits verifier with every ledger the sales tests check.
func verifyKitLedgers(t *testing.T, tenant uuid.UUID) {
	t.Helper()
	verifySalesLedgers(t, tenant)
	if got := kitsVerify(t, tenant); len(got) > 0 {
		t.Errorf("kits verify: %v", got)
	}
}

func kitsVerify(t *testing.T, tenant uuid.UUID) []string {
	t.Helper()
	var out []string
	testkit.InTenant(t, testkit.DB(t), tenant, func(q db.Querier) error {
		var err error
		out, err = kits.Verify(context.Background(), q)
		return err
	})
	return out
}

// sellableSku reports whether the sellable SKU search finds code.
func sellableSku(t *testing.T, b *browser, code string) bool {
	t.Helper()
	hits := send[dataOf[searchHit]](t, b, http.MethodGet, "/search?type=sku&sellable=true&q="+code, nil).Data
	return slices.ContainsFunc(hits, func(h searchHit) bool { return h.Code == code })
}

// onPos reports whether the POS bootstrap shows a tile for sku.
func onPos(t *testing.T, b *browser, sku uuid.UUID) bool {
	t.Helper()
	boot := send[struct {
		Tiles []struct {
			SkuID uuid.UUID `json:"sku_id"`
		} `json:"tiles"`
	}](t, b, http.MethodGet, "/pos/bootstrap", nil)
	return slices.ContainsFunc(boot.Tiles, func(tile struct {
		SkuID uuid.UUID `json:"sku_id"`
	}) bool {
		return tile.SkuID == sku
	})
}

func sellableOf(t *testing.T, tenant, sku uuid.UUID) (sellable, reserved money.Qty) {
	t.Helper()
	testkit.InTenant(t, testkit.DB(t), tenant, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(), `SELECT sellable, reserved FROM sku_stock WHERE sku_id = $1`, sku).
			Scan(&sellable, &reserved)
	})
	return sellable, reserved
}

// Every subtest builds the documents it works on, so any of them runs alone (go test -run).
func TestKitsOverHTTP(t *testing.T) {
	s := newKitsStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildKitStore(t, a)
	red, purple := st.colors["do"], st.colors["tim"]

	t.Run("a draft kit is hidden until published and its preview prices the components", func(t *testing.T) {
		rec := a.do(http.MethodPost, v1+"/kits", st.giftBasket("GIO-NHAP"))
		expect(t, rec, http.StatusCreated, "")
		kit := testkit.DecodeJSON[kitOut](t, rec)
		if kit.Status != catalog.StatusDraft || kit.Latest.Status != kits.RecipeDraft || len(kit.Latest.Items) != 4 {
			t.Fatalf("draft kit = %+v", kit)
		}
		if sellableSku(t, a, "GIO-NHAP") {
			t.Error("the sellable search finds a draft kit")
		}
		view := send[struct {
			Components []struct {
				SkuID      uuid.UUID `json:"sku_id"`
				SaleAmount string    `json:"sale_amount"`
				Cost       string    `json:"cost"`
				Sellable   string    `json:"sellable"`
			} `json:"components"`
			ComponentsSale string  `json:"components_sale"`
			ComponentsCost string  `json:"components_cost"`
			TotalCost      string  `json:"total_cost"`
			KitPrice       *string `json:"kit_price"`
			Margin         *string `json:"margin"`
			Buildable      string  `json:"buildable"`
		}](t, a, http.MethodGet, "/kits/"+kit.ProductID.String()+"/preview", nil)
		// 2 × 20000 + 50000 + 5000 + 30000 at sale; 2 × 12000 + 30000 + 2000 + 15000 at cost.
		if len(view.Components) != 4 || view.ComponentsSale != "125000" || view.ComponentsCost != "71000" ||
			view.TotalCost != "81000" || view.KitPrice == nil || *view.KitPrice != "200000" || view.Margin == nil ||
			*view.Margin != "119000" || view.Buildable != "10" {
			t.Errorf("preview = %+v", view)
		}

		published := publishKit(t, a, kit)
		if published.Status != catalog.StatusActive || !sellableSku(t, a, "GIO-NHAP") {
			t.Errorf("published kit = %+v, want it found by the sellable search", published)
		}
		expect(t, a.doWith(http.MethodDelete, v1+"/kits/"+kit.ProductID.String(), nil, ifMatchOf(published.Version)),
			http.StatusConflict, kits.CodeKitPublished)

		// A draft never priced nor sold is deleted outright.
		unpriced := st.giftBasket("GIO-BO")
		delete(unpriced, "retail_price")
		rec = a.do(http.MethodPost, v1+"/kits", unpriced)
		expect(t, rec, http.StatusCreated, "")
		scrapped := testkit.DecodeJSON[kitOut](t, rec)
		rec = a.doWith(http.MethodDelete, v1+"/kits/"+scrapped.ProductID.String(), nil, ifMatchOf(scrapped.Version))
		expect(t, rec, http.StatusOK, "")
		if !strings.Contains(rec.Body.String(), `"archived":false`) {
			t.Errorf("delete = %s, want the draft deleted, not archived", rec.Body)
		}
		expect(t, a.do(http.MethodGet, v1+"/kits/"+scrapped.ProductID.String(), nil), http.StatusNotFound, "")
	})

	t.Run("a priced draft is deleted without price history and its history starts when published", func(t *testing.T) {
		draft := func() kitOut {
			rec := a.do(http.MethodPost, v1+"/kits", st.giftBasket("GIO-GIA"))
			expect(t, rec, http.StatusCreated, "")
			return testkit.DecodeJSON[kitOut](t, rec)
		}
		history := func(sku uuid.UUID, where string) int {
			var n int
			testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
				return q.QueryRowxContext(context.Background(), `SELECT count(*) FROM price_changes WHERE sku_id = $1`+where, sku).Scan(&n)
			})
			return n
		}
		first := draft()
		rec := a.doWith(http.MethodDelete, v1+"/kits/"+first.ProductID.String(), nil, ifMatchOf(first.Version))
		expect(t, rec, http.StatusOK, "")
		if !strings.Contains(rec.Body.String(), `"archived":false`) {
			t.Errorf("delete = %s, want the priced draft deleted, not archived", rec.Body)
		}
		if n := history(first.SkuID, ""); n != 0 {
			t.Errorf("price history of the deleted draft = %d rows, want none", n)
		}

		// The deleted draft's name and code are free again.
		second := draft()
		if n := history(second.SkuID, ""); n != 0 {
			t.Errorf("price history of a draft = %d rows, want none before it is published", n)
		}
		publishKit(t, a, second)
		if n := history(second.SkuID, ""); n != 1 {
			t.Errorf("price history after publishing = %d rows, want 1", n)
		}
		if n := history(second.SkuID, ` AND old_price IS NULL AND new_price = 200000 AND source = 'catalog'`); n != 1 {
			t.Error("the first price history row is not the draft's 200000 added from the catalog")
		}
	})

	t.Run("an assembly is one balanced posting and the kit lot takes the earliest expiry", func(t *testing.T) {
		kit := createKit(t, a, st.giftBasket("GIO-A"))
		moves := countRows(t, st.tenant, `SELECT count(*) FROM stock_moves`)
		rec := a.do(http.MethodPost, v1+"/assembly-orders", map[string]any{"kit_id": kit.ProductID, "planned_qty": "3",
			"colors": []map[string]any{{"sku_id": st.basket, "color_id": purple}}})
		expect(t, rec, http.StatusCreated, kits.OrderDraft)
		order := testkit.DecodeJSON[assemblyOrderOut](t, rec)
		if n := countRows(t, st.tenant, `SELECT count(*) FROM stock_moves`); n != moves {
			t.Errorf("stock moves after planning = %d, want %d: planning consumes nothing", n, moves)
		}
		_, candyReserved := sellableOf(t, st.tenant, st.candy)
		rec = a.doWith(http.MethodPost, v1+"/assembly-orders/"+order.ID.String()+":reserve", nil, keyed(nil))
		expect(t, rec, http.StatusOK, kits.OrderReserved)
		if _, reserved := sellableOf(t, st.tenant, st.candy); !reserved.Sub(candyReserved).Equal(money.MustQty("6")) {
			t.Errorf("candy reserved = %s, want 6 more than %s", reserved, candyReserved)
		}

		rec = a.doWith(http.MethodPost, v1+"/assembly-orders/"+order.ID.String()+"/postings", map[string]any{"produced_qty": "3"},
			keyed(nil))
		expect(t, rec, http.StatusCreated, "")
		batch := testkit.DecodeJSON[assemblyBatchOut](t, rec)
		p := batch.Posting
		// 3 × (2 × 12000 + 30000 + 2000 + 15000) of components and 3 × 10000 of packing.
		if p.ComponentsCost != "213000" || p.PackingCost != "30000" || p.OutputUnitCost != "81000" || p.OutputExpiry == nil ||
			*p.OutputExpiry != "2026-12-31" || batch.Order.Status != kits.OrderCompleted {
			t.Fatalf("posting = %+v, order %s", p, batch.Order.Status)
		}
		for _, it := range p.Items {
			if it.SkuID == st.ribbon && (it.ColorID == nil || *it.ColorID != red) ||
				it.SkuID == st.basket && (it.ColorID == nil || *it.ColorID != purple) {
				t.Errorf("posting item = %+v, want the fixed red ribbon and the purple basket", it)
			}
		}
		var consumed, produced money.Money
		testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
			return q.QueryRowxContext(context.Background(), `
				SELECT COALESCE(-SUM(value_amount) FILTER (WHERE move_type = 'assembly_consume'), 0),
				       COALESCE(SUM(value_amount) FILTER (WHERE move_type = 'assembly_produce'), 0)
				FROM stock_moves WHERE doc_id = $1`, p.ID).Scan(&consumed, &produced)
		})
		if !consumed.Add(money.MustMoney(p.PackingCost)).Equal(produced) || produced.String() != "243000" {
			t.Errorf("consumed %s + packing %s, produced %s", consumed, p.PackingCost, produced)
		}
		if got := stockOf(t, st.tenant, kit.SkuID); got.OnHand.String() != "3" {
			t.Errorf("kit stock = %+v, want 3", got)
		}
		if _, reserved := sellableOf(t, st.tenant, st.candy); !reserved.Equal(candyReserved) {
			t.Errorf("candy reserved after the batch = %s, want the reservation released", reserved)
		}

		gen := send[struct {
			Components []struct {
				ChildLotID uuid.UUID `json:"child_lot_id"`
				SkuID      uuid.UUID `json:"sku_id"`
				Expiry     *string   `json:"expiry_date"`
				Qty        string    `json:"qty"`
			} `json:"components"`
		}](t, a, http.MethodGet, "/lots/"+p.OutputLotID.String()+"/genealogy", nil)
		if len(gen.Components) != 4 {
			t.Errorf("genealogy = %+v, want the 4 component lots", gen)
		}
		label := send[struct {
			Lots []struct {
				Expiry     *string           `json:"expiry_date"`
				Components []json.RawMessage `json:"components"`
			} `json:"lots"`
		}](t, a, http.MethodGet, "/assembly-orders/"+order.ID.String()+"/label", nil)
		if len(label.Lots) != 1 || label.Lots[0].Expiry == nil || *label.Lots[0].Expiry != "2026-12-31" ||
			len(label.Lots[0].Components) != 4 {
			t.Errorf("label = %+v", label)
		}

		candyBefore := stockOf(t, st.tenant, st.candy)
		sale := checkout(t, a, st.cashSale("200000", saleLine(kit.SkuID, "1", nil)))
		if len(sale.Document.Lines) != 1 || sale.Document.CogsAmount != "81000" {
			t.Errorf("kit sale = %+v, want one line at the kit's cost", sale.Document)
		}
		if got := stockOf(t, st.tenant, st.candy); !got.OnHand.Equal(candyBefore.OnHand) {
			t.Errorf("candy after a kit sale = %+v, want %+v: only the kit is issued", got, candyBefore)
		}
		if got := stockOf(t, st.tenant, kit.SkuID); got.OnHand.String() != "2" {
			t.Errorf("kit stock after the sale = %+v, want 2", got)
		}
		verifyKitLedgers(t, st.tenant)
	})

	t.Run("a substitution is held in place of what it replaces and moves the expected cost", func(t *testing.T) {
		kit := createKit(t, a, st.giftBasket("GIO-C"))
		rec := a.do(http.MethodPost, v1+"/assembly-orders", map[string]any{"kit_id": kit.ProductID, "planned_qty": "2",
			"colors": []map[string]any{{"sku_id": st.basket, "color_id": purple}}})
		expect(t, rec, http.StatusCreated, "")
		order := testkit.DecodeJSON[assemblyOrderOut](t, rec)
		path := v1 + "/assembly-orders/" + order.ID.String()
		expect(t, a.doWith(http.MethodPost, path+":reserve", nil, keyed(nil)), http.StatusOK, kits.OrderReserved)
		before := send[struct {
			ExpectedCost string `json:"expected_cost"`
		}](t, a, http.MethodGet, "/assembly-orders/"+order.ID.String(), nil)
		tea := order.Items[slices.IndexFunc(order.Items, func(it struct {
			ID          uuid.UUID  `json:"id"`
			SkuID       uuid.UUID  `json:"sku_id"`
			ColorID     *uuid.UUID `json:"color_id"`
			PlannedQty  string     `json:"planned_qty"`
			ConsumedQty string     `json:"consumed_qty"`
			ReservedQty string     `json:"reserved_qty"`
		}) bool {
			return it.SkuID == st.tea
		})].ID
		_, teaHeld := sellableOf(t, st.tenant, st.tea)
		_, candyHeld := sellableOf(t, st.tenant, st.candy)

		// The tea ran out at the packing table: both kits take two more candies instead.
		rec = a.doWith(http.MethodPost, path+"/substitutions", map[string]any{"item_id": tea, "sku_id": st.candy, "qty": "2",
			"substitute_qty": "4"}, keyed(nil))
		expect(t, rec, http.StatusCreated, "")
		after := testkit.DecodeJSON[struct {
			ExpectedCost string `json:"expected_cost"`
			Items        []struct {
				SkuID         uuid.UUID  `json:"sku_id"`
				SubstitutesID *uuid.UUID `json:"substitutes_item_id"`
				PlannedQty    string     `json:"planned_qty"`
				ReservedQty   string     `json:"reserved_qty"`
			} `json:"items"`
		}](t, rec)
		var replaced, substitute bool
		for _, it := range after.Items {
			switch {
			case it.SkuID == st.tea:
				replaced = it.PlannedQty == "0" && it.ReservedQty == "0"
			case it.SubstitutesID != nil && *it.SubstitutesID == tea:
				substitute = it.SkuID == st.candy && it.PlannedQty == "4" && it.ReservedQty == "4"
			}
		}
		if !replaced || !substitute {
			t.Errorf("items after the substitution = %+v", after.Items)
		}
		if _, held := sellableOf(t, st.tenant, st.tea); !teaHeld.Sub(held).Equal(money.MustQty("2")) {
			t.Errorf("tea reserved = %s, want the 2 the order no longer needs released from %s", held, teaHeld)
		}
		if _, held := sellableOf(t, st.tenant, st.candy); !held.Sub(candyHeld).Equal(money.MustQty("4")) {
			t.Errorf("candy reserved = %s, want 4 more than %s", held, candyHeld)
		}
		want := money.MustMoney(before.ExpectedCost).Sub(stockOf(t, st.tenant, st.tea).AvgCost.MulQty(money.MustQty("2"))).
			Add(stockOf(t, st.tenant, st.candy).AvgCost.MulQty(money.MustQty("4")))
		if !money.MustMoney(after.ExpectedCost).Equal(want) {
			t.Errorf("expected cost = %s, want %s after the substitution (was %s)", after.ExpectedCost, want, before.ExpectedCost)
		}

		rec = a.doWith(http.MethodPost, path+"/postings", map[string]any{"produced_qty": "2"}, keyed(nil))
		expect(t, rec, http.StatusCreated, "")
		batch := testkit.DecodeJSON[assemblyBatchOut](t, rec)
		for _, it := range batch.Posting.Items {
			if it.SkuID == st.tea {
				t.Errorf("posting item %+v: the substituted tea was consumed", it)
			}
		}
		if batch.Order.Status != kits.OrderCompleted {
			t.Errorf("order after the batch = %+v", batch.Order)
		}
		verifyKitLedgers(t, st.tenant)
	})

	t.Run("taking kits apart returns each component to its own lot and color", func(t *testing.T) {
		kit := createKit(t, a, st.giftBasket("GIO-B"))
		order, p := st.assemble(t, a, kit, "3", purple)
		ribbonBefore := stockOf(t, st.tenant, st.ribbon)
		path := v1 + "/assembly-orders/" + order.ID.String() + "/disassemblies"
		rec := a.doWith(http.MethodPost, path, map[string]any{"kit_lot_id": p.OutputLotID, "qty": "2",
			"reason_id": st.damageReason, "damaged": []map[string]any{{"sku_id": st.ribbon, "qty": "1"}}}, keyed(nil))
		expect(t, rec, http.StatusCreated, "")
		d := testkit.DecodeJSON[disassemblyOut](t, rec)
		if len(d.Lines) != 4 {
			t.Fatalf("disassembly = %+v, want 4 lines", d)
		}
		for _, l := range d.Lines {
			at := slices.IndexFunc(p.Items, func(it struct {
				SkuID       uuid.UUID  `json:"sku_id"`
				LotID       uuid.UUID  `json:"lot_id"`
				ColorID     *uuid.UUID `json:"color_id"`
				ConsumedQty string     `json:"consumed_qty"`
				ScrapQty    string     `json:"scrap_qty"`
			}) bool {
				return it.LotID == l.SourceLotID && it.SkuID == l.SkuID
			})
			if at < 0 {
				t.Errorf("line %+v returns to a lot the kits were not made from", l)
				continue
			}
			if c := p.Items[at].ColorID; (c == nil) != (l.ColorID == nil) || c != nil && *c != *l.ColorID {
				t.Errorf("line %+v returns another color than %v", l, c)
			}
			want := map[uuid.UUID][2]string{st.candy: {"4", "0"}, st.tea: {"2", "0"}, st.ribbon: {"1", "1"}, st.basket: {"2", "0"}}[l.SkuID]
			if l.ReturnQty != want[0] || l.DamagedQty != want[1] {
				t.Errorf("line of %s = %s back, %s damaged; want %v", l.SkuID, l.ReturnQty, l.DamagedQty, want)
			}
		}
		ribbon := stockOf(t, st.tenant, st.ribbon)
		// On hand counts quarantined stock: both ribbons come back, one of them to quarantine.
		if !ribbon.Quarantine.Sub(ribbonBefore.Quarantine).Equal(money.MustQty("1")) ||
			!ribbon.OnHand.Sub(ribbonBefore.OnHand).Equal(money.MustQty("2")) {
			t.Errorf("ribbon = %+v, before %+v: want 2 back on hand, 1 of them in quarantine", ribbon, ribbonBefore)
		}
		if got := stockOf(t, st.tenant, kit.SkuID); got.OnHand.String() != "1" {
			t.Errorf("kit stock = %+v, want 1 left", got)
		}
		got := send[assemblyOrderOut](t, a, http.MethodGet, "/assembly-orders/"+order.ID.String(), nil)
		if got.Status != kits.OrderInProgress || got.DisassembledQty != "2" {
			t.Fatalf("order after the disassembly = %+v", got)
		}

		expect(t, a.doWith(http.MethodPost, path, map[string]any{"kit_lot_id": p.OutputLotID, "qty": "2",
			"reason_id": st.damageReason}, keyed(nil)), http.StatusUnprocessableEntity, kits.CodeExceedsRemaining)

		rec = a.doWith(http.MethodPost, v1+"/assembly-orders/"+order.ID.String()+"/postings", map[string]any{"produced_qty": "2"},
			keyed(nil))
		expect(t, rec, http.StatusCreated, "")
		again := testkit.DecodeJSON[assemblyBatchOut](t, rec)
		if again.Order.Status != kits.OrderCompleted || again.Order.CompletedQty != "5" {
			t.Errorf("order after assembling again = %+v", again.Order)
		}
		verifyKitLedgers(t, st.tenant)
	})

	t.Run("a combo is hidden until published and its counter colors are required", func(t *testing.T) {
		combo := map[string]any{"type": "combo", "name": "Combo ruy băng", "sku_code": "COMBO-RB", "base_unit_id": st.units["cái"],
			"retail_price": "40000", "recipe": map[string]any{"items": []map[string]any{
				{"kind": "goods", "sku_id": st.candy, "qty": "1"},
				{"kind": "accessory", "sku_id": st.ribbon, "qty": "2"},
				{"kind": "basket", "sku_id": st.basket, "qty": "1", "color_id": red},
			}}}
		rec := a.do(http.MethodPost, v1+"/kits", combo)
		expect(t, rec, http.StatusCreated, "")
		draft := testkit.DecodeJSON[kitOut](t, rec)
		if sellableSku(t, a, "COMBO-RB") || onPos(t, a, draft.SkuID) {
			t.Error("a draft combo is offered for sale")
		}
		expect(t, post(a, "/pos/checkout", st.cashSale("40000", saleLine(draft.SkuID, "1", nil))),
			http.StatusUnprocessableEntity, kits.CodeComboNotSellable)

		published := publishKit(t, a, draft)
		if !sellableSku(t, a, "COMBO-RB") || !onPos(t, a, published.SkuID) {
			t.Error("a published combo is not offered for sale")
		}
		rec = post(a, "/pos/checkout", st.cashSale("40000", saleLine(published.SkuID, "1", nil)))
		expect(t, rec, http.StatusUnprocessableEntity, sales.CodeColorRequired)
		if !strings.Contains(rec.Body.String(), `"lines.0.component_colors"`) {
			t.Errorf("refused combo = %s, want the component colors field", rec.Body)
		}

		line := saleLine(published.SkuID, "1", nil)
		line["component_colors"] = []map[string]any{{"sku_id": st.ribbon, "color_id": purple}}
		sale := checkout(t, a, st.cashSale("40000", line))
		colors := map[uuid.UUID]*uuid.UUID{}
		for _, l := range sale.Document.Lines {
			if l.Kind == sales.KindComboComponent && l.SkuID != nil {
				colors[*l.SkuID] = l.ColorID
			}
		}
		if c := colors[st.ribbon]; c == nil || *c != purple {
			t.Errorf("ribbon color = %v, want the purple the counter chose", c)
		}
		if c := colors[st.basket]; c == nil || *c != red {
			t.Errorf("basket color = %v, want the recipe's red", c)
		}
		verifyKitLedgers(t, st.tenant)
	})
}

// The rules around kits that leave stock and status alone: kits an invoice holds stay whole, a
// kit's status follows its recipe, a disassembly or substitution names what the ledger needs, and
// a disassembly expenses the packing it cannot give back.
func TestKitRulesOverHTTP(t *testing.T) {
	s := newKitsStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildKitStore(t, a)
	red := st.colors["do"]

	t.Run("kits an invoice holds are not taken apart and the reason must explain a disassembly", func(t *testing.T) {
		kit := createKit(t, a, st.giftBasket("GIO-HD"))
		order, p := st.assemble(t, a, kit, "6", red)
		rec := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
			"lines": []map[string]any{saleLine(kit.SkuID, "5", nil)}})
		expect(t, rec, http.StatusCreated, "")
		inv := testkit.DecodeJSON[invoiceOut](t, rec)
		expect(t, a.doWith(http.MethodPost, v1+"/invoices/"+inv.ID.String()+":confirm", nil, keyed(ifMatchOf(inv.Version))),
			http.StatusOK, sales.InvoiceConfirmed)

		path := v1 + "/assembly-orders/" + order.ID.String() + "/disassemblies"
		takeApart := func(qty string, reason uuid.UUID) *httptest.ResponseRecorder {
			return a.doWith(http.MethodPost, path, map[string]any{"kit_lot_id": p.OutputLotID, "qty": qty, "reason_id": reason},
				keyed(nil))
		}
		// Six kits, five of them held: only one is free to take apart.
		expect(t, takeApart("5", st.damageReason), http.StatusConflict, kits.CodeInsufficientStock)
		expect(t, takeApart("2", st.damageReason), http.StatusConflict, kits.CodeInsufficientStock)
		gift := send[withID](t, a, http.MethodPost, "/reasons", map[string]any{"kind": "gift", "name": "Tặng khách"}).ID
		expect(t, takeApart("1", gift), http.StatusUnprocessableEntity, "wrong_kind")
		expect(t, takeApart("1", st.damageReason), http.StatusCreated, "")
		if sellable, reserved := sellableOf(t, st.tenant, kit.SkuID); sellable.String() != "5" || reserved.String() != "5" {
			t.Errorf("kits sellable %s reserved %s, want the 5 the invoice holds", sellable, reserved)
		}
		expect(t, takeApart("1", st.damageReason), http.StatusConflict, kits.CodeInsufficientStock)
		verifyKitLedgers(t, st.tenant)
	})

	t.Run("a kit takes its status from its recipe, not from the product form", func(t *testing.T) {
		rec := a.do(http.MethodPost, v1+"/kits", st.giftBasket("GIO-TT"))
		expect(t, rec, http.StatusCreated, "")
		draft := testkit.DecodeJSON[kitOut](t, rec)
		product := v1 + "/products/" + draft.ProductID.String()
		expect(t, a.doWith(http.MethodPatch, product, map[string]any{"status": "active"}, ifMatchOf(draft.Version)),
			http.StatusUnprocessableEntity, "set_by_recipe")
		if sellableSku(t, a, "GIO-TT") {
			t.Error("the sellable search finds a draft kit the product form tried to activate")
		}
		published := publishKit(t, a, draft)
		expect(t, a.doWith(http.MethodPatch, product, map[string]any{"status": "inactive"}, ifMatchOf(published.Version)),
			http.StatusUnprocessableEntity, "set_by_recipe")

		// A combo made on the product form has no recipe to expand: it is a draft the counter does
		// not offer, and the form cannot make it active.
		form := map[string]any{"type": "combo", "name": "Combo chưa có công thức", "status": "active",
			"sku": map[string]any{"code": "COMBO-TRONG", "base_unit_id": st.units["cái"]}}
		expect(t, a.do(http.MethodPost, v1+"/products", form), http.StatusUnprocessableEntity, "set_by_recipe")
		delete(form, "status")
		rec = a.do(http.MethodPost, v1+"/products", form)
		expect(t, rec, http.StatusCreated, catalog.StatusDraft)
		combo := testkit.DecodeJSON[productRef](t, rec).Skus[0].ID
		send[map[string]any](t, a, http.MethodPost, "/skus/"+combo.String()+"/sale-price",
			map[string]any{"kind": "retail", "price": "30000", "source": "catalog"})
		if onPos(t, a, combo) || sellableSku(t, a, "COMBO-TRONG") {
			t.Error("a combo without a published recipe is offered for sale")
		}
	})

	t.Run("a substitute stocked by color names its color", func(t *testing.T) {
		kit := createKit(t, a, st.giftBasket("GIO-TM"))
		rec := a.do(http.MethodPost, v1+"/assembly-orders", map[string]any{"kit_id": kit.ProductID, "planned_qty": "1",
			"colors": []map[string]any{{"sku_id": st.basket, "color_id": red}}})
		expect(t, rec, http.StatusCreated, kits.OrderDraft)
		order := testkit.DecodeJSON[assemblyOrderOut](t, rec)
		path := v1 + "/assembly-orders/" + order.ID.String()
		expect(t, a.doWith(http.MethodPost, path+":reserve", nil, keyed(nil)), http.StatusOK, kits.OrderReserved)
		var candy uuid.UUID
		for _, it := range order.Items {
			if it.SkuID == st.candy {
				candy = it.ID
			}
		}

		// The candy ran out: two bows go in instead, and the bows are stocked by color.
		bow := st.newSku(t, a, "NO-HOA", "3000")
		st.receive(t, a, bow, &red, "5", "1000")
		body := map[string]any{"item_id": candy, "sku_id": bow, "qty": "2", "substitute_qty": "2"}
		rec = a.doWith(http.MethodPost, path+"/substitutions", body, keyed(nil))
		expect(t, rec, http.StatusUnprocessableEntity, kits.CodeColorRequired)
		if !strings.Contains(rec.Body.String(), `"color_id"`) {
			t.Errorf("refused substitution = %s, want the color field", rec.Body)
		}
		body["color_id"] = red
		expect(t, a.doWith(http.MethodPost, path+"/substitutions", body, keyed(nil)), http.StatusCreated, "")
		verifyKitLedgers(t, st.tenant)
	})

	t.Run("taking a kit apart gives its components back at their cost and expenses the packing", func(t *testing.T) {
		kit := createKit(t, a, st.giftBasket("GIO-DG"))
		order, p := st.assemble(t, a, kit, "1", red)
		rec := a.doWith(http.MethodPost, v1+"/assembly-orders/"+order.ID.String()+"/disassemblies", map[string]any{
			"kit_lot_id": p.OutputLotID, "qty": "1", "reason_id": st.damageReason}, keyed(nil))
		expect(t, rec, http.StatusCreated, "")
		d := testkit.DecodeJSON[disassemblyOut](t, rec)
		var returned, expensed money.Money
		testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
			return q.QueryRowxContext(context.Background(), `
				SELECT (SELECT COALESCE(SUM(value_amount), 0) FROM stock_moves WHERE doc_id = $1 AND move_type = 'disassembly_return'),
				       (SELECT COALESCE(SUM(amount), 0) FROM cogs_adjustments WHERE source_doc_type = $2 AND source_doc_id = $1)`,
				d.ID, kits.CogsSourceDisassembly).Scan(&returned, &expensed)
		})
		// 2 × 12000 + 30000 + 2000 + 15000 back to the component lots, and the 10000 of packing
		// expensed: the 81000 the kit was made at.
		if returned.String() != "71000" || expensed.String() != "10000" {
			t.Errorf("returned %s and expensed %s, want 71000 and 10000", returned, expensed)
		}
		verifyKitLedgers(t, st.tenant)
	})
}

// Combo components join the lock set of a sale: twelve tills selling a combo whose bag a
// confirmed invoice holds two of share only the bags the invoice does not hold.
func TestConcurrentComboSalesNeverTakeReservedStock(t *testing.T) {
	s := newKitsStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	bag, card := st.newSku(t, a, "TUI-GIAY", "10000"), st.newSku(t, a, "THIEP", "5000")
	st.receive(t, a, bag, nil, "5", "4000")
	st.receive(t, a, card, nil, "50", "1000")
	combo := createKit(t, a, map[string]any{"type": "combo", "name": "Túi quà kèm thiệp", "sku_code": "COMBO-TUI",
		"base_unit_id": st.units["cái"], "retail_price": "14000", "recipe": map[string]any{"items": []map[string]any{
			{"kind": "packaging", "sku_id": bag, "qty": "1"}, {"kind": "accessory", "sku_id": card, "qty": "1"}}}})

	rec := a.do(http.MethodPost, v1+"/invoices", map[string]any{"customer_party_id": st.customer,
		"lines": []map[string]any{saleLine(bag, "2", nil)}})
	expect(t, rec, http.StatusCreated, "")
	inv := testkit.DecodeJSON[invoiceOut](t, rec)
	expect(t, a.doWith(http.MethodPost, v1+"/invoices/"+inv.ID.String()+":confirm", nil, keyed(ifMatchOf(inv.Version))),
		http.StatusOK, sales.InvoiceConfirmed)

	const tills = 12
	recs := make([]*httptest.ResponseRecorder, tills)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range tills {
		till := tillOf(a)
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			recs[i] = till.doWith(http.MethodPost, v1+"/pos/checkout", st.cashSale("14000", saleLine(combo.SkuID, "1", nil)),
				http.Header{"Idempotency-Key": {ids.New().String()}})
		}()
	}
	close(start)
	wg.Wait()

	sold := 0
	for _, rec := range recs {
		switch rec.Code {
		case http.StatusCreated:
			sold++
		case http.StatusConflict:
			if !strings.Contains(rec.Body.String(), `"`+sales.CodeInsufficientStock+`"`) {
				t.Errorf("refused checkout = %s, want %s", rec.Body, sales.CodeInsufficientStock)
			}
		default:
			t.Errorf("checkout = %d %s", rec.Code, rec.Body)
		}
	}
	if sold != 3 {
		t.Errorf("successful checkouts = %d, want the 3 bags the invoice does not hold", sold)
	}
	// Sellable still counts the reserved bags: nothing is left beyond the invoice's two.
	if sellable, reserved := sellableOf(t, st.tenant, bag); sellable.String() != "2" || reserved.String() != "2" {
		t.Errorf("bags sellable %s reserved %s, want 2 and 2", sellable, reserved)
	}
	if got := stockOf(t, st.tenant, card); got.OnHand.String() != "47" {
		t.Errorf("cards = %+v, want 47 left", got)
	}
	verifyKitLedgers(t, st.tenant)
}

// The verifier catches what only a bug could leave behind: each row is corrupted as superuser,
// reported, then put back, and the store verifies clean again.
func TestKitsVerifyReportsTamperedRows(t *testing.T) {
	s := newKitsStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildKitStore(t, a)
	kit := createKit(t, a, st.giftBasket("GIO-KT"))
	order, p := st.assemble(t, a, kit, "3", st.colors["tim"])
	rec := a.doWith(http.MethodPost, v1+"/assembly-orders/"+order.ID.String()+"/disassemblies", map[string]any{
		"kit_lot_id": p.OutputLotID, "qty": "1", "reason_id": st.damageReason,
		"damaged": []map[string]any{{"sku_id": st.ribbon, "qty": "1"}}}, keyed(nil))
	expect(t, rec, http.StatusCreated, "")
	d := testkit.DecodeJSON[disassemblyOut](t, rec)
	candyLine := d.Lines[slices.IndexFunc(d.Lines, func(l struct {
		ID          uuid.UUID  `json:"id"`
		SkuID       uuid.UUID  `json:"sku_id"`
		SourceLotID uuid.UUID  `json:"source_lot_id"`
		ColorID     *uuid.UUID `json:"color_id"`
		ReturnQty   string     `json:"return_qty"`
		DamagedQty  string     `json:"damaged_qty"`
	}) bool {
		return l.SkuID == st.candy
	})].ID
	if got := kitsVerify(t, st.tenant); len(got) > 0 {
		t.Fatalf("verify before tampering = %v", got)
	}
	super := testkit.SuperDB(t)
	tamper := func(t *testing.T, id uuid.UUID, set, undo string, want ...string) {
		t.Helper()
		if _, err := super.Exec(set, id); err != nil {
			t.Fatal(err)
		}
		got := kitsVerify(t, st.tenant)
		if _, err := super.Exec(undo, id); err != nil {
			t.Fatal(err)
		}
		for _, w := range want {
			if !slices.ContainsFunc(got, func(m string) bool { return strings.HasPrefix(m, w+" ") }) {
				t.Errorf("verify = %v, want a mismatch %q", got, w)
			}
		}
		if len(got) != len(want) {
			t.Errorf("verify = %v, want %d mismatches", got, len(want))
		}
		if again := kitsVerify(t, st.tenant); len(again) > 0 {
			t.Fatalf("verify after putting the row back = %v", again)
		}
	}
	posting := p.ID.String()

	t.Run("a batch whose kits are worth more than their components and packing", func(t *testing.T) {
		tamper(t, p.ID, `UPDATE tally.assembly_postings SET packing_cost = packing_cost + 1000 WHERE id = $1`,
			`UPDATE tally.assembly_postings SET packing_cost = packing_cost - 1000 WHERE id = $1`,
			"assembly posting balance "+posting, "assembly posting unit cost "+posting)
	})
	t.Run("a batch whose components cost is not what its moves took out", func(t *testing.T) {
		tamper(t, p.ID, `UPDATE tally.assembly_postings SET components_cost = components_cost + 1000 WHERE id = $1`,
			`UPDATE tally.assembly_postings SET components_cost = components_cost - 1000 WHERE id = $1`,
			"assembly posting components cost "+posting, "assembly posting unit cost "+posting)
	})
	t.Run("a genealogy that links more than the kits hold", func(t *testing.T) {
		tamper(t, p.ID, `UPDATE tally.lot_genealogy SET child_base_qty = child_base_qty + 1 WHERE assembly_posting_id = $1`,
			`UPDATE tally.lot_genealogy SET child_base_qty = child_base_qty - 1 WHERE assembly_posting_id = $1`,
			"lot genealogy "+posting)
	})
	t.Run("a kit lot that outlives its earliest component", func(t *testing.T) {
		tamper(t, p.OutputLotID, `UPDATE tally.lots SET expiry_date = expiry_date + 1 WHERE id = $1`,
			`UPDATE tally.lots SET expiry_date = expiry_date - 1 WHERE id = $1`, "kit lot expiry "+posting)
	})
	t.Run("a batch item that took more than its moves", func(t *testing.T) {
		tamper(t, p.ID, `UPDATE tally.assembly_posting_items SET consumed_base_qty = consumed_base_qty + 1
			WHERE id = (SELECT id FROM tally.assembly_posting_items WHERE assembly_posting_id = $1 ORDER BY id LIMIT 1)`,
			`UPDATE tally.assembly_posting_items SET consumed_base_qty = consumed_base_qty - 1
			WHERE id = (SELECT id FROM tally.assembly_posting_items WHERE assembly_posting_id = $1 ORDER BY id LIMIT 1)`,
			"assembly posting consumption "+posting, "lot genealogy "+posting, "assembly order component consumed "+order.ID.String())
	})
	t.Run("a disassembly that counts more kits than its moves took apart", func(t *testing.T) {
		tamper(t, d.ID, `UPDATE tally.disassemblies SET qty = qty + 1 WHERE id = $1`,
			`UPDATE tally.disassemblies SET qty = qty - 1 WHERE id = $1`,
			"disassembly balance "+d.ID.String(), "assembly order disassembled "+order.ID.String())
	})
	t.Run("a disassembly that expensed less than its kits were worth beyond their components", func(t *testing.T) {
		tamper(t, d.ID, `UPDATE tally.cogs_adjustments SET amount = amount - 1000 WHERE source_doc_id = $1`,
			`UPDATE tally.cogs_adjustments SET amount = amount + 1000 WHERE source_doc_id = $1`,
			"disassembly balance "+d.ID.String())
	})
	t.Run("a disassembly line that returned more than its moves", func(t *testing.T) {
		tamper(t, candyLine, `UPDATE tally.disassembly_lines SET return_base_qty = return_base_qty + 1 WHERE id = $1`,
			`UPDATE tally.disassembly_lines SET return_base_qty = return_base_qty - 1 WHERE id = $1`,
			"disassembly line returned "+candyLine.String(), "assembly order component consumed "+order.ID.String())
	})
	t.Run("a disassembly line that calls returned goods damaged", func(t *testing.T) {
		tamper(t, candyLine,
			`UPDATE tally.disassembly_lines SET damaged_base_qty = damaged_base_qty + 1, return_base_qty = return_base_qty - 1 WHERE id = $1`,
			`UPDATE tally.disassembly_lines SET damaged_base_qty = damaged_base_qty - 1, return_base_qty = return_base_qty + 1 WHERE id = $1`,
			"disassembly line damaged "+candyLine.String())
	})
	t.Run("an order that counts more kits made than its batches", func(t *testing.T) {
		tamper(t, order.ID, `UPDATE tally.assembly_orders SET completed_qty = completed_qty + 1 WHERE id = $1`,
			`UPDATE tally.assembly_orders SET completed_qty = completed_qty - 1 WHERE id = $1`,
			"assembly order completed "+order.ID.String())
	})
	t.Run("an order that counts fewer kits taken apart than its disassemblies", func(t *testing.T) {
		tamper(t, order.ID, `UPDATE tally.assembly_orders SET disassembled_qty = disassembled_qty - 1 WHERE id = $1`,
			`UPDATE tally.assembly_orders SET disassembled_qty = disassembled_qty + 1 WHERE id = $1`,
			"assembly order disassembled "+order.ID.String())
	})
	t.Run("an order item that consumed more than its batches put into kits", func(t *testing.T) {
		item := order.Items[0].ID
		tamper(t, item, `UPDATE tally.assembly_order_items SET consumed_base_qty = consumed_base_qty + 1 WHERE id = $1`,
			`UPDATE tally.assembly_order_items SET consumed_base_qty = consumed_base_qty - 1 WHERE id = $1`,
			"assembly order component consumed "+order.ID.String())
	})
}
