package catalog

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// quickCodePrefix numbers the SKUs created from a purchase order line without a code ("SP-12").
const quickCodePrefix = "SP"

// QuickCreateInput creates a goods product with one SKU from a purchase order line. Units are the
// SKU's conversion units with their own prices; instead of Units, a single purchase unit can be
// given with Factor, how many base units it holds (required when it is not the base unit). With
// Units, PurchaseUnitID alone picks the default purchase unit among the base unit and Units.
// PurchasePrice is per default purchase unit and becomes the reference cost per base unit;
// SalePrice and WholesalePrice are the retail and wholesale prices per base unit; TrackLot tracks
// lots and expiry dates.
type QuickCreateInput struct {
	Name           string
	Code           *string
	CategoryID     *uuid.UUID
	BaseUnitID     uuid.UUID
	PurchaseUnitID *uuid.UUID
	Factor         *string
	Units          []UnitPriceInput
	PurchasePrice  *money.Money
	SalePrice      *money.Money
	WholesalePrice *money.Money
	Barcode        *string
	TrackLot       bool
	// PriceSource names the screen for the price history; empty means the catalog.
	PriceSource string
}

// QuickCreateResult is the created product; SalePriceSaved is false when a sale price was given
// but no price writer is wired yet.
type QuickCreateResult struct {
	Product        ProductDetail
	SalePriceSaved bool
}

// QuickCreateProduct creates the product, its SKU, its conversion units and the optional sale
// prices in one transaction. The product gets no attribute set (the owner completes it later),
// so required attributes never block a purchase order.
func (s *Service) QuickCreateProduct(ctx context.Context, actor audit.Actor, in QuickCreateInput) (QuickCreateResult, error) {
	f := fieldErrors{}
	f.text("name", &in.Name, true, maxNameLen)
	if f["name"] == "required" {
		return QuickCreateResult{}, errCodeAndNameRequired(f)
	}
	sku := SkuInput{BaseUnitID: in.BaseUnitID, Barcode: in.Barcode, TrackLot: in.TrackLot, TrackExpiry: in.TrackLot}
	if in.Code != nil {
		sku.Code = *in.Code
	}
	if in.PurchasePrice != nil && in.PurchasePrice.IsNegative() {
		f["purchase_price"] = "negative"
	}
	if in.SalePrice != nil && in.SalePrice.IsNegative() {
		f["sale_price"] = "negative"
	}
	if in.WholesalePrice != nil && in.WholesalePrice.IsNegative() {
		f["wholesale_price"] = "negative"
	}
	sku.normaliseFields(f, "")
	if sku.Code = strings.TrimSpace(sku.Code); sku.Code != "" && !skuCodeFormat.MatchString(sku.Code) {
		f["code"] = "invalid_format"
	}
	plan, err := planUnits(f, "", in.BaseUnitID, in.PurchaseUnitID, in.Factor, in.Units)
	if err := f.err(); err != nil {
		return QuickCreateResult{}, err
	}
	if err != nil {
		return QuickCreateResult{}, err
	}
	if in.PurchasePrice != nil {
		cost, err := money.NewMoney(in.PurchasePrice.Decimal().DivRound(plan.purchaseFactor().Decimal(), money.MoneyScale).String())
		if err != nil {
			return QuickCreateResult{}, errInvalid(map[string]string{"purchase_price": "out_of_range"})
		}
		sku.RefCost = &cost
	}
	var res QuickCreateResult
	err = s.write(ctx, actor, func(q db.Querier) error {
		taken, err := exists(ctx, q, `SELECT 1 FROM products WHERE name_key = $1`, nameKey(in.Name))
		if err != nil {
			return err
		}
		if taken {
			return errProductNameTaken()
		}
		if in.CategoryID != nil {
			if _, err := readCategory(ctx, q, *in.CategoryID); apperrNotFound(err) {
				return errInvalid(map[string]string{"category_id": "unknown_category"})
			} else if err != nil {
				return err
			}
		}
		if sku.Code == "" {
			var codes []string
			if err := sqlx.SelectContext(ctx, q, &codes, `SELECT code FROM skus WHERE upper(code) LIKE $1 || '-%'`, quickCodePrefix); err != nil {
				return err
			}
			sku.Code = NewSkuCodeAllocator(quickCodePrefix, codes).Next()
		}
		now := s.clock.Now()
		p := Product{ID: ids.New(), Type: ProductGoods, Name: in.Name, NameKey: nameKey(in.Name), CategoryID: in.CategoryID,
			Attributes: JSONObject("{}"), Status: StatusActive, CreatedAt: now, UpdatedAt: now, Version: 1}
		if err := insertProduct(ctx, q, actor.TenantID, p); err != nil {
			return err
		}
		created, err := s.insertSku(ctx, q, actor.TenantID, p, SkuDraft{}, sku, "")
		if err != nil {
			return err
		}
		if err := addUnits(ctx, q, actor.TenantID, created.ID, plan, now); err != nil {
			return err
		}
		if res.SalePriceSaved, err = s.setNewSkuPrices(ctx, q, actor, created.ID, in.BaseUnitID, in.SalePrice, in.WholesalePrice,
			plan, in.PriceSource); err != nil {
			return err
		}
		if res.Product, err = s.readProductDetail(ctx, q, p.ID); err != nil {
			return err
		}
		return s.record(ctx, q, actor, "product.quick_create", "product", p.ID, nil, res.Product)
	})
	return res, err
}

// QuickCreateProductIn is QuickCreateProduct in the caller's tenant transaction q: a purchase
// document creates the products of its new lines together with the document.
func (s *Service) QuickCreateProductIn(ctx context.Context, q db.Querier, actor audit.Actor, in QuickCreateInput) (QuickCreateResult, error) {
	return s.within(q).QuickCreateProduct(ctx, actor, in)
}

// setNewSkuPrices stores the given retail and wholesale prices of a new SKU: per base unit, then
// the own prices of its conversion units. It reports false when no price was given or no price
// writer is wired.
func (s *Service) setNewSkuPrices(ctx context.Context, q db.Querier, actor audit.Actor, skuID, baseUnitID uuid.UUID,
	retail, wholesale *money.Money, plan unitPlan, source string) (bool, error) {
	saved, err := s.setUnitPrices(ctx, q, actor, skuID, baseUnitID, retail, wholesale, source)
	if err != nil {
		return false, err
	}
	for _, u := range plan.units {
		ok, err := s.setUnitPrices(ctx, q, actor, skuID, u.UnitID, u.RetailPrice, u.WholesalePrice, source)
		if err != nil {
			return false, err
		}
		saved = saved || ok
	}
	return saved, nil
}

// setUnitPrices stores the given retail and wholesale prices per unitID of a SKU. It reports
// false when no price was given or no price writer is wired.
func (s *Service) setUnitPrices(ctx context.Context, q db.Querier, actor audit.Actor, skuID, unitID uuid.UUID,
	retail, wholesale *money.Money, source string) (bool, error) {
	if retail == nil && wholesale == nil {
		return false, nil
	}
	if s.prices == nil {
		return false, nil
	}
	for _, p := range []struct {
		kind  string
		price *money.Money
	}{{PriceRetail, retail}, {PriceWholesale, wholesale}} {
		if p.price == nil {
			continue
		}
		if err := s.prices.SetSalePrice(ctx, q, actor, SalePrice{SkuID: skuID, UnitID: unitID, Kind: p.kind, Price: p.price,
			Source: source}); err != nil {
			return false, err
		}
	}
	return true, nil
}

// UnitPriceInput is a conversion unit of a new SKU holding Factor base units, with its own retail
// and wholesale prices per unit; a nil price sells the unit at base price × factor.
type UnitPriceInput struct {
	UnitID         uuid.UUID
	Factor         *string
	RetailPrice    *money.Money
	WholesalePrice *money.Money
}

// newUnit is a checked conversion unit of a new SKU; field names its unit_id in errors.
type newUnit struct {
	UnitID         uuid.UUID
	Factor         money.Factor
	RetailPrice    *money.Money
	WholesalePrice *money.Money
	field          string
}

// unitPlan is the conversion units a new SKU gets and its default purchase unit; uuid.Nil keeps
// the base unit as the default purchase unit.
type unitPlan struct {
	units    []newUnit
	purchase uuid.UUID
}

// purchaseFactor is how many base units the default purchase unit holds.
func (p unitPlan) purchaseFactor() money.Factor {
	for _, u := range p.units {
		if u.UnitID == p.purchase {
			return u.Factor
		}
	}
	return money.FactorOne
}

// maxUnitRows caps the units of one SKU a request may write: each row costs a unit write and two
// price writes under the store's base price list lock.
const maxUnitRows = 20

// planUnits checks the conversion units of a new SKU against its base unit. Without units it is
// the single purchase unit with its factor, whose errors it returns as before. Units go into f
// under prefix ("sku.units[1].factor"): each unit is given once, is not the base unit and has a
// positive factor; units exclude a factor. purchaseUnitID, with units, must be the base unit or
// one of them and becomes the default purchase unit; without it the unit with the largest factor
// above 1 is, as on the product form.
func planUnits(f fieldErrors, prefix string, baseUnitID uuid.UUID, purchaseUnitID *uuid.UUID, rawFactor *string,
	units []UnitPriceInput) (unitPlan, error) {
	if len(units) == 0 {
		unitID, factor, err := purchaseUnitOf(baseUnitID, purchaseUnitID, rawFactor)
		if err != nil || unitID == uuid.Nil {
			return unitPlan{}, err
		}
		return unitPlan{units: []newUnit{{UnitID: unitID, Factor: factor, field: "purchase_unit_id"}}, purchase: unitID}, nil
	}
	if len(units) > maxUnitRows {
		f[prefix+"units"] = "too_many"
		return unitPlan{}, nil
	}
	if rawFactor != nil {
		f[prefix+"units"] = "with_purchase_unit"
	}
	plan := unitPlan{units: make([]newUnit, 0, len(units))}
	seen := make(map[uuid.UUID]bool, len(units))
	biggest := money.FactorOne.Decimal()
	for i, in := range units {
		at := fmt.Sprintf("%sunits[%d].", prefix, i)
		switch {
		case in.UnitID == uuid.Nil:
			f[at+"unit_id"] = "required"
		case in.UnitID == baseUnitID:
			f[at+"unit_id"] = "is_base"
		case seen[in.UnitID]:
			f[at+"unit_id"] = "duplicate"
		}
		seen[in.UnitID] = true
		factor, reason := factorOf(in.Factor)
		if reason != "" {
			f[at+"factor"] = reason
		}
		checkPrices(f, at, in.RetailPrice, in.WholesalePrice)
		u := newUnit{UnitID: in.UnitID, Factor: factor, RetailPrice: in.RetailPrice, WholesalePrice: in.WholesalePrice,
			field: at + "unit_id"}
		plan.units = append(plan.units, u)
		if reason == "" && factor.Decimal().GreaterThan(biggest) {
			biggest, plan.purchase = factor.Decimal(), in.UnitID
		}
	}
	if purchaseUnitID != nil {
		plan.purchase = uuid.Nil
		switch {
		case *purchaseUnitID == baseUnitID:
		case seen[*purchaseUnitID]:
			plan.purchase = *purchaseUnitID
		default:
			f[prefix+"purchase_unit_id"] = "not_a_unit"
		}
	}
	return plan, nil
}

// factorOf parses a required factor and returns the field reason when it is not valid.
func factorOf(raw *string) (money.Factor, string) {
	if raw == nil {
		return money.Factor{}, "required"
	}
	factor, err := parseFactor(*raw)
	if err == nil {
		return factor, ""
	}
	if e, ok := apperr.As(err); ok {
		if reason, ok := e.Fields["factor"]; ok {
			return money.Factor{}, reason
		}
	}
	return money.Factor{}, "invalid"
}

// checkPrices refuses negative unit prices under prefix.
func checkPrices(f fieldErrors, prefix string, retail, wholesale *money.Money) {
	if retail != nil && retail.IsNegative() {
		f[prefix+"retail_price"] = "negative"
	}
	if wholesale != nil && wholesale.IsNegative() {
		f[prefix+"wholesale_price"] = "negative"
	}
}

// addUnits adds the planned conversion units to a new SKU; each unit must be active in the store.
func addUnits(ctx context.Context, q db.Querier, tenantID, skuID uuid.UUID, plan unitPlan, now time.Time) error {
	if len(plan.units) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(plan.units))
	for i, u := range plan.units {
		ids[i] = u.UnitID
	}
	var active []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &active, `SELECT id FROM units WHERE id = ANY($1) AND active`, ids); err != nil {
		return fmt.Errorf("catalog: read units: %w", err)
	}
	f := fieldErrors{}
	for _, u := range plan.units {
		if !slices.Contains(active, u.UnitID) {
			f[u.field] = "unknown_unit"
		}
	}
	if err := f.err(); err != nil {
		return err
	}
	if plan.purchase != uuid.Nil {
		if err := clearDefaults(ctx, q, skuID, false, true); err != nil {
			return err
		}
	}
	for _, u := range plan.units {
		if err := insertSkuUnit(ctx, q, tenantID, SkuUnit{SkuID: skuID, UnitID: u.UnitID, Factor: u.Factor, Active: true,
			IsDefaultPurchase: u.UnitID == plan.purchase}, now); err != nil {
			return err
		}
	}
	return nil
}

// purchaseUnitOf validates an optional purchase unit and its factor against the base unit and
// returns the unit to add, or uuid.Nil when the SKU buys in its base unit.
func purchaseUnitOf(baseUnitID uuid.UUID, unitID *uuid.UUID, rawFactor *string) (uuid.UUID, money.Factor, error) {
	if unitID == nil || *unitID == baseUnitID {
		return uuid.Nil, money.FactorOne, nil
	}
	if rawFactor == nil {
		return uuid.Nil, money.Factor{}, errInvalid(map[string]string{"factor": "required"})
	}
	factor, err := parseFactor(*rawFactor)
	if err != nil {
		return uuid.Nil, money.Factor{}, err
	}
	return *unitID, factor, nil
}
