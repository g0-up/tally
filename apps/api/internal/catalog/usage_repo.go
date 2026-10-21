package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// SQLUsageReader answers UsageReader with read-only queries on the tables of the modules that
// reference the catalog (inventory, sales, procurement, pricing, kits). The table lists below are
// every column of the schema that references a SKU, a SKU unit or a mix group.

// skuReferences are the "table.column" pairs that reference skus(id), besides the catalog's own
// sku_units, sku_barcodes, sku_variant_values and product_images.
var skuReferences = []string{
	"assembly_order_items.sku_id", "assembly_posting_items.sku_id", "cogs_adjustments.sku_id",
	"disassembly_lines.sku_id", "invoice_lines.sku_id", "kit_recipe_items.sku_id", "kit_recipe_mix_items.sku_id",
	"kit_recipes.output_sku_id", "lot_balances.sku_id", "lots.sku_id", "mix_group_items.sku_id",
	"pos_cart_lines.sku_id", "price_changes.sku_id", "price_list_items.sku_id", "promotion_items.sku_id",
	"purchase_order_lines.sku_id", "purchase_return_lines.sku_id", "quote_lines.sku_id", "receipt_lines.sku_id",
	"reservations.sku_id", "sales_document_lines.sku_id", "sales_return_lines.sku_id", "sku_stock.sku_id",
	"stock_adjustment_lines.sku_id", "stock_count_lines.sku_id", "stock_moves.sku_id", "stock_out_lines.sku_id",
}

// skuUnitReferences are the tables whose rows carry both sku_id and unit_id (a unit of a SKU).
var skuUnitReferences = []string{
	"invoice_lines", "kit_recipe_items", "pos_cart_lines", "price_changes", "price_list_items",
	"purchase_order_lines", "purchase_return_lines", "quote_lines", "receipt_lines", "sales_document_lines",
	"sales_return_lines", "stock_adjustment_lines",
}

// mixGroupReferences are the document line tables that carry mix_group_id, with the column of
// their document and the Vietnamese name of the document kind.
var mixGroupReferences = []struct{ table, docColumn, label string }{
	{"quote_lines", "quote_id", "báo giá"},
	{"invoice_lines", "invoice_id", "hóa đơn"},
	{"sales_document_lines", "sales_document_id", "chứng từ bán"},
	{"pos_cart_lines", "pos_cart_id", "giỏ hàng"},
	{"kit_recipe_items", "kit_recipe_id", "công thức giỏ"},
}

// SQLUsageReader is the UsageReader used until the referencing modules provide their own.
type SQLUsageReader struct{}

var _ UsageReader = SQLUsageReader{}

func exists(ctx context.Context, q db.Querier, query string, args ...any) (bool, error) {
	var found bool
	if err := q.QueryRowxContext(ctx, `SELECT EXISTS (`+query+`)`, args...).Scan(&found); err != nil {
		return false, fmt.Errorf("catalog: usage query: %w", err)
	}
	return found, nil
}

// AttributeInUse implements UsageReader.
func (SQLUsageReader) AttributeInUse(ctx context.Context, q db.Querier, attributeID uuid.UUID, code string) (bool, error) {
	return exists(ctx, q, `
		SELECT 1 FROM products WHERE attributes -> $2 IS NOT NULL
		UNION ALL SELECT 1 FROM sku_variant_values WHERE attribute_id = $1
		UNION ALL SELECT 1 FROM product_variant_axes WHERE attribute_id = $1`, attributeID, code)
}

// OptionInUse implements UsageReader.
func (SQLUsageReader) OptionInUse(ctx context.Context, q db.Querier, attributeCode string, optionID uuid.UUID) (bool, error) {
	return exists(ctx, q, `
		SELECT 1 FROM sku_variant_values WHERE option_id = $1
		UNION ALL SELECT 1 FROM products
		WHERE attributes @> jsonb_build_object($2::text, $1::text)
		   OR attributes @> jsonb_build_object($2::text, jsonb_build_array($1::text))`, optionID, attributeCode)
}

// SkusHaveStockMoves implements UsageReader.
func (SQLUsageReader) SkusHaveStockMoves(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (bool, error) {
	if len(skuIDs) == 0 {
		return false, nil
	}
	return exists(ctx, q, `SELECT 1 FROM stock_moves WHERE sku_id = ANY($1)`, skuIDs)
}

// SkuInUse implements UsageReader.
func (SQLUsageReader) SkuInUse(ctx context.Context, q db.Querier, skuID uuid.UUID) (bool, error) {
	parts := make([]string, len(skuReferences))
	for i, ref := range skuReferences {
		table, column, _ := strings.Cut(ref, ".")
		parts[i] = fmt.Sprintf("SELECT 1 FROM %s WHERE %s = $1", table, column)
	}
	return exists(ctx, q, strings.Join(parts, " UNION ALL "), skuID)
}

// SkuUnitInUse implements UsageReader.
func (SQLUsageReader) SkuUnitInUse(ctx context.Context, q db.Querier, skuID, unitID uuid.UUID) (bool, error) {
	parts := make([]string, len(skuUnitReferences))
	for i, table := range skuUnitReferences {
		parts[i] = fmt.Sprintf("SELECT 1 FROM %s WHERE sku_id = $1 AND unit_id = $2", table)
	}
	return exists(ctx, q, strings.Join(parts, " UNION ALL "), skuID, unitID)
}

// UnitLocks implements UsageReader. The document tables are skuUnitReferences without the price
// tables: a current price row alone does not lock a unit (deleting the unit deletes it), while its
// price_changes rows are history that stays. Each asked pair is checked on its own with EXISTS, in
// the order of the reasons, so a check stops at the first row it finds.
func (SQLUsageReader) UnitLocks(ctx context.Context, q db.Querier, units []SkuUnitKey) (map[SkuUnitKey]string, error) {
	out := map[SkuUnitKey]string{}
	if len(units) == 0 {
		return out, nil
	}
	skuIDs, unitIDs := make([]uuid.UUID, len(units)), make([]uuid.UUID, len(units))
	for i, u := range units {
		skuIDs[i], unitIDs[i] = u.SkuID, u.UnitID
	}
	onLine := func(table string) string {
		return fmt.Sprintf("EXISTS (SELECT 1 FROM %s t WHERE t.sku_id = k.sku_id AND t.unit_id = k.unit_id)", table)
	}
	docs := make([]string, 0, len(skuUnitReferences))
	for _, table := range skuUnitReferences {
		if table != "price_changes" && table != "price_list_items" {
			docs = append(docs, onLine(table))
		}
	}
	query := fmt.Sprintf(`SELECT sku_id, unit_id, reason FROM (
		SELECT k.sku_id, k.unit_id, CASE
			WHEN %s THEN '%s'
			WHEN %s THEN '%s'
			WHEN %s THEN '%s'
		END AS reason
		FROM unnest($1::uuid[], $2::uuid[]) AS k(sku_id, unit_id)) locks
		WHERE reason IS NOT NULL`,
		strings.Join(docs, " OR "), UnitLockedByDocuments,
		onLine("price_changes"), UnitLockedByPriceHistory,
		onLine("sku_barcodes"), UnitLockedByBarcodes)
	var found []struct {
		SkuID  uuid.UUID `db:"sku_id"`
		UnitID uuid.UUID `db:"unit_id"`
		Reason string    `db:"reason"`
	}
	if err := sqlx.SelectContext(ctx, q, &found, query, skuIDs, unitIDs); err != nil {
		return nil, fmt.Errorf("catalog: read unit locks: %w", err)
	}
	for _, f := range found {
		out[SkuUnitKey{SkuID: f.SkuID, UnitID: f.UnitID}] = f.Reason
	}
	return out, nil
}

// SkuOnHand implements UsageReader.
func (SQLUsageReader) SkuOnHand(ctx context.Context, q db.Querier, skuID uuid.UUID) (money.Qty, error) {
	var total money.Qty
	if err := q.QueryRowxContext(ctx, `SELECT COALESCE(sum(on_hand), 0) FROM sku_stock WHERE sku_id = $1`, skuID).Scan(&total); err != nil {
		return money.Qty{}, fmt.Errorf("catalog: read on hand: %w", err)
	}
	return total, nil
}

// MixGroupUsage implements UsageReader.
func (SQLUsageReader) MixGroupUsage(ctx context.Context, q db.Querier, mixGroupID uuid.UUID) ([]UsageCount, error) {
	var out []UsageCount
	for _, ref := range mixGroupReferences {
		var n int
		query := fmt.Sprintf("SELECT count(DISTINCT %s) FROM %s WHERE mix_group_id = $1", ref.docColumn, ref.table)
		if err := q.QueryRowxContext(ctx, query, mixGroupID).Scan(&n); err != nil {
			return nil, fmt.Errorf("catalog: count mix group usage: %w", err)
		}
		if n > 0 {
			out = append(out, UsageCount{Label: ref.label, Count: n})
		}
	}
	return out, nil
}

// MixGroupUsedSkus implements UsageReader.
func (SQLUsageReader) MixGroupUsedSkus(ctx context.Context, q db.Querier, mixGroupID uuid.UUID) ([]uuid.UUID, error) {
	parts := make([]string, 0, len(mixGroupReferences)+1)
	for _, ref := range mixGroupReferences {
		parts = append(parts, fmt.Sprintf("SELECT sku_id FROM %s WHERE mix_group_id = $1", ref.table))
	}
	parts = append(parts, `SELECT m.sku_id FROM kit_recipe_mix_items m
		JOIN kit_recipe_items i ON i.id = m.kit_recipe_item_id WHERE i.mix_group_id = $1`)
	var out []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &out, `SELECT DISTINCT sku_id FROM (`+strings.Join(parts, " UNION ALL ")+`) used ORDER BY sku_id`,
		mixGroupID); err != nil {
		return nil, fmt.Errorf("catalog: read mix group flavors in use: %w", err)
	}
	return out, nil
}

// CategoryInUse implements UsageReader.
func (SQLUsageReader) CategoryInUse(ctx context.Context, q db.Querier, categoryID uuid.UUID) (bool, error) {
	return exists(ctx, q, `
		SELECT 1 FROM products WHERE category_id = $1
		UNION ALL SELECT 1 FROM categories WHERE parent_id = $1
		UNION ALL SELECT 1 FROM stock_counts WHERE category_id = $1`, categoryID)
}

// ProductInUse implements UsageReader: a kit recipe of the product or a SKU in use.
func (r SQLUsageReader) ProductInUse(ctx context.Context, q db.Querier, productID uuid.UUID) (bool, error) {
	used, err := exists(ctx, q, `SELECT 1 FROM kit_recipes WHERE product_id = $1`, productID)
	if err != nil || used {
		return used, err
	}
	var skuIDs []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &skuIDs, `SELECT id FROM skus WHERE product_id = $1`, productID); err != nil {
		return false, fmt.Errorf("catalog: read skus of product: %w", err)
	}
	for _, id := range skuIDs {
		if used, err := r.SkuInUse(ctx, q, id); err != nil || used {
			return used, err
		}
	}
	return false, nil
}
