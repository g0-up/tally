package kits

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

func TestOrderStatusMoves(t *testing.T) {
	var st AssemblyOrderStatus
	allowed := []struct{ from, to string }{
		{OrderDraft, OrderReserved},
		{OrderDraft, OrderClosed},
		{OrderReserved, OrderPartiallyCompleted},
		{OrderPartiallyCompleted, OrderCompleted},
		{OrderPartiallyCompleted, OrderInProgress},
		{OrderCompleted, OrderInProgress},
		{OrderCompleted, OrderClosed},
		{OrderReserved, OrderReserved},
	}
	for _, m := range allowed {
		if got, err := st.Move(m.from, m.to); err != nil || got != m.to {
			t.Errorf("Move(%s, %s) = %s, %v", m.from, m.to, got, err)
		}
	}
	refused := []struct{ from, to string }{
		{OrderClosed, OrderClosed},
		{OrderClosed, OrderInProgress},
		{OrderCompleted, OrderPartiallyCompleted},
		{OrderReserved, OrderDraft},
		{OrderInProgress, OrderReserved},
	}
	for _, m := range refused {
		got, err := st.Move(m.from, m.to)
		wantCode(t, err, apperr.Conflict, CodeOrderStatus)
		if got != m.from {
			t.Errorf("Move(%s, %s) = %s, want it to stay", m.from, m.to, got)
		}
	}
	for s, open := range map[string]bool{OrderDraft: true, OrderReserved: true, OrderInProgress: true,
		OrderPartiallyCompleted: true, OrderCompleted: false, OrderClosed: false} {
		if st.Open(s) != open {
			t.Errorf("Open(%s) = %v, want %v", s, !open, open)
		}
	}
}

func TestOrderStatusAfterProgress(t *testing.T) {
	var st AssemblyOrderStatus
	tests := []struct {
		from, net, planned string
		disassembly        bool
		want               string
	}{
		{OrderReserved, "4", "10", false, OrderPartiallyCompleted},
		{OrderPartiallyCompleted, "10", "10", false, OrderCompleted},
		{OrderCompleted, "8", "10", true, OrderInProgress},
		{OrderPartiallyCompleted, "2", "10", true, OrderInProgress},
		{OrderReserved, "0", "10", true, OrderReserved},
		{OrderClosed, "10", "10", false, OrderClosed},
		{OrderClosed, "2", "10", true, OrderClosed},
	}
	for _, tt := range tests {
		if got := st.AfterProgress(tt.from, qty(tt.net), qty(tt.planned), tt.disassembly); got != tt.want {
			t.Errorf("AfterProgress(%s, %s/%s, %v) = %s, want %s", tt.from, tt.net, tt.planned, tt.disassembly, got, tt.want)
		}
	}
}

func TestValidateOrder(t *testing.T) {
	sku := uuid.New()
	ok := OrderInput{KitID: uuid.New(), PlannedQty: qty("10"), Note: ptr("  for Tết  "),
		Colors: []ComponentColor{{SkuID: sku, ColorID: uuid.New()}}}
	if fields := validateOrder(&ok); len(fields) > 0 {
		t.Fatalf("fields = %v", fields)
	}
	if *ok.Note != "for Tết" {
		t.Errorf("note = %q, want it trimmed", *ok.Note)
	}

	bad := OrderInput{PlannedQty: qty("1.5"), Note: ptr("a\x00b"),
		Colors: []ComponentColor{{SkuID: sku, ColorID: uuid.New()}, {SkuID: sku}, {ColorID: uuid.New()}}}
	want := map[string]string{
		"kit_id": "required", "planned_qty": "must_be_positive_whole", "note": "invalid_text",
		"colors.1.sku_id": "duplicate", "colors.1.color_id": "required", "colors.2.sku_id": "required",
	}
	wantFields(t, invalid(validateOrder(&bad)), want)

	many := OrderInput{KitID: uuid.New(), PlannedQty: qty("1"), Note: ptr(strings.Repeat("x", maxNoteLen+1))}
	for range MaxItems + 1 {
		many.Colors = append(many.Colors, ComponentColor{SkuID: uuid.New(), ColorID: uuid.New()})
	}
	fields := validateOrder(&many)
	if fields["colors"] != "too_many" || fields["note"] != "too_long" {
		t.Errorf("fields = %v, want colors too_many and note too_long", fields)
	}
}

func TestApplyColorsFillsOnlyOpenComponents(t *testing.T) {
	ids := newIDs(5)
	open, fixed, other, red, blue := ids[0], ids[1], ids[2], ids[3], ids[4]
	comps := []Component{
		{SkuID: open, BaseQty: qty("2")},
		{SkuID: fixed, ColorID: &red, BaseQty: qty("1")},
	}
	active := map[uuid.UUID]bool{red: true, blue: true}

	got, err := applyColors(comps, []ComponentColor{{SkuID: open, ColorID: blue}}, active)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ColorID == nil || *got[0].ColorID != blue || *got[1].ColorID != red {
		t.Errorf("colors = %v, %v", got[0].ColorID, got[1].ColorID)
	}
	if comps[0].ColorID != nil {
		t.Error("applyColors changed its input")
	}

	_, err = applyColors(comps, []ComponentColor{
		{SkuID: other, ColorID: blue},
		{SkuID: fixed, ColorID: blue},
		{SkuID: open, ColorID: uuid.New()},
	}, active)
	wantFields(t, err, map[string]string{
		"colors.0.sku_id": "not_a_component", "colors.1.color_id": "color_fixed", "colors.2.color_id": "unknown_color",
	})
}

func TestPlanItemsMultipliesThePerKitQuantity(t *testing.T) {
	ids := newIDs(3)
	comps := []Component{
		{ItemID: ids[0], SkuID: ids[1], BaseQty: qty("2.5")},
		{ItemID: ids[0], SkuID: ids[2], BaseQty: qty("1")},
	}
	items, err := planItems(comps, qty("4"))
	if err != nil {
		t.Fatal(err)
	}
	wantQty(t, "first", items[0].PlannedBaseQty, "10")
	wantQty(t, "second", items[1].PlannedBaseQty, "4")
	if items[0].RecipeItemID == nil || *items[0].RecipeItemID != ids[0] || items[0].ID == items[1].ID {
		t.Errorf("items = %+v", items)
	}
	wantQty(t, "consumed", items[0].ConsumedBaseQty, "0")

	_, err = planItems([]Component{{SkuID: ids[1], BaseQty: qty("0.0001")}}, qty("0.5"))
	wantFields(t, err, map[string]string{"planned_qty": "too_precise"})
}

func TestValidateBatch(t *testing.T) {
	wantFields(t, invalid(validateBatch(BatchInput{ProducedQty: qty("2"),
		Lines: []BatchLine{{OrderItemID: uuid.New(), Qty: qty("0"), ScrapQty: qty("1")}}})), nil)

	wantFields(t, invalid(validateBatch(BatchInput{ProducedQty: qty("0"), Lines: []BatchLine{
		{Qty: qty("1")},
		{OrderItemID: uuid.New(), Qty: qty("-1")},
		{OrderItemID: uuid.New(), Qty: qty("1"), ScrapQty: qty("-1")},
		{OrderItemID: uuid.New()},
	}})), map[string]string{
		"produced_qty": "must_be_positive_whole", "lines.0.order_item_id": "required", "lines.1.qty": "must_not_be_negative",
		"lines.2.scrap_qty": "must_not_be_negative", "lines.3.qty": "must_be_positive",
	})
}

func TestPlanBatch(t *testing.T) {
	ids := newIDs(6)
	red, blue := ids[4], ids[5]
	ribbon := OrderItem{ID: ids[0], SkuID: ids[1], PlannedBaseQty: qty("10"), ConsumedBaseQty: qty("0")}
	candy := OrderItem{ID: ids[2], SkuID: ids[3], ColorID: &red, PlannedBaseQty: qty("20"), ConsumedBaseQty: qty("4")}
	items := []OrderItem{ribbon, candy}

	t.Run("an item without a line takes its share", func(t *testing.T) {
		issues, err := planBatch(items, qty("2"), qty("5"), []BatchLine{{OrderItemID: ribbon.ID, Qty: qty("3"), ScrapQty: qty("1")}})
		if err != nil {
			t.Fatal(err)
		}
		if len(issues) != 2 {
			t.Fatalf("issues = %+v", issues)
		}
		if issues[0].Line != 0 || issues[0].Item.ID != ribbon.ID {
			t.Errorf("first issue = %+v", issues[0])
		}
		wantQty(t, "named qty", issues[0].Qty, "3")
		wantQty(t, "named scrap", issues[0].Scrap, "1")
		if issues[1].Line != -1 || issues[1].Item.ID != candy.ID || issues[1].ColorID == nil || *issues[1].ColorID != red {
			t.Errorf("second issue = %+v", issues[1])
		}
		// 16 remaining × 2 produced / 5 left.
		wantQty(t, "share", issues[1].Qty, "6.4")
	})

	t.Run("the last batch takes all that remains", func(t *testing.T) {
		issues, err := planBatch(items, qty("5"), qty("5"), nil)
		if err != nil {
			t.Fatal(err)
		}
		wantQty(t, "ribbon", issues[0].Qty, "10")
		wantQty(t, "candy", issues[1].Qty, "16")
	})

	t.Run("a consumed item takes nothing", func(t *testing.T) {
		done := ribbon
		done.ConsumedBaseQty = qty("10")
		issues, err := planBatch([]OrderItem{done}, qty("1"), qty("2"), nil)
		if err != nil || len(issues) != 0 {
			t.Errorf("issues = %+v, %v", issues, err)
		}
	})

	t.Run("refused lines", func(t *testing.T) {
		_, err := planBatch(items, qty("1"), qty("5"), []BatchLine{
			{OrderItemID: uuid.New(), Qty: qty("1")},
			{OrderItemID: candy.ID, Qty: qty("1"), ColorID: &blue},
			{OrderItemID: ribbon.ID, Qty: qty("6")},
			{OrderItemID: ribbon.ID, Qty: qty("5")},
		})
		wantFields(t, err, map[string]string{
			"lines.0.order_item_id": "not_found", "lines.1.color_id": "color_fixed", "lines.3.qty": CodeExceedsRemaining,
		})
	})

	t.Run("scrap does not count against the remaining quantity", func(t *testing.T) {
		issues, err := planBatch(items, qty("1"), qty("5"), []BatchLine{{OrderItemID: ribbon.ID, Qty: qty("10"), ScrapQty: qty("3")}})
		if err != nil {
			t.Fatal(err)
		}
		wantQty(t, "scrap", issues[0].Scrap, "3")
	})
}

func TestPostingItemsSumsByItemAndLot(t *testing.T) {
	ids := newIDs(5)
	item, sku, lotA, lotB := ids[0], ids[1], ids[2], ids[3]
	used := []Move{
		{ID: uuid.New(), SkuID: sku, LotID: lotA, DocLineID: &item, Qty: qty("-3"), Value: vnd("-300"), LotNo: "A"},
		{ID: uuid.New(), SkuID: sku, LotID: lotB, DocLineID: &item, Qty: qty("-1"), Value: vnd("-120"), LotNo: "B"},
		{ID: uuid.New(), SkuID: sku, LotID: lotA, DocLineID: &item, Qty: qty("-1"), Value: vnd("-100"), LotNo: "A"},
	}
	scrap := []Move{{ID: uuid.New(), SkuID: sku, LotID: lotA, DocLineID: &item, Qty: qty("-1"), Value: vnd("-100"), LotNo: "A"}}
	got, err := postingItems(used, scrap)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].LotID != lotA || got[1].LotID != lotB {
		t.Fatalf("items = %+v", got)
	}
	wantQty(t, "lot A consumed", got[0].ConsumedQty, "5")
	wantQty(t, "lot A scrap", got[0].ScrapQty, "1")
	wantMoney(t, "lot A unit cost", got[0].UnitCost, "100")
	wantQty(t, "lot B consumed", got[1].ConsumedQty, "1")
	wantMoney(t, "lot B unit cost", got[1].UnitCost, "120")
	if got[0].OrderItemID != item || got[0].LotNo != "A" {
		t.Errorf("lot A = %+v", got[0])
	}

	if _, err := postingItems([]Move{{ID: ids[4], Qty: qty("-1")}}, nil); err == nil {
		t.Error("a move without an order item was accepted")
	}
}

func TestValidateSubstitute(t *testing.T) {
	in := SubstituteInput{ItemID: uuid.New(), SkuID: uuid.New(), Qty: qty("2")}
	if fields := validateSubstitute(&in); len(fields) > 0 {
		t.Fatalf("fields = %v", fields)
	}
	wantQty(t, "substitute qty defaults to qty", in.SubstituteQty, "2")

	bad := SubstituteInput{Qty: qty("0"), SubstituteQty: qty("-1")}
	wantFields(t, invalid(validateSubstitute(&bad)), map[string]string{
		"item_id": "required", "sku_id": "required", "qty": "must_be_positive", "substitute_qty": "must_be_positive",
	})
}

func TestOrderItemRemainingIsNeverNegative(t *testing.T) {
	wantQty(t, "remaining", OrderItem{PlannedBaseQty: qty("5"), ConsumedBaseQty: qty("2")}.Remaining(), "3")
	wantQty(t, "over-consumed", OrderItem{PlannedBaseQty: qty("2"), ConsumedBaseQty: qty("5")}.Remaining(), "0")
}
