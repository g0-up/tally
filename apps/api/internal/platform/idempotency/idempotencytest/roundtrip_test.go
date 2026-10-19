package idempotencytest

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

type line struct {
	SkuID uuid.UUID
	Qty   money.Qty
	Note  *string
}

type header struct {
	Total money.Money
}

type order struct {
	header
	ID        uuid.UUID
	At        time.Time
	Lines     []line
	Tags      map[string]int
	Hidden    string `json:"-"`
	unchecked int
}

// recorder keeps the failures of an assertion that is expected to fail.
type recorder struct {
	testing.TB
	failures []string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func (r *recorder) Fatalf(format string, args ...any) { r.Errorf(format, args...) }

func TestFilledSetsEveryExportedField(t *testing.T) {
	o := Filled[order](t)
	if o.ID == uuid.Nil || o.At.IsZero() || o.Total.IsZero() || len(o.Lines) != 2 || o.Lines[1].Note == nil ||
		o.Lines[0].Qty.IsZero() || o.Lines[0].SkuID == uuid.Nil || len(o.Tags) != 1 || o.Hidden == "" || o.unchecked != 0 {
		t.Fatalf("filled = %+v", o)
	}
}

func TestAssertRoundTrip(t *testing.T) {
	o := Filled[order](t)
	AssertRoundTrip(t, o, func(o order) order { return o })

	r := &recorder{TB: t}
	AssertRoundTrip(r, o, func(o order) string { return o.Hidden })
	if len(r.failures) != 1 {
		t.Fatalf("a view of a field that is not stored passed: %v", r.failures)
	}
}
