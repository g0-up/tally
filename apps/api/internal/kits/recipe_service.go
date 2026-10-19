package kits

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// KitInput creates a draft kit or combo: its catalog product with one SKU, and recipe version 1.
type KitInput struct {
	Type        string
	Name        string
	Description *string
	CategoryID  *uuid.UUID
	SkuCode     string
	BaseUnitID  uuid.UUID
	TaxRateID   *uuid.UUID
	Barcode     *string
	// RetailPrice and WholesalePrice are the kit's own selling prices per base unit.
	RetailPrice    *money.Money
	WholesalePrice *money.Money
	// MinStock is the stock of pre-packed kits below which the kit is restocked; a combo holds no
	// stock of its own.
	MinStock *money.Qty
	Recipe   RecipeInput
}

// KitPatch edits a kit. Name and description may change at any time; Recipe rewrites the latest
// version, which must still be a draft.
type KitPatch struct {
	Name           *string
	Description    *string
	DescriptionSet bool
	Recipe         *RecipeInput
}

// RecipeDetail is one recipe version with its items.
type RecipeDetail struct {
	Recipe
	Items []RecipeItem
}

// DeleteResult tells how a kit was removed: deleted, or archived (made inactive) when the price
// history of its SKU must be kept.
type DeleteResult struct {
	Archived bool
}

// Kits lists kits and combos by name.
func (s *Service) Kits(ctx context.Context, scope Scope, f KitFilter) ([]KitRow, error) {
	var out []KitRow
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = listKits(ctx, q, f)
		return err
	})
	return out, err
}

// Kit returns a kit with its latest recipe version.
func (s *Service) Kit(ctx context.Context, scope Scope, productID uuid.UUID) (Kit, error) {
	var out Kit
	err := s.read(ctx, scope, func(q db.Querier) (err error) {
		out, err = readKit(ctx, q, productID)
		return err
	})
	return out, err
}

// RecipeVersion returns one recipe version of a kit.
func (s *Service) RecipeVersion(ctx context.Context, scope Scope, productID uuid.UUID, version int) (RecipeDetail, error) {
	var out RecipeDetail
	err := s.read(ctx, scope, func(q db.Querier) error {
		r, err := recipeByVersion(ctx, q, productID, version)
		if err != nil {
			return err
		}
		out.Recipe = r
		out.Items, err = readItems(ctx, q, r.ID)
		return err
	})
	return out, err
}

// CreateKit saves a draft kit or combo. A draft is not sellable: it stays out of POS, invoices
// and the sellable search until it is published. With a key it runs once per Idempotency-Key: a
// retry of the same request returns the stored kit (replayed true) instead of a second one.
func (s *Service) CreateKit(ctx context.Context, actor audit.Actor, key *idempotency.Key, in KitInput) (Kit, bool, error) {
	fields := map[string]string{}
	if in.Type != TypeKit && in.Type != TypeCombo {
		fields["type"] = "invalid"
	}
	if in.BaseUnitID == uuid.Nil {
		fields["base_unit_id"] = "required"
	}
	if in.RetailPrice != nil && in.RetailPrice.IsNegative() {
		fields["retail_price"] = "must_not_be_negative"
	}
	if in.WholesalePrice != nil && in.WholesalePrice.IsNegative() {
		fields["wholesale_price"] = "must_not_be_negative"
	}
	switch {
	case in.MinStock == nil:
	case in.Type == TypeCombo:
		fields["min_stock"] = "not_allowed"
	case in.MinStock.IsNegative():
		fields["min_stock"] = "must_not_be_negative"
	}
	validateRecipeShape(fields, "recipe.", &in.Recipe)
	if len(fields) > 0 {
		return Kit{}, false, errInvalid(fields)
	}
	var out Kit
	var replayed bool
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ScopeKitCreate, key, http.StatusCreated, &out, func() error {
			productID, skuID, err := s.catalog.CreateProduct(ctx, q, actor, NewProduct{Type: in.Type, Name: in.Name,
				Description: in.Description, CategoryID: in.CategoryID, SkuCode: in.SkuCode, BaseUnitID: in.BaseUnitID,
				TaxRateID: in.TaxRateID, Barcode: in.Barcode, TrackLot: in.Type == TypeKit, MinStock: in.MinStock})
			if err != nil {
				return remapCatalog(err)
			}
			if in.RetailPrice != nil {
				if err := s.draftPrices.SetDraftRetailPrice(ctx, q, actor, skuID, *in.RetailPrice); err != nil {
					return err
				}
			}
			if in.WholesalePrice != nil {
				if err := s.draftPrices.SetDraftWholesalePrice(ctx, q, actor, skuID, *in.WholesalePrice); err != nil {
					return err
				}
			}
			items, _, err := buildRecipe(ctx, q, skuID, "recipe.", in.Recipe)
			if err != nil {
				return err
			}
			now := s.clock.Now()
			r := Recipe{ID: ids.New(), ProductID: productID, VersionNo: 1, Status: RecipeDraft, ValidFrom: in.Recipe.ValidFrom,
				ValidTo: in.Recipe.ValidTo, PackingCost: in.Recipe.PackingCost, OutputSkuID: skuID, Note: in.Recipe.Note,
				CreatedAt: now, UpdatedAt: now, Version: 1}
			if err := s.writeRecipe(ctx, q, actor.TenantID, r, items); err != nil {
				return err
			}
			if out, err = readKit(ctx, q, productID); err != nil {
				return err
			}
			return s.record(ctx, q, actor, ScopeKitCreate, "kit", productID, nil, out)
		})
		return err
	})
	return out, replayed, err
}

func (s *Service) writeRecipe(ctx context.Context, q db.Querier, tenantID uuid.UUID, r Recipe, items []RecipeItem) error {
	for i := range items {
		items[i].RecipeID = r.ID
	}
	if err := insertRecipe(ctx, q, tenantID, r); err != nil {
		return err
	}
	return insertItems(ctx, q, tenantID, items)
}

// remapCatalog names the fields of a catalog validation error as the kit request does: the SKU's
// "sku.code" is "sku_code", its other "sku.x" fields are "x".
func remapCatalog(err error) error {
	e, ok := apperr.As(err)
	if !ok || len(e.Fields) == 0 {
		return err
	}
	fields := make(map[string]string, len(e.Fields))
	for k, v := range e.Fields {
		switch {
		case k == "sku.code":
			k = "sku_code"
		case strings.HasPrefix(k, "sku."):
			k = strings.TrimPrefix(k, "sku.")
		}
		fields[k] = v
	}
	out := *e
	out.Fields = fields
	return out.WithCause(err)
}

// lockLatest locks the latest recipe of a kit and checks the caller's ETag against it.
func lockLatest(ctx context.Context, q db.Querier, productID uuid.UUID, expected int) (Recipe, error) {
	r, err := latestRecipe(ctx, q, productID, true)
	if err != nil {
		return Recipe{}, err
	}
	if r.Version != expected {
		return Recipe{}, apperr.NewPreconditionFailed("The kit was changed by someone else; reload it and try again")
	}
	return r, nil
}

// saveRecipe stores the header of recipe r guarded by its version and returns the new version.
func (s *Service) saveRecipe(ctx context.Context, q db.Querier, r Recipe) (int, error) {
	return db.UpdateWithVersion(ctx, q, "kit_recipes", r.ID, r.Version, s.clock.Now(),
		db.Set{Column: "status", Value: r.Status}, db.Set{Column: "valid_from", Value: r.ValidFrom},
		db.Set{Column: "valid_to", Value: r.ValidTo}, db.Set{Column: "packing_cost", Value: r.PackingCost},
		db.Set{Column: "note", Value: r.Note})
}

// UpdateKit edits a kit guarded by the version of its latest recipe (the kit's ETag).
func (s *Service) UpdateKit(ctx context.Context, actor audit.Actor, productID uuid.UUID, expected int, in KitPatch) (Kit, error) {
	fields := map[string]string{}
	if in.Recipe != nil {
		validateRecipeShape(fields, "recipe.", in.Recipe)
	}
	if len(fields) > 0 {
		return Kit{}, errInvalid(fields)
	}
	var out Kit
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		before, err := readKit(ctx, q, productID)
		if err != nil {
			return err
		}
		r, err := lockLatest(ctx, q, productID, expected)
		if err != nil {
			return err
		}
		if in.Name != nil || in.DescriptionSet {
			if err := s.catalog.UpdateProduct(ctx, q, actor, productID, ProductChange{Name: in.Name,
				Description: in.Description, DescriptionSet: in.DescriptionSet}); err != nil {
				return remapCatalog(err)
			}
		}
		if in.Recipe != nil {
			if r.Status != RecipeDraft {
				return errRecipeNotDraft()
			}
			items, _, err := buildRecipe(ctx, q, r.OutputSkuID, "recipe.", *in.Recipe)
			if err != nil {
				return err
			}
			if err := deleteItems(ctx, q, r.ID); err != nil {
				return err
			}
			for i := range items {
				items[i].RecipeID = r.ID
			}
			if err := insertItems(ctx, q, actor.TenantID, items); err != nil {
				return err
			}
			r.ValidFrom, r.ValidTo, r.PackingCost, r.Note = in.Recipe.ValidFrom, in.Recipe.ValidTo, in.Recipe.PackingCost, in.Recipe.Note
		}
		if _, err := s.saveRecipe(ctx, q, r); err != nil {
			return err
		}
		if out, err = readKit(ctx, q, productID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "kit.update", "kit", productID, before, out)
	})
	return out, err
}

// NewRecipeVersion adds a draft version after a published one. The kit keeps selling its active
// version until the new one is published.
func (s *Service) NewRecipeVersion(ctx context.Context, actor audit.Actor, productID uuid.UUID, expected int, in RecipeInput) (Kit, error) {
	fields := map[string]string{}
	validateRecipeShape(fields, "", &in)
	if len(fields) > 0 {
		return Kit{}, errInvalid(fields)
	}
	var out Kit
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		latest, err := lockLatest(ctx, q, productID, expected)
		if err != nil {
			return err
		}
		if latest.Status == RecipeDraft {
			return apperr.NewConflict(CodeDraftExists, "The kit already has a draft version; edit or publish it")
		}
		items, _, err := buildRecipe(ctx, q, latest.OutputSkuID, "", in)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		r := Recipe{ID: ids.New(), ProductID: productID, VersionNo: latest.VersionNo + 1, Status: RecipeDraft,
			ValidFrom: in.ValidFrom, ValidTo: in.ValidTo, PackingCost: in.PackingCost, OutputSkuID: latest.OutputSkuID,
			Note: in.Note, CreatedAt: now, UpdatedAt: now, Version: latest.Version + 1}
		if err := s.writeRecipe(ctx, q, actor.TenantID, r, items); err != nil {
			return err
		}
		if out, err = readKit(ctx, q, productID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "kit_recipe.create", "kit_recipe", r.ID, nil, RecipeDetail{Recipe: r, Items: out.Items})
	})
	return out, err
}

// PublishKit activates the latest (draft) version: the active version is superseded, the product
// becomes sellable and the price history of its draft price starts. A key replays the first
// result of the request it identifies, before the version check.
func (s *Service) PublishKit(ctx context.Context, actor audit.Actor, key *idempotency.Key, productID uuid.UUID, expected int) (Kit, bool, error) {
	var out Kit
	var replayed bool
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) (err error) {
		replayed, err = idempotency.Once(ctx, q, s.idem, actor.TenantID, ScopeKitPublish, key, http.StatusOK, &out, func() error {
			before, err := readKit(ctx, q, productID)
			if err != nil {
				return err
			}
			r, err := lockLatest(ctx, q, productID, expected)
			if err != nil {
				return err
			}
			if r.Status != RecipeDraft {
				return errRecipeNotDraft()
			}
			if err := checkPublishable(ctx, q, r.ID); err != nil {
				return err
			}
			active, found, err := activeRecipe(ctx, q, productID)
			if err != nil {
				return err
			}
			if found {
				active.Status = RecipeSuperseded
				if _, err := s.saveRecipe(ctx, q, active); err != nil {
					return err
				}
			}
			r.Status = RecipeActive
			if _, err := s.saveRecipe(ctx, q, r); err != nil {
				return err
			}
			if before.Status != ProductActive {
				status := ProductActive
				if err := s.catalog.UpdateProduct(ctx, q, actor, productID, ProductChange{Status: &status}); err != nil {
					return err
				}
			}
			if err := s.draftPrices.RecordDraftPrices(ctx, q, actor, before.SkuID); err != nil {
				return err
			}
			if out, err = readKit(ctx, q, productID); err != nil {
				return err
			}
			return s.record(ctx, q, actor, ScopeKitPublish, "kit", productID, before, out)
		})
		return err
	})
	return out, replayed, err
}

// checkPublishable refuses to publish a recipe one of whose components was deactivated since it
// was saved: a SKU, a mix group, or a flavor of a mix pack, which must also still be goods.
func checkPublishable(ctx context.Context, q db.Querier, recipeID uuid.UUID) error {
	var bad []struct {
		LineNo int    `db:"line_no"`
		Field  string `db:"field"`
	}
	if err := sqlx.SelectContext(ctx, q, &bad, `
		SELECT i.line_no, 'sku_id' AS field FROM kit_recipe_items i
		JOIN skus s ON s.id = i.sku_id JOIN products p ON p.id = s.product_id
		WHERE i.kit_recipe_id = $1 AND (s.status <> 'active' OR p.status <> 'active')
		UNION
		SELECT i.line_no, 'mix_group_id' FROM kit_recipe_items i JOIN mix_groups g ON g.id = i.mix_group_id
		WHERE i.kit_recipe_id = $1 AND g.status <> 'active'
		UNION
		SELECT i.line_no, 'flavors' FROM kit_recipe_items i
		JOIN kit_recipe_mix_items m ON m.kit_recipe_item_id = i.id
		JOIN skus s ON s.id = m.sku_id JOIN products p ON p.id = s.product_id
		WHERE i.kit_recipe_id = $1 AND (s.status <> 'active' OR p.status <> 'active' OR p.type <> 'goods')
		ORDER BY 1, 2`, recipeID); err != nil {
		return fmt.Errorf("kits: check recipe components: %w", err)
	}
	if len(bad) == 0 {
		return nil
	}
	fields := make(map[string]string, len(bad))
	for _, b := range bad {
		fields[itemField("", b.LineNo-1, b.Field)] = "inactive"
	}
	return errInvalid(fields)
}

// DeleteKit removes a draft kit that was never sold, with its draft prices. A kit whose SKU has
// price history (its price was edited on a product screen) is archived instead: the history is
// append-only.
func (s *Service) DeleteKit(ctx context.Context, actor audit.Actor, productID uuid.UUID, expected int) (DeleteResult, error) {
	var out DeleteResult
	err := s.db.WithTenantTx(ctx, actor.TenantID, func(q db.Querier) error {
		k, err := readKit(ctx, q, productID)
		if err != nil {
			return err
		}
		if _, err := lockLatest(ctx, q, productID, expected); err != nil {
			return err
		}
		switch {
		case k.ActiveVersion != nil || k.Status == ProductActive:
			return apperr.NewConflict(CodeKitPublished, "A published kit cannot be deleted; make it inactive instead")
		case k.Sold:
			return apperr.NewConflict(CodeKitSold, "The kit was sold or assembled and cannot be deleted")
		}
		var recorded bool
		if err := q.QueryRowxContext(ctx, `SELECT EXISTS (SELECT 1 FROM price_changes WHERE sku_id = $1)`,
			k.SkuID).Scan(&recorded); err != nil {
			return fmt.Errorf("kits: read kit price history: %w", err)
		}
		if recorded {
			if k.Status != ProductInactive {
				status := ProductInactive
				if err := s.catalog.UpdateProduct(ctx, q, actor, productID, ProductChange{Status: &status}); err != nil {
					return err
				}
			}
			out.Archived = true
			return s.record(ctx, q, actor, "kit.archive", "kit", productID, k, nil)
		}
		if err := s.draftPrices.DeleteDraftPrices(ctx, q, k.SkuID); err != nil {
			return err
		}
		var recipeIDs []uuid.UUID
		if err := sqlx.SelectContext(ctx, q, &recipeIDs, `SELECT id FROM kit_recipes WHERE product_id = $1`, productID); err != nil {
			return fmt.Errorf("kits: list kit recipes: %w", err)
		}
		for _, id := range recipeIDs {
			if err := deleteItems(ctx, q, id); err != nil {
				return err
			}
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM kit_recipes WHERE product_id = $1`, productID); err != nil {
			return fmt.Errorf("kits: delete kit recipes: %w", err)
		}
		if err := s.catalog.DeleteProduct(ctx, q, actor, productID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "kit.delete", "kit", productID, k, nil)
	})
	return out, err
}

// KitPreview is the economics of a kit's latest version: the card a draft is reviewed on.
func (s *Service) KitPreview(ctx context.Context, scope Scope, productID uuid.UUID) (CostView, error) {
	var out CostView
	err := s.read(ctx, scope, func(q db.Querier) error {
		k, err := readKit(ctx, q, productID)
		if err != nil {
			return err
		}
		comps, err := readComponents(ctx, q, k.Latest.ID)
		if err != nil {
			return err
		}
		kitPrice, err := s.kitPrice(ctx, q, scope.TenantID, k.SkuID, k.BaseUnitID)
		if err != nil {
			return err
		}
		out, err = s.costView(ctx, q, scope.TenantID, k.Items, comps, k.Latest.PackingCost, kitPrice)
		return err
	})
	return out, err
}

// PreviewRecipe is the economics of an unsaved recipe; kitPrice is the price the kit would sell
// at, nil to leave the margin out.
func (s *Service) PreviewRecipe(ctx context.Context, scope Scope, in RecipeInput, kitPrice *money.Money) (CostView, error) {
	fields := map[string]string{}
	validateRecipeShape(fields, "recipe.", &in)
	if kitPrice != nil && kitPrice.IsNegative() {
		fields["kit_price"] = "must_not_be_negative"
	}
	if len(fields) > 0 {
		return CostView{}, errInvalid(fields)
	}
	var out CostView
	err := s.read(ctx, scope, func(q db.Querier) error {
		items, comps, err := buildRecipe(ctx, q, uuid.Nil, "recipe.", in)
		if err != nil {
			return err
		}
		out, err = s.costView(ctx, q, scope.TenantID, items, comps, in.PackingCost, kitPrice)
		return err
	})
	return out, err
}

// kitPrice is the retail price of one kit, nil when it has none.
func (s *Service) kitPrice(ctx context.Context, q db.Querier, tenantID, skuID, unitID uuid.UUID) (*money.Money, error) {
	prices, err := s.prices.RetailPrices(ctx, q, tenantID, []PriceQuery{{SkuID: skuID, UnitID: unitID, Qty: money.NewQtyFromInt(1)}})
	if err != nil {
		return nil, err
	}
	return prices[0], nil
}

// componentStock is what the cost view reads of a component SKU.
type componentStock struct {
	SkuID      uuid.UUID   `db:"sku_id"`
	Code       string      `db:"code"`
	Name       string      `db:"name"`
	BaseUnitID uuid.UUID   `db:"base_unit_id"`
	UnitCost   money.Money `db:"unit_cost"`
	Sellable   money.Qty   `db:"sellable"`
}

// readComponentStock reads the code, cost and free sellable stock of SKUs: the cost is the moving
// average (the reference cost before any stock), the free stock is sellable less reserved.
func readComponentStock(ctx context.Context, q db.Querier, skuIDs []uuid.UUID) (map[uuid.UUID]componentStock, error) {
	var rows []componentStock
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT s.id AS sku_id, s.code, s.display_name AS name, s.base_unit_id,
		       COALESCE(NULLIF(st.avg_cost, 0), s.ref_cost, 0) AS unit_cost,
		       GREATEST(COALESCE(st.sellable - st.reserved, 0), 0) AS sellable
		FROM skus s
		LEFT JOIN LATERAL (SELECT sum(on_hand) AS on_hand, sum(sellable) AS sellable, sum(reserved) AS reserved,
		                          CASE WHEN sum(on_hand) > 0 THEN round(sum(stock_value) / sum(on_hand), 4) ELSE 0 END AS avg_cost
		                   FROM sku_stock WHERE sku_id = s.id) st ON true
		WHERE s.id = ANY($1)`, skuIDs); err != nil {
		return nil, fmt.Errorf("kits: read component stock: %w", err)
	}
	out := make(map[uuid.UUID]componentStock, len(rows))
	for _, r := range rows {
		out[r.SkuID] = r
	}
	return out, nil
}

// readColorStock reads the sellable stock of each (SKU, color) pair: unexpired, unquarantined lots
// of that color.
func readColorStock(ctx context.Context, q db.Querier, today time.Time, skuIDs []uuid.UUID) (map[[2]uuid.UUID]money.Qty, error) {
	var rows []struct {
		SkuID   uuid.UUID `db:"sku_id"`
		ColorID uuid.UUID `db:"color_id"`
		Qty     money.Qty `db:"qty"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT b.sku_id, b.color_id, sum(b.qty) AS qty
		FROM lot_balances b JOIN lots l ON l.id = b.lot_id
		WHERE b.sku_id = ANY($1) AND b.color_id IS NOT NULL AND b.qty > 0 AND NOT l.is_quarantine
		  AND (l.expiry_date IS NULL OR l.expiry_date >= $2::date)
		GROUP BY b.sku_id, b.color_id`, skuIDs, today); err != nil {
		return nil, fmt.Errorf("kits: read color stock: %w", err)
	}
	out := make(map[[2]uuid.UUID]money.Qty, len(rows))
	for _, r := range rows {
		out[[2]uuid.UUID{r.SkuID, r.ColorID}] = r.Qty
	}
	return out, nil
}

// costView builds the component table of a recipe: one row per SKU item and per mix pack flavor
// with its retail price, cost and the kits its free stock covers.
func (s *Service) costView(ctx context.Context, q db.Querier, tenantID uuid.UUID, items []RecipeItem, comps []Component,
	packing money.Money, kitPrice *money.Money) (CostView, error) {
	skuIDs := make([]uuid.UUID, len(comps))
	for i, c := range comps {
		skuIDs[i] = c.SkuID
	}
	stock, err := readComponentStock(ctx, q, skuIDs)
	if err != nil {
		return CostView{}, err
	}
	today, err := s.todayIn(ctx, q)
	if err != nil {
		return CostView{}, err
	}
	colors, err := readColorStock(ctx, q, today, skuIDs)
	if err != nil {
		return CostView{}, err
	}
	queries := make([]PriceQuery, len(comps))
	for i, c := range comps {
		queries[i] = PriceQuery{SkuID: c.SkuID, ColorID: c.ColorID, UnitID: stock[c.SkuID].BaseUnitID, Qty: c.BaseQty}
	}
	prices := make([]*money.Money, len(comps))
	if len(queries) > 0 {
		if prices, err = s.prices.RetailPrices(ctx, q, tenantID, queries); err != nil {
			return CostView{}, err
		}
	}
	byItem := make(map[uuid.UUID]RecipeItem, len(items))
	for _, it := range items {
		byItem[it.ID] = it
	}
	view := CostView{ComponentsSale: money.ZeroMoney, ComponentsCost: money.ZeroMoney, PackingCost: packing, KitPrice: kitPrice}
	var buildable *money.Qty
	for i, c := range comps {
		st := stock[c.SkuID]
		it := byItem[c.ItemID]
		row := ComponentCost{ItemID: c.ItemID, LineNo: c.LineNo, Kind: c.Kind, SkuID: c.SkuID, SkuCode: st.Code, Name: st.Name,
			UnitID: it.UnitID, Qty: it.Qty, BaseQty: c.BaseQty, ColorID: c.ColorID, MixGroupID: it.MixGroupID,
			UnitPrice: prices[i], SaleAmount: money.ZeroMoney, UnitCost: st.UnitCost, Cost: st.UnitCost.MulQty(c.BaseQty),
			Sellable: st.Sellable}
		if it.MixGroupID != nil {
			row.UnitID, row.Qty = st.BaseUnitID, c.BaseQty
		}
		if c.ColorID != nil {
			colorQty := colors[[2]uuid.UUID{c.SkuID, *c.ColorID}]
			if colorQty.Cmp(row.Sellable) < 0 {
				row.Sellable = colorQty
			}
		}
		if row.UnitPrice != nil {
			row.SaleAmount = row.UnitPrice.MulQty(c.BaseQty)
		}
		row.Buildable = kitsCovered(row.Sellable, c.BaseQty)
		if buildable == nil || row.Buildable.Cmp(*buildable) < 0 {
			b := row.Buildable
			buildable = &b
		}
		view.ComponentsSale = view.ComponentsSale.Add(row.SaleAmount)
		view.ComponentsCost = view.ComponentsCost.Add(row.Cost)
		view.Components = append(view.Components, row)
	}
	view.TotalCost = view.ComponentsCost.Add(packing)
	if buildable != nil {
		view.Buildable = *buildable
	}
	if kitPrice != nil {
		m := kitPrice.Sub(view.TotalCost)
		view.Margin = &m
	}
	return view, nil
}

// kitsCovered is how many whole kits stock covers at perKit each.
func kitsCovered(stock, perKit money.Qty) money.Qty {
	if !perKit.IsPositive() || !stock.IsPositive() {
		return money.ZeroQty
	}
	n, err := decQty(stock.Decimal().Div(perKit.Decimal()).Floor())
	if err != nil {
		return money.ZeroQty
	}
	return n
}
