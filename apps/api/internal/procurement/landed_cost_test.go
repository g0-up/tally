package procurement

import (
	"strings"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

func TestValidateLandedCost(t *testing.T) {
	payee, method := ids.New(), ids.New()
	ok := LandedCostInput{CostType: CostShipping, Amount: vnd("100000")}
	wantFields(t, validateLandedCost(ok), map[string]string{})
	paid := LandedCostInput{CostType: CostLoading, Method: MethodByQty, Amount: vnd("100000"), PayeePartyID: &payee,
		DueDate: ptr(day("2026-11-01")), PaidNowAmount: vnd("100000"), PaymentMethodID: &method}
	wantFields(t, validateLandedCost(paid), map[string]string{})

	wantFields(t, validateLandedCost(LandedCostInput{Method: "by_weight", Description: ptr(strings.Repeat("x", MaxText+1))}),
		map[string]string{"cost_type": "required", "method": "invalid", "description": "too_long", "amount": "must_be_positive"})
	wantFields(t, validateLandedCost(LandedCostInput{CostType: "tip", Amount: vnd("10.5")}),
		map[string]string{"cost_type": "invalid", "amount": "not_whole_dong"})

	cases := []struct {
		name  string
		edit  func(*LandedCostInput)
		field string
		code  string
	}{
		{"negative paid now", func(in *LandedCostInput) { in.PaidNowAmount = vnd("-1") }, "paid_now_amount", "must_not_be_negative"},
		{"paid now without payee", func(in *LandedCostInput) { in.PayeePartyID, in.DueDate = nil, nil }, "paid_now_amount", "needs_payee"},
		{"paid more than the cost", func(in *LandedCostInput) { in.PaidNowAmount = vnd("100001") }, "paid_now_amount", "exceeds_total"},
		{"paid in fractions", func(in *LandedCostInput) { in.PaidNowAmount = vnd("10.5") }, "paid_now_amount", "not_whole_dong"},
		{"paid without a method", func(in *LandedCostInput) { in.PaymentMethodID = nil }, "payment_method_id", "required"},
	}
	for _, tc := range cases {
		in := paid
		tc.edit(&in)
		if got := validateLandedCost(in); len(got) != 1 || got[tc.field] != tc.code {
			t.Errorf("%s: fields = %v, want %s %s", tc.name, got, tc.field, tc.code)
		}
	}

	due := ok
	due.DueDate = ptr(day("2026-11-01"))
	wantFields(t, validateLandedCost(due), map[string]string{"due_date": "needs_payee"})
}
