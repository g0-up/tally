package procurement

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestValidateNewProduct(t *testing.T) {
	base, carton, other := uuid.New(), uuid.New(), uuid.New()
	negative := money.MustMoney("-1")
	cases := []struct {
		name   string
		sku    uuid.UUID
		unit   *uuid.UUID
		p      *NewProduct
		fields map[string]string
	}{
		{"neither sku nor product", uuid.Nil, nil, nil, map[string]string{"lines.2.sku_id": "required"}},
		{"existing sku", uuid.New(), nil, nil, map[string]string{}},
		{"product counted in its purchase unit", uuid.Nil, &carton,
			&NewProduct{Name: "Bia", BaseUnitID: base, PurchaseUnitID: &carton}, map[string]string{}},
		{"both sku and product", uuid.New(), nil, &NewProduct{Name: "Bia", BaseUnitID: base},
			map[string]string{"lines.2.new_product": "not_allowed_with_sku_id"}},
		{"blank product", uuid.Nil, &other, &NewProduct{Name: "  ", RetailPrice: &negative},
			map[string]string{"lines.2.new_product.name": "required", "lines.2.new_product.base_unit_id": "required",
				"lines.2.unit_id": "not_new_product_unit", "lines.2.new_product.retail_price": "must_not_be_negative"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fields := map[string]string{}
			validateNewProduct(fields, 2, c.sku, c.unit, c.p)
			wantFields(t, fields, c.fields)
		})
	}
}

func TestNewProductErrorPointsAtTheLine(t *testing.T) {
	e := wantCode(t, newProductError(errInvalid(map[string]string{"factor": "required"}), 1), apperr.Validation, "validation_failed")
	wantFields(t, e.Fields, map[string]string{"lines.1.new_product.factor": "required"})

	e = wantCode(t, newProductError(apperr.NewConflict("product_name_taken", "taken"), 0), apperr.Conflict, "product_name_taken")
	wantFields(t, e.Fields, map[string]string{"lines.0.new_product.name": "product_name_taken"})

	e = wantCode(t, newProductError(apperr.NewConflict("barcode_taken", "taken"), 3), apperr.Conflict, "barcode_taken")
	wantFields(t, e.Fields, map[string]string{"lines.3.new_product.barcode": "barcode_taken"})
}

// fakeCatalog records the new products it creates and refuses barcodes with a space or longer
// than 64 characters, as the catalog does.
type fakeCatalog struct {
	sku     uuid.UUID
	created []NewProductInput
}

func (c *fakeCatalog) CreateProduct(_ context.Context, _ db.Querier, _ audit.Actor, p NewProductInput) (uuid.UUID, error) {
	c.created = append(c.created, p)
	return c.sku, nil
}

func (c *fakeCatalog) ValidateBarcode(code string) error {
	switch {
	case len(code) > 64:
		return errInvalid(map[string]string{"code": "too_long"})
	case strings.ContainsRune(code, ' '):
		return errInvalid(map[string]string{"code": "invalid_format"})
	}
	return nil
}

func TestCreateProductPassesTheBarcode(t *testing.T) {
	base := uuid.New()
	cases := []struct {
		name, barcode string
		want          *string
	}{
		{"trimmed", "  8935001234567 ", ptr("8935001234567")},
		{"blank means none", "   ", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cat := &fakeCatalog{sku: uuid.New()}
			s := &Service{catalog: cat}
			sku, unit, err := s.createProduct(context.Background(), nil, audit.Actor{}, 0,
				NewProduct{Name: "Sữa hộp", BaseUnitID: base, Barcode: &c.barcode}, nil, nil)
			if err != nil || sku != cat.sku || unit != base {
				t.Fatalf("createProduct = %v, %v, %v", sku, unit, err)
			}
			got := cat.created[0].Barcode
			if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
				t.Errorf("barcode sent to the catalog = %v, want %v", got, c.want)
			}
		})
	}

	t.Run("a code the catalog refuses lands on the line's barcode", func(t *testing.T) {
		cat := &fakeCatalog{sku: uuid.New()}
		s := &Service{catalog: cat}
		_, _, err := s.createProduct(context.Background(), nil, audit.Actor{}, 2,
			NewProduct{Name: "Sữa hộp", BaseUnitID: base, Barcode: ptr("89 35")}, nil, nil)
		e := wantCode(t, err, apperr.Validation, "validation_failed")
		wantFields(t, e.Fields, map[string]string{"lines.2.new_product.barcode": "invalid_format"})
		if len(cat.created) != 0 {
			t.Errorf("created = %+v, want nothing", cat.created)
		}
	})
}

func TestNewProductLineUnit(t *testing.T) {
	base, carton := uuid.New(), uuid.New()
	if got := (NewProduct{BaseUnitID: base}).lineUnit(); got != base {
		t.Errorf("without purchase unit = %v, want base", got)
	}
	if got := (NewProduct{BaseUnitID: base, PurchaseUnitID: &carton}).lineUnit(); got != carton {
		t.Errorf("with purchase unit = %v, want carton", got)
	}
}
