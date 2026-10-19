package money

import (
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/shopspring/decimal"
)

// ErrNoWeight is returned when an amount must be spread over weights that are all zero.
var ErrNoWeight = errors.New("no positive weight to allocate over")

// ErrNegativeWeight is returned when a weight is negative.
var ErrNegativeWeight = errors.New("negative weight")

// Allocate splits total over weights in proportion, at the storage scale (4 decimals), by the
// largest-remainder method: every part is the exact share truncated to 4 decimals, and the units
// left over go one by one to the parts with the largest truncated remainder (the earlier part on a
// tie). The parts always add up to total exactly and each is within one unit of its exact share.
// A zero total over zero weights yields zeros; a non-zero total needs a positive weight.
func Allocate(total Money, weights []Money) ([]Money, error) {
	return allocate(total, weights, MoneyScale)
}

// AllocateVND is Allocate in whole đồng; total must be a whole amount.
func AllocateVND(total Money, weights []Money) ([]Money, error) {
	return allocate(total, weights, 0)
}

func allocate(total Money, weights []Money, scale int32) ([]Money, error) {
	if !total.d.Equal(total.d.Truncate(scale)) {
		return nil, fmt.Errorf("allocate %s: %w (max %d)", total.d.String(), ErrScale, scale)
	}
	out := make([]Money, len(weights))
	units := scaledInt(total.d, scale)
	sum := new(big.Int)
	ws := make([]*big.Int, len(weights))
	for i, w := range weights {
		if w.IsNegative() {
			return nil, fmt.Errorf("allocate weight %d = %s: %w", i, w.d.String(), ErrNegativeWeight)
		}
		ws[i] = scaledInt(w.d, MoneyScale)
		sum.Add(sum, ws[i])
	}
	if units.Sign() == 0 {
		return out, nil
	}
	if sum.Sign() == 0 {
		return nil, fmt.Errorf("allocate %s: %w", total.d.String(), ErrNoWeight)
	}
	negative := units.Sign() < 0
	units.Abs(units)
	type rest struct {
		i   int
		rem *big.Int
	}
	rests := make([]rest, len(ws))
	given := new(big.Int)
	parts := make([]*big.Int, len(ws))
	for i, w := range ws {
		q, r := new(big.Int).QuoRem(new(big.Int).Mul(units, w), sum, new(big.Int))
		parts[i] = q
		given.Add(given, q)
		rests[i] = rest{i: i, rem: r}
	}
	sort.SliceStable(rests, func(a, b int) bool { return rests[a].rem.Cmp(rests[b].rem) > 0 })
	left := new(big.Int).Sub(units, given).Int64() // < len(weights)
	for k := int64(0); k < left; k++ {
		p := parts[rests[k].i]
		p.Add(p, big.NewInt(1))
	}
	for i, p := range parts {
		if negative {
			p.Neg(p)
		}
		out[i] = Money{d: decimal.NewFromBigInt(p, -scale)}
	}
	return out, nil
}

// scaledInt returns d × 10^scale as an integer; d must have at most scale decimals.
func scaledInt(d decimal.Decimal, scale int32) *big.Int {
	return d.Shift(scale).BigInt()
}

// RoundVNDKeepTotal rounds every amount to whole đồng and hands the difference between the
// rounded sum and the sum of the rounded amounts to the largest amounts, one đồng each, starting
// with the largest (by absolute value, the earlier one on a tie). The parts add up to the rounded
// total, so no đồng is lost or invented. Non-negative amounts stay non-negative: taking back n
// đồng means at least n amounts were rounded up from ≥ 0.5, so the n largest are each ≥ 1 đồng.
func RoundVNDKeepTotal(amounts []Money) []Money {
	out := make([]Money, len(amounts))
	if len(amounts) == 0 {
		return out
	}
	rounded := ZeroMoney
	for i, a := range amounts {
		out[i] = a.RoundVND()
		rounded = rounded.Add(out[i])
	}
	diff := SumMoney(amounts...).RoundVND().Sub(rounded).d.IntPart() // |diff| <= len(amounts)/2
	if diff == 0 {
		return out
	}
	order := make([]int, len(amounts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		return amounts[order[a]].Abs().Cmp(amounts[order[b]].Abs()) > 0
	})
	step := NewMoneyFromInt(1)
	if diff < 0 {
		step, diff = step.Neg(), -diff
	}
	for k := int64(0); k < diff; k++ {
		i := order[k%int64(len(order))]
		out[i] = out[i].Add(step)
	}
	return out
}
