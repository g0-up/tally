package sales

import (
	"strings"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestSpreadLots(t *testing.T) {
	first := soldLot{LotID: ids.New(), Sold: qty("3"), Returned: qty("1")}
	spent := soldLot{LotID: ids.New(), Sold: qty("2"), Returned: qty("2")}
	last := soldLot{LotID: ids.New(), Sold: qty("4")}
	lots := []soldLot{first, spent, last}

	takes, ok := spreadLots(lots, qty("3"))
	if !ok || len(takes) != 2 {
		t.Fatalf("spreadLots = %+v, %v; want two takes", takes, ok)
	}
	if takes[0].Lot.LotID != first.LotID || !takes[0].Qty.Equal(qty("2")) {
		t.Errorf("first take = %s of %s, want 2 of the first lot", takes[0].Qty, takes[0].Lot.LotID)
	}
	if takes[1].Lot.LotID != last.LotID || !takes[1].Qty.Equal(qty("1")) {
		t.Errorf("second take = %s of %s, want 1 of the last lot", takes[1].Qty, takes[1].Lot.LotID)
	}

	if takes, ok = spreadLots(lots, qty("6")); !ok || len(takes) != 2 || !takes[1].Qty.Equal(qty("4")) {
		t.Errorf("all that is left = %+v, %v", takes, ok)
	}
	if _, ok = spreadLots(lots, qty("7")); ok {
		t.Error("more than the lots gave was accepted")
	}
	if _, ok = spreadLots(nil, qty("1")); ok {
		t.Error("a line without lots took a return")
	}
}

func TestReturnAmount(t *testing.T) {
	// 3 units sold for 100 000: one at a time comes back as 33 333, 33 333, then the rest 33 334.
	got, err := returnAmount(vnd("100000"), vnd("0"), qty("3"), qty("0"), qty("1"))
	if err != nil {
		t.Fatal(err)
	}
	wantMoney(t, "first unit", got, "33333")
	if got, err = returnAmount(vnd("100000"), vnd("33333"), qty("3"), qty("1"), qty("1")); err != nil {
		t.Fatal(err)
	}
	wantMoney(t, "second unit", got, "33333")
	if got, err = returnAmount(vnd("100000"), vnd("66666"), qty("3"), qty("2"), qty("1")); err != nil {
		t.Fatal(err)
	}
	wantMoney(t, "last unit", got, "33334")
	if got, err = returnAmount(vnd("100000"), vnd("0"), qty("3"), qty("0"), qty("3")); err != nil {
		t.Fatal(err)
	}
	wantMoney(t, "whole line", got, "100000")
	if got, err = returnAmount(vnd("150000"), vnd("0"), qty("4"), qty("0"), qty("1.5")); err != nil {
		t.Fatal(err)
	}
	wantMoney(t, "fraction of a line", got, "56250")
}

func TestSplitAmount(t *testing.T) {
	got, err := splitAmount(vnd("100000"), []money.Qty{qty("1"), qty("1"), qty("1")}, qty("3"))
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"33333", "33333", "33334"} {
		wantMoney(t, "part", got[i], want)
	}
	if got, err = splitAmount(vnd("90000"), []money.Qty{qty("2"), qty("1")}, qty("3")); err != nil {
		t.Fatal(err)
	}
	wantMoney(t, "first", got[0], "60000")
	wantMoney(t, "second", got[1], "30000")
	if got, err = splitAmount(vnd("5000"), []money.Qty{qty("4")}, qty("4")); err != nil || len(got) != 1 {
		t.Fatalf("one part = %v, %v", got, err)
	}
	wantMoney(t, "only part", got[0], "5000")
}

func TestValidateReturn(t *testing.T) {
	doc, line, sku := ids.New(), ids.New(), ids.New()
	reason := ids.New()

	t.Run("sourced", func(t *testing.T) {
		ok := ReturnInput{SalesDocumentID: &doc, ReasonID: reason, Lines: []ReturnLineInput{
			{SalesDocumentLineID: &line, Qty: qty("1"), RestockTo: RestockQuarantine},
		}}
		wantFields(t, validateReturn(ok), nil)

		customer := ids.New()
		bad := ReturnInput{SalesDocumentID: &doc, CustomerID: &customer, Lines: []ReturnLineInput{
			{SalesDocumentLineID: &line, Qty: qty("0"), SkuID: &sku, UnitPrice: ptr(vnd("1")), LotNo: "L1", RestockTo: "shelf"},
			{SalesDocumentLineID: &line, Qty: qty("1")},
			{Qty: qty("1"), ExpiryDate: &Date{}},
		}}
		wantFields(t, validateReturn(bad), map[string]string{
			"reason_id":                      "required",
			"customer_party_id":              "not_allowed",
			"lines.0.qty":                    "must_be_positive",
			"lines.0.restock_to":             "unknown",
			"lines.0.sku_id":                 "not_allowed",
			"lines.0.unit_price":             "not_allowed",
			"lines.0.lot_no":                 "not_allowed",
			"lines.1.sales_document_line_id": "duplicate",
			"lines.2.sales_document_line_id": "required",
			"lines.2.expiry_date":            "not_allowed",
		})
	})
	t.Run("without a document", func(t *testing.T) {
		ok := ReturnInput{ReasonID: reason, Lines: []ReturnLineInput{{SkuID: &sku, Qty: qty("2"), UnitPrice: ptr(vnd("0"))}}}
		wantFields(t, validateReturn(ok), nil)

		bad := ReturnInput{ReasonID: reason, Lines: []ReturnLineInput{
			{SalesDocumentLineID: &line, Qty: qty("1"), UnitPrice: ptr(vnd("-1"))},
			{SkuID: &sku, Qty: qty("1"), LotNo: strings.Repeat("l", maxLabelLen+1)},
		}}
		wantFields(t, validateReturn(bad), map[string]string{
			"lines.0.sales_document_line_id": "not_allowed",
			"lines.0.sku_id":                 "required",
			"lines.0.unit_price":             "negative",
			"lines.1.unit_price":             "required",
			"lines.1.lot_no":                 "too_long",
		})
		wantFields(t, validateReturn(ReturnInput{ReasonID: reason}), map[string]string{"lines": "required"})
	})
}
