package sales

import (
	"testing"

	"github.com/google/uuid"
)

func TestValidateComponentColors(t *testing.T) {
	sku, red := uuid.New(), uuid.New()
	fields := map[string]string{}
	validateComponentColors(fields, 2, []ComponentColor{
		{SkuID: sku, ColorID: red},
		{SkuID: uuid.New(), ColorID: uuid.New()},
	})
	if len(fields) > 0 {
		t.Fatalf("fields = %v", fields)
	}

	validateComponentColors(fields, 2, []ComponentColor{{}, {SkuID: sku, ColorID: red}, {SkuID: sku, ColorID: red}})
	wantFields(t, errInvalid(fields), map[string]string{
		"lines.2.component_colors.0.sku_id": "required", "lines.2.component_colors.0.color_id": "required",
		"lines.2.component_colors.2.sku_id": "duplicate",
	})

	fields = map[string]string{}
	many := make([]ComponentColor, maxComponentColors+1)
	for k := range many {
		many[k] = ComponentColor{SkuID: uuid.New(), ColorID: red}
	}
	validateComponentColors(fields, 0, many)
	if fields["lines.0.component_colors"] != "too_many" {
		t.Errorf("fields = %v, want too_many", fields)
	}
}

func TestChooseComponentColorsFillsOnlyOpenComponents(t *testing.T) {
	ids := newIDs(5)
	ribbon, candy, stranger, red, blue := ids[0], ids[1], ids[2], ids[3], ids[4]
	parts := func() []ComboLine {
		return []ComboLine{
			{SkuID: ribbon, ColorID: &red, BaseQty: qty("1")},
			{SkuID: candy, BaseQty: qty("2")},
		}
	}

	t.Run("the counter's color goes to the open component", func(t *testing.T) {
		p := parts()
		// The fixed red ribbon is only repeated, as a stored combo copied back lists it.
		if err := chooseComponentColors(p, []ComponentColor{{SkuID: ribbon, ColorID: red}, {SkuID: candy, ColorID: blue}}, 1); err != nil {
			t.Fatal(err)
		}
		if *p[0].ColorID != red || p[1].ColorID == nil || *p[1].ColorID != blue {
			t.Errorf("colors = %v, %v", p[0].ColorID, p[1].ColorID)
		}
	})

	t.Run("no choice leaves the component open", func(t *testing.T) {
		p := parts()
		if err := chooseComponentColors(p, nil, 0); err != nil || p[1].ColorID != nil {
			t.Errorf("color = %v, err = %v", p[1].ColorID, err)
		}
	})

	t.Run("refused choices", func(t *testing.T) {
		err := chooseComponentColors(parts(), []ComponentColor{
			{SkuID: stranger, ColorID: red},
			{SkuID: ribbon, ColorID: blue},
			{SkuID: candy, ColorID: red},
			{SkuID: candy, ColorID: blue},
		}, 3)
		wantFields(t, err, map[string]string{
			"lines.3.component_colors.0.sku_id": "not_a_component",
			"lines.3.component_colors.1.sku_id": "not_a_component",
			"lines.3.component_colors.3.sku_id": "duplicate",
		})
	})
}

func TestDraftOfCopiesTheComponentColorsOfACombo(t *testing.T) {
	ids := newIDs(6)
	combo, comboSku, unit, ribbon, candy, red := ids[0], ids[1], ids[2], ids[3], ids[4], ids[5]
	lines := []Line{
		{ID: combo, Kind: KindCombo, SkuID: &comboSku, UnitID: unit, Qty: qty("2"), Discount: vnd("0")},
		{ID: uuid.New(), Kind: KindComboComponent, ParentLineID: &combo, SkuID: &ribbon, ColorID: &red, UnitID: unit, Qty: qty("2")},
		{ID: uuid.New(), Kind: KindComboComponent, ParentLineID: &combo, SkuID: &candy, UnitID: unit, Qty: qty("4")},
	}
	got := draftOf(lines)
	if len(got) != 1 {
		t.Fatalf("draft = %+v, want only the combo line", got)
	}
	in := got[0]
	if in.SkuID == nil || *in.SkuID != comboSku || !in.Qty.Equal(qty("2")) || in.UnitPrice != nil {
		t.Errorf("combo line = %+v", in)
	}
	if len(in.ComponentColors) != 1 || in.ComponentColors[0] != (ComponentColor{SkuID: ribbon, ColorID: red}) {
		t.Errorf("component colors = %+v", in.ComponentColors)
	}
	fields := map[string]string{}
	validateComponentColors(fields, 0, in.ComponentColors)
	if len(fields) > 0 {
		t.Errorf("the copied colors do not validate: %v", fields)
	}
}

func newIDs(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}
