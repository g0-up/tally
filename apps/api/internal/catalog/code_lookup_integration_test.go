//go:build integration

package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestLookupCodeResolvesTheUnitOfTheBarcode(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	scope := scopeOfActor(a)
	milk := h.product(t, a, "Sữa hộp", "SH-1").Skus[0].Sku
	thung := h.unitID(t, a, "thùng")
	if _, err := h.svc.AddSkuUnit(ctx, a, milk.ID, SkuUnitInput{UnitID: thung, Factor: ptr2("48")}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.AddBarcode(ctx, a, milk.ID, BarcodeInput{Code: "18935001234564", UnitID: &thung}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.AddBarcode(ctx, a, milk.ID, BarcodeInput{Code: "0036000291452"}); err != nil {
		t.Fatal(err)
	}

	t.Run("a carton barcode answers the carton and its factor", func(t *testing.T) {
		m, err := h.svc.LookupCode(ctx, scope, "18935001234564")
		if err != nil || m.SkuID != milk.ID || m.UnitID != thung || m.UnitName != "thùng" ||
			!m.Factor.Equal(money.MustFactor("48")) || m.MatchedBy != MatchedByBarcode {
			t.Fatalf("lookup = %+v, %v", m, err)
		}
	})
	t.Run("a 12 digit UPC finds the stored 13 digit code", func(t *testing.T) {
		m, err := h.svc.LookupCode(ctx, scope, "036000291452")
		if err != nil || m.SkuID != milk.ID || m.UnitID != milk.BaseUnitID || !m.Factor.Equal(money.MustFactor("1")) {
			t.Fatalf("lookup = %+v, %v", m, err)
		}
	})
	t.Run("the SKU code answers the base unit, in any case", func(t *testing.T) {
		m, err := h.svc.LookupCode(ctx, scope, " sh-1 ")
		if err != nil || m.SkuID != milk.ID || m.UnitID != milk.BaseUnitID || m.UnitName != "hộp" || m.MatchedBy != MatchedBySkuCode {
			t.Fatalf("lookup = %+v, %v", m, err)
		}
	})
	t.Run("an unknown or malformed code", func(t *testing.T) {
		_, err := h.svc.LookupCode(ctx, scope, "8930000000001")
		wantCode(t, err, "code_not_found")
		_, err = h.svc.LookupCode(ctx, scope, "  ")
		wantField(t, err, "code", "required")
		_, err = h.svc.LookupCode(ctx, scope, "bánh quy")
		wantField(t, err, "code", "invalid_format")
	})
	t.Run("the SKU comes with the match", func(t *testing.T) {
		m, err := h.svc.LookupSkuCode(ctx, scope, "18935001234564")
		if err != nil || m.Sku.ID != milk.ID || len(m.Sku.Units) != 2 || m.UnitID != thung {
			t.Fatalf("lookup = %+v, %v", m, err)
		}
	})
	t.Run("inside the caller's transaction", func(t *testing.T) {
		var m CodeMatch
		if err := h.d.WithTenantTx(ctx, a.TenantID, func(q db.Querier) error {
			var err error
			m, err = h.svc.LookupCodeIn(ctx, q, "18935001234564")
			return err
		}); err != nil || m.UnitID != thung {
			t.Fatalf("lookup in tx = %+v, %v", m, err)
		}
	})
}

func TestLookupCodePrefersBarcodesAndExpandsUPCE(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	scope := scopeOfActor(a)
	// The SKU code of one SKU is the barcode of another: the barcode wins.
	h.product(t, a, "Kẹo dừa", "8935000000017")
	byBarcode := h.product(t, a, "Mứt gừng", "MG-1").Skus[0].Sku
	if _, err := h.svc.AddBarcode(ctx, a, byBarcode.ID, BarcodeInput{Code: "8935000000017"}); err != nil {
		t.Fatal(err)
	}
	m, err := h.svc.LookupCode(ctx, scope, "8935000000017")
	if err != nil || m.SkuID != byBarcode.ID || m.MatchedBy != MatchedByBarcode {
		t.Errorf("barcode against sku code = %+v, %v; want %s by barcode", m, err, byBarcode.ID)
	}

	upc := h.product(t, a, "Bánh xốp", "BX-1").Skus[0].Sku
	if _, err := h.svc.AddBarcode(ctx, a, upc.ID, BarcodeInput{Code: "012345000065"}); err != nil {
		t.Fatal(err)
	}
	if m, err := h.svc.LookupCode(ctx, scope, "01234565"); err != nil || m.SkuID != upc.ID {
		t.Errorf("UPC-E against stored UPC-A = %+v, %v", m, err)
	}
}

func TestLookupCodeSkipsInactiveSkusAndOtherTenants(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, b := h.tenant(t), h.tenant(t)
	stopped := h.product(t, a, "Trà túi lọc", "TT-1")
	sku := stopped.Skus[0].Sku
	if _, err := h.svc.AddBarcode(ctx, a, sku.ID, BarcodeInput{Code: "8935000000024"}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.LookupCode(ctx, scopeOfActor(b), "8935000000024"); !isCode(err, "code_not_found") {
		t.Errorf("another tenant's barcode = %v, want code_not_found", err)
	}
	if _, err := h.svc.LookupCode(ctx, scopeOfActor(b), "TT-1"); !isCode(err, "code_not_found") {
		t.Errorf("another tenant's sku code = %v, want code_not_found", err)
	}
	detail, err := h.svc.Sku(ctx, scopeOfActor(a), sku.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.UpdateSku(ctx, a, sku.ID, detail.Sku.Version, SkuPatch{Status: ptr2(StatusInactive)}); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"8935000000024", "TT-1"} {
		if _, err := h.svc.LookupCode(ctx, scopeOfActor(a), code); !isCode(err, "code_not_found") {
			t.Errorf("inactive sku by %s = %v, want code_not_found", code, err)
		}
	}
}

func TestBarcodesThatAreVariantsOfEachOtherCannotCoexist(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	first := h.product(t, a, "Nước suối", "NS-1").Skus[0].Sku
	second := h.product(t, a, "Nước ngọt", "NN-1").Skus[0].Sku
	if _, err := h.svc.AddBarcode(ctx, a, first.ID, BarcodeInput{Code: "036000291452"}); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"0036000291452", "00036000291452"} {
		_, err := h.svc.AddBarcode(ctx, a, second.ID, BarcodeInput{Code: code})
		wantCode(t, err, "barcode_taken")
		_, err = h.svc.AddBarcode(ctx, a, first.ID, BarcodeInput{Code: code})
		wantCode(t, err, "barcode_taken")
	}
	if _, err := h.svc.AddBarcode(ctx, a, second.ID, BarcodeInput{Code: "012345000065"}); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.AddBarcode(ctx, a, first.ID, BarcodeInput{Code: "01234565"})
	wantCode(t, err, "barcode_taken")
	// A new SKU's barcode goes through the same check.
	_, err = h.svc.CreateProduct(ctx, a, ProductInput{Name: "Nước tăng lực",
		Sku: &SkuInput{Code: "NTL-1", BaseUnitID: h.unitID(t, a, "hộp"), Barcode: ptr2("0012345000065")}})
	wantCode(t, err, "barcode_taken")
}

func isCode(err error, code string) bool { return apperr.Is(err, code) }

func TestUpdateBarcodeUnitMovesTheNextLookup(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, b := h.tenant(t), h.tenant(t)
	scope := scopeOfActor(a)
	milk := h.product(t, a, "Sữa hộp", "SH-1").Skus[0].Sku
	tea := h.product(t, a, "Trà xanh", "TX-1").Skus[0].Sku
	thung, loc := h.unitID(t, a, "thùng"), h.unitID(t, a, "lốc")
	if _, err := h.svc.AddSkuUnit(ctx, a, milk.ID, SkuUnitInput{UnitID: thung, Factor: ptr2("24")}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.AddSkuUnit(ctx, a, tea.ID, SkuUnitInput{UnitID: loc, Factor: ptr2("6")}); err != nil {
		t.Fatal(err)
	}
	carton, err := h.svc.AddBarcode(ctx, a, milk.ID, BarcodeInput{Code: "18935001234564"})
	if err != nil {
		t.Fatal(err)
	}
	teaCode, err := h.svc.AddBarcode(ctx, a, tea.ID, BarcodeInput{Code: "8935000000031"})
	if err != nil {
		t.Fatal(err)
	}
	updates := func() []struct{ Before, After Barcode } {
		t.Helper()
		var out []struct{ Before, After Barcode }
		if err := h.d.WithTenantTx(ctx, a.TenantID, func(q db.Querier) error {
			rows, err := q.QueryxContext(ctx, `SELECT before, after FROM audit_events
				WHERE action = 'sku.barcode_update' AND entity_id = $1 ORDER BY created_at, id`, milk.ID)
			if err != nil {
				return err
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var before, after []byte
				if err := rows.Scan(&before, &after); err != nil {
					return err
				}
				var e struct{ Before, After Barcode }
				if err := json.Unmarshal(before, &e.Before); err != nil {
					return err
				}
				if err := json.Unmarshal(after, &e.After); err != nil {
					return err
				}
				out = append(out, e)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	unitOf := func(code string) uuid.UUID {
		t.Helper()
		m, err := h.svc.LookupCode(ctx, scope, code)
		if err != nil {
			t.Fatal(err)
		}
		return m.UnitID
	}

	t.Run("a unit of another SKU is refused and the barcode keeps its unit", func(t *testing.T) {
		_, err := h.svc.UpdateBarcodeUnit(ctx, a, milk.ID, carton.ID, loc)
		wantField(t, err, "unit_id", "not_a_unit_of_sku")
		_, err = h.svc.UpdateBarcodeUnit(ctx, a, milk.ID, carton.ID, uuid.Nil)
		wantField(t, err, "unit_id", "required")
		if got := unitOf("18935001234564"); got != milk.BaseUnitID {
			t.Errorf("unit after refusal = %s, want the base unit %s", got, milk.BaseUnitID)
		}
	})
	t.Run("a barcode of another SKU or another store is not found", func(t *testing.T) {
		_, err := h.svc.UpdateBarcodeUnit(ctx, a, milk.ID, teaCode.ID, thung)
		wantCode(t, err, "not_found")
		_, err = h.svc.UpdateBarcodeUnit(ctx, b, milk.ID, carton.ID, thung)
		wantCode(t, err, "not_found")
		own := h.product(t, b, "Sữa hộp", "SH-1").Skus[0].Sku
		_, err = h.svc.UpdateBarcodeUnit(ctx, b, own.ID, carton.ID, own.BaseUnitID)
		wantCode(t, err, "not_found")
		if got := unitOf("8935000000031"); got != tea.BaseUnitID {
			t.Errorf("tea barcode unit = %s, want %s", got, tea.BaseUnitID)
		}
	})
	t.Run("moving the box barcode to the carton answers the carton", func(t *testing.T) {
		moved, err := h.svc.UpdateBarcodeUnit(ctx, a, milk.ID, carton.ID, thung)
		if err != nil || moved.ID != carton.ID || moved.UnitID != thung || moved.Code != carton.Code {
			t.Fatalf("update = %+v, %v", moved, err)
		}
		m, err := h.svc.LookupCode(ctx, scope, "18935001234564")
		if err != nil || m.UnitID != thung || m.UnitName != "thùng" || !m.Factor.Equal(money.MustFactor("24")) {
			t.Errorf("lookup = %+v, %v", m, err)
		}
		got := updates()
		if len(got) != 1 || got[0].Before.UnitID != milk.BaseUnitID || got[0].After.UnitID != thung ||
			got[0].After.ID != carton.ID {
			t.Errorf("audit = %+v, want one move from %s to %s", got, milk.BaseUnitID, thung)
		}
	})
	t.Run("the same unit again changes nothing and records nothing", func(t *testing.T) {
		same, err := h.svc.UpdateBarcodeUnit(ctx, a, milk.ID, carton.ID, thung)
		if err != nil || same.UnitID != thung {
			t.Fatalf("update = %+v, %v", same, err)
		}
		if got := updates(); len(got) != 1 {
			t.Errorf("audit rows = %d, want 1", len(got))
		}
	})
}
