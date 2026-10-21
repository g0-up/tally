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
// no product behind. Units are its conversion units with their own prices; without Units, Factor
// is how many base units the single purchase unit holds. With Units, PurchaseUnitID is the base
// unit or one of Units and Factor stays empty. The prices are per base unit; Barcode, when given,
// becomes a barcode of the base unit.
type NewProduct struct {
	Name           string
	CategoryID     *uuid.UUID
	BaseUnitID     uuid.UUID
	PurchaseUnitID *uuid.UUID
	Factor         *string
	Units          []NewProductUnit
	TrackLot       bool
	RetailPrice    *money.Money
	WholesalePrice *money.Money
	Barcode        *string
}

// NewProductUnit is a conversion unit of a new product holding Factor base units, with its own
// prices per unit; a nil price sells it at base price × factor.
type NewProductUnit struct {
	UnitID         uuid.UUID
	Factor         *string
	RetailPrice    *money.Money
	WholesalePrice *money.Money
}

// codeBarcodeTaken is the catalog's conflict for a barcode another SKU of the store holds.
const codeBarcodeTaken = "barcode_taken"

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

// hasUnit reports whether a line of the new product may be counted in unitID: its base unit, its
// purchase unit or one of its units.
func (p NewProduct) hasUnit(unitID uuid.UUID) bool {
	if unitID == p.BaseUnitID || unitID == p.lineUnit() {
		return true
	}
	for _, u := range p.Units {
		if u.UnitID == unitID {
			return true
		}
	}
	return false
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
	if unitID != nil && !p.hasUnit(*unitID) {
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
	// With a unit table and no purchase unit, the catalog would default to the largest unit; the
	// line's unit becomes the purchase unit instead, so the line price is the cost of that unit.
	if p.PurchaseUnitID == nil && len(p.Units) > 0 {
		p.PurchaseUnitID = &unit
	}
	// The reference cost is per purchase unit; a line counted in another unit leaves it unset.
	if unit != p.lineUnit() {
		price = nil
	}
	if err := s.checkBarcode(&p, i); err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	skuID, err := s.catalog.CreateProduct(ctx, q, actor, NewProductInput{NewProduct: p, PurchasePrice: price})
	if err != nil {
		return uuid.Nil, uuid.Nil, newProductError(err, i)
	}
	return skuID, unit, nil
}

// checkBarcode trims the barcode of the new product of request line i, drops a blank one and
// checks the rest against the catalog's barcode rules.
func (s *Service) checkBarcode(p *NewProduct, i int) error {
	if p.Barcode == nil {
		return nil
	}
	code := strings.TrimSpace(*p.Barcode)
	if code == "" {
		p.Barcode = nil
		return nil
	}
	if err := s.catalog.ValidateBarcode(code); err != nil {
		e, ok := apperr.As(err)
		if !ok {
			return err
		}
		// The catalog names one field (the code) with the reason.
		reason := e.Code
		for _, v := range e.Fields {
			reason = v
		}
		return errInvalid(map[string]string{lineField(i, "new_product.barcode"): reason})
	}
	p.Barcode = &code
	return nil
}

// newProductError points an error of the catalog at the new product of request line i: its fields
// move under lines.i.new_product; a barcode taken by another SKU lands on the barcode and any other
// error without fields (a duplicate name) on the name.
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
	switch {
	case len(fields) > 0:
	case e.Code == codeBarcodeTaken:
		fields[prefix+"barcode"] = e.Code
	default:
		fields[prefix+"name"] = e.Code
	}
	out := *e
	out.Fields = fields
	return out.WithCause(err)
}
