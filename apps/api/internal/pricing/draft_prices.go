package pricing

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// A draft price is a base retail or wholesale price of a product no one can sell yet (a draft kit
// or combo): the price row the resolver reads, so the draft's preview shows its margin, but no
// history until the product is published. A draft deleted before then leaves nothing behind in
// the append-only history.

// SetDraftRetailPrice sets the base retail price per base unit of skuID without recording history.
func (w PriceWriter) SetDraftRetailPrice(ctx context.Context, q db.Querier, actor audit.Actor, skuID uuid.UUID, price money.Money) error {
	return w.setDraftPrice(ctx, q, actor, KindRetail, skuID, price)
}

// SetDraftWholesalePrice sets the base wholesale price per base unit of skuID without recording
// history.
func (w PriceWriter) SetDraftWholesalePrice(ctx context.Context, q db.Querier, actor audit.Actor, skuID uuid.UUID, price money.Money) error {
	return w.setDraftPrice(ctx, q, actor, KindWholesale, skuID, price)
}

// setDraftPrice sets the price of skuID on the base list of kind.
func (w PriceWriter) setDraftPrice(ctx context.Context, q db.Querier, actor audit.Actor, kind string, skuID uuid.UUID, price money.Money) error {
	if price.IsNegative() {
		return errInvalid(map[string]string{"price": "negative"})
	}
	s := w.svc
	list, err := s.ensureBaseList(ctx, q, actor.TenantID, kind)
	if err != nil {
		return err
	}
	// The list row is locked before its item rows, as every price writer does.
	if _, err := readPriceList(ctx, q, list.ID, true); err != nil {
		return err
	}
	k := rowKey{ListID: list.ID, SkuID: skuID, MinQty: money.ZeroQty}
	row, found, err := lockRow(ctx, q, k)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	if found {
		if _, err := q.ExecContext(ctx, `UPDATE price_list_items SET price = $2, updated_at = $3 WHERE id = $1`,
			row.ID, price, now); err != nil {
			return fmt.Errorf("pricing: update draft price: %w", err)
		}
		return nil
	}
	if _, err := q.ExecContext(ctx, `
		INSERT INTO price_list_items (id, tenant_id, price_list_id, sku_id, color_id, unit_id, min_qty, price, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NULL, NULL, $5, $6, $7, $7)`,
		ids.New(), actor.TenantID, list.ID, skuID, k.MinQty, price, now); err != nil {
		return fmt.Errorf("pricing: insert draft price: %w", err)
	}
	return nil
}

// RecordDraftPrices starts the price history of skuID when its product is published: every price
// row of the SKU without history gets its first row, a price added from the catalog. Rows that
// already have history are left alone, so publishing again records nothing.
func (w PriceWriter) RecordDraftPrices(ctx context.Context, q db.Querier, actor audit.Actor, skuID uuid.UUID) error {
	var rows []priceRow
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT i.id, i.price_list_id, i.sku_id, i.color_id, i.unit_id, i.min_qty, i.price FROM price_list_items i
		WHERE i.sku_id = $1 AND NOT EXISTS (
		  SELECT 1 FROM price_changes c
		  WHERE c.price_list_id = i.price_list_id AND c.sku_id = i.sku_id AND c.color_id IS NOT DISTINCT FROM i.color_id
		    AND c.unit_id IS NOT DISTINCT FROM i.unit_id AND c.min_qty = i.min_qty)
		ORDER BY i.price_list_id, i.color_id NULLS FIRST, i.unit_id NULLS FIRST, i.min_qty
		FOR UPDATE OF i`, skuID); err != nil {
		return fmt.Errorf("pricing: read unrecorded prices: %w", err)
	}
	for _, r := range rows {
		price := r.Price
		if _, err := w.svc.recordChange(ctx, q, actor, r.key(), nil, &price, origin{Source: ChangeCatalog}); err != nil {
			return err
		}
	}
	return nil
}

// DeleteDraftPrices removes the price rows of skuID, a draft without price history that is being
// deleted. The caller checks there is no history: a SKU with history keeps its rows and product.
func (w PriceWriter) DeleteDraftPrices(ctx context.Context, q db.Querier, skuID uuid.UUID) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM price_list_items WHERE sku_id = $1`, skuID); err != nil {
		return fmt.Errorf("pricing: delete draft prices: %w", err)
	}
	return nil
}
