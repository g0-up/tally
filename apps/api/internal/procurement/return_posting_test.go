package procurement

import (
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

func TestValidateReturn(t *testing.T) {
	line := ids.New()
	ok := ReturnInput{ReceiptID: ids.New(), ReasonID: ids.New(), Settlement: SettlementCreditPayable,
		Lines: []ReturnLineInput{{ReceiptLineID: line, Qty: qty("6")}}}
	wantFields(t, validateReturn(ok), map[string]string{})
	for _, s := range []string{SettlementRefund, SettlementReplacement} {
		ok.Settlement = s
		wantFields(t, validateReturn(ok), map[string]string{})
	}

	wantFields(t, validateReturn(ReturnInput{}), map[string]string{
		"receipt_id": "required", "reason_id": "required", "settlement": "required", "lines": "required",
	})
	bad := ReturnInput{ReceiptID: ids.New(), ReasonID: ids.New(), Settlement: "cash",
		Lines: []ReturnLineInput{{ReceiptLineID: line, Qty: qty("1")}, {ReceiptLineID: line, Qty: qty("0")}, {Qty: qty("-1")}}}
	wantFields(t, validateReturn(bad), map[string]string{
		"settlement":              "invalid",
		"lines.1.receipt_line_id": "duplicate",
		"lines.1.qty":             "must_be_positive",
		"lines.2.receipt_line_id": "required",
		"lines.2.qty":             "must_be_positive",
	})
	bad.Lines = make([]ReturnLineInput, MaxLines+1)
	if got := validateReturn(bad)["lines"]; got != "too_many" {
		t.Errorf("lines = %q, want too_many", got)
	}
}

func TestReturnCreditIsTheLinePricePerUnitWithAdjustments(t *testing.T) {
	// 1 carton of 24 at 240000 with 10% tax: 110000 owed per base unit.
	l := ReceiptLine{ID: ids.New(), LineAmount: vnd("2640000"), BaseQty: qty("24")}
	got, err := returnCredit(l, nil, qty("6"))
	if err != nil || got.String() != "660000" {
		t.Fatalf("credit = %s %v, want 660000", got, err)
	}

	// A price cut of 24000 over the 24 kept units lowers what each returned unit gives back by 1000.
	adj := []adjustedUnits{{ReceiptLineID: l.ID, Amount: vnd("-24000"), BaseQty: qty("24")}, {ReceiptLineID: l.ID}}
	if got, _ := returnCredit(l, adj, qty("6")); got.String() != "654000" {
		t.Errorf("credit after a price cut = %s, want 654000", got)
	}

	thirds := ReceiptLine{LineAmount: vnd("1000"), BaseQty: qty("3")}
	if got, _ := returnCredit(thirds, nil, qty("1")); got.String() != "333" {
		t.Errorf("credit = %s, want whole dong 333", got)
	}
	if got, _ := returnCredit(ReceiptLine{LineAmount: vnd("1000")}, nil, qty("1")); !got.IsZero() {
		t.Errorf("a line with no quantity credits %s", got)
	}
}

func TestStockCarriesTheReturnCostDifferenceUpToWhatItHolds(t *testing.T) {
	cases := []struct {
		name, delta, onHand, value string
		booked                     string
	}{
		{"stock takes it all", "-5000", "4", "200000", "-5000"},
		{"average above the receipt cost gives value back", "8000", "4", "200000", "8000"},
		{"never below zero", "-5000", "4", "3000", "-3000"},
		{"nothing left to carry it", "8000", "0", "0", "0"},
	}
	for _, tc := range cases {
		booked := stockShare(vnd(tc.delta), skuStock{OnHand: qty(tc.onHand), StockValue: vnd(tc.value)})
		if booked.String() != tc.booked {
			t.Errorf("%s: booked %s, want %s", tc.name, booked, tc.booked)
		}
	}
}
