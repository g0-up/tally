package procurement

import (
	"errors"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
)

func TestRemapLinesRenumbersToTheRequestLines(t *testing.T) {
	err := errInvalid(map[string]string{"lines[1].unit_price": "must_not_be_negative", "lines.0.sku_id": "not_found",
		"lines.7.qty": "x", "note": "too_long"})
	e := wantCode(t, remapLines(err, []int{3, 5}), apperr.Validation, "validation_failed")
	wantFields(t, e.Fields, map[string]string{"lines.5.unit_price": "must_not_be_negative", "lines.3.sku_id": "not_found",
		"lines.7.qty": "x", "note": "too_long"})

	e = wantCode(t, remapLines(errInvalid(map[string]string{"lines[2].qty": "x"}), nil), apperr.Validation, "validation_failed")
	wantFields(t, e.Fields, map[string]string{"lines.2.qty": "x"})
}

func TestRemapLinesPassesOtherErrorsThrough(t *testing.T) {
	plain := errors.New("boom")
	if !errors.Is(remapLines(plain, []int{1}), plain) {
		t.Error("a plain error changed")
	}
	for _, err := range []error{apperr.NewConflict("c", "c"), errInvalid(map[string]string{"note": "too_long"})} {
		if got := remapLines(err, []int{1}); got != err {
			t.Errorf("%v changed to %v", err, got)
		}
	}
}

func TestParseLineField(t *testing.T) {
	cases := []struct {
		key  string
		i    int
		tail string
		ok   bool
	}{
		{"lines.4.qty", 4, "qty", true},
		{"lines[12].unit_id", 12, "unit_id", true},
		{"lines.2", 0, "", false},
		{"lines[2]", 0, "", false},
		{"lines.x.qty", 0, "", false},
		{"lines[-1].qty", 0, "", false},
		{"schedule.0.amount", 0, "", false},
	}
	for _, tc := range cases {
		i, tail, ok := parseLineField(tc.key)
		if i != tc.i || tail != tc.tail || ok != tc.ok {
			t.Errorf("parseLineField(%q) = %d %q %v", tc.key, i, tail, ok)
		}
	}
}
