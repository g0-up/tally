package kits

import (
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestKitLotNo(t *testing.T) {
	if got := kitLotNo("LD-0007", 3); got != "LD-0007-3" {
		t.Errorf("kitLotNo = %q, want LD-0007-3", got)
	}
}

func TestKitExpiryIsTheEarliestComponentExpiry(t *testing.T) {
	early, late := day("2026-11-01"), day("2027-03-01")
	tests := []struct {
		name  string
		moves []Move
		want  *time.Time
	}{
		{"no moves", nil, nil},
		{"nothing expires", []Move{{}, {}}, nil},
		{"earliest wins", []Move{{Expiry: &late}, {}, {Expiry: &early}}, &early},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := kitExpiry(tt.moves)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("kitExpiry = %v, want none", got)
			case tt.want != nil && (got == nil || !got.Equal(*tt.want)):
				t.Errorf("kitExpiry = %v, want %v", got, tt.want)
			}
		})
	}
	moves := []Move{{Expiry: &early}}
	got := kitExpiry(moves)
	*got = late
	if !moves[0].Expiry.Equal(early) {
		t.Error("kitExpiry returned the move's own expiry, not a copy")
	}
}

func TestShareCutsToTheQuantityScale(t *testing.T) {
	tests := []struct{ total, part, whole, want string }{
		{"10", "2", "5", "4"},
		{"10", "1", "3", "3.3333"},
		{"10", "5", "5", "10"},
		{"10", "7", "5", "10"},
	}
	for _, tt := range tests {
		got, err := share(qty(tt.total), qty(tt.part), qty(tt.whole))
		if err != nil {
			t.Fatal(err)
		}
		wantQty(t, "share("+tt.total+","+tt.part+","+tt.whole+")", got, tt.want)
	}
}

func TestProportionalGivesTheRemainderToTheLastPositiveWeight(t *testing.T) {
	got, err := proportional(qty("10"), []money.Qty{qty("1"), qty("0"), qty("1"), qty("1"), qty("0")})
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"3.3333", "0", "3.3333", "3.3334", "0"} {
		wantQty(t, "share", got[i], want)
	}

	got, err = proportional(qty("4"), []money.Qty{qty("0"), qty("0")})
	if err != nil {
		t.Fatal(err)
	}
	wantQty(t, "all-zero first", got[0], "0")
	wantQty(t, "all-zero last", got[1], "4")

	if got, err = proportional(qty("4"), nil); err != nil || len(got) != 0 {
		t.Errorf("proportional(nil) = %v, %v", got, err)
	}
}
