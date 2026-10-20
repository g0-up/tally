//go:build integration

package app_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestScanLookupAndPosTileBarcodes(t *testing.T) {
	s, _ := newSalesStack(t)
	a := s.signIn(t, s.signUp(t))
	st := buildSalesStore(t, a)
	sku := st.newSku(t, a, "SUA-HOP", "12000")
	send[map[string]any](t, a, http.MethodPost, "/skus/"+sku.String()+"/units",
		map[string]any{"unit_id": st.units["thùng"], "factor": "48"})
	send[map[string]any](t, a, http.MethodPost, "/skus/"+sku.String()+"/barcodes", map[string]any{"code": "8935001234567"})
	send[map[string]any](t, a, http.MethodPost, "/skus/"+sku.String()+"/barcodes",
		map[string]any{"code": "18935001234564", "unit_id": st.units["thùng"]})

	type match struct {
		Sku struct {
			ID    uuid.UUID `json:"id"`
			Units []struct {
				UnitID uuid.UUID `json:"unit_id"`
			} `json:"units"`
		} `json:"sku"`
		UnitID    uuid.UUID `json:"unit_id"`
		UnitName  string    `json:"unit_name"`
		Factor    string    `json:"factor"`
		MatchedBy string    `json:"matched_by"`
	}
	t.Run("a carton barcode answers the SKU, the carton and its factor", func(t *testing.T) {
		m := send[match](t, a, http.MethodGet, "/sku-lookup?code=18935001234564", nil)
		if m.Sku.ID != sku || len(m.Sku.Units) != 2 || m.UnitID != st.units["thùng"] || m.UnitName != "thùng" ||
			m.Factor != "48" || m.MatchedBy != "barcode" {
			t.Errorf("lookup = %+v", m)
		}
	})
	t.Run("a SKU code answers the base unit", func(t *testing.T) {
		m := send[match](t, a, http.MethodGet, "/sku-lookup?code=sua-hop", nil)
		if m.Sku.ID != sku || m.UnitID != st.units["cái"] || m.Factor != "1" || m.MatchedBy != "sku_code" {
			t.Errorf("lookup = %+v", m)
		}
	})
	t.Run("unknown and malformed codes", func(t *testing.T) {
		expect(t, a.do(http.MethodGet, v1+"/sku-lookup?code=8930000000001", nil), http.StatusNotFound, "code_not_found")
		expect(t, a.do(http.MethodGet, v1+"/sku-lookup", nil), http.StatusUnprocessableEntity, "required")
		expect(t, a.do(http.MethodGet, v1+"/sku-lookup?code=b%C3%A1nh", nil), http.StatusUnprocessableEntity, "invalid_format")
	})
	t.Run("a variant of a taken barcode is refused", func(t *testing.T) {
		other := st.newSku(t, a, "SUA-CHUA", "8000")
		expect(t, a.do(http.MethodPost, v1+"/skus/"+other.String()+"/barcodes", map[string]any{"code": "08935001234567"}),
			http.StatusConflict, "barcode_taken")
	})
	t.Run("a barcode moved to the carton is looked up as the carton", func(t *testing.T) {
		barcodes := send[dataOf[struct {
			ID     uuid.UUID `json:"id"`
			Code   string    `json:"code"`
			UnitID uuid.UUID `json:"unit_id"`
		}]](t, a, http.MethodGet, "/skus/"+sku.String()+"/barcodes", nil).Data
		var box uuid.UUID
		for _, b := range barcodes {
			if b.Code == "8935001234567" {
				box = b.ID
			}
		}
		if box == uuid.Nil {
			t.Fatalf("no box barcode in %+v", barcodes)
		}
		path := "/skus/" + sku.String() + "/barcodes/" + box.String()
		expect(t, a.do(http.MethodPatch, v1+path, map[string]any{"unit_id": st.units["lốc"]}),
			http.StatusUnprocessableEntity, "not_a_unit_of_sku")
		expect(t, a.do(http.MethodPatch, v1+path, map[string]any{}), http.StatusUnprocessableEntity, "required")
		other := st.newSku(t, a, "SUA-DAU", "9000")
		expect(t, a.do(http.MethodPatch, v1+"/skus/"+other.String()+"/barcodes/"+box.String(),
			map[string]any{"unit_id": st.units["cái"]}), http.StatusNotFound, "not_found")

		moved := send[struct {
			ID     uuid.UUID `json:"id"`
			UnitID uuid.UUID `json:"unit_id"`
		}](t, a, http.MethodPatch, path, map[string]any{"unit_id": st.units["thùng"]})
		// The POS tiles check below expects the box barcode on the base unit again, even when a
		// check here stops this subtest early.
		t.Cleanup(func() {
			expect(t, a.do(http.MethodPatch, v1+path, map[string]any{"unit_id": st.units["cái"]}), http.StatusOK, "")
		})
		if moved.ID != box || moved.UnitID != st.units["thùng"] {
			t.Errorf("moved = %+v", moved)
		}
		m := send[match](t, a, http.MethodGet, "/sku-lookup?code=8935001234567", nil)
		if m.Sku.ID != sku || m.UnitID != st.units["thùng"] || m.UnitName != "thùng" || m.Factor != "48" {
			t.Errorf("lookup after the move = %+v", m)
		}
	})
	t.Run("the POS tiles carry the barcodes of their units", func(t *testing.T) {
		boot := send[struct {
			Tiles []struct {
				SkuID    uuid.UUID `json:"sku_id"`
				Barcodes []struct {
					Code     string    `json:"code"`
					UnitID   uuid.UUID `json:"unit_id"`
					UnitName string    `json:"unit_name"`
				} `json:"barcodes"`
			} `json:"tiles"`
		}](t, a, http.MethodGet, "/pos/bootstrap", nil)
		found := false
		for _, tile := range boot.Tiles {
			if tile.Barcodes == nil {
				t.Errorf("tile %s has no barcodes array", tile.SkuID)
			}
			if tile.SkuID != sku {
				continue
			}
			found = true
			want := map[string]uuid.UUID{"8935001234567": st.units["cái"], "18935001234564": st.units["thùng"]}
			if len(tile.Barcodes) != len(want) {
				t.Fatalf("barcodes = %+v, want %v", tile.Barcodes, want)
			}
			for _, b := range tile.Barcodes {
				if want[b.Code] != b.UnitID || b.UnitName == "" {
					t.Errorf("barcode %+v, want unit %s", b, want[b.Code])
				}
			}
		}
		if !found {
			t.Errorf("no tile for %s in %+v", sku, boot.Tiles)
		}
	})
}
