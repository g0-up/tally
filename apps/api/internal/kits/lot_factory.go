package kits

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// kitLotNo is the lot number of the n-th batch of an order: the order's LD number and n, so each
// batch gets its own kit lot carrying its own expiry.
func kitLotNo(docNo string, n int) string { return fmt.Sprintf("%s-%d", docNo, n) }

// kitExpiry is the expiry of a kit lot: the earliest expiry of the component lots its kits were
// made from, nil when none of them expires.
func kitExpiry(moves []Move) *time.Time {
	var out *time.Time
	for _, m := range moves {
		if m.Expiry != nil && (out == nil || m.Expiry.Before(*out)) {
			e := *m.Expiry
			out = &e
		}
	}
	return out
}

// truncQty is d cut (not rounded) to the quantity scale, so shares never add up to more than
// their whole.
func truncQty(d decimal.Decimal) (money.Qty, error) {
	return money.NewQty(d.Truncate(4).String())
}

// share is part/whole of total cut to the quantity scale; all of total when part covers whole.
func share(total, part, whole money.Qty) (money.Qty, error) {
	if part.Cmp(whole) >= 0 {
		return total, nil
	}
	return truncQty(total.Decimal().Mul(part.Decimal()).Div(whole.Decimal()))
}

// proportional splits total over weights, each cut to the quantity scale, the remainder going to
// the last positive weight. Zero weights get nothing; all-zero weights give the last entry all.
func proportional(total money.Qty, weights []money.Qty) ([]money.Qty, error) {
	out := make([]money.Qty, len(weights))
	sum := money.ZeroQty
	last := len(weights) - 1
	for i, w := range weights {
		out[i] = money.ZeroQty
		if w.IsPositive() {
			sum = sum.Add(w)
			last = i
		}
	}
	if last < 0 {
		return out, nil
	}
	if !sum.IsPositive() {
		out[last] = total
		return out, nil
	}
	given := money.ZeroQty
	for i, w := range weights {
		if i == last || !w.IsPositive() {
			continue
		}
		q, err := truncQty(total.Decimal().Mul(w.Decimal()).Div(sum.Decimal()))
		if err != nil {
			return nil, err
		}
		out[i] = q
		given = given.Add(q)
	}
	out[last] = total.Sub(given)
	return out, nil
}
