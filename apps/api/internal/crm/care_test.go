package crm

import (
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestPlanReorderCopiesSellableLinesAndFlagsTheRest(t *testing.T) {
	sku, color, box, piece := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	lines := []ReorderLine{
		{SkuID: &sku, ColorID: &color, UnitID: box, Qty: money.MustQty("2"), BaseQty: money.MustQty("24"), SkuActive: true, UnitActive: true},
		{SkuID: &sku, UnitID: box, Qty: money.MustQty("1"), BaseQty: money.MustQty("12"), SkuActive: true, BaseUnitID: &piece},
		{SkuID: &sku, UnitID: box, Qty: money.MustQty("1"), SkuActive: false, UnitActive: true},
		{SkuID: nil, Kind: "service", UnitID: box, Qty: money.MustQty("1")},
		{SkuID: &sku, Kind: "mix_pack", UnitID: box, Qty: money.MustQty("1"), SkuActive: true, UnitActive: true},
	}
	draft := planReorder(lines)
	if len(draft) != 2 {
		t.Fatalf("draft = %+v, want the two sellable lines", draft)
	}
	if draft[0].UnitID != box || !draft[0].Qty.Equal(money.MustQty("2")) || draft[0].ColorID == nil || *draft[0].ColorID != color {
		t.Errorf("first line = %+v, want it copied as sold", draft[0])
	}
	// The unit is no longer offered: the line falls back to the base unit and quantity.
	if draft[1].UnitID != piece || !draft[1].Qty.Equal(money.MustQty("12")) {
		t.Errorf("second line = %+v, want 12 in the base unit", draft[1])
	}
	wantFlags := []string{"", "", FlagDiscontinued, FlagNeedsChoice, FlagNeedsChoice}
	for i, l := range lines {
		if l.Flag != wantFlags[i] || l.Copied != (wantFlags[i] == "") {
			t.Errorf("line %d: flag %q copied %v, want flag %q", i, l.Flag, l.Copied, wantFlags[i])
		}
	}
}
