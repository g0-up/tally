//go:build integration

package app_test

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.yaml.in/yaml/v3"

	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// pricingStore is one store's SKU, price lists and promotion, built over HTTP.
type pricingStore struct {
	sku, red, thung      uuid.UUID
	retail, season, item uuid.UUID
	promotion            uuid.UUID
	seasonVersion        string
}

type versioned struct {
	ID      uuid.UUID `json:"id"`
	Kind    string    `json:"kind"`
	Version int       `json:"version"`
}

type priceSourceOut struct {
	Kind          string     `json:"kind"`
	PriceListID   *uuid.UUID `json:"price_list_id"`
	PriceListName *string    `json:"price_list_name"`
	ColorID       *uuid.UUID `json:"color_id"`
}

type priceChangeOut struct {
	OldPrice   *string    `json:"old_price"`
	NewPrice   *string    `json:"new_price"`
	ColorID    *uuid.UUID `json:"color_id"`
	ScopeLabel string     `json:"scope_label"`
	Source     string     `json:"source"`
}

func colorsOf(t *testing.T, b *browser) map[string]uuid.UUID {
	t.Helper()
	out := map[string]uuid.UUID{}
	for _, c := range send[dataOf[struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	}]](t, b, http.MethodGet, "/colors", nil).Data {
		out[c.Name] = c.ID
	}
	return out
}

func buildPricingStore(t *testing.T, b *browser, code string) pricingStore {
	t.Helper()
	units := unitsOf(t, b)
	st := pricingStore{red: colorsOf(t, b)["Đỏ"], thung: units["thùng"]}
	p := send[productRef](t, b, http.MethodPost, "/products", map[string]any{
		"name": "Sữa tươi " + code, "sku": map[string]any{"code": code, "base_unit_id": units["hộp"]}})
	st.sku = p.Skus[0].ID
	send[withID](t, b, http.MethodPost, "/skus/"+st.sku.String()+"/units", map[string]any{"unit_id": st.thung, "factor": "24"})
	for _, l := range send[dataOf[versioned]](t, b, http.MethodGet, "/price-lists?kind=retail", nil).Data {
		st.retail = l.ID
	}
	salePrice := "/skus/" + st.sku.String() + "/sale-price"
	send[dataOf[priceChangeOut]](t, b, http.MethodPost, salePrice, map[string]any{"kind": "retail", "price": "10000", "source": "catalog"})
	send[dataOf[priceChangeOut]](t, b, http.MethodPost, salePrice,
		map[string]any{"kind": "retail", "color_id": st.red, "price": "11000", "source": "price_list"})

	rec := b.do(http.MethodPost, v1+"/price-lists", map[string]any{
		"name": "Tết " + code, "kind": "season", "valid_from": "2026-03-01", "valid_to": "2026-03-31"})
	expect(t, rec, http.StatusCreated, "")
	st.seasonVersion = rec.Header().Get("ETag")
	st.season = testkit.DecodeJSON[withID](t, rec).ID
	items := send[dataOf[withID]](t, b, http.MethodPut, "/price-lists/"+st.season.String()+"/items",
		map[string]any{"items": []map[string]any{{"sku_id": st.sku, "price": "9500"}}})
	if len(items.Data) != 1 {
		t.Fatalf("season items = %+v", items)
	}
	st.item = items.Data[0].ID

	rec = b.do(http.MethodPost, v1+"/promotions", map[string]any{
		"code": "tet" + strings.ToLower(code), "name": "Giảm 10%", "kind": "order_percent", "value": "10",
		"valid_from": "2026-03-01", "valid_to": "2026-03-31"})
	expect(t, rec, http.StatusCreated, "")
	st.promotion = testkit.DecodeJSON[withID](t, rec).ID
	return st
}

// pricingIDRoutesOf are the id-bearing pricing operations against st's records; own supplies the
// calling store's ids wherever a body must name one.
func pricingIDRoutesOf(st, own pricingStore) []idRoute {
	u := func(args ...uuid.UUID) []uuid.UUID { return args }
	return []idRoute{
		{method: "GET", path: "/price-lists/{id}", args: u(st.season)},
		{method: "PATCH", path: "/price-lists/{id}", args: u(st.season), body: map[string]any{"name": "x"}, ifMatch: true},
		{method: "DELETE", path: "/price-lists/{id}", args: u(st.season)},
		{method: "GET", path: "/price-lists/{id}/items", args: u(st.season)},
		{method: "PUT", path: "/price-lists/{id}/items", args: u(st.season),
			body: map[string]any{"items": []map[string]any{{"sku_id": own.sku, "price": "1"}}}},
		{method: "DELETE", path: "/price-lists/{id}/items/{itemId}", args: u(st.season, st.item)},
		{method: "POST", path: "/skus/{id}/sale-price", args: u(st.sku), body: map[string]any{"kind": "retail", "price": "1", "source": "catalog"}},
		{method: "GET", path: "/skus/{id}/price-history", args: u(st.sku)},
		{method: "GET", path: "/promotions/{id}", args: u(st.promotion)},
		{method: "PATCH", path: "/promotions/{id}", args: u(st.promotion), body: map[string]any{"name": "x"}, ifMatch: true},
		{method: "POST", path: "/promotions/{id}:activate", args: u(st.promotion)},
		{method: "POST", path: "/promotions/{id}:deactivate", args: u(st.promotion)},
	}
}

// pricingIDOperationsInSpec are the documented pricing operations whose path names a resource.
func pricingIDOperationsInSpec(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]struct {
			Tags []string `yaml:"tags"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	var out []string
	for path, ops := range spec.Paths {
		for method, op := range ops {
			if strings.Contains(path, "{") && slices.Contains(op.Tags, "pricing") {
				out = append(out, strings.ToUpper(method)+" "+path)
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestPricingOverHTTP(t *testing.T) {
	s := newStack(t)
	ownerA, ownerB := s.signUp(t), s.signUp(t)
	a, b := s.signIn(t, ownerA), s.signIn(t, ownerB)
	storeA, storeB := buildPricingStore(t, a, "A1"), buildPricingStore(t, b, "B1")

	t.Run("a new store has its two base lists", func(t *testing.T) {
		var kinds []string
		for _, l := range send[dataOf[versioned]](t, b, http.MethodGet, "/price-lists?kind=retail", nil).Data {
			kinds = append(kinds, l.Kind)
		}
		for _, l := range send[dataOf[versioned]](t, b, http.MethodGet, "/price-lists?kind=wholesale", nil).Data {
			kinds = append(kinds, l.Kind)
		}
		if !slices.Equal(kinds, []string{"retail", "wholesale"}) {
			t.Fatalf("base lists = %v", kinds)
		}
		expect(t, a.do(http.MethodPost, v1+"/price-lists", map[string]any{"name": "Lẻ 2", "kind": "retail"}),
			http.StatusConflict, "base_price_list_exists")
	})

	t.Run("quote walks the chain and prices units, tiers and colors", func(t *testing.T) {
		out := send[dataOf[struct {
			Amount      string         `json:"amount"`
			BaseQty     string         `json:"base_qty"`
			PriceSource priceSourceOut `json:"price_source"`
		}]](t, a, http.MethodPost, "/pricing/quote", map[string]any{"lines": []map[string]any{
			{"sku_id": storeA.sku, "qty": "1"},
			{"sku_id": storeA.sku, "unit_id": storeA.thung, "qty": "2"},
			{"sku_id": storeA.sku, "qty": "1"},
		}}).Data
		if out[0].Amount != "9500" || out[0].PriceSource.Kind != "season_list" || out[0].PriceSource.PriceListID == nil ||
			*out[0].PriceSource.PriceListID != storeA.season || out[1].Amount != "228000" || out[1].BaseQty != "48" {
			t.Fatalf("quote = %+v", out)
		}
		after := send[dataOf[struct {
			Amount      string         `json:"amount"`
			PriceSource priceSourceOut `json:"price_source"`
		}]](t, a, http.MethodPost, "/pricing/quote", map[string]any{"business_date": "2026-04-01", "lines": []map[string]any{
			{"sku_id": storeA.sku, "qty": "1"}, {"sku_id": storeA.sku, "color_id": storeA.red, "qty": "1"},
		}}).Data
		if after[0].Amount != "10000" || after[0].PriceSource.Kind != "base_list" || after[1].Amount != "11000" ||
			after[1].PriceSource.ColorID == nil || *after[1].PriceSource.ColorID != storeA.red {
			t.Fatalf("after the season = %+v", after)
		}
		expect(t, a.do(http.MethodPost, v1+"/pricing/quote", map[string]any{"lines": []map[string]any{{"sku_id": storeA.sku, "qty": "0"}}}),
			http.StatusUnprocessableEntity, "validation_failed")
	})

	t.Run("price lists take If-Match and items are upserted and removed", func(t *testing.T) {
		path := v1 + "/price-lists/" + storeA.season.String()
		expect(t, a.do(http.MethodPatch, path, map[string]any{"priority": 5}), http.StatusPreconditionRequired, "")
		expect(t, a.doWith(http.MethodPatch, path, map[string]any{"priority": 5}, http.Header{"If-Match": {`"9"`}}),
			http.StatusPreconditionFailed, "")
		rec := a.doWith(http.MethodPatch, path, map[string]any{"priority": 5}, http.Header{"If-Match": {storeA.seasonVersion}})
		expect(t, rec, http.StatusOK, "")
		if rec.Header().Get("ETag") == storeA.seasonVersion {
			t.Errorf("ETag unchanged after an update: %s", rec.Header().Get("ETag"))
		}
		items := send[dataOf[struct {
			ID      uuid.UUID `json:"id"`
			SkuCode string    `json:"sku_code"`
			Price   string    `json:"price"`
		}]](t, a, http.MethodGet, "/price-lists/"+storeA.season.String()+"/items", nil).Data
		if len(items) != 1 || items[0].SkuCode != "A1" || items[0].Price != "9500" {
			t.Fatalf("items = %+v", items)
		}
		expect(t, a.do(http.MethodDelete, path+"/items/"+storeA.item.String(), nil), http.StatusNoContent, "")
		expect(t, a.do(http.MethodDelete, path+"/items/"+storeA.item.String(), nil), http.StatusNotFound, "not_found")
	})

	t.Run("Bỏ giá riêng clears a color price and the history shows it first", func(t *testing.T) {
		sku := "/skus/" + storeA.sku.String()
		out := send[dataOf[priceChangeOut]](t, a, http.MethodPost, sku+"/sale-price",
			map[string]any{"kind": "retail", "color_id": storeA.red, "price": nil, "source": "price_list"}).Data
		if len(out) != 1 || out[0].OldPrice == nil || *out[0].OldPrice != "11000" || out[0].NewPrice != nil || out[0].ScopeLabel != "Đỏ" {
			t.Fatalf("removal = %+v", out)
		}
		history := send[dataOf[priceChangeOut]](t, a, http.MethodGet, sku+"/price-history", nil).Data
		// Newest first, across every list: the removal, the season row's delete and put, the red
		// price, then the first retail price from the catalog.
		last := len(history) - 1
		if len(history) != 5 || history[0].NewPrice != nil || history[0].ScopeLabel != "Đỏ" ||
			history[last].NewPrice == nil || *history[last].NewPrice != "10000" || history[last].Source != "catalog" {
			t.Fatalf("history = %+v", history)
		}
		expect(t, a.do(http.MethodPost, v1+sku+"/sale-price", map[string]any{"kind": "season", "price": "1", "source": "catalog"}),
			http.StatusUnprocessableEntity, "validation_failed")
	})

	t.Run("search shows the retail price hint", func(t *testing.T) {
		hits := send[dataOf[struct {
			ID        uuid.UUID `json:"id"`
			PriceHint *string   `json:"price_hint"`
		}]](t, a, http.MethodGet, "/search?type=sku&q=sua+tuoi", nil).Data
		if len(hits) != 1 || hits[0].ID != storeA.sku || hits[0].PriceHint == nil || *hits[0].PriceHint != "10000" {
			t.Fatalf("hits = %+v", hits)
		}
	})

	t.Run("promotion codes are checked, applied and switched off", func(t *testing.T) {
		type check struct {
			Valid     bool    `json:"valid"`
			Reason    string  `json:"reason"`
			Promotion *withID `json:"promotion"`
		}
		got := send[check](t, a, http.MethodGet, "/promotions/validate-code?code=TETA1", nil)
		if !got.Valid || got.Promotion == nil || got.Promotion.ID != storeA.promotion {
			t.Fatalf("check = %+v", got)
		}
		doc := send[struct {
			GoodsAmount string `json:"goods_amount"`
			Promotions  []struct {
				ID     uuid.UUID `json:"id"`
				Amount string    `json:"amount"`
			} `json:"promotions"`
			Lines []struct {
				LineKind  string `json:"line_kind"`
				UnitPrice string `json:"unit_price"`
			} `json:"lines"`
		}](t, a, http.MethodPost, "/pricing/calculate", map[string]any{
			"lines": []map[string]any{{"sku_id": storeA.sku, "qty": "2"}}, "promotion_codes": []string{"teta1"}})
		if doc.GoodsAmount != "20000" || len(doc.Promotions) != 1 || doc.Promotions[0].ID != storeA.promotion ||
			doc.Promotions[0].Amount != "2000" || len(doc.Lines) != 1 || doc.Lines[0].UnitPrice != "10000" {
			t.Fatalf("calculate = %+v", doc)
		}
		expect(t, a.do(http.MethodPost, v1+"/pricing/calculate", map[string]any{
			"lines": []map[string]any{{"sku_id": storeA.sku, "qty": "1"}}, "promotion_codes": []string{"KHONGCO"}}),
			http.StatusUnprocessableEntity, "validation_failed")

		path := v1 + "/promotions/" + storeA.promotion.String()
		expect(t, a.do(http.MethodPost, path+":deactivate", nil), http.StatusOK, "inactive")
		if got := send[check](t, a, http.MethodGet, "/promotions/validate-code?code=TETA1", nil); got.Valid || got.Reason != "inactive" || got.Promotion != nil {
			t.Fatalf("deactivated = %+v", got)
		}
		expect(t, a.do(http.MethodPost, path+":activate", nil), http.StatusOK, "active")
		rec := a.do(http.MethodGet, path, nil)
		expect(t, rec, http.StatusOK, "")
		expect(t, a.doWith(http.MethodPatch, path, map[string]any{"name": "Giảm 10% Tết"}, http.Header{"If-Match": {rec.Header().Get("ETag")}}),
			http.StatusOK, "Giảm 10% Tết")
		if got := send[check](t, a, http.MethodGet, "/promotions/validate-code?code=TETA1&business_date=2026-04-01", nil); got.Reason != "expired" {
			t.Fatalf("expired = %+v", got)
		}
		if got := send[check](t, b, http.MethodGet, "/promotions/validate-code?code=TETA1", nil); got.Reason != "not_found" {
			t.Fatalf("B checks A's code = %+v", got)
		}
	})

	t.Run("every documented pricing id route is covered", func(t *testing.T) {
		var covered []string
		for _, r := range pricingIDRoutesOf(storeA, storeB) {
			covered = append(covered, r.method+" "+r.path)
		}
		slices.Sort(covered)
		if documented := pricingIDOperationsInSpec(t); !slices.Equal(covered, documented) {
			t.Errorf("covered operations\n%v\ndiffer from the documented ones\n%v", covered, documented)
		}
	})

	// A fresh session: the API limit is per client address.
	t.Run("ids of another store answer 404", func(t *testing.T) {
		b := s.signIn(t, ownerB)
		for _, r := range pricingIDRoutesOf(storeA, storeB) {
			header := http.Header{}
			if r.ifMatch {
				header.Set("If-Match", `"1"`)
			}
			rec := b.doWith(r.method, v1+r.url(), r.body, header)
			if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"not_found"`) {
				t.Errorf("%s %s = %d %s", r.method, r.path, rec.Code, rec.Body)
			}
		}
		// B cannot price A's SKU, name A's lists in its own, nor see them listed.
		expect(t, b.do(http.MethodPost, v1+"/pricing/quote", map[string]any{"lines": []map[string]any{{"sku_id": storeA.sku, "qty": "1"}}}),
			http.StatusUnprocessableEntity, "validation_failed")
		for _, l := range send[dataOf[versioned]](t, b, http.MethodGet, "/price-lists", nil).Data {
			if l.ID == storeA.season || l.ID == storeA.retail {
				t.Errorf("B lists A's price list %s", l.ID)
			}
		}
		for _, p := range send[dataOf[withID]](t, b, http.MethodGet, "/promotions", nil).Data {
			if p.ID == storeA.promotion {
				t.Error("B lists A's promotion")
			}
		}
		// Nothing of A changed.
		got := send[struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		}](t, a, http.MethodGet, "/promotions/"+storeA.promotion.String(), nil)
		if got.Name != "Giảm 10% Tết" || got.Status != "active" {
			t.Errorf("A's promotion changed: %+v", got)
		}
		if items := send[dataOf[withID]](t, a, http.MethodGet, "/price-lists/"+storeA.season.String()+"/items", nil).Data; len(items) != 0 {
			t.Errorf("A's season list gained items: %+v", items)
		}
	})
}
