package sales

import (
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestPlanTender(t *testing.T) {
	cash := PaymentMethod{ID: ids.New(), Cash: true}
	bank := PaymentMethod{ID: ids.New()}
	pay := func(m PaymentMethod, amount string) PosPayment {
		return PosPayment{MethodID: m.ID, Amount: vnd(amount)}
	}

	t.Run("split payment with change", func(t *testing.T) {
		tn, err := planTender(vnd("250000"), true, []PosPayment{pay(cash, "100000"), pay(bank, "150000")},
			[]PaymentMethod{cash, bank}, ptr(vnd("200000")))
		if err != nil {
			t.Fatal(err)
		}
		wantMoney(t, "paid", tn.Paid, "250000")
		wantMoney(t, "cash", tn.Cash, "100000")
		wantMoney(t, "tendered", tn.Tendered, "200000")
		wantMoney(t, "change", tn.Change, "100000")
	})
	t.Run("exact cash without tendered amount", func(t *testing.T) {
		tn, err := planTender(vnd("50000"), true, []PosPayment{pay(cash, "50000")}, []PaymentMethod{cash}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wantMoney(t, "tendered", tn.Tendered, "50000")
		wantMoney(t, "change", tn.Change, "0")
	})
	t.Run("named customer may leave the rest on credit", func(t *testing.T) {
		tn, err := planTender(vnd("300000"), false, []PosPayment{pay(bank, "100000")}, []PaymentMethod{bank}, nil)
		if err != nil {
			t.Fatal(err)
		}
		wantMoney(t, "paid", tn.Paid, "100000")
		if tn, err = planTender(vnd("300000"), false, nil, nil, nil); err != nil || !tn.Paid.IsZero() {
			t.Fatalf("full credit sale = %+v, %v", tn, err)
		}
	})
	t.Run("walk-in pays in full", func(t *testing.T) {
		_, err := planTender(vnd("300000"), true, []PosPayment{pay(cash, "100000")}, []PaymentMethod{cash}, nil)
		wantCode(t, err, apperr.Validation, CodeWalkInPaysInFull)
	})
	t.Run("payments over the total", func(t *testing.T) {
		_, err := planTender(vnd("100000"), false, []PosPayment{pay(cash, "60000"), pay(bank, "60000")},
			[]PaymentMethod{cash, bank}, nil)
		wantCode(t, err, apperr.Validation, CodePaymentMismatch)
	})
	t.Run("tendered below the cash part", func(t *testing.T) {
		_, err := planTender(vnd("100000"), true, []PosPayment{pay(cash, "100000")}, []PaymentMethod{cash}, ptr(vnd("90000")))
		wantFields(t, err, map[string]string{"cash_tendered_amount": "below_cash_payment"})
	})
	t.Run("tendered without a cash payment", func(t *testing.T) {
		_, err := planTender(vnd("100000"), true, []PosPayment{pay(bank, "100000")}, []PaymentMethod{bank}, ptr(vnd("100000")))
		wantFields(t, err, map[string]string{"cash_tendered_amount": "no_cash_payment"})
	})
}

func TestValidateCheckout(t *testing.T) {
	sku := ids.New()
	draft := DraftInput{Lines: []LineInput{{SkuID: &sku, Qty: qty("2")}}}
	ok := CheckoutInput{DraftInput: draft, Payments: []PosPayment{{MethodID: ids.New(), Amount: vnd("1000")}}}
	wantFields(t, validateCheckout(ok), nil)

	bad := CheckoutInput{
		Payments:     []PosPayment{{Amount: vnd("0")}, {MethodID: ids.New(), Amount: vnd("-5")}},
		CashTendered: ptr(vnd("-1")),
	}
	wantFields(t, validateCheckout(bad), map[string]string{
		"lines":                        "required",
		"payments.0.payment_method_id": "required",
		"payments.0.amount":            "must_be_positive",
		"payments.1.amount":            "must_be_positive",
		"cash_tendered_amount":         "negative",
	})

	many := ok
	many.Payments = make([]PosPayment, maxPayments+1)
	for i := range many.Payments {
		many.Payments[i] = PosPayment{MethodID: ids.New(), Amount: vnd("1")}
	}
	wantFields(t, validateCheckout(many), map[string]string{"payments": "too_many"})
}

func TestCheckReversible(t *testing.T) {
	customer := ids.New()
	pos := SalesDocument{Source: SourcePOS, Status: DocPosted, CustomerID: &customer, Totals: Totals{Total: vnd("100000")}}
	if err := checkReversible(pos); err != nil {
		t.Fatalf("named POS sale: %v", err)
	}
	free := SalesDocument{Source: SourcePOS, Status: DocPosted, Totals: Totals{Total: money.ZeroMoney}}
	if err := checkReversible(free); err != nil {
		t.Fatalf("walk-in sale of nothing: %v", err)
	}

	cases := []struct {
		name string
		doc  SalesDocument
		code string
	}{
		{"reversing document", SalesDocument{Source: SourcePOS, ReversalOf: ptr(uuid.New())}, CodeInvalidTransition},
		{"already reversed", SalesDocument{Source: SourcePOS, Status: DocReversed, CustomerID: &customer}, CodeDocumentReversed},
		{"stock out document", SalesDocument{Source: SourceStockOut, Status: DocPosted, CustomerID: &customer}, CodeInvalidTransition},
		{"exchange sale", SalesDocument{Source: SourceExchange, Status: DocPosted, CustomerID: &customer}, CodeInvalidTransition},
		{"walk-in sale", SalesDocument{Source: SourcePOS, Status: DocPosted, Totals: Totals{Total: vnd("1")}}, CodeWalkInReverse},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantCode(t, checkReversible(c.doc), apperr.Conflict, c.code)
		})
	}
}

func TestLockedSkusCoverLines(t *testing.T) {
	combo, part, gift, other := ids.New(), ids.New(), ids.New(), ids.New()
	lines := []Line{
		{Kind: KindCombo, SkuID: &combo},
		{Kind: KindComboComponent, SkuID: &part},
		{Kind: KindGift, SkuID: &gift},
		{Kind: KindItem},
	}
	got := stockedSkus(lines)
	if len(got) != 2 || got[0] != part || got[1] != gift {
		t.Fatalf("stocked skus = %v, want the component and the gift", got)
	}
	if err := setOf([]uuid.UUID{combo, part, gift}).covers(got); err != nil {
		t.Errorf("locks naming the component and the gift: %v", err)
	}
	wantCode(t, setOf([]uuid.UUID{combo, other}).covers(got), apperr.Conflict, CodeSaleChanged)
	wantCode(t, setOf(nil).covers([]uuid.UUID{gift}), apperr.Conflict, CodeSaleChanged)
}
