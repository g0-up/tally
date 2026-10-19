package sales

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// maxTiles is how many quick-pick tiles the POS screen shows.
const maxTiles = 30

// tileWindowDays is how far back the POS sales that rank the tiles go.
const tileWindowDays = 30

// PosBootstrap is what the POS screen loads when it opens and caches for offline selling: the
// store's POS settings, the active payment methods, the open shift, and the best-selling SKUs
// with their walk-in price and stock per color.
type PosBootstrap struct {
	Settings PosSettings     `json:"settings"`
	Methods  []PaymentMethod `json:"payment_methods"`
	Shift    *Shift          `json:"shift"`
	Tiles    []PosTile       `json:"tiles"`
	// PricedAt is when the tile prices were read; an offline client shows them as of then.
	PricedAt time.Time `json:"priced_at"`
}

// PosSettings are the tenant settings the POS works by.
type PosSettings struct {
	PricesIncludeTax bool        `json:"prices_include_tax"`
	OfflineEnabled   bool        `json:"offline_pos_enabled"`
	OfflineMaxAmount money.Money `json:"offline_pos_max_amount"`
	OfflineMaxHours  int         `json:"offline_pos_max_hours"`
}

// PosTile is a quick-pick SKU.
type PosTile struct {
	SkuID        uuid.UUID `db:"id" json:"sku_id"`
	Code         string    `db:"code" json:"sku_code"`
	Name         string    `db:"display_name" json:"name"`
	Variant      string    `db:"variant_label" json:"variant_label"`
	BaseUnitID   uuid.UUID `db:"base_unit_id" json:"unit_id"`
	BaseUnitName string    `db:"unit_name" json:"unit_name"`
	// RetailPrice is nil when the retail list has no price for the SKU.
	RetailPrice *money.Money `db:"-" json:"retail_price"`
	Stock       []TileStock  `db:"-" json:"stock"`
}

// TileStock is the sellable on-hand quantity of one color of a tile; ColorID nil is the stock
// without a color.
type TileStock struct {
	ColorID *uuid.UUID `json:"color_id"`
	Qty     money.Qty  `json:"qty"`
}

// Bootstrap returns what the POS screen loads when it opens.
func (s *Service) Bootstrap(ctx context.Context, scope Scope) (PosBootstrap, error) {
	var out PosBootstrap
	err := s.read(ctx, scope, func(q db.Querier) error {
		st, err := loadSettings(ctx, q)
		if err != nil {
			return err
		}
		out.Settings = PosSettings{PricesIncludeTax: st.PricesIncludeTax, OfflineEnabled: st.OfflineEnabled,
			OfflineMaxAmount: st.OfflineMaxAmount, OfflineMaxHours: st.OfflineMaxHours}
		if out.Methods, err = s.methods.ActiveMethods(ctx, q); err != nil {
			return err
		}
		var sh Shift
		err = sqlx.GetContext(ctx, q, &sh, `SELECT `+shiftColumns+` FROM shifts WHERE status = 'open'`)
		switch {
		case err == nil:
			out.Shift = &sh
		case !db.IsNoRows(err):
			return wrap("read open shift", err)
		}
		today, err := s.today(ctx, q, scope.TenantID)
		if err != nil {
			return err
		}
		out.PricedAt = s.clock.Now()
		out.Tiles, err = s.tiles(ctx, q, scope.TenantID, today.AddDate(0, 0, -tileWindowDays))
		return err
	})
	return out, err
}

// tiles are the active goods, kits and published combos ranked by the quantity the POS sold of
// them since since, then by code, with their walk-in price in the base unit and their stock per
// color (none for a combo).
func (s *Service) tiles(ctx context.Context, q db.Querier, tenantID uuid.UUID, since time.Time) ([]PosTile, error) {
	out := []PosTile{}
	if err := sqlx.SelectContext(ctx, q, &out, `
		SELECT s.id, s.code, s.display_name, s.variant_label, s.base_unit_id, u.name AS unit_name
		FROM skus s
		JOIN products p ON p.id = s.product_id
		JOIN units u ON u.id = s.base_unit_id
		LEFT JOIN (
			SELECT l.sku_id, SUM(l.base_qty) AS sold
			FROM sales_document_lines l JOIN sales_documents d ON d.id = l.sales_document_id
			WHERE d.source IN ('pos', 'pos_offline') AND d.reversal_of IS NULL AND d.status <> 'reversed'
			  AND d.business_date >= $1 AND l.sku_id IS NOT NULL
			GROUP BY l.sku_id
		) t ON t.sku_id = s.id
		WHERE s.status = 'active' AND p.status = 'active'
		  AND (p.type = 'goods' OR (p.type IN ('kit', 'combo')
		       AND EXISTS (SELECT 1 FROM kit_recipes r WHERE r.product_id = p.id AND r.status = 'active')))
		ORDER BY COALESCE(t.sold, 0) DESC, s.code
		LIMIT $2`, since, maxTiles); err != nil {
		return nil, wrap("read pos tiles", err)
	}
	if len(out) == 0 {
		return out, nil
	}
	queries := make([]RetailQuery, len(out))
	skus := make([]uuid.UUID, len(out))
	for i, t := range out {
		unit := t.BaseUnitID
		queries[i] = RetailQuery{SkuID: t.SkuID, UnitID: &unit, Qty: money.NewQtyFromInt(1)}
		skus[i] = t.SkuID
	}
	prices, err := s.pricer.RetailPrices(ctx, q, tenantID, queries)
	if err != nil {
		return nil, err
	}
	if len(prices) != len(queries) {
		return nil, fmt.Errorf("sales: pricing returned %d retail prices for %d tiles", len(prices), len(queries))
	}
	stock, err := s.stock.OnHandByColor(ctx, q, sortedUnique(skus))
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].RetailPrice = prices[i]
		out[i].Stock = []TileStock{}
		for _, c := range stock[out[i].SkuID] {
			out[i].Stock = append(out[i].Stock, TileStock(c))
		}
	}
	return out, nil
}
