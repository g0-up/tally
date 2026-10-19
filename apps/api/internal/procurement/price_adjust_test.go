package procurement

import (
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestValidateAdjustment(t *testing.T) {
	line := ids.New()
	ok := AdjustmentInput{ReceiptID: ids.New(), ReasonID: ids.New(), Kind: KindFinalPrice,
		Lines: []AdjustmentLineInput{{ReceiptLineID: line, NewUnitPrice: vnd("0")}}}
	wantFields(t, validateAdjustment(ok), map[string]string{})
	ok.Kind = KindPostPurchaseDiscount
	wantFields(t, validateAdjustment(ok), map[string]string{})

	wantFields(t, validateAdjustment(AdjustmentInput{}), map[string]string{
		"receipt_id": "required", "reason_id": "required", "kind": "required", "lines": "required",
	})
	bad := AdjustmentInput{ReceiptID: ids.New(), ReasonID: ids.New(), Kind: "rebate", Lines: []AdjustmentLineInput{
		{ReceiptLineID: line, NewUnitPrice: vnd("1")}, {ReceiptLineID: line, NewUnitPrice: vnd("-1")}, {}}}
	wantFields(t, validateAdjustment(bad), map[string]string{
		"kind":                    "invalid",
		"lines.1.receipt_line_id": "duplicate",
		"lines.1.new_unit_price":  "must_not_be_negative",
		"lines.2.receipt_line_id": "required",
	})
	bad.Lines = make([]AdjustmentLineInput, MaxLines+1)
	if got := validateAdjustment(bad)["lines"]; got != "too_many" {
		t.Errorf("lines = %q, want too_many", got)
	}
}

// carton is 1 carton of 24 at 240000 with 10% tax, 4 units of which went back.
func carton() ReceiptLine {
	return ReceiptLine{UnitPrice: vnd("240000"), Qty: qty("1"), UnitFactorSnap: money.MustFactor("24"), BaseQty: qty("24"),
		ReturnedBaseQty: qty("4"), LineAmount: vnd("264000")}
}

func TestPriceToAmountCarriesTheTax(t *testing.T) {
	for _, onTop := range []bool{true, false} {
		// A line with a net reads the ratio off its own amount, whatever the receipt's basis.
		if got := priceToAmount(carton(), onTop).String(); got != "1.1" {
			t.Errorf("ratio (tax on top %v) = %s, want 1.1", onTop, got)
		}
	}
	free := carton()
	free.UnitPrice, free.LineAmount, free.TaxRateSnap = money.ZeroMoney, money.ZeroMoney, money.MustPercent("10")
	if got := priceToAmount(free, true).String(); got != "1.1" {
		t.Errorf("ratio of a free line with tax added on top = %s, want 1 + 10%%", got)
	}
	if got := priceToAmount(free, false).String(); got != "1" {
		t.Errorf("ratio of a free line whose prices hold the tax = %s, want 1", got)
	}
}

func TestAdjustmentDeltaTaxesARiseOnAFreeLine(t *testing.T) {
	// 10 at 2000 less a 20000 discount at 10% tax: received at nothing, nothing returned.
	l := ReceiptLine{UnitPrice: vnd("2000"), Qty: qty("10"), DiscountAmount: vnd("20000"), TaxRateSnap: money.MustPercent("10"),
		UnitFactorSnap: money.MustFactor("1"), BaseQty: qty("10"), ReturnedBaseQty: money.ZeroQty, LineAmount: money.ZeroMoney}
	if delta, err := adjustmentDelta(l, vnd("2000"), vnd("3000"), true); err != nil || delta.String() != "11000" {
		t.Errorf("rise with tax on top = %s %v, want 10000 net + 1000 tax", delta, err)
	}
	if delta, err := adjustmentDelta(l, vnd("2000"), vnd("3000"), false); err != nil || delta.String() != "10000" {
		t.Errorf("rise with tax in the price = %s %v, want 10000", delta, err)
	}
}

func TestAdjustmentDeltaCoversOnlyTheKeptQuantity(t *testing.T) {
	l := carton()
	l.UnitCost = vnd("11000")
	delta, err := adjustmentDelta(l, vnd("240000"), vnd("216000"), true)
	if err != nil {
		t.Fatal(err)
	}
	carried, residue, cost, err := carriedCost(l, delta)
	if err != nil {
		t.Fatal(err)
	}
	if delta.String() != "-22000" || !carried.Equal(delta) || !residue.IsZero() || cost.String() != "9900" {
		t.Errorf("delta %s carried %s residue %s cost %s, want -22000 on 20 kept units, cost 11000-1100", delta, carried, residue, cost)
	}
	delta, _ = adjustmentDelta(l, vnd("240000"), vnd("264000"), true)
	if _, _, cost, _ = carriedCost(l, delta); delta.String() != "22000" || cost.String() != "12100" {
		t.Errorf("price rise: delta %s cost %s", delta, cost)
	}
}

func TestAdjustmentDeltaStopsAtTheLineValue(t *testing.T) {
	// 10 at 10000 less a 20000 discount: the line holds 80000, 8000 a unit; 4 went back.
	l := ReceiptLine{UnitPrice: vnd("10000"), Qty: qty("10"), DiscountAmount: vnd("20000"), UnitFactorSnap: money.MustFactor("1"),
		BaseQty: qty("10"), ReturnedBaseQty: qty("4"), LineAmount: vnd("80000"), UnitCost: vnd("8000")}
	for _, c := range []struct {
		what, oldPrice, newPrice, want string
	}{
		{"a drop to zero credits what the six kept units are owed, not 10 × 10000", "10000", "0", "-48000"},
		{"a drop to where the discount takes the whole line", "10000", "2000", "-48000"},
		{"a drop that leaves the line some value", "10000", "5000", "-30000"},
		{"a rise from a line the discount took whole owes only above the discount", "1000", "3000", "6000"},
		{"a rise that stays under the discount owes nothing", "0", "1500", "0"},
	} {
		delta, err := adjustmentDelta(l, vnd(c.oldPrice), vnd(c.newPrice), true)
		if err != nil || delta.String() != c.want {
			t.Errorf("%s: delta = %s %v, want %s", c.what, delta, err, c.want)
		}
	}
	delta, _ := adjustmentDelta(l, vnd("10000"), vnd("0"), true)
	if carried, residue, cost, err := carriedCost(l, delta); err != nil || !carried.Equal(delta) || !residue.IsZero() || !cost.IsZero() {
		t.Errorf("drop to zero: carried %s residue %s cost %s %v, want all of it on the cost, which ends at 0", carried, residue, cost, err)
	}
}

func TestCarriedCostStopsAtZero(t *testing.T) {
	l := ReceiptLine{BaseQty: qty("10"), ReturnedBaseQty: qty("4"), UnitCost: vnd("8000")}
	// A drop worth more than the 48000 the six kept units cost leaves them at zero; the rest goes
	// to COGS.
	if carried, residue, cost, _ := carriedCost(l, vnd("-48001")); carried.String() != "-48000" || residue.String() != "-1" || !cost.IsZero() {
		t.Errorf("drop past the kept cost: carried %s residue %s cost %s", carried, residue, cost)
	}
	// Exactly what the kept units cost takes the cost to zero with nothing left over.
	if carried, residue, cost, _ := carriedCost(l, vnd("-48000")); carried.String() != "-48000" || !residue.IsZero() || !cost.IsZero() {
		t.Errorf("drop of the kept cost: carried %s residue %s cost %s", carried, residue, cost)
	}
}
