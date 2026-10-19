package kits

import (
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestValidateDisassembly(t *testing.T) {
	wantFields(t, invalid(validateDisassembly(DisassemblyInput{KitLotID: uuid.New(), Qty: qty("2"), ReasonID: uuid.New(),
		Damaged: []DamagedInput{{SkuID: uuid.New(), Qty: qty("1")}}})), nil)

	wantFields(t, invalid(validateDisassembly(DisassemblyInput{Qty: qty("1.5"),
		Damaged: []DamagedInput{{Qty: qty("0")}}})), map[string]string{
		"kit_lot_id": "required", "qty": "must_be_positive_whole", "reason_id": "required",
		"damaged.0.sku_id": "required", "damaged.0.qty": "must_be_positive",
	})

	many := DisassemblyInput{KitLotID: uuid.New(), Qty: qty("1"), ReasonID: uuid.New()}
	for range MaxItems + 1 {
		many.Damaged = append(many.Damaged, DamagedInput{SkuID: uuid.New(), Qty: qty("1")})
	}
	if fields := validateDisassembly(many); fields["damaged"] != "too_many" {
		t.Errorf("fields = %v, want damaged too_many", fields)
	}
}

func TestChildLotsGroupWhatWentIntoTheKitsByLot(t *testing.T) {
	ids := newIDs(6)
	itemA, itemB, sku, lotA, lotB, color := ids[0], ids[1], ids[2], ids[3], ids[4], ids[5]
	p := AssemblyPosting{Items: []PostingItem{
		{OrderItemID: itemA, SkuID: sku, LotID: lotA, ColorID: &color, ConsumedQty: qty("5"), ScrapQty: qty("1"), UnitCost: vnd("100")},
		{OrderItemID: itemB, SkuID: sku, LotID: lotB, ConsumedQty: qty("2"), ScrapQty: qty("2"), UnitCost: vnd("90")},
		{OrderItemID: itemB, SkuID: sku, LotID: lotA, ConsumedQty: qty("3"), ScrapQty: qty("0"), UnitCost: vnd("100")},
	}}
	got := childLots(p)
	if len(got) != 1 {
		t.Fatalf("children = %+v, want only lot A: lot B went wholly to scrap", got)
	}
	c := got[0]
	if c.LotID != lotA || c.SkuID != sku || c.ColorID == nil || *c.ColorID != color || len(c.Items) != 2 {
		t.Errorf("child = %+v", c)
	}
	wantQty(t, "total", c.Total, "7")
	wantQty(t, "returned", c.Returned, "0")
	wantMoney(t, "cost", c.Cost, "700")
}

func TestSplitDisassemblyValue(t *testing.T) {
	back := func() []childLot {
		return []childLot{
			{Total: qty("3"), Cost: vnd("30000"), Share: qty("1")},
			{Total: qty("3"), Cost: vnd("10000"), Share: qty("2")},
		}
	}

	t.Run("components go back at their cost and the packing is expensed", func(t *testing.T) {
		values, expense, err := splitDisassemblyValue(vnd("20000"), back())
		if err != nil {
			t.Fatal(err)
		}
		wantMoney(t, "first", values[0], "10000")
		wantMoney(t, "second", values[1], "6666.6667")
		wantMoney(t, "expense", expense, "3333.3333")
	})

	t.Run("kits worth exactly their components expense nothing", func(t *testing.T) {
		_, expense, err := splitDisassemblyValue(vnd("16666.6667"), back())
		if err != nil {
			t.Fatal(err)
		}
		wantMoney(t, "expense", expense, "0")
	})

	t.Run("kits that left below their components cost split their value by that cost", func(t *testing.T) {
		values, expense, err := splitDisassemblyValue(vnd("12000"), back())
		if err != nil {
			t.Fatal(err)
		}
		wantMoney(t, "expense", expense, "0")
		wantMoney(t, "total", values[0].Add(values[1]), "12000")
		if values[0].Cmp(values[1]) <= 0 {
			t.Errorf("values = %v, want the costlier share to take more", values)
		}
	})
}

func TestShareOut(t *testing.T) {
	t.Run("a part of the kits takes their part of each lot", func(t *testing.T) {
		c := []childLot{{Total: qty("10"), Returned: qty("0")}, {Total: qty("5"), Returned: qty("0")}}
		if err := shareOut(c, qty("2"), qty("0"), qty("5")); err != nil {
			t.Fatal(err)
		}
		wantQty(t, "first", c[0].Share, "4")
		wantQty(t, "second", c[1].Share, "2")
	})

	t.Run("cutting never adds up and the last kit takes the rest", func(t *testing.T) {
		c := []childLot{{Total: qty("10"), Returned: qty("0")}}
		for i, want := range []string{"3.3333", "3.3333", "3.3334"} {
			if err := shareOut(c, qty("1"), money.NewQtyFromInt(int64(i)), qty("3")); err != nil {
				t.Fatal(err)
			}
			wantQty(t, fmt.Sprintf("kit %d", i+1), c[0].Share, want)
			c[0].Returned = c[0].Returned.Add(c[0].Share)
		}
		wantQty(t, "returned", c[0].Returned, "10")
	})

	t.Run("kits sold from the lot keep their part", func(t *testing.T) {
		// 3 kits made, 1 sold: taking the other 2 apart gives back 2/3 of the lot, cut once.
		c := []childLot{{Total: qty("10"), Returned: qty("0")}}
		if err := shareOut(c, qty("2"), qty("0"), qty("3")); err != nil {
			t.Fatal(err)
		}
		wantQty(t, "share", c[0].Share, "6.6666")
	})

	t.Run("a lot that gave back ahead of its part gives nothing until the last kit", func(t *testing.T) {
		c := []childLot{{Total: qty("10"), Returned: qty("7")}}
		if err := shareOut(c, qty("1"), qty("1"), qty("3")); err != nil {
			t.Fatal(err)
		}
		wantQty(t, "middle kit", c[0].Share, "0")
		if err := shareOut(c, qty("1"), qty("2"), qty("3")); err != nil {
			t.Fatal(err)
		}
		wantQty(t, "last kit", c[0].Share, "3")
	})
}

func TestAllotDamaged(t *testing.T) {
	ids := newIDs(4)
	sku, other, lotA, lotB := ids[0], ids[1], ids[2], ids[3]
	children := func() []childLot {
		return []childLot{
			{SkuID: sku, LotID: lotA, Share: qty("2"), Damaged: qty("0")},
			{SkuID: sku, LotID: lotB, Share: qty("3"), Damaged: qty("0")},
		}
	}

	t.Run("damage fills the lots in order", func(t *testing.T) {
		c := children()
		if err := allotDamaged(c, []DamagedInput{{SkuID: sku, Qty: qty("3")}}); err != nil {
			t.Fatal(err)
		}
		wantQty(t, "lot A", c[0].Damaged, "2")
		wantQty(t, "lot B", c[1].Damaged, "1")
	})

	t.Run("a named lot takes all of it", func(t *testing.T) {
		c := children()
		if err := allotDamaged(c, []DamagedInput{{SkuID: sku, SourceLotID: &lotB, Qty: qty("1")}}); err != nil {
			t.Fatal(err)
		}
		wantQty(t, "lot A", c[0].Damaged, "0")
		wantQty(t, "lot B", c[1].Damaged, "1")
	})

	t.Run("refused damage", func(t *testing.T) {
		stranger := uuid.New()
		err := allotDamaged(children(), []DamagedInput{
			{SkuID: sku, SourceLotID: &stranger, Qty: qty("1")},
			{SkuID: other, Qty: qty("1")},
			{SkuID: sku, Qty: qty("6")},
		})
		wantFields(t, err, map[string]string{
			"damaged.0.source_lot_id": "not_a_component", "damaged.1.sku_id": "not_a_component", "damaged.2.qty": CodeExceedsRemaining,
		})
	})
}
