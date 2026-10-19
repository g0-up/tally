package kits

import (
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestValidateSuggest(t *testing.T) {
	sku, red := uuid.New(), uuid.New()
	wantFields(t, invalid(validateSuggest(SuggestInput{Budget: vnd("500000"), PackingCost: vnd("0"), Candidates: []SuggestCandidate{
		{SkuID: sku, MinQty: qty("1"), MaxQty: ptr(qty("3"))},
		{SkuID: sku, ColorID: &red, MinQty: qty("0")},
	}})), nil)

	wantFields(t, invalid(validateSuggest(SuggestInput{Budget: vnd("0"), PackingCost: vnd("-1"), Candidates: []SuggestCandidate{
		{MinQty: qty("0")},
		{SkuID: sku, MinQty: qty("1.5")},
		{SkuID: sku, MinQty: qty("0")},
		{SkuID: red, MinQty: qty("2"), MaxQty: ptr(qty("1"))},
		{SkuID: uuid.New(), MinQty: qty("-1"), MaxQty: ptr(qty("0"))},
	}})), map[string]string{
		"budget": "must_be_positive", "packing_cost": "must_not_be_negative", "candidates.0.sku_id": "required",
		"candidates.1.min_qty": "must_be_whole", "candidates.2.sku_id": "duplicate", "candidates.3.max_qty": "invalid",
		"candidates.4.min_qty": "must_be_whole", "candidates.4.max_qty": "invalid",
	})

	if fields := validateSuggest(SuggestInput{Budget: vnd("1")}); fields["candidates"] != "required" {
		t.Errorf("fields = %v, want candidates required", fields)
	}
}

func option(index int, minQty, price, cost, sellable, limit string) suggestOption {
	p, c := vnd(price), vnd(cost)
	return suggestOption{index: index, cand: SuggestCandidate{SkuID: uuid.New(), MinQty: qty(minQty)},
		line:  SuggestLine{UnitPrice: p, UnitCost: c, UnitMargin: p.Sub(c), Sellable: qty(sellable), Qty: money.ZeroQty},
		limit: qty(limit)}
}

func TestSuggestFillsTheBudgetBestMarginFirst(t *testing.T) {
	in := SuggestInput{Budget: vnd("500000"), PackingCost: vnd("20000")}
	opts := []suggestOption{
		// Wine: margin 40 000, but at most one in stock beyond the minimum.
		option(0, "1", "150000", "110000", "2", "2"),
		// Candy box: margin 30 000.
		option(1, "0", "60000", "30000", "50", "50"),
		// Tea: margin 30 000 too but dearer, so it comes after the candy.
		option(2, "0", "90000", "60000", "50", "50"),
		// Ribbon at no price: whatever stock allows.
		option(3, "0", "0", "1000", "3", "3"),
	}
	got, err := suggest(in, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Lines) != 3 {
		t.Fatalf("lines = %+v, want wine, candy and ribbon", got.Lines)
	}
	wantQty(t, "wine", got.Lines[0].Qty, "2")
	// 500 000 - 300 000 leaves room for three candy boxes and no tea.
	wantQty(t, "candy", got.Lines[1].Qty, "3")
	wantQty(t, "ribbon", got.Lines[2].Qty, "3")
	wantMoney(t, "candy amount", got.Lines[1].Amount, "180000")
	wantMoney(t, "expected price", got.ExpectedPrice, "480000")
	wantMoney(t, "components cost", got.ComponentsCost, "313000")
	wantMoney(t, "total cost", got.TotalCost, "333000")
	wantMoney(t, "margin", got.Margin, "147000")
	wantMoney(t, "budget left", got.BudgetLeft, "20000")
}

func TestSuggestRefusesMinimumsItCannotHold(t *testing.T) {
	in := SuggestInput{Budget: vnd("100000"), PackingCost: vnd("0")}

	_, err := suggest(in, []suggestOption{option(4, "3", "10000", "5000", "2", "2")})
	wantFields(t, err, map[string]string{"candidates.4.min_qty": "exceeds_sellable"})

	_, err = suggest(in, []suggestOption{option(0, "2", "60000", "30000", "9", "9")})
	wantFields(t, err, map[string]string{"budget": "below_minimum"})
}
