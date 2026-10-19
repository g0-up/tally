package procurement

import (
	"maps"
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func vnd(s string) money.Money { return money.MustMoney(s) }

func qty(s string) money.Qty { return money.MustQty(s) }

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr[T any](v T) *T { return &v }

// wantFields fails unless fields holds exactly want.
func wantFields(t *testing.T, fields, want map[string]string) {
	t.Helper()
	if !maps.Equal(fields, want) {
		t.Errorf("fields = %v, want %v", fields, want)
	}
}

// wantCode fails unless err is an *apperr.Error of the kind and code.
func wantCode(t *testing.T, err error, kind apperr.Kind, code string) *apperr.Error {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("err = %v, want %s", err, code)
	}
	if e.Kind != kind || e.Code != code {
		t.Fatalf("err = kind %v code %q, want kind %v code %q", e.Kind, e.Code, kind, code)
	}
	return e
}

func moneys(in ...money.Money) []string {
	out := make([]string, len(in))
	for i, m := range in {
		out[i] = m.String()
	}
	return out
}
