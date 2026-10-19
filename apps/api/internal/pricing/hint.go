package pricing

import (
	"context"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// hintResolver prices a picker hint from the base list alone: a walk-in customer, no party,
// group or season. Base lists carry no validity dates, so the hint never needs the tenant's time
// zone and works in a read-only support transaction too.
var hintResolver = PriceResolver{chain: []ListSource{BaseListSource()}}

// PriceHints returns the retail base-list price of one base unit, general color, for each SKU
// that has one, inside the caller's tenant transaction. The SKU search shows it as price_hint.
func (s *Service) PriceHints(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID]money.Money, error) {
	out := make(map[uuid.UUID]money.Money, len(skuIDs))
	ids := uniqueIDs(skuIDs)
	if len(ids) == 0 {
		return out, nil
	}
	skus, err := loadSkuInfo(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	lines := make([]pricedLine, 0, len(skus))
	for _, id := range ids {
		sku, ok := skus[id]
		if !ok {
			continue
		}
		one := money.NewQtyFromInt(1)
		lines = append(lines, pricedLine{
			Index: len(lines), Sku: sku, UnitID: sku.BaseUnitID, IsBase: true, Factor: money.FactorOne, Qty: one, BaseQty: one,
		})
	}
	if len(lines) == 0 {
		return out, nil
	}
	prices, err := hintResolver.resolve(ctx, q, buyer{BaseKind: KindRetail}, clock.TruncateDate(s.clock.Now()), lines)
	if err != nil {
		return nil, err
	}
	for i, p := range prices {
		if p.Source.Kind != SourceManual {
			out[lines[i].Sku.ID] = p.Amount
		}
	}
	return out, nil
}
