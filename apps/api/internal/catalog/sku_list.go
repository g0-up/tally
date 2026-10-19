package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// maxSkuIDs bounds the id filter of Skus, the page size limit.
const maxSkuIDs = 200

// SkuFilter narrows Skus. AfterCode/AfterID are the keyset position.
type SkuFilter struct {
	Q          string
	ProductID  *uuid.UUID
	CategoryID *uuid.UUID
	Type       string
	Status     string
	// IDs, when not empty, lists only these SKUs.
	IDs       []uuid.UUID
	AfterCode *string
	AfterID   uuid.UUID
	Limit     int // 0 lists all
}

// SkuListItem is a SKU as the product lists show it: its product's type and category, its units
// and first barcode, the base sale prices (general color, base unit, no tier), what the store
// holds of it over every warehouse, its average cost and the unit price of its latest receipt per
// base unit. The figures are read from the pricing, inventory and procurement tables the way
// SQLUsageReader reads them.
type SkuListItem struct {
	Sku
	ProductType       string       `db:"product_type"`
	CategoryID        *uuid.UUID   `db:"category_id"`
	CategoryName      *string      `db:"category_name"`
	Barcode           *string      `db:"barcode"`
	RetailPrice       *money.Money `db:"retail_price"`
	WholesalePrice    *money.Money `db:"wholesale_price"`
	HasColorPrices    bool         `db:"has_color_prices"`
	OnHand            money.Qty    `db:"on_hand"`
	Sellable          money.Qty    `db:"sellable"`
	AvgCost           *money.Money `db:"avg_cost"`
	LastPurchasePrice *money.Money `db:"last_purchase_price"`
	InMixGroup        bool         `db:"in_mix_group"`
	Units             []SkuUnit
	// OnHandByColor is empty until the inventory module is wired.
	OnHandByColor []ColorQty
}

// skuListSelect reads a page of SkuListItem; the base price lists are the single retail and
// wholesale lists of the store.
const skuListSelect = `
	SELECT s.id, s.product_id, s.code, s.variant_key, s.display_name, s.variant_label, s.base_unit_id, s.qty_scale,
	       s.track_lot, s.track_expiry, s.ref_cost, s.min_price, s.min_stock, s.tax_rate_id, s.spec_text, s.status,
	       s.search_norm, s.created_at, s.updated_at, s.version,
	       p.type AS product_type, p.category_id, c.name AS category_name,
	       (SELECT b.code FROM sku_barcodes b WHERE b.sku_id = s.id ORDER BY b.created_at, b.code LIMIT 1) AS barcode,
	       (SELECT i.price FROM price_list_items i JOIN price_lists l ON l.id = i.price_list_id
	        WHERE l.kind = 'retail' AND i.sku_id = s.id AND i.color_id IS NULL AND i.unit_id IS NULL AND i.min_qty = 0) AS retail_price,
	       (SELECT i.price FROM price_list_items i JOIN price_lists l ON l.id = i.price_list_id
	        WHERE l.kind = 'wholesale' AND i.sku_id = s.id AND i.color_id IS NULL AND i.unit_id IS NULL AND i.min_qty = 0) AS wholesale_price,
	       EXISTS (SELECT 1 FROM price_list_items i JOIN price_lists l ON l.id = i.price_list_id
	        WHERE l.kind IN ('retail', 'wholesale') AND i.sku_id = s.id AND i.color_id IS NOT NULL) AS has_color_prices,
	       COALESCE(st.on_hand, 0) AS on_hand, COALESCE(st.sellable, 0) AS sellable, st.avg_cost,
	       (SELECT round(rl.unit_price / rl.unit_factor_snap, 4) FROM receipt_lines rl JOIN receipts r ON r.id = rl.receipt_id
	        WHERE rl.sku_id = s.id AND r.reversal_of IS NULL AND r.status <> 'reversed' AND rl.condition <> 'wrong_item'
	        ORDER BY r.posted_at DESC, rl.line_no DESC LIMIT 1) AS last_purchase_price,
	       EXISTS (SELECT 1 FROM mix_group_items m WHERE m.sku_id = s.id AND m.active) AS in_mix_group
	FROM skus s
	JOIN products p ON p.id = s.product_id
	LEFT JOIN categories c ON c.id = p.category_id
	LEFT JOIN LATERAL (
	  SELECT SUM(k.on_hand) AS on_hand, SUM(k.sellable) AS sellable,
	         CASE WHEN SUM(k.on_hand) > 0 THEN round(SUM(k.stock_value) / SUM(k.on_hand), 4) ELSE MAX(k.avg_cost) END AS avg_cost
	  FROM sku_stock k WHERE k.sku_id = s.id) st ON true`

// Skus lists SKUs by code with the figures of the product lists.
func (s *Service) Skus(ctx context.Context, scope Scope, f SkuFilter) ([]SkuListItem, error) {
	fields := fieldErrors{}
	if f.Status != "" && f.Status != StatusActive && f.Status != StatusInactive {
		fields["status"] = "invalid"
	}
	if f.Type != "" && !validProductType(f.Type) {
		fields["type"] = "invalid"
	}
	if len(f.Q) > maxSearchTermLen*4 {
		fields["q"] = "too_long"
	}
	if len(f.IDs) > maxSkuIDs {
		fields["id"] = "too_long"
	}
	if err := fields.err(); err != nil {
		return nil, err
	}
	words := strings.Fields(textnorm.SearchNorm(f.Q))
	patterns := make([]string, len(words))
	for i, w := range words {
		patterns[i] = "%" + w + "%"
	}
	afterCode := ""
	if f.AfterCode != nil {
		afterCode = *f.AfterCode
	}
	ids := f.IDs
	if ids == nil {
		ids = []uuid.UUID{}
	}
	out := []SkuListItem{}
	err := s.read(ctx, scope, func(q db.Querier) error {
		if err := sqlx.SelectContext(ctx, q, &out, skuListSelect+`
			WHERE ($1::uuid IS NULL OR s.product_id = $1)
			  AND ($2 = '' OR s.status = $2)
			  AND s.search_norm LIKE ALL ($3::text[])
			  AND ($4::uuid IS NULL OR p.category_id = $4)
			  AND ($5 = '' OR p.type = $5)
			  AND (NOT $6 OR (s.code, s.id) > ($7, $8::uuid))
			  AND (cardinality($10::uuid[]) = 0 OR s.id = ANY($10::uuid[]))
			ORDER BY s.code, s.id
			LIMIT NULLIF($9, 0)`, f.ProductID, f.Status, patterns, f.CategoryID, f.Type, f.AfterCode != nil, afterCode, f.AfterID, f.Limit,
			ids); err != nil {
			return fmt.Errorf("catalog: list skus: %w", err)
		}
		return s.fillSkuList(ctx, q, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// fillSkuList adds the units and the per-color stock of each listed SKU.
func (s *Service) fillSkuList(ctx context.Context, q db.Querier, items []SkuListItem) error {
	if len(items) == 0 {
		return nil
	}
	skuIDs := make([]uuid.UUID, len(items))
	byID := make(map[uuid.UUID]*SkuListItem, len(items))
	for i := range items {
		skuIDs[i] = items[i].ID
		items[i].Units, items[i].OnHandByColor = []SkuUnit{}, []ColorQty{}
		byID[items[i].ID] = &items[i]
	}
	var units []SkuUnit
	if err := sqlx.SelectContext(ctx, q, &units, skuUnitSelect+` WHERE su.sku_id = ANY($1) ORDER BY su.is_base DESC, su.factor, u.name`, skuIDs); err != nil {
		return fmt.Errorf("catalog: read sku units: %w", err)
	}
	for _, u := range units {
		byID[u.SkuID].Units = append(byID[u.SkuID].Units, u)
	}
	if s.stock == nil {
		return nil
	}
	colors, err := s.stock.OnHandByColor(ctx, q, skuIDs)
	if err != nil {
		return err
	}
	for id, qty := range colors {
		if it, ok := byID[id]; ok {
			it.OnHandByColor = qty
		}
	}
	return nil
}
