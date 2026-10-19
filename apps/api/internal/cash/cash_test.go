package cash

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

func TestCategoryFor(t *testing.T) {
	cases := []struct {
		direction, purpose, source string
		want                       string
	}{
		{DirectionIn, PurposeSettlement, "pos", CategorySales},
		{DirectionIn, PurposeSettlement, "invoice", CategorySales},
		{DirectionIn, PurposeSettlement, "exchange", CategorySales},
		{DirectionIn, PurposeSettlement, "debt", CategoryDebtCollection},
		{DirectionIn, PurposeSettlement, "", CategoryDebtCollection},
		{DirectionIn, PurposeAdvance, "debt", CategoryCustomerDeposit},
		{DirectionIn, PurposeDeposit, "invoice", CategoryCustomerDeposit},
		{DirectionIn, PurposeOpeningAdvance, "", CategoryCustomerDeposit},
		{DirectionOut, PurposeSettlement, "purchase", CategorySupplierPayment},
		{DirectionOut, PurposeSettlement, "debt", CategorySupplierPayment},
		{DirectionOut, PurposeSettlement, "landed_cost", CategoryLandedCost},
		{DirectionOut, PurposeAdvance, "debt", CategorySupplierAdvance},
		{DirectionOut, PurposeRefund, "sales_return", CategoryRefund},
		{DirectionIn, PurposeRefund, "debt", CategoryRefund},
		{DirectionOut, PurposeAdvanceRefund, "debt", CategoryRefund},
		{DirectionIn, PurposeAdvanceRefund, "debt", CategoryRefund},
	}
	for _, tc := range cases {
		got, ok := CategoryFor(tc.direction, tc.purpose, tc.source)
		if !ok || got != tc.want {
			t.Errorf("CategoryFor(%s, %s, %s) = %q, %v; want %q", tc.direction, tc.purpose, tc.source, got, ok, tc.want)
		}
		if !validCategory(got) {
			t.Errorf("category %q is not a known category", got)
		}
	}
	if _, ok := CategoryFor("sideways", PurposeSettlement, ""); ok {
		t.Error("unknown direction classified")
	}
	if _, ok := CategoryFor(DirectionIn, "gift", ""); ok {
		t.Error("unknown purpose classified")
	}
}

func TestFundPatchTellsAbsentFromNull(t *testing.T) {
	var p fundPatch
	if err := json.Unmarshal([]byte(`{"bank_name": null, "account_no": "0123", "opening_date": "2026-09-01"}`), &p); err != nil {
		t.Fatal(err)
	}
	in, err := p.input()
	if err != nil {
		t.Fatal(err)
	}
	if !in.BankName.Set || in.BankName.Value != nil {
		t.Errorf("bank_name null = %+v, want a clear", in.BankName)
	}
	if !in.AccountNo.Set || in.AccountNo.Value == nil || *in.AccountNo.Value != "0123" {
		t.Errorf("account_no = %+v", in.AccountNo)
	}
	if !in.OpeningDate.Set || in.OpeningDate.Value == nil || in.OpeningDate.Value.Format(time.DateOnly) != "2026-09-01" {
		t.Errorf("opening_date = %+v", in.OpeningDate)
	}
	if in.Name != nil || in.OpeningBalance != nil {
		t.Errorf("absent fields set: %+v", in)
	}

	var absent fundPatch
	if err := json.Unmarshal([]byte(`{}`), &absent); err != nil {
		t.Fatal(err)
	}
	if in, err := absent.input(); err != nil || in.BankName.Set || in.AccountNo.Set || in.OpeningDate.Set {
		t.Errorf("empty patch = %+v, %v", in, err)
	}
}

func TestFundPatchRejectsBadTypes(t *testing.T) {
	var p fundPatch
	if err := json.Unmarshal([]byte(`{"bank_name": 12, "opening_date": "01/09/2026"}`), &p); err != nil {
		t.Fatal(err)
	}
	_, err := p.input()
	e, ok := apperr.As(err)
	if !ok || e.Fields["bank_name"] != "invalid_type" || e.Fields["opening_date"] != "invalid_date" {
		t.Fatalf("err = %v, want bank_name and opening_date errors", err)
	}
}
