package procurement

import (
	"slices"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func receiptLine(condition, amount, base, returned string) ReceiptLine {
	lotID := ids.New()
	return ReceiptLine{ID: ids.New(), SkuID: ids.New(), Condition: condition, LineAmount: vnd(amount), BaseQty: qty(base),
		ReturnedBaseQty: qty(returned), Qty: qty(base), UnitFactorSnap: money.MustFactor("1"), LotID: &lotID}
}

func TestLandedCostSplitsByLargestRemainder(t *testing.T) {
	lines := []ReceiptLine{
		receiptLine(ConditionAccepted, "500000", "10", "0"),
		receiptLine(ConditionAccepted, "500000", "10", "0"),
		receiptLine(ConditionAccepted, "500000", "10", "0"),
	}
	for _, method := range []string{MethodByValue, MethodByQty} {
		weights, err := allocationWeights(lines, method)
		if err != nil {
			t.Fatal(err)
		}
		shares, err := money.AllocateVND(vnd("100000"), weights)
		if err != nil {
			t.Fatal(err)
		}
		if got := moneys(shares...); !slices.Equal(got, []string{"33334", "33333", "33333"}) {
			t.Errorf("%s: shares = %v, want 33334/33333/33333", method, got)
		}
	}
}

func TestAllocationWeightsUseWhatTheLineStillKeeps(t *testing.T) {
	lines := []ReceiptLine{
		receiptLine(ConditionAccepted, "1000000", "10", "5"),
		receiptLine(ConditionAccepted, "300000", "30", "0"),
	}
	byValue, err := allocationWeights(lines, MethodByValue)
	if err != nil {
		t.Fatal(err)
	}
	if got := moneys(byValue...); !slices.Equal(got, []string{"500000", "300000"}) {
		t.Errorf("by_value = %v", got)
	}
	byQty, err := allocationWeights(lines, MethodByQty)
	if err != nil {
		t.Fatal(err)
	}
	if got := moneys(byQty...); !slices.Equal(got, []string{"5", "30"}) {
		t.Errorf("by_qty = %v", got)
	}
}

func TestAllocationWeightsFallBackToQuantityForFreeGoods(t *testing.T) {
	lines := []ReceiptLine{receiptLine(ConditionAccepted, "0", "4", "0"), receiptLine(ConditionAccepted, "0", "6", "0")}
	w, err := allocationWeights(lines, MethodByValue)
	if err != nil {
		t.Fatal(err)
	}
	if got := moneys(w...); !slices.Equal(got, []string{"4", "6"}) {
		t.Errorf("weights = %v, want the quantities", got)
	}
}

func TestKeptLinesSkipWrongItemsAndFullyReturnedLines(t *testing.T) {
	keep := receiptLine(ConditionAccepted, "100", "5", "0")
	damaged := receiptLine(ConditionDamaged, "100", "5", "0")
	lines := []ReceiptLine{keep, receiptLine(ConditionWrongItem, "100", "5", "0"), receiptLine(ConditionAccepted, "100", "5", "5"), damaged}
	got := keptLines(lines)
	if len(got) != 2 || got[0].ID != keep.ID || got[1].ID != damaged.ID {
		t.Errorf("kept = %v, want the accepted and the damaged line", got)
	}
}

func TestSplitShareBetweenStockAndSoldGoods(t *testing.T) {
	cases := []struct {
		name, share, held, kept string
		onHand, sold            string
	}{
		{"all on hand", "33334", "10", "10", "33334", "0"},
		{"lot holds more than the line (shared lot)", "33334", "25", "10", "33334", "0"},
		{"all sold", "33334", "0", "10", "0", "33334"},
		{"part sold rounds the held part", "10000", "1", "3", "3333", "6667"},
		{"nothing kept", "500", "5", "0", "0", "500"},
		{"negative share (price cut)", "-9000", "2", "3", "-6000", "-3000"},
	}
	for _, tc := range cases {
		onHand, sold, err := splitShare(vnd(tc.share), qty(tc.held), qty(tc.kept))
		if err != nil {
			t.Fatal(err)
		}
		if onHand.String() != tc.onHand || sold.String() != tc.sold {
			t.Errorf("%s: on hand %s sold %s, want %s and %s", tc.name, onHand, sold, tc.onHand, tc.sold)
		}
		if !onHand.Add(sold).Equal(vnd(tc.share)) {
			t.Errorf("%s: parts do not add up to the share", tc.name)
		}
	}
}

func TestKeptValueProratesTheLineAmount(t *testing.T) {
	v, err := keptValue(receiptLine(ConditionAccepted, "1000", "3", "1"))
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != "666.6667" {
		t.Errorf("kept value = %s, want 666.6667", v)
	}
	if v, _ := keptValue(ReceiptLine{}); !v.IsZero() {
		t.Errorf("empty line = %s", v)
	}
}

func TestCostAfterAReturnSpreadsOverTheKeptUnits(t *testing.T) {
	// 10 at 50000, 4 sent back: a landed cost of 60000 falls on the 6 kept units only.
	l := receiptLine(ConditionAccepted, "500000", "10", "4")
	l.UnitCost = vnd("50000")
	per, err := perKeptUnit(l, vnd("60000"))
	if err != nil {
		t.Fatal(err)
	}
	cost := l.UnitCost.Add(per)
	if cost.String() != "60000" {
		t.Errorf("unit cost = %s, want 60000", cost)
	}
	// Returning the 6 left takes out what the stock was given: 300000 of goods + 60000 landed.
	if got := cost.MulQty(l.KeptBaseQty()); got.String() != "360000" {
		t.Errorf("value of the rest = %s, want 360000", got)
	}
	if per, _ := perKeptUnit(receiptLine(ConditionAccepted, "100", "5", "5"), vnd("100")); !per.IsZero() {
		t.Errorf("nothing kept = %s", per)
	}
}

func TestUnitCostIncludesLandedCost(t *testing.T) {
	c, err := unitCost(vnd("240000"), vnd("33334"), qty("24"))
	if err != nil {
		t.Fatal(err)
	}
	if c.String() != "11388.9167" {
		t.Errorf("unit cost = %s", c)
	}
	if c, _ := unitCost(vnd("1"), vnd("0"), qty("0")); !c.IsZero() {
		t.Errorf("zero quantity = %s", c)
	}
}
