package money

import (
	"errors"
	"math/rand"
	"testing"
	"testing/quick"

	"github.com/shopspring/decimal"
)

func moneys(ss ...string) []Money {
	out := make([]Money, len(ss))
	for i, s := range ss {
		out[i] = MustMoney(s)
	}
	return out
}

func equalMoneys(a, b []Money) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}
	return true
}

func TestAllocate(t *testing.T) {
	cases := []struct {
		name    string
		total   string
		weights []string
		vnd     bool
		want    []string
	}{
		{"even split", "100", []string{"1", "1"}, true, []string{"50", "50"}},
		{"remainder to the largest remainder", "100", []string{"1", "1", "1"}, true, []string{"34", "33", "33"}},
		{"proportional", "30000", []string{"200000", "100000"}, true, []string{"20000", "10000"}},
		{"zero weight gets nothing", "10", []string{"0", "3", "1"}, true, []string{"0", "8", "2"}},
		{"storage scale", "1", []string{"1", "1", "1"}, false, []string{"0.3334", "0.3333", "0.3333"}},
		{"negative total", "-100", []string{"1", "1", "1"}, true, []string{"-34", "-33", "-33"}},
		{"zero total over zero weights", "0", []string{"0", "0"}, true, []string{"0", "0"}},
		{"no weights", "0", nil, true, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			alloc := Allocate
			if tc.vnd {
				alloc = AllocateVND
			}
			got, err := alloc(MustMoney(tc.total), moneys(tc.weights...))
			if err != nil {
				t.Fatal(err)
			}
			if !equalMoneys(got, moneys(tc.want...)) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAllocateRejects(t *testing.T) {
	if _, err := AllocateVND(MustMoney("10"), moneys("0", "0")); !errors.Is(err, ErrNoWeight) {
		t.Errorf("all-zero weights: err = %v", err)
	}
	if _, err := AllocateVND(MustMoney("10"), moneys("1", "-1")); !errors.Is(err, ErrNegativeWeight) {
		t.Errorf("negative weight: err = %v", err)
	}
	if _, err := AllocateVND(MustMoney("10.5"), moneys("1")); !errors.Is(err, ErrScale) {
		t.Errorf("fractional VND total: err = %v", err)
	}
}

// Property: for random totals and weights the parts add up to the total exactly, keep the
// requested scale and stay within one unit of the exact proportional share.
func TestPropertyAllocateKeepsTotal(t *testing.T) {
	r := rand.New(rand.NewSource(20261017))
	for _, scale := range []int32{0, MoneyScale} {
		unit := decimal.New(1, -scale)
		prop := func(rawTotal int64, n uint8) bool {
			total := Money{d: scaledDecimal(rawTotal%1_000_000_000_000, scale)}
			weights := make([]Money, int(n%20)+1)
			sum := decimal.Zero
			for i := range weights {
				if r.Intn(5) > 0 {
					weights[i] = Money{d: scaledDecimal(r.Int63n(10_000_000_000), MoneyScale)}
				}
				sum = sum.Add(weights[i].d)
			}
			if sum.IsZero() {
				weights[0] = NewMoneyFromInt(1)
				sum = decimal.NewFromInt(1)
			}
			parts, err := allocate(total, weights, scale)
			if err != nil {
				return false
			}
			if !SumMoney(parts...).Equal(total) {
				return false
			}
			for i, p := range parts {
				exact := total.d.Mul(weights[i].d).Div(sum)
				if !p.d.Equal(p.d.Truncate(scale)) || p.d.Sub(exact).Abs().GreaterThanOrEqual(unit) {
					return false
				}
			}
			return true
		}
		if err := quick.Check(prop, quickConfig()); err != nil {
			t.Fatalf("scale %d: %v", scale, err)
		}
	}
}

func TestRoundVNDKeepTotal(t *testing.T) {
	got := RoundVNDKeepTotal(moneys("10.4", "10.4", "20.4"))
	// Rounded one by one: 10 + 10 + 20 = 40; the rounded total is 41, the extra đồng goes to 20.4.
	if !equalMoneys(got, moneys("10", "10", "21")) {
		t.Fatalf("got %v", got)
	}
	// Five halves round up to 5 one by one but to 3 together: two đồng come back from the first
	// two amounts, and none goes negative.
	if got := RoundVNDKeepTotal(moneys("0.5", "0.5", "0.5", "0.5", "0.5")); !equalMoneys(got, moneys("0", "0", "1", "1", "1")) {
		t.Fatalf("halves: got %v", got)
	}
	if len(RoundVNDKeepTotal(nil)) != 0 {
		t.Fatal("empty input")
	}
}

// Property: rounding a document never loses or invents a đồng: the rounded parts are whole, never
// negative for non-negative amounts, within 1.5 đồng of the exact amount, and add up to the
// rounded exact total.
func TestPropertyRoundVNDKeepTotal(t *testing.T) {
	r := rand.New(rand.NewSource(20261018))
	prop := func(n uint8) bool {
		amounts := make([]Money, int(n%50)+1)
		for i := range amounts {
			// Mostly small amounts with frequent halves, where the remainder is largest.
			switch r.Intn(3) {
			case 0:
				amounts[i] = Money{d: scaledDecimal(r.Int63n(100_000_000_000_000), MoneyScale)}
			case 1:
				amounts[i] = Money{d: scaledDecimal(r.Int63n(30_000), MoneyScale)}
			default:
				amounts[i] = Money{d: scaledDecimal(r.Int63n(10)*10_000+5_000, MoneyScale)}
			}
		}
		got := RoundVNDKeepTotal(amounts)
		limit := decimal.RequireFromString("1.5")
		for i, g := range got {
			if !g.d.Equal(g.d.Truncate(0)) || g.IsNegative() || g.d.Sub(amounts[i].d).Abs().GreaterThan(limit) {
				return false
			}
		}
		return SumMoney(got...).Equal(SumMoney(amounts...).RoundVND())
	}
	if err := quick.Check(prop, quickConfig()); err != nil {
		t.Fatal(err)
	}
}

func TestRoundMoney(t *testing.T) {
	third := decimal.NewFromInt(100).Div(decimal.NewFromInt(3))
	got, err := RoundMoney(third)
	if err != nil || !got.Equal(MustMoney("33.3333")) {
		t.Fatalf("RoundMoney(100/3) = %v, %v; want 33.3333", got, err)
	}
	got, err = RoundMoney(decimal.RequireFromString("-0.00005"))
	if err != nil || !got.Equal(MustMoney("-0.0001")) {
		t.Fatalf("RoundMoney(-0.00005) = %v, %v; want -0.0001 (half away from zero)", got, err)
	}
	if _, err := RoundMoney(decimal.New(1, 15)); !errors.Is(err, ErrRange) {
		t.Fatalf("RoundMoney(1e15) error = %v; want ErrRange", err)
	}
}
