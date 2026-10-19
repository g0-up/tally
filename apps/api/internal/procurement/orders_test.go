package procurement

import (
	"strings"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
)

func TestValidateOrderLines(t *testing.T) {
	fields := map[string]string{}
	validateOrderLines(fields, []OrderLineInput{
		{SkuID: ids.New(), Qty: qty("100"), UnitPrice: ptr(vnd("240000")), RetailPrice: ptr(vnd("12000"))},
		{SkuID: ids.New(), Qty: qty("1"), UnitPrice: ptr(vnd("0"))},
	})
	wantFields(t, fields, map[string]string{})

	fields = map[string]string{}
	validateOrderLines(fields, []OrderLineInput{
		{Qty: qty("0"), DiscountAmount: vnd("-1"), RetailPrice: ptr(vnd("-1")), SpecText: ptr(strings.Repeat("a", MaxText+1))},
		{SkuID: ids.New(), Qty: qty("-2"), UnitPrice: ptr(vnd("-5"))},
	})
	wantFields(t, fields, map[string]string{
		"lines.0.sku_id":          "required",
		"lines.0.qty":             "must_be_positive",
		"lines.0.unit_price":      "required",
		"lines.0.discount_amount": "must_not_be_negative",
		"lines.0.retail_price":    "must_not_be_negative",
		"lines.0.spec_text":       "too_long",
		"lines.1.qty":             "must_be_positive",
		"lines.1.unit_price":      "must_not_be_negative",
	})

	fields = map[string]string{}
	validateOrderLines(fields, make([]OrderLineInput, MaxLines+1))
	wantFields(t, fields, map[string]string{"lines": "too_many"})
}

func TestValidateOrderDates(t *testing.T) {
	today := day("2026-10-17")
	fields := map[string]string{}
	validateOrderDates(fields, today, ptr(day("2026-10-20")), today)
	validateOrderDates(fields, day("2026-10-01"), ptr(day("2026-10-01")), today)
	wantFields(t, fields, map[string]string{})

	validateOrderDates(fields, day("2026-10-18"), ptr(day("2026-10-10")), today)
	wantFields(t, fields, map[string]string{"order_date": "in_future", "expected_date": "before_order_date"})
}

func TestValidTextCountsCharactersNotBytes(t *testing.T) {
	fields := map[string]string{}
	validText(fields, "note", ptr(strings.Repeat("đ", MaxText)))
	validText(fields, "missing", nil)
	wantFields(t, fields, map[string]string{})
	validText(fields, "note", ptr(strings.Repeat("a", MaxText+1)))
	wantFields(t, fields, map[string]string{"note": "too_long"})
}

func TestLineSpecPrefersTypedText(t *testing.T) {
	sku := ptr("24 hộp/thùng")
	cases := []struct {
		typed *string
		want  *string
	}{
		{typed: nil, want: sku},
		{typed: ptr("   "), want: sku},
		{typed: ptr(" hộp thiếc vuông "), want: ptr("hộp thiếc vuông")},
	}
	for _, c := range cases {
		got := lineSpec(c.typed, sku)
		if got == nil || *got != *c.want {
			t.Errorf("lineSpec(%v) = %v, want %q", c.typed, got, *c.want)
		}
	}
	if got := lineSpec(nil, nil); got != nil {
		t.Errorf("lineSpec(nil, nil) = %q, want nil", *got)
	}
}
