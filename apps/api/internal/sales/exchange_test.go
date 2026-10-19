package sales

import (
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

func TestExchangeSplit(t *testing.T) {
	cases := []struct {
		name                              string
		returned, taken, open             string
		walkIn                            bool
		debtReduction, customerPays, back string
		message                           string
	}{
		{"walk-in takes more", "500000", "650000", "0", true, "0", "150000", "0", "Khách trả thêm 150 000"},
		{"walk-in takes less", "650000", "500000", "0", true, "0", "0", "150000", "Cửa hàng trả lại khách 150 000"},
		{"walk-in even swap", "300000", "300000", "0", true, "0", "0", "0", "Không phát sinh thu chi"},
		{"named, paid original, takes more", "500000", "650000", "0", false, "0", "150000", "0", "Khách trả thêm 150 000"},
		{"named, open original absorbs the return", "500000", "650000", "500000", false, "500000", "650000", "0", "Khách trả thêm 650 000"},
		{"named, part open", "500000", "650000", "200000", false, "200000", "350000", "0", "Khách trả thêm 350 000"},
		{"named, refund after clearing debt", "900000", "300000", "200000", false, "200000", "0", "400000", "Cửa hàng trả lại khách 400 000"},
		{"named, overpaid original counts as nothing open", "500000", "650000", "-100000", false, "0", "150000", "0", "Khách trả thêm 150 000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sp := exchangeSplit(vnd(c.returned), vnd(c.taken), vnd(c.open), c.walkIn)
			wantMoney(t, "returned", sp.Returned, c.returned)
			wantMoney(t, "taken", sp.Taken, c.taken)
			wantMoney(t, "debt reduction", sp.DebtReduction, c.debtReduction)
			wantMoney(t, "customer pays", sp.CustomerPays, c.customerPays)
			wantMoney(t, "store refunds", sp.StoreRefunds, c.back)
			settles := sp.Returned.Sub(sp.DebtReduction).Sub(sp.StoreRefunds).Add(sp.CustomerPays)
			if !settles.Equal(sp.Taken) {
				t.Errorf("returned - debt reduction - refund + customer pays = %s, want taken %s", settles.String(), sp.Taken.String())
			}
			if got := exchangeMessage(sp); got != c.message {
				t.Errorf("message = %q, want %q", got, c.message)
			}
		})
	}
}

func TestFormatVND(t *testing.T) {
	for in, want := range map[string]string{
		"0": "0", "999": "999", "1000": "1 000", "150000": "150 000", "1250000": "1 250 000",
		"12345678": "12 345 678", "-1250000": "-1 250 000", "999.6": "1 000",
	} {
		if got := formatVND(vnd(in)); got != want {
			t.Errorf("formatVND(%s) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateExchange(t *testing.T) {
	line, sku := ids.New(), ids.New()
	ok := ExchangeInput{
		OriginalDocumentID: ids.New(), ReasonID: ids.New(),
		ReturnLines: []ReturnLineInput{{SalesDocumentLineID: &line, Qty: qty("1")}},
		DraftInput:  DraftInput{Lines: []LineInput{{SkuID: &sku, Qty: qty("1")}}},
	}
	wantFields(t, validateExchange(ok), nil)

	bad := ExchangeInput{
		ReturnLines:          []ReturnLineInput{{SkuID: &sku, Qty: qty("1")}},
		ExpectedCustomerPays: ptr(vnd("-1")),
		ExpectedStoreRefunds: ptr(vnd("-1")),
	}
	wantFields(t, validateExchange(bad), map[string]string{
		"original_sales_document_id":            "required",
		"reason_id":                             "required",
		"return_lines.0.sales_document_line_id": "required",
		"return_lines.0.sku_id":                 "not_allowed",
		"lines":                                 "required",
		"expected_customer_pays_amount":         "negative",
		"expected_store_refunds_amount":         "negative",
	})
}
