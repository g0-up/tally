package procurement

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// NewProduct is a product typed by name on a purchase line that the catalog does not have yet.
// The document creates it with one SKU in its own transaction, so cancelling the document leaves
// no product behind. Factor is how many base units one purchase unit holds; the prices are per
// base unit.
type NewProduct struct {
	Name           string
	CategoryID     *uuid.UUID
	BaseUnitID     uuid.UUID
	PurchaseUnitID *uuid.UUID
	Factor         *string
	TrackLot       bool
	RetailPrice    *money.Money
	WholesalePrice *money.Money
}

// NewProductInput is a NewProduct with the price typed on its line per purchase unit, which
// becomes the reference cost.
type NewProductInput struct {
	NewProduct
	PurchasePrice *money.Money
}

// lineUnit is the unit a line of the new product is counted in: its purchase unit, else its base
// unit.
func (p NewProduct) lineUnit() uuid.UUID {
	if p.PurchaseUnitID != nil {
		return *p.PurchaseUnitID
	}
	return p.BaseUnitID
}

// validateNewProduct checks the shape of the new product of request line i; the catalog checks
// the rest (name, units, factor, duplicate names).
func validateNewProduct(fields map[string]string, i int, skuID uuid.UUID, unitID *uuid.UUID, p *NewProduct) {
	if p == nil {
		if skuID == uuid.Nil {
			fields[lineField(i, "sku_id")] = "required"
		}
		return
	}
	if skuID != uuid.Nil {
		fields[lineField(i, "new_product")] = "not_allowed_with_sku_id"
	}
	if strings.TrimSpace(p.Name) == "" {
		fields[lineField(i, "new_product.name")] = "required"
	}
	if p.BaseUnitID == uuid.Nil {
		fields[lineField(i, "new_product.base_unit_id")] = "required"
	}
	if unitID != nil && *unitID != p.BaseUnitID && *unitID != p.lineUnit() {
		fields[lineField(i, "unit_id")] = "not_new_product_unit"
	}
	for name, v := range map[string]*money.Money{"retail_price": p.RetailPrice, "wholesale_price": p.WholesalePrice} {
		if v != nil && v.IsNegative() {
			fields[lineField(i, "new_product."+name)] = "must_not_be_negative"
		}
	}
}

// createProduct creates the new product of request line i and returns its SKU and the line unit.
func (s *Service) createProduct(ctx context.Context, q db.Querier, actor audit.Actor, i int, p NewProduct,
	unitID *uuid.UUID, price *money.Money) (uuid.UUID, uuid.UUID, error) {
	if s.catalog == nil {
		return uuid.Nil, uuid.Nil, errInvalid(map[string]string{lineField(i, "new_product"): "not_supported"})
	}
	unit := p.lineUnit()
	if unitID != nil {
		unit = *unitID
	}
	// The reference cost is per purchase unit; a line counted in another unit leaves it unset.
	if unit != p.lineUnit() {
		price = nil
	}
	skuID, err := s.catalog.CreateProduct(ctx, q, actor, NewProductInput{NewProduct: p, PurchasePrice: price})
	if err != nil {
		return uuid.Nil, uuid.Nil, newProductError(err, i)
	}
	return skuID, unit, nil
}

// newProductError points an error of the catalog at the new product of request line i: its fields
// move under lines.i.new_product, and an error without fields (a duplicate name) lands on the name.
func newProductError(err error, i int) error {
	e, ok := apperr.As(err)
	if !ok {
		return err
	}
	prefix := lineField(i, "new_product.")
	fields := make(map[string]string, len(e.Fields)+1)
	for k, v := range e.Fields {
		fields[prefix+k] = v
	}
	if len(fields) == 0 {
		fields[prefix+"name"] = e.Code
	}
	out := *e
	out.Fields = fields
	return out.WithCause(err)
}
