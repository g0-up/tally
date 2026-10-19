package kits

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

func TestValidateRecipeShape(t *testing.T) {
	sku, group := uuid.New(), uuid.New()
	t.Run("a valid recipe", func(t *testing.T) {
		from, to := day("2026-11-01"), day("2027-02-01")
		in := RecipeInput{ValidFrom: &from, ValidTo: &to, PackingCost: vnd("15000"), Note: ptr("  "), Items: []ItemInput{
			{Kind: KindGoods, SkuID: &sku, Qty: qty("2")},
			{Kind: KindMixPack, MixGroupID: &group, Qty: qty("1"), Flavors: []FlavorInput{{SkuID: uuid.New(), Parts: qty("3")}}},
		}}
		fields := map[string]string{}
		validateRecipeShape(fields, "", &in)
		if len(fields) > 0 {
			t.Fatalf("fields = %v", fields)
		}
		if in.Note != nil {
			t.Errorf("note = %q, want a blank note dropped", *in.Note)
		}
	})

	t.Run("the fields of a refused recipe carry the prefix", func(t *testing.T) {
		from, to := day("2027-02-01"), day("2026-11-01")
		in := RecipeInput{ValidFrom: &from, ValidTo: &to, PackingCost: vnd("-1"), Items: []ItemInput{
			{Kind: "gift", SkuID: &sku, Qty: qty("1")},
			{Kind: KindGoods, Qty: qty("0")},
			{Kind: KindPackaging, SkuID: &sku, MixGroupID: &group, Qty: qty("1")},
			{Kind: KindAccessory, SkuID: &sku, Qty: qty("1"), Flavors: []FlavorInput{{SkuID: sku, Parts: qty("1")}}},
			{Kind: KindMixPack, Qty: qty("1"), Flavors: []FlavorInput{{SkuID: sku, Parts: qty("0.5")}}},
			{Kind: KindMixPack, MixGroupID: &group, SkuID: &sku, Qty: qty("1")},
			{Kind: KindMixPack, MixGroupID: &group, ColorID: &sku, Qty: qty("1"), Flavors: []FlavorInput{{SkuID: sku, Parts: qty("1")}}},
		}}
		fields := map[string]string{}
		validateRecipeShape(fields, "recipe.", &in)
		wantFields(t, invalid(fields), map[string]string{
			"recipe.packing_cost":            "must_not_be_negative",
			"recipe.valid_to":                "before_valid_from",
			"recipe.items.0.kind":            "invalid",
			"recipe.items.1.qty":             "must_be_positive",
			"recipe.items.1.sku_id":          "required",
			"recipe.items.2.mix_group_id":    "only_for_mix_pack",
			"recipe.items.3.flavors":         "only_for_mix_pack",
			"recipe.items.4.mix_group_id":    "required",
			"recipe.items.4.flavors.0.parts": "must_be_positive_whole",
			"recipe.items.5.sku_id":          "not_allowed_with_mix_group",
			"recipe.items.5.flavors":         "required",
			"recipe.items.6.color_id":        "not_allowed_with_mix_group",
		})
	})

	t.Run("item count", func(t *testing.T) {
		fields := map[string]string{}
		validateRecipeShape(fields, "", &RecipeInput{})
		if fields["items"] != "required" {
			t.Errorf("fields = %v, want items required", fields)
		}
		in := RecipeInput{}
		for range MaxItems + 1 {
			in.Items = append(in.Items, ItemInput{Kind: KindGoods, SkuID: &sku, Qty: qty("1")})
		}
		fields = map[string]string{}
		validateRecipeShape(fields, "", &in)
		if fields["items"] != "too_many" {
			t.Errorf("fields = %v, want items too_many", fields)
		}
	})
}

func TestOptionalText(t *testing.T) {
	tests := []struct {
		name   string
		in     *string
		want   *string
		reason string
	}{
		{"absent", nil, nil, ""},
		{"blank", ptr(" \t "), nil, ""},
		{"trimmed", ptr("  Quà Tết  "), ptr("Quà Tết"), ""},
		{"line breaks are text", ptr("a\nb"), ptr("a\nb"), ""},
		{"too long", ptr(strings.Repeat("ê", 9)), ptr(strings.Repeat("ê", 9)), "too_long"},
		{"control character", ptr("a\x07b"), ptr("a\x07b"), "invalid_text"},
		{"broken utf-8", ptr("a\xffb"), ptr("a\xffb"), "invalid_text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := map[string]string{}
			got := optionalText(fields, "note", tt.in, 8)
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Errorf("optionalText = %v, want %v", got, tt.want)
			}
			if fields["note"] != tt.reason {
				t.Errorf("reason = %q, want %q", fields["note"], tt.reason)
			}
		})
	}
}

func TestKitsCoveredCountsWholeKits(t *testing.T) {
	tests := []struct{ stock, perKit, want string }{
		{"10", "3", "3"},
		{"9", "3", "3"},
		{"2.5", "0.5", "5"},
		{"2", "3", "0"},
		{"0", "1", "0"},
		{"-4", "1", "0"},
		{"5", "0", "0"},
	}
	for _, tt := range tests {
		wantQty(t, "kitsCovered("+tt.stock+", "+tt.perKit+")", kitsCovered(qty(tt.stock), qty(tt.perKit)), tt.want)
	}
}

func TestExpandComponentsMergesASkuAndColor(t *testing.T) {
	ids := newIDs(3)
	candy, ribbon, red := ids[0], ids[1], ids[2]
	comps := []Component{
		{SkuID: candy, BaseQty: qty("2")},
		{SkuID: ribbon, ColorID: &red, BaseQty: qty("1")},
		{SkuID: candy, BaseQty: qty("0.5"), Kind: KindMixPack},
		{SkuID: ribbon, BaseQty: qty("1")},
	}
	parts, err := expandComponents(comps, qty("3"))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 3 {
		t.Fatalf("parts = %+v", parts)
	}
	wantQty(t, "candy", parts[0].BaseQty, "7.5")
	wantQty(t, "red ribbon", parts[1].BaseQty, "3")
	if parts[1].ColorID == nil || *parts[1].ColorID != red || parts[2].ColorID != nil {
		t.Errorf("colors = %v, %v", parts[1].ColorID, parts[2].ColorID)
	}
	wantQty(t, "open ribbon", parts[2].BaseQty, "3")

	_, err = expandComponents([]Component{{SkuID: candy, BaseQty: qty("0.0001")}}, qty("0.5"))
	wantFields(t, err, map[string]string{"lines.0.qty": "too_precise"})
}

func TestMulExact(t *testing.T) {
	if got, ok := mulExact(qty("1.25"), qty("4")); !ok || !got.Equal(qty("5")) {
		t.Errorf("mulExact(1.25, 4) = %s, %v", got, ok)
	}
	if _, ok := mulExact(qty("0.0001"), qty("0.1")); ok {
		t.Error("mulExact accepted a product beyond the quantity scale")
	}
}

func TestRecipeValidOn(t *testing.T) {
	from, to := day("2026-12-01"), day("2027-01-31")
	open := Recipe{}
	window := Recipe{ValidFrom: &from, ValidTo: &to}
	tests := []struct {
		r    Recipe
		day  string
		want bool
	}{
		{open, "2020-01-01", true},
		{window, "2026-11-30", false},
		{window, "2026-12-01", true},
		{window, "2027-01-31", true},
		{window, "2027-02-01", false},
		{Recipe{ValidTo: &to}, "2020-01-01", true},
		{Recipe{ValidFrom: &from}, "2030-01-01", true},
	}
	for _, tt := range tests {
		if got := tt.r.ValidOn(day(tt.day)); got != tt.want {
			t.Errorf("ValidOn(%s) with %v..%v = %v, want %v", tt.day, tt.r.ValidFrom, tt.r.ValidTo, got, tt.want)
		}
	}
}

func TestRemapLinesPointsAtTheRequestLines(t *testing.T) {
	err := apperr.NewValidation(CodeInsufficientStock, "Not enough stock", map[string]string{
		"lines.0.qty": "insufficient", "lines.1.color_id": "unknown", "lines.7.qty": "out_of_range", "business_date": "closed",
	})
	got := remapLines(err, []int{3, -1}, "kit_lot_id")
	wantCode(t, got, apperr.Validation, CodeInsufficientStock)
	want := map[string]string{"lines.3.qty": "insufficient", "kit_lot_id": "unknown", "lines.7.qty": "out_of_range", "business_date": "closed"}
	wantFields(t, got, want)
	if !errors.Is(got, err) {
		t.Error("the remapped error lost its cause")
	}

	plain := errors.New("boom")
	if remapLines(plain, []int{1}, "x") != plain {
		t.Error("a plain error was changed")
	}
	same := apperr.NewValidation("validation_failed", "bad", map[string]string{"qty": "must_be_positive"})
	if remapLines(same, []int{1}, "x") != same {
		t.Error("an error without line fields was copied")
	}
}

func TestOptTellsAbsentFromNull(t *testing.T) {
	var in struct {
		Note  opt[string] `json:"note"`
		Price opt[string] `json:"price"`
		Kept  opt[string] `json:"kept"`
	}
	if err := json.Unmarshal([]byte(`{"note": null, "price": "12000"}`), &in); err != nil {
		t.Fatal(err)
	}
	if !in.Note.set || in.Note.value != nil {
		t.Errorf("note = %+v, want set and cleared", in.Note)
	}
	if !in.Price.set || in.Price.value == nil || *in.Price.value != "12000" {
		t.Errorf("price = %+v", in.Price)
	}
	if in.Kept.set {
		t.Error("an absent field was marked as given")
	}
	if err := json.Unmarshal([]byte(`{"note": 5}`), &in); err == nil {
		t.Error("a number was accepted for a text field")
	}
}

func TestDateReadsACivilDate(t *testing.T) {
	var d date
	if err := json.Unmarshal([]byte(`"2026-12-24"`), &d); err != nil {
		t.Fatal(err)
	}
	got := d.time()
	if got.Year() != 2026 || got.Month() != time.December || got.Day() != 24 || got.Hour() != 0 {
		t.Errorf("date = %v", got)
	}
	for _, bad := range []string{`"24/12/2026"`, `20261224`, `"2026-12-24T00:00:00Z"`} {
		if err := json.Unmarshal([]byte(bad), &d); err == nil {
			t.Errorf("%s was accepted", bad)
		}
	}
	var none *date
	if none.time() != nil {
		t.Error("a missing date read as a time")
	}
}
