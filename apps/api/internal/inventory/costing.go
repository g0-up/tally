package inventory

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// CostingMethod values stock movements of one SKU. The state it receives is the SKU's
// projection before the movement; it returns the signed value delta of the move (positive for
// receipts) and the unit cost recorded on it.
type CostingMethod interface {
	// OnReceive values qty received at unitCost.
	OnReceive(s SkuStock, qty money.Qty, unitCost money.Money) (money.Money, error)
	// OnIssue values qty leaving the stock; value is the positive amount taken out.
	OnIssue(s SkuStock, qty money.Qty) (unitCost, value money.Money, err error)
	// OnCostAdjust checks a cost-only delta on the remaining stock.
	OnCostAdjust(s SkuStock, delta money.Money) error
	// Average is the unit cost of the SKU after a movement left it at s.
	Average(s SkuStock) (money.Money, error)
}

// MovingAverageCosting is the costing of Tally 1.1: one average cost per SKU (not per color or
// lot), recomputed as stock_value / on_hand after receipts and cost adjustments; issues take the
// current average, and the issue that empties the stock takes the whole remaining value, so
// rounding residue never stays behind a zero quantity.
type MovingAverageCosting struct{}

var _ CostingMethod = MovingAverageCosting{}

// OnReceive implements CostingMethod: value = round(qty × unit cost, 4).
func (MovingAverageCosting) OnReceive(_ SkuStock, qty money.Qty, unitCost money.Money) (money.Money, error) {
	if unitCost.IsNegative() {
		return money.Money{}, errInvalid(map[string]string{"unit_cost": "negative"})
	}
	return unitCost.MulQty(qty), nil
}

// OnIssue implements CostingMethod: value = qty × avg_cost, never more than the stock value,
// and exactly the stock value when the issue takes the whole on-hand quantity.
func (MovingAverageCosting) OnIssue(s SkuStock, qty money.Qty) (money.Money, money.Money, error) {
	if qty.Cmp(s.OnHand) >= 0 {
		return s.AvgCost, s.StockValue, nil
	}
	value := s.AvgCost.MulQty(qty)
	if value.Cmp(s.StockValue) > 0 {
		value = s.StockValue
	}
	return s.AvgCost, value, nil
}

// OnCostAdjust implements CostingMethod: a cost delta needs stock to carry it and may not take
// the stock value below zero.
func (MovingAverageCosting) OnCostAdjust(s SkuStock, delta money.Money) error {
	if !s.OnHand.IsPositive() {
		return apperr.NewConflict("no_stock_to_adjust", "There is no stock left to carry a cost adjustment")
	}
	if s.StockValue.Add(delta).IsNegative() {
		return apperr.NewConflict("stock_value_negative", "The cost adjustment would make the stock value negative")
	}
	return nil
}

// Average implements CostingMethod: stock_value / on_hand at 4 decimal places; the last average
// is kept while the stock is empty, so the next issue preview still shows a cost.
func (MovingAverageCosting) Average(s SkuStock) (money.Money, error) {
	if !s.OnHand.IsPositive() {
		return s.AvgCost, nil
	}
	if s.StockValue.IsNegative() {
		return money.Money{}, fmt.Errorf("inventory: negative stock value %s", s.StockValue)
	}
	return moneyOf(s.StockValue.Decimal().DivRound(s.OnHand.Decimal(), money.MoneyScale))
}

// moneyOf converts a decimal already rounded to the money scale.
func moneyOf(d decimal.Decimal) (money.Money, error) {
	m, err := money.NewMoney(d.Round(money.MoneyScale).String())
	if err != nil {
		return money.Money{}, fmt.Errorf("inventory: amount %s: %w", d.String(), err)
	}
	return m, nil
}

// minQty returns the smaller quantity.
func minQty(a, b money.Qty) money.Qty {
	if a.Cmp(b) <= 0 {
		return a
	}
	return b
}
