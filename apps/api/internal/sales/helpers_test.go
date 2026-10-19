package sales

import (
	"maps"
	"testing"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func vnd(s string) money.Money { return money.MustMoney(s) }

func qty(s string) money.Qty { return money.MustQty(s) }

// wantFields fails unless err is nil and want is empty, or err is a validation error with exactly
// the field reasons of want.
func wantFields(t *testing.T, err error, want map[string]string) {
	t.Helper()
	if len(want) == 0 {
		if err != nil {
			t.Fatalf("err = %v, want none", err)
		}
		return
	}
	e, ok := apperr.As(err)
	if !ok || e.Kind != apperr.Validation {
		t.Fatalf("err = %v, want a validation error", err)
	}
	if !maps.Equal(e.Fields, want) {
		t.Errorf("fields = %v, want %v", e.Fields, want)
	}
}

// wantCode fails unless err is an *apperr.Error of the kind and code.
func wantCode(t *testing.T, err error, kind apperr.Kind, code string) map[string]string {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("err = %v, want %s", err, code)
	}
	if e.Kind != kind || e.Code != code {
		t.Fatalf("err = %s/%s, want %s/%s", e.Kind, e.Code, kind, code)
	}
	return e.Fields
}

func wantMoney(t *testing.T, name string, got money.Money, want string) {
	t.Helper()
	if !got.Equal(vnd(want)) {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}
