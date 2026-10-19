package procurement

import (
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
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
