//go:build integration

package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

// A create retried with its Idempotency-Key (a double click, a lost response) stores one record
// and answers the retry with the first response: same body, same ETag, Idempotent-Replay: true.
func TestCreatesWithAnIdempotencyKeyRunOnce(t *testing.T) {
	s := newKitsStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildKitStore(t, a)
	sku := st.newSku(t, a, "IDEM-HANG", "10000")
	receipt := postReceipt(t, a, map[string]any{"supplier_party_id": st.supplier,
		"lines": []map[string]any{{"sku_id": sku, "qty": "5", "unit_price": "8000"}}})
	kit := createKit(t, a, st.giftBasket("GIO-IDEM"))
	note := map[string]any{"note": "khác"}

	cases := []struct {
		name, path, table string
		body              map[string]any
		status            int
		// other is a field that makes a different request of the body.
		other map[string]any
	}{
		{"quote", "/quotes", "quotes", map[string]any{"customer_party_id": st.customer, "valid_until": "2026-03-31",
			"lines": []map[string]any{saleLine(sku, "2", nil)}}, http.StatusCreated, note},
		{"invoice", "/invoices", "invoices", map[string]any{"customer_party_id": st.customer,
			"lines": []map[string]any{saleLine(sku, "3", nil)}}, http.StatusCreated, note},
		{"purchase order", "/purchase-orders", "purchase_orders", map[string]any{"supplier_party_id": st.supplier,
			"lines": []map[string]any{{"sku_id": sku, "qty": "4", "unit_price": "8000"}}}, http.StatusCreated, note},
		{"supplier document", "/supplier-documents", "supplier_documents", map[string]any{"supplier_party_id": st.supplier,
			"document_no": "HD-IDEM", "document_date": "2026-03-15", "goods_amount": "40000", "tax_amount": "0",
			"receipt_ids": []uuid.UUID{receipt.ID}}, http.StatusCreated, note},
		{"sale price", "/skus/" + sku.String() + "/sale-price", "price_changes", map[string]any{"kind": "retail",
			"price": "12000", "source": "catalog"}, http.StatusOK, map[string]any{"price": "13000"}},
		{"kit", "/kits", "kit_recipes", st.giftBasket("GIO-IDEM-2"), http.StatusCreated, map[string]any{"description": "khác"}},
		{"assembly order", "/assembly-orders", "assembly_orders", map[string]any{"kit_id": kit.ProductID, "planned_qty": "1",
			"colors": []map[string]any{{"sku_id": st.basket, "color_id": st.colors["do"]}}}, http.StatusCreated, note},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows := `SELECT count(*) FROM ` + tc.table
			before := countRows(t, st.tenant, rows)
			key := http.Header{"Idempotency-Key": {ids.New().String()}}

			first := a.doWith(http.MethodPost, v1+tc.path, tc.body, key)
			expect(t, first, tc.status, "")
			if first.Header().Get("Idempotent-Replay") != "" {
				t.Errorf("first response is flagged as a replay")
			}
			again := a.doWith(http.MethodPost, v1+tc.path, tc.body, key)
			expect(t, again, tc.status, "")
			if again.Header().Get("Idempotent-Replay") != "true" {
				t.Errorf("retry is not flagged as a replay: %v", again.Header())
			}
			if first.Header().Get("ETag") != again.Header().Get("ETag") {
				t.Errorf("ETag = %q, then %q", first.Header().Get("ETag"), again.Header().Get("ETag"))
			}
			if got, want := jsonOf(t, again), jsonOf(t, first); !reflect.DeepEqual(got, want) {
				t.Errorf("replayed body differs:\n first: %s\nreplay: %s", first.Body, again.Body)
			}
			if n := countRows(t, st.tenant, rows); n != before+1 {
				t.Errorf("%s rows = %d, want %d", tc.table, n, before+1)
			}

			changed := map[string]any{}
			for k, v := range tc.body {
				changed[k] = v
			}
			for k, v := range tc.other {
				changed[k] = v
			}
			expect(t, a.doWith(http.MethodPost, v1+tc.path, changed, key), http.StatusConflict, idempotency.CodeKeyReused)
			expect(t, a.doWith(http.MethodPost, v1+tc.path, tc.body, http.Header{"Idempotency-Key": {"has space"}}),
				http.StatusUnprocessableEntity, "idempotency_key_invalid")
			if n := countRows(t, st.tenant, rows); n != before+1 {
				t.Errorf("%s rows after the refused retries = %d, want %d", tc.table, n, before+1)
			}
		})
	}

	t.Run("without a key every request creates", func(t *testing.T) {
		before := countRows(t, st.tenant, `SELECT count(*) FROM quotes`)
		body := map[string]any{"customer_party_id": st.customer, "valid_until": "2026-03-31",
			"lines": []map[string]any{saleLine(sku, "1", nil)}}
		first, again := a.do(http.MethodPost, v1+"/quotes", body), a.do(http.MethodPost, v1+"/quotes", body)
		expect(t, first, http.StatusCreated, "")
		expect(t, again, http.StatusCreated, "")
		if again.Header().Get("Idempotent-Replay") != "" || jsonOf(t, first)["id"] == jsonOf(t, again)["id"] {
			t.Errorf("unkeyed retry replayed: %s / %s", first.Body, again.Body)
		}
		if n := countRows(t, st.tenant, `SELECT count(*) FROM quotes`); n != before+2 {
			t.Errorf("quotes = %d, want %d", n, before+2)
		}
	})
}

func jsonOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	return v
}
