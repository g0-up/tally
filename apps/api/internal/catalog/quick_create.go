package catalog

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// quickCodePrefix numbers the SKUs created from a purchase order line without a code ("SP-12").
const quickCodePrefix = "SP"

// QuickCreateInput creates a goods product with one SKU from a purchase order line. Factor is how
// many base units one purchase unit holds (required when the purchase unit is not the base unit);
// PurchasePrice is per purchase unit and becomes the reference cost per base unit; SalePrice and
// WholesalePrice are the retail and wholesale prices per base unit; TrackLot tracks lots and
// expiry dates.
type QuickCreateInput struct {
	Name           string
	Code           *string
	CategoryID     *uuid.UUID
	BaseUnitID     uuid.UUID
	PurchaseUnitID *uuid.UUID
	Factor         *string
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

// QuickCreateProduct creates the product, its SKU, the purchase unit conversion and the optional
// sale price in one transaction. The product gets no attribute set (the owner completes it later),
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
	if err := f.err(); err != nil {
		return QuickCreateResult{}, err
	}
	purchaseUnit, factor, err := purchaseUnitOf(in.BaseUnitID, in.PurchaseUnitID, in.Factor)
	if err != nil {
		return QuickCreateResult{}, err
	}
	if in.PurchasePrice != nil {
		cost, err := money.NewMoney(in.PurchasePrice.Decimal().DivRound(factor.Decimal(), money.MoneyScale).String())
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
		if purchaseUnit != uuid.Nil {
			if err := addPurchaseUnit(ctx, q, actor.TenantID, created.ID, purchaseUnit, factor, now); err != nil {
				return err
			}
		}
		if res.SalePriceSaved, err = s.setBasePrices(ctx, q, actor, created.ID, in.BaseUnitID, in.SalePrice, in.WholesalePrice,
			in.PriceSource); err != nil {
			return err
		}
		if res.Product, err = readProductDetail(ctx, q, p.ID); err != nil {
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

// setBasePrices stores the given retail and wholesale prices per base unit of a new SKU. It
// reports false when a price was given but no price writer is wired.
func (s *Service) setBasePrices(ctx context.Context, q db.Querier, actor audit.Actor, skuID, baseUnitID uuid.UUID,
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
		if err := s.prices.SetSalePrice(ctx, q, actor, SalePrice{SkuID: skuID, UnitID: baseUnitID, Kind: p.kind, Price: *p.price,
			Source: source}); err != nil {
			return false, err
		}
	}
	return true, nil
}

// addPurchaseUnit adds unitID as the SKU's default purchase unit of factor base units.
func addPurchaseUnit(ctx context.Context, q db.Querier, tenantID, skuID, unitID uuid.UUID, factor money.Factor, now time.Time) error {
	u, err := readUnit(ctx, q, unitID)
	if apperrNotFound(err) || (err == nil && !u.Active) {
		return errInvalid(map[string]string{"purchase_unit_id": "unknown_unit"})
	}
	if err != nil {
		return err
	}
	if err := clearDefaults(ctx, q, skuID, false, true); err != nil {
		return err
	}
	return insertSkuUnit(ctx, q, tenantID, SkuUnit{SkuID: skuID, UnitID: unitID, Factor: factor, Active: true,
		IsDefaultPurchase: true}, now)
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
