package procurement

import (
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestValidateReceiptInput(t *testing.T) {
	supplier, method := ids.New(), ids.New()
	ok := ReceiptInput{SupplierPartyID: &supplier, Lines: []ReceiptLineInput{{SkuID: ids.New(), Qty: qty("1"), UnitPrice: ptr(vnd("1000"))}}}
	wantFields(t, validateReceiptInput(ok), map[string]string{})

	advance := ids.New()
	bad := ReceiptInput{
		Note:          ptr(strings.Repeat("x", MaxText+1)),
		PaidNowAmount: vnd("100.5"),
		UseAdvances:   []AdvanceUse{{PaymentID: advance, Amount: vnd("1")}, {PaymentID: advance, Amount: vnd("0")}, {}},
		Lines: []ReceiptLineInput{
			{Condition: "broken", Qty: qty("0"), UnitPrice: ptr(vnd("-1")), DiscountAmount: ptr(vnd("-1")),
				LotNo: strings.Repeat("L", MaxLotNo+1), SpecText: ptr(strings.Repeat("q", MaxText+1))},
		},
	}
	wantFields(t, validateReceiptInput(bad), map[string]string{
		"supplier_party_id":         "required",
		"note":                      "too_long",
		"paid_now_amount":           "not_whole_dong",
		"use_advances.1.payment_id": "duplicate",
		"use_advances.1.amount":     "must_be_positive",
		"use_advances.2.payment_id": "required",
		"use_advances.2.amount":     "must_be_positive",
		"lines.0.condition":         "invalid",
		"lines.0.sku_id":            "required",
		"lines.0.qty":               "must_be_positive",
		"lines.0.unit_price":        "must_not_be_negative",
		"lines.0.discount_amount":   "must_not_be_negative",
		"lines.0.lot_no":            "too_long",
		"lines.0.spec_text":         "too_long",
	})

	payNow := ok
	payNow.PaidNowAmount = vnd("500")
	wantFields(t, validateReceiptInput(payNow), map[string]string{"payment_method_id": "required"})
	payNow.PaymentMethodID = &method
	wantFields(t, validateReceiptInput(payNow), map[string]string{})

	empty := ReceiptInput{SupplierPartyID: &supplier}
	wantFields(t, validateReceiptInput(empty), map[string]string{"lines": "required"})
	empty.Lines = make([]ReceiptLineInput, MaxLines+1)
	if got := validateReceiptInput(empty)["lines"]; got != "too_many" {
		t.Errorf("lines = %q, want too_many", got)
	}
}

func TestPayableScheduleSplitsWhatStaysOpen(t *testing.T) {
	date := day("2026-10-17")
	fields := map[string]string{}
	got := payableSchedule(fields, []Installment{
		{DueDate: day("2026-11-01"), Amount: vnd("600000")},
		{DueDate: day("2026-12-01"), Amount: vnd("300000")},
	}, vnd("1000000"), vnd("100000"), date)
	wantFields(t, fields, map[string]string{})
	if len(got) != 3 || !got[0].DueDate.Equal(date) || got[0].Amount.String() != "100000" {
		t.Fatalf("schedule = %+v, want the part paid now first, due on the business date", got)
	}

	fields = map[string]string{}
	got = payableSchedule(fields, []Installment{{DueDate: day("2026-11-01"), Amount: vnd("1000000")}}, vnd("1000000"), money.ZeroMoney, date)
	if len(got) != 1 || len(fields) != 0 {
		t.Errorf("unpaid schedule = %+v %v", got, fields)
	}

	if got := payableSchedule(map[string]string{}, nil, vnd("1000"), money.ZeroMoney, date); got != nil {
		t.Errorf("no schedule = %+v, want nil (one due date)", got)
	}
}

func TestPayableScheduleRejectsBadInstallments(t *testing.T) {
	fields := map[string]string{}
	payableSchedule(fields, []Installment{
		{DueDate: day("2026-10-16"), Amount: vnd("100")},
		{DueDate: day("2026-12-01"), Amount: vnd("0")},
		{DueDate: day("2026-11-01"), Amount: vnd("100")},
	}, vnd("1000"), money.ZeroMoney, day("2026-10-17"))
	wantFields(t, fields, map[string]string{
		"schedule.0.due_date": "before_business_date",
		"schedule.1.amount":   "must_be_positive",
		"schedule.2.due_date": "not_in_order",
		"schedule":            "total_mismatch",
	})
}

func purchaseLine(base, received string) *PurchaseOrderLine {
	color := ids.New()
	return &PurchaseOrderLine{ID: ids.New(), LineNo: 1, SkuID: ids.New(), UnitID: ids.New(), ColorID: &color, Qty: qty(base),
		BaseQty: qty(base), ReceivedBaseQty: qty(received), UnitPrice: vnd("1000"), DiscountAmount: vnd("100")}
}

func TestResolveReceiptLinesTakesTheOrderedLine(t *testing.T) {
	ol := purchaseLine("100", "0")
	order := &PurchaseOrder{ID: ids.New()}
	lines := map[uuid.UUID]*PurchaseOrderLine{ol.ID: ol}
	refs, fields := resolveReceiptLines([]ReceiptLineInput{{PurchaseOrderLineID: &ol.ID, Qty: qty("60"), SpecText: ptr("hộp thiếc")}}, order, lines)
	wantFields(t, fields, map[string]string{})
	r := refs[0]
	if r.condition != ConditionAccepted || r.orderLine != ol || r.price.SkuID != ol.SkuID || *r.price.UnitID != ol.UnitID ||
		*r.price.ColorID != *ol.ColorID || r.price.UnitPrice.String() != "1000" || r.price.Discount.String() != "60" {
		t.Errorf("ref = %+v, want the order line's SKU, unit, color, price and 60%% of its discount", r)
	}
	if r.spec == nil || *r.spec != "hộp thiếc" {
		t.Errorf("spec = %v, want the typed Quy cách", r.spec)
	}

	price := vnd("900")
	refs, _ = resolveReceiptLines([]ReceiptLineInput{{PurchaseOrderLineID: &ol.ID, Qty: qty("1"), UnitPrice: &price,
		DiscountAmount: ptr(vnd("0"))}}, order, lines)
	if refs[0].price.UnitPrice.String() != "900" || !refs[0].price.Discount.IsZero() {
		t.Errorf("typed price = %+v, want 900 with no discount", refs[0].price)
	}
}

func TestResolveReceiptLinesRejectsLinesOffTheOrder(t *testing.T) {
	ol := purchaseLine("100", "0")
	order := &PurchaseOrder{ID: ids.New()}
	lines := map[uuid.UUID]*PurchaseOrderLine{ol.ID: ol}
	other, otherColor, otherUnit, unknown := ids.New(), ids.New(), ids.New(), ids.New()
	_, fields := resolveReceiptLines([]ReceiptLineInput{
		{PurchaseOrderLineID: &ol.ID, SkuID: other, UnitID: &otherUnit, ColorID: &otherColor, Qty: qty("1")},
		{PurchaseOrderLineID: &unknown, Qty: qty("1")},
		{SkuID: other, Qty: qty("1")},
		{SkuID: other, Qty: qty("1"), Condition: ConditionExcess, UnitPrice: ptr(vnd("1"))},
	}, order, lines)
	wantFields(t, fields, map[string]string{
		"lines.0.sku_id":                 "not_on_order",
		"lines.0.unit_id":                "not_order_unit",
		"lines.0.color_id":               "not_on_order",
		"lines.1.purchase_order_line_id": "not_on_order",
		"lines.2.purchase_order_line_id": "required",
		"lines.2.unit_price":             "required",
	})
}

func TestResolveReceiptLinesWithoutAnOrder(t *testing.T) {
	ref := ids.New()
	refs, fields := resolveReceiptLines([]ReceiptLineInput{
		{PurchaseOrderLineID: &ref, Qty: qty("1")},
		{SkuID: ids.New(), Qty: qty("1"), Condition: ConditionExcess, UnitPrice: ptr(vnd("1"))},
		{SkuID: ids.New(), Qty: qty("2"), UnitPrice: ptr(vnd("5000")), Condition: ConditionDamaged, LotNo: "L1"},
	}, nil, nil)
	wantFields(t, fields, map[string]string{
		"lines.0.purchase_order_line_id": "not_allowed",
		"lines.1.condition":              "not_allowed",
	})
	if refs[2].condition != ConditionDamaged || refs[2].lotNo != "L1" || refs[2].price.UnitPrice.String() != "5000" {
		t.Errorf("quick line = %+v", refs[2])
	}
}

func TestProratedDiscountOfTheBatchesAddsUpToTheOrderedDiscount(t *testing.T) {
	// 100 of discount over 3 units received one by one: 33, 34, 33.
	var sum money.Money
	for i, want := range []string{"33", "34", "33"} {
		got := proratedDiscount(*purchaseLine("3", strconv.Itoa(i)), money.ZeroQty, qty("1"))
		if got.String() != want {
			t.Errorf("batch %d = %s, want %s", i+1, got, want)
		}
		sum = sum.Add(got)
	}
	if sum.String() != "100" {
		t.Errorf("batches add up to %s, want the ordered 100", sum)
	}
	if got := proratedDiscount(*purchaseLine("3", "0"), qty("1"), qty("1")); got.String() != "34" {
		t.Errorf("second line of the same receipt = %s, want 34", got)
	}
	if got := proratedDiscount(*purchaseLine("3", "0"), money.ZeroQty, qty("5")); got.String() != "100" {
		t.Errorf("more than ordered = %s, want the whole discount", got)
	}
	if got := proratedDiscount(*purchaseLine("3", "3"), money.ZeroQty, qty("1")); !got.IsZero() {
		t.Errorf("beyond a fully received line = %s, want none", got)
	}

	// Received quantities are in base units: 60 of 100 cartons of 24 are in, the last 40 take 40.
	cartons := *purchaseLine("100", "1440")
	cartons.BaseQty = qty("2400")
	if got := proratedDiscount(cartons, money.ZeroQty, qty("40")); got.String() != "40" {
		t.Errorf("last 40 cartons = %s, want 40", got)
	}

	none := *purchaseLine("3", "0")
	none.DiscountAmount = money.ZeroMoney
	if got := proratedDiscount(none, money.ZeroQty, qty("1")); !got.IsZero() {
		t.Errorf("no discount = %s", got)
	}
}

func TestResolveReceiptLinesSplitsTheDiscountOverLinesOfTheSameOrderedLine(t *testing.T) {
	ol := purchaseLine("3", "0")
	refs, fields := resolveReceiptLines([]ReceiptLineInput{
		{PurchaseOrderLineID: &ol.ID, Qty: qty("1")},
		{PurchaseOrderLineID: &ol.ID, Qty: qty("2")},
	}, &PurchaseOrder{ID: ids.New()}, map[uuid.UUID]*PurchaseOrderLine{ol.ID: ol})
	wantFields(t, fields, map[string]string{})
	if refs[0].price.Discount.String() != "33" || refs[1].price.Discount.String() != "67" {
		t.Errorf("discounts = %s and %s, want 33 and 67", refs[0].price.Discount, refs[1].price.Discount)
	}
}

func TestCheckLotFieldsFollowsTheSkuLotMode(t *testing.T) {
	expiry := day("2027-01-01")
	fields := map[string]string{}
	checkLotFields(fields, 0, skuInfo{TrackLot: true, TrackExpiry: true}, receiptLineRef{condition: ConditionAccepted})
	checkLotFields(fields, 1, skuInfo{}, receiptLineRef{condition: ConditionAccepted, lotNo: "L", expiry: &expiry})
	checkLotFields(fields, 2, skuInfo{TrackLot: true}, receiptLineRef{condition: ConditionWrongItem})
	wantFields(t, fields, map[string]string{
		"lines.0.lot_no":      "required",
		"lines.0.expiry_date": "required",
		"lines.1.lot_no":      "not_tracked",
		"lines.1.expiry_date": "not_tracked",
	})
}

func receivedOn(ol *PurchaseOrderLine, condition, base string) ReceiptLine {
	return ReceiptLine{PurchaseOrderLineID: &ol.ID, Condition: condition, BaseQty: qty(base)}
}

func TestOrderReceivedCountsWhatEnteredStock(t *testing.T) {
	ol := purchaseLine("2400", "0")
	got := orderReceived([]ReceiptLine{
		receivedOn(ol, ConditionAccepted, "1200"),
		receivedOn(ol, ConditionDamaged, "24"),
		receivedOn(ol, ConditionExcess, "24"),
		receivedOn(ol, ConditionWrongItem, "48"),
		{Condition: ConditionAccepted, BaseQty: qty("5")},
	})
	if len(got) != 1 || got[ol.ID].String() != "1248" {
		t.Errorf("received = %v, want 1248 on the ordered line", got)
	}
}

func TestCheckOrderQtyRefusesMoreThanPending(t *testing.T) {
	ol := purchaseLine("2400", "1440")
	lines := map[uuid.UUID]*PurchaseOrderLine{ol.ID: ol}
	if err := checkOrderQty([]ReceiptLine{receivedOn(ol, ConditionAccepted, "960")}, lines); err != nil {
		t.Errorf("the rest of the order: %v", err)
	}
	if err := checkOrderQty([]ReceiptLine{receivedOn(ol, ConditionAccepted, "960"), receivedOn(ol, ConditionExcess, "24"),
		receivedOn(ol, ConditionWrongItem, "24")}, lines); err != nil {
		t.Errorf("flagged excess and wrong items: %v", err)
	}
	err := checkOrderQty([]ReceiptLine{receivedOn(ol, ConditionAccepted, "900"), receivedOn(ol, ConditionDamaged, "61")}, lines)
	_ = wantCode(t, err, apperr.Conflict, CodeReceiptExceedsOrder)
}

func TestReceiptTotalsLeaveWrongItemsOut(t *testing.T) {
	lines := []ReceiptLine{
		{Condition: ConditionAccepted, DiscountAmount: vnd("100"), LineAmount: vnd("1980")},
		{Condition: ConditionWrongItem, DiscountAmount: vnd("50"), LineAmount: vnd("5000")},
		{Condition: ConditionDamaged, LineAmount: vnd("1100")},
	}
	var r Receipt
	receiptTotals(&r, lines, []money.Money{vnd("2000"), vnd("5000"), vnd("1000")}, []money.Money{vnd("80"), vnd("0"), vnd("100")})
	if r.GoodsAmount.String() != "3000" || r.DiscountAmount.String() != "100" || r.TaxAmount.String() != "180" ||
		r.TotalAmount.String() != "3080" {
		t.Errorf("totals = goods %s discount %s tax %s total %s", r.GoodsAmount, r.DiscountAmount, r.TaxAmount, r.TotalAmount)
	}
}

func TestMovedLotsSeparatesQuarantine(t *testing.T) {
	a, b, lotA, lotB := ids.New(), ids.New(), ids.New(), ids.New()
	moves := []StockMove{
		{LineID: &a, LotID: lotA, Qty: qty("5")},
		{LineID: &b, LotID: lotB, Qty: qty("2"), Quarantine: true},
		{LineID: &a, LotID: ids.New(), Qty: qty("-5")},
		{LotID: ids.New(), Qty: qty("1")},
	}
	if got := movedLots(moves, false); len(got) != 1 || got[a] != lotA {
		t.Errorf("sellable = %v", got)
	}
	if got := movedLots(moves, true); len(got) != 1 || got[b] != lotB {
		t.Errorf("quarantine = %v", got)
	}
}

func TestRenameFieldsPrefixesValidationErrors(t *testing.T) {
	err := renameFields(errInvalid(map[string]string{"amount": "exceeds_open"}), "use_advances.0")
	e := wantCode(t, err, apperr.Validation, "validation_failed")
	wantFields(t, e.Fields, map[string]string{"use_advances.0.amount": "exceeds_open"})

	e = wantCode(t, renameFields(apperr.NewValidation("x", "x", nil), "use_advances.1"), apperr.Validation, "x")
	wantFields(t, e.Fields, map[string]string{"use_advances.1": "invalid"})

	conflict := apperr.NewConflict("c", "c")
	if got := renameFields(conflict, "p"); got != conflict {
		t.Errorf("a conflict passes through, got %v", got)
	}
}

func TestValidCondition(t *testing.T) {
	for _, c := range []string{ConditionAccepted, ConditionDamaged, ConditionWrongItem, ConditionExcess} {
		if !validCondition(c) {
			t.Errorf("%s is valid", c)
		}
	}
	if validCondition("lost") || inStock(ConditionWrongItem) || !inStock(ConditionDamaged) {
		t.Error("lost is not a condition; wrong items never enter stock; damaged goods do")
	}
}
