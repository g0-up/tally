package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if !apperr.Is(err, code) {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func wantField(t *testing.T, err error, field, reason string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Fields[field] != reason {
		t.Fatalf("error = %v (fields %v), want %s=%s", err, fieldsOf(err), field, reason)
	}
}

func fieldsOf(err error) map[string]string {
	if e, ok := apperr.As(err); ok {
		return e.Fields
	}
	return nil
}

func wantMessage(t *testing.T, err error, code, message string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code || e.Message != message {
		t.Fatalf("error = %v, want %s %q", err, code, message)
	}
}

func selectAttr(code string, labels ...string) Attribute {
	a := Attribute{ID: ids.New(), Code: code, Name: code, DataType: DataSelect, Status: StatusActive}
	for i, l := range labels {
		a.Options = append(a.Options, AttributeOption{ID: ids.New(), AttributeID: a.ID, Label: l, SortOrder: i, Active: true})
	}
	return a
}

func TestValueValidatorsPerType(t *testing.T) {
	sel := selectAttr("flavor", "Dâu", "Cam")
	inactive := sel.Options[1].ID
	sel.Options[1].Active = false
	active := sel.Options[0].ID
	q := func(s string) string { return `"` + s + `"` }
	cases := []struct {
		typ      DataType
		raw      string
		previous string
		want     string // JSON of the normalised value
		reason   string
	}{
		{typ: DataText, raw: `"  Kinh Đô  "`, want: `"Kinh Đô"`},
		{typ: DataText, raw: `12`, reason: "invalid_type"},
		{typ: DataText, raw: `"a\u0007b"`, reason: "invalid_text"},
		{typ: DataText, raw: q(strings.Repeat("a", MaxTextValueLen+1)), reason: "too_long"},
		{typ: DataNumber, raw: `500`, want: `500`},
		{typ: DataNumber, raw: `-1.250000`, want: `-1.25`},
		{typ: DataNumber, raw: `"500"`, reason: "invalid_type"},
		{typ: DataNumber, raw: `1e12`, reason: "out_of_range"},
		{typ: DataNumber, raw: `0.1234567`, reason: "too_many_decimals"},
		{typ: DataBoolean, raw: `true`, want: `true`},
		{typ: DataBoolean, raw: `"yes"`, reason: "invalid_type"},
		{typ: DataDate, raw: `"2026-02-10"`, want: `"2026-02-10"`},
		{typ: DataDate, raw: `"10/02/2026"`, reason: "invalid_date"},
		{typ: DataDate, raw: `20260210`, reason: "invalid_type"},
		{typ: DataSelect, raw: q(active.String()), want: q(active.String())},
		{typ: DataSelect, raw: q(ids.New().String()), reason: "unknown_option"},
		{typ: DataSelect, raw: q("not-a-uuid"), reason: "unknown_option"},
		{typ: DataSelect, raw: q(inactive.String()), reason: "option_inactive"},
		{typ: DataSelect, raw: q(inactive.String()), previous: q(inactive.String()), want: q(inactive.String())},
		{typ: DataSelect, raw: `["x"]`, reason: "invalid_type"},
		{typ: DataMultiselect, raw: `[]`, want: `[]`},
		{typ: DataMultiselect, raw: fmt.Sprintf(`[%q]`, active), want: fmt.Sprintf(`[%q]`, active)},
		{typ: DataMultiselect, raw: fmt.Sprintf(`[%q,%q]`, active, active), reason: "duplicate_option"},
		{typ: DataMultiselect, raw: fmt.Sprintf(`[%q]`, inactive), reason: "option_inactive"},
		{typ: DataMultiselect, raw: fmt.Sprintf(`[%q]`, inactive), previous: fmt.Sprintf(`[%q]`, inactive), want: fmt.Sprintf(`[%q]`, inactive)},
		{typ: DataMultiselect, raw: q(active.String()), reason: "invalid_type"},
	}
	reg := DefaultAttributeTypes()
	for _, tc := range cases {
		t.Run(string(tc.typ)+" "+tc.raw[:min(len(tc.raw), 20)], func(t *testing.T) {
			attr := sel
			attr.DataType = tc.typ
			var prev json.RawMessage
			if tc.previous != "" {
				prev = json.RawMessage(tc.previous)
			}
			got, reason := reg[tc.typ].Validate(attr, json.RawMessage(tc.raw), prev)
			if reason != tc.reason {
				t.Fatalf("reason = %q, want %q", reason, tc.reason)
			}
			if tc.reason != "" {
				return
			}
			raw, _ := json.Marshal(got)
			if string(raw) != tc.want {
				t.Errorf("value = %s, want %s", raw, tc.want)
			}
		})
	}
	for _, typ := range []DataType{DataText, DataNumber, DataBoolean, DataDate, DataSelect, DataMultiselect} {
		if reg[typ] == nil || !typ.Valid() {
			t.Errorf("type %s has no validator", typ)
		}
	}
	if DataType("color").Valid() {
		t.Error("unknown data types are not valid")
	}
}

func TestAttributeValidatorAppliesTheSet(t *testing.T) {
	brand := Attribute{ID: ids.New(), Code: "brand", DataType: DataText, Status: StatusActive}
	weight := Attribute{ID: ids.New(), Code: "net_weight", DataType: DataNumber, Status: StatusActive}
	old := Attribute{ID: ids.New(), Code: "origin", DataType: DataText, Status: StatusInactive}
	set := []SetAttribute{{Attribute: brand, Required: true}, {Attribute: weight}, {Attribute: old}}
	v := NewAttributeValidator()

	_, err := v.Validate([]byte(`{"net_weight": 500}`), set, nil)
	wantField(t, err, "attributes.brand", "required")

	_, err = v.Validate([]byte(`{"brand": "Kinh Đô", "color": "đỏ"}`), set, nil)
	wantField(t, err, "attributes.color", "not_in_attribute_set")

	_, err = v.Validate([]byte(`{"brand": "Kinh Đô", "origin": "Việt Nam"}`), set, nil)
	wantField(t, err, "attributes.origin", "attribute_inactive")

	_, err = v.Validate([]byte(`[1]`), set, nil)
	wantField(t, err, "attributes", "not_an_object")

	// Values of attributes no longer in the set are hidden, kept and may be echoed back.
	prev := JSONObject(`{"brand":"Cũ","origin":"Việt Nam","flavor":"x"}`)
	got, err := v.Validate([]byte(`{"brand": "Kinh Đô", "origin": "Việt Nam", "flavor": "x", "net_weight": null}`), set, prev)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if m["brand"] != "Kinh Đô" || m["origin"] != "Việt Nam" || m["flavor"] != "x" || m["net_weight"] != nil {
		t.Errorf("stored = %s", got)
	}
	_, err = v.Validate([]byte(`{"brand": "Kinh Đô", "flavor": "y"}`), set, prev)
	wantField(t, err, "attributes.flavor", "not_in_attribute_set")

	_, err = v.Validate([]byte(`{"brand": "`+strings.Repeat("a", MaxAttributesBytes)+`"}`), set, nil)
	wantCode(t, err, "attributes_too_large")
}

func TestVariantMatrixSpansEveryCombinationWithStableKeys(t *testing.T) {
	size := selectAttr("size", "Hộp 250g", "Hộp 500g")
	flavor := selectAttr("flavor", "Dâu", "Cam", "Nho")
	sugar := selectAttr("sugar", "Có đường", "Không đường")
	axes := []AxisOptions{{size, size.Options}, {flavor, flavor.Options}, {sugar, sugar.Options}}

	drafts, err := VariantMatrixFactory{}.Build("Kẹo dẻo", axes)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 12 {
		t.Fatalf("%d variants, want 12", len(drafts))
	}
	keys := map[string]bool{}
	for _, d := range drafts {
		if keys[d.VariantKey] {
			t.Errorf("duplicate key %s", d.VariantKey)
		}
		keys[d.VariantKey] = true
		if d.VariantKey != VariantKeyOf(d.OptionIDs) || len(d.AttributeIDs) != 3 || d.AttributeIDs[1] != flavor.ID {
			t.Errorf("draft = %+v", d)
		}
	}
	first, last := drafts[0], drafts[11]
	if first.VariantLabel != "Hộp 250g · Dâu · Có đường" || first.DisplayName != "Kẹo dẻo – Hộp 250g · Dâu · Có đường" {
		t.Errorf("first = %q / %q", first.VariantLabel, first.DisplayName)
	}
	if last.VariantLabel != "Hộp 500g · Nho · Không đường" || drafts[1].VariantLabel != "Hộp 250g · Dâu · Không đường" {
		t.Errorf("order = %q, %q", drafts[1].VariantLabel, last.VariantLabel)
	}

	// The same axes always give the same keys in the same order.
	again, _ := VariantMatrixFactory{}.Build("Tên khác", axes)
	for i := range drafts {
		if again[i].VariantKey != drafts[i].VariantKey {
			t.Fatalf("key %d changed: %s vs %s", i, again[i].VariantKey, drafts[i].VariantKey)
		}
	}

	single, err := VariantMatrixFactory{}.Build("Bánh", nil)
	if err != nil || len(single) != 1 || single[0].VariantKey != "" || single[0].DisplayName != "Bánh" {
		t.Errorf("no axes = %+v, %v", single, err)
	}
	_, err = VariantMatrixFactory{}.Build("x", []AxisOptions{{size, nil}})
	wantField(t, err, "axes[0].option_ids", "required")
	_, err = VariantMatrixFactory{}.Build("x", append(axes, axes[0]))
	wantField(t, err, "axes", "too_many")
}

func TestSkuCodeAllocatorSkipsTakenCodes(t *testing.T) {
	a := NewSkuCodeAllocator("TH", []string{"th-1", "TH-3"})
	a.Reserve("TH-2")
	if got := []string{a.Next(), a.Next()}; got[0] != "TH-4" || got[1] != "TH-5" {
		t.Errorf("codes = %v", got)
	}
	if !a.Taken("th-4") || a.Taken("TH-6") {
		t.Error("Taken is case-insensitive and tracks handed out codes")
	}
}

func TestNameKeysCollideAcrossDiacriticsAndCase(t *testing.T) {
	if nameKey("Kẹo Dừa") != nameKey("keo dua") || nameKey("  Đường   phèn ") != "duong phen" {
		t.Errorf("keys = %q, %q", nameKey("Kẹo Dừa"), nameKey("keo dua"))
	}
	if nameKey("Kẹo dừa") == nameKey("Kẹo dứa nướng") {
		t.Error("different names must keep different keys")
	}
}

func TestCategoryTreeRejectsCyclesAndDepth(t *testing.T) {
	a, b, c, d := ids.New(), ids.New(), ids.New(), ids.New()
	tree := CategoryTree{a: nil, b: &a, c: &b, d: &c} // a > b > c > d: four levels
	if err := tree.CheckPlacement(uuid.Nil, &c); err != nil {
		t.Errorf("a new leaf under level 3 is allowed: %v", err)
	}
	wantCode(t, tree.CheckPlacement(uuid.Nil, &d), "category_too_deep")
	wantCode(t, tree.CheckPlacement(a, &c), "category_cycle")
	wantCode(t, tree.CheckPlacement(a, &a), "category_cycle")
	other := ids.New()
	wantField(t, tree.CheckPlacement(uuid.Nil, &other), "parent_id", "unknown_category")
	e := ids.New()
	tree[e] = nil
	wantCode(t, tree.CheckPlacement(a, &e), "category_too_deep") // moving the 4-level subtree down one level
	if err := tree.CheckPlacement(c, &e); err != nil {
		t.Errorf("moving a two-level subtree under a root is allowed: %v", err)
	}
}

type mixFixture struct {
	draft MixGroupDraft
	facts MixFacts
	spare uuid.UUID // a third flavor SKU counted in chai, not in the draft
}

func newMixFixture() mixFixture {
	thung, day, hop, chai := ids.New(), ids.New(), ids.New(), ids.New()
	s1, s2, s3 := ids.New(), ids.New(), ids.New()
	facts := MixFacts{
		UnitNames: map[uuid.UUID]string{thung: "thùng", day: "dây", hop: "hộp", chai: "chai"},
		Skus: map[uuid.UUID]MixSku{
			s1: {ID: s1, Name: "Sữa – Dâu", ProductType: ProductGoods, BaseUnitID: hop, FactorOneUnits: []uuid.UUID{hop}},
			s2: {ID: s2, Name: "Sữa – Cam", ProductType: ProductGoods, BaseUnitID: hop, FactorOneUnits: []uuid.UUID{hop}},
			s3: {ID: s3, Name: "Sữa – Nho", ProductType: ProductGoods, BaseUnitID: chai, FactorOneUnits: []uuid.UUID{chai}},
		},
	}
	d := MixGroupDraft{Name: "Sữa 180ml", PackUnitID: thung, PartUnitID: day, UnitsPerPack: 48, UnitsPerPart: 4,
		Items: []MixGroupItemDraft{{SkuID: s1, FlavorLabel: "Dâu", Active: true}, {SkuID: s2, FlavorLabel: "Cam", Active: true}}}
	return mixFixture{draft: d, facts: facts, spare: s3}
}

func TestMixGroupPolicyMessagesAreVerbatim(t *testing.T) {
	fx := newMixFixture()
	piece, err := MixGroupPolicy{}.Check(fx.draft, fx.facts)
	if err != nil || fx.facts.UnitNames[piece] != "hộp" {
		t.Fatalf("valid group: piece %v, %v", piece, err)
	}
	cases := []struct {
		name    string
		mutate  func(fx *mixFixture)
		code    string
		message string
	}{
		{"name", func(fx *mixFixture) { fx.draft.Name = "  " }, "mix_group_name_required", "Cần tên nhóm mix."},
		{"name taken", func(fx *mixFixture) { fx.facts.NameTaken = true }, "mix_group_name_taken", "Tên nhóm mix đã tồn tại."},
		{"units", func(fx *mixFixture) { fx.draft.PartUnitID = uuid.Nil }, "mix_group_units_required",
			"Cần đơn vị thùng và đơn vị chọn vị (VD: thùng / dây)."},
		{"part size", func(fx *mixFixture) { fx.draft.UnitsPerPart = 0 }, "mix_group_part_size_invalid",
			"Số đơn vị lẻ trong 1 dây phải là số nguyên dương."},
		{"pack size", func(fx *mixFixture) { fx.draft.UnitsPerPack = -1 }, "mix_group_pack_size_invalid",
			"Số đơn vị lẻ trong 1 thùng phải là số nguyên dương."},
		{"divisible", func(fx *mixFixture) { fx.draft.UnitsPerPack = 50 }, "mix_group_pack_not_divisible",
			"1 thùng (50) phải chia hết cho 1 dây (4)."},
		{"two parts", func(fx *mixFixture) { fx.draft.UnitsPerPack = 4 }, "mix_group_too_few_parts",
			"1 thùng phải gồm ít nhất 2 dây thì mới mix được."},
		{"duplicate", func(fx *mixFixture) { fx.draft.Items[1].SkuID = fx.draft.Items[0].SkuID }, "mix_group_duplicate_flavor",
			"Một mặt hàng đang được chọn hai lần."},
		{"two flavors", func(fx *mixFixture) { fx.draft.Items[1].Active = false }, "mix_group_too_few_flavors",
			"Nhóm mix cần ít nhất 2 vị."},
		{"goods", func(fx *mixFixture) {
			s := fx.facts.Skus[fx.draft.Items[1].SkuID]
			s.ProductType = ProductMaterial
			fx.facts.Skus[s.ID] = s
		}, "mix_group_flavor_not_goods", "Sữa – Cam không phải hàng hóa bán lẻ."},
		{"piece units", func(fx *mixFixture) {
			fx.draft.Items = append(fx.draft.Items, MixGroupItemDraft{SkuID: fx.spare, FlavorLabel: "Nho", Active: true})
		}, "mix_group_mixed_piece_units", "Các vị phải cùng đơn vị lẻ, đang lẫn: hộp, chai."},
		{"label", func(fx *mixFixture) { fx.draft.Items[0].FlavorLabel = " " }, "mix_group_flavor_label_required",
			"Cần tên vị ngắn cho Sữa – Dâu."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newMixFixture()
			tc.mutate(&fx)
			_, err := MixGroupPolicy{}.Check(fx.draft, fx.facts)
			wantMessage(t, err, tc.code, tc.message)
		})
	}

	// Flavors with different base units mix when they share a unit converting at factor 1.
	fx = newMixFixture()
	hop := fx.facts.Skus[fx.draft.Items[0].SkuID].BaseUnitID
	s := fx.facts.Skus[fx.spare]
	s.FactorOneUnits = append(s.FactorOneUnits, hop)
	fx.facts.Skus[fx.spare] = s
	fx.draft.Items = append(fx.draft.Items, MixGroupItemDraft{SkuID: fx.spare, FlavorLabel: "Nho", Active: true})
	piece, err = MixGroupPolicy{}.Check(fx.draft, fx.facts)
	if err != nil || piece != hop {
		t.Errorf("shared factor-1 unit: %v, %v", fx.facts.UnitNames[piece], err)
	}
}

func TestAttributeSetChangeSpecHidesRemovedAttributes(t *testing.T) {
	brand := Attribute{ID: ids.New(), Code: "brand", Status: StatusActive}
	origin := Attribute{ID: ids.New(), Code: "origin", Status: StatusActive}
	old := Attribute{ID: ids.New(), Code: "old", Status: StatusInactive}
	attrs := map[uuid.UUID]Attribute{brand.ID: brand, origin.ID: origin, old.ID: old}
	current := []AttributeSetItem{{AttributeID: brand.ID, AttributeCode: "brand"}, {AttributeID: old.ID, AttributeCode: "old"},
		{AttributeID: origin.ID, AttributeCode: "origin"}}

	hidden, err := AttributeSetChangeSpec{}.Check(current, []SetItem{{AttributeID: old.ID}}, attrs)
	if err != nil || !slices.Equal(hidden, []string{"brand", "origin"}) {
		t.Errorf("hidden = %v, %v", hidden, err)
	}
	_, err = AttributeSetChangeSpec{}.Check(nil, []SetItem{{AttributeID: old.ID}}, attrs)
	wantField(t, err, "items[0].attribute_id", "attribute_inactive")
	_, err = AttributeSetChangeSpec{}.Check(nil, []SetItem{{AttributeID: brand.ID}, {AttributeID: brand.ID}}, attrs)
	wantField(t, err, "items[1].attribute_id", "duplicate")
	_, err = AttributeSetChangeSpec{}.Check(nil, []SetItem{{AttributeID: ids.New()}}, attrs)
	wantField(t, err, "items[0].attribute_id", "unknown_attribute")
}

// usageStub answers the edit-rule questions with fixed values.
type usageStub struct {
	attributeInUse, optionInUse, moved, skuInUse bool
	mixUsage                                     []UsageCount
	mixUsedSkus                                  []uuid.UUID
}

func (u usageStub) AttributeInUse(context.Context, db.Querier, uuid.UUID, string) (bool, error) {
	return u.attributeInUse, nil
}
func (u usageStub) OptionInUse(context.Context, db.Querier, string, uuid.UUID) (bool, error) {
	return u.optionInUse, nil
}
func (u usageStub) SkusHaveStockMoves(context.Context, db.Querier, []uuid.UUID) (bool, error) {
	return u.moved, nil
}
func (u usageStub) SkuInUse(context.Context, db.Querier, uuid.UUID) (bool, error) {
	return u.skuInUse, nil
}
func (u usageStub) SkuUnitInUse(context.Context, db.Querier, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}
func (u usageStub) ProductInUse(context.Context, db.Querier, uuid.UUID) (bool, error) {
	return false, nil
}
func (u usageStub) SkuOnHand(context.Context, db.Querier, uuid.UUID) (money.Qty, error) {
	return money.Qty{}, nil
}
func (u usageStub) MixGroupUsage(context.Context, db.Querier, uuid.UUID) ([]UsageCount, error) {
	return u.mixUsage, nil
}
func (u usageStub) MixGroupUsedSkus(context.Context, db.Querier, uuid.UUID) ([]uuid.UUID, error) {
	return u.mixUsedSkus, nil
}
func (u usageStub) CategoryInUse(context.Context, db.Querier, uuid.UUID) (bool, error) {
	return false, nil
}

func TestEditSpecsBlockChangesOnceUsed(t *testing.T) {
	ctx := context.Background()
	attr := Attribute{ID: ids.New(), Code: "brand", DataType: DataText, Status: StatusActive}
	used := usageStub{attributeInUse: true, optionInUse: true, moved: true, skuInUse: true}

	if err := (AttributeChangeSpec{Usage: used}).Check(ctx, nil, attr, AttributeChange{}); err != nil {
		t.Errorf("renaming a used attribute is allowed: %v", err)
	}
	wantCode(t, AttributeChangeSpec{Usage: used}.Check(ctx, nil, attr, AttributeChange{DataTypeChanged: true}), "attribute_in_use")
	wantCode(t, AttributeChangeSpec{Usage: used}.Check(ctx, nil, attr, AttributeChange{Delete: true}), "attribute_in_use")
	if err := (AttributeChangeSpec{Usage: usageStub{}}).Check(ctx, nil, attr, AttributeChange{CodeChanged: true}); err != nil {
		t.Errorf("an unused attribute may change: %v", err)
	}
	wantCode(t, OptionDeleteSpec{Usage: used}.Check(ctx, nil, attr, ids.New()), "option_in_use")
	wantCode(t, SKUChangeSpec{Usage: used}.CheckDelete(ctx, nil, ids.New()), "sku_in_use")
	wantCode(t, UnitConversionSpec{Usage: used}.CheckBaseChange(ctx, nil, ids.New()), "base_unit_locked")

	onHand := func(qty string) OnHandFunc {
		return func(context.Context, db.Querier, uuid.UUID) (money.Qty, error) { return money.MustQty(qty), nil }
	}
	sku := Sku{ID: ids.New()}
	wantCode(t, SKUChangeSpec{OnHand: onHand("3")}.CheckTracking(ctx, nil, sku, true, false), "tracking_locked")
	if err := (SKUChangeSpec{OnHand: onHand("0")}).CheckTracking(ctx, nil, sku, true, true); err != nil {
		t.Errorf("tracking may change without stock: %v", err)
	}

	axis := selectAttr("flavor", "Dâu")
	size := selectAttr("size", "Nhỏ")
	text := Attribute{ID: ids.New(), Code: "brand", DataType: DataText, Status: StatusActive}
	attrs := map[uuid.UUID]Attribute{axis.ID: axis, size.ID: size, text.ID: text}
	current := []VariantAxis{{AttributeID: axis.ID, Position: 1}}
	skus := []uuid.UUID{ids.New()}
	spec := VariantAxisChangeSpec{Usage: used}
	if err := spec.Check(ctx, nil, skus, current, []uuid.UUID{axis.ID}, attrs); err != nil {
		t.Errorf("unchanged axes are allowed: %v", err)
	}
	wantCode(t, spec.Check(ctx, nil, skus, current, []uuid.UUID{axis.ID, size.ID}, attrs), "variant_axes_locked")
	wantField(t, spec.Check(ctx, nil, nil, nil, []uuid.UUID{text.ID}, attrs), "axes[0].attribute_id", "not_select")
	wantField(t, spec.Check(ctx, nil, nil, nil, []uuid.UUID{axis.ID, axis.ID}, attrs), "axes[1].attribute_id", "duplicate")
	if err := (VariantAxisChangeSpec{Usage: usageStub{}}).Check(ctx, nil, skus, current, []uuid.UUID{size.ID}, attrs); err != nil {
		t.Errorf("axes of SKUs without stock moves may change: %v", err)
	}

	sold := ids.New()
	g := MixGroup{ID: ids.New(), PackUnitID: ids.New(), PartUnitID: ids.New(), UnitsPerPack: 24, UnitsPerPart: 6,
		Items: []MixGroupItem{{SkuID: sold}, {SkuID: ids.New()}}}
	mixUsed := MixGroupChangeSpec{Usage: usageStub{mixUsage: []UsageCount{{Label: "hóa đơn", Count: 2}, {Label: "báo giá", Count: 1}},
		mixUsedSkus: []uuid.UUID{sold}}}
	next := g
	next.UnitsPerPack = 12
	wantCode(t, mixUsed.CheckUpdate(ctx, nil, g, next, nil), "mix_group_pack_locked")
	next = g
	next.Items = g.Items[1:]
	err := mixUsed.CheckUpdate(ctx, nil, g, next, func(uuid.UUID) string { return "Cola" })
	wantMessage(t, err, "mix_group_flavor_in_use", "Không gỡ được vị đã dùng trên chứng từ: Cola.")
	next.Name, next.Items = "Tên mới", append(slices.Clone(g.Items), MixGroupItem{SkuID: ids.New()})
	if err := mixUsed.CheckUpdate(ctx, nil, g, next, nil); err != nil {
		t.Errorf("renaming and adding flavors stays allowed: %v", err)
	}
	wantMessage(t, mixUsed.CheckDelete(ctx, nil, g.ID), "mix_group_in_use",
		"Nhóm đã dùng trên 2 hóa đơn, 1 báo giá. Chỉ có thể ngừng dùng.")
}

func TestRankCandidatesPutsExactThenPrefixMatchesFirst(t *testing.T) {
	cands := []SearchCandidate{
		{Code: "BK-01", Label: "Hộp quà bánh đậu xanh"},
		{Code: "BDX", Label: "Bánh đậu xanh Rồng Vàng"},
		{Code: "X1", Label: "Bánh"},
		{Code: "8935001", Label: "Trà xanh", Exact: true},
	}
	got := RankCandidates(cands, "banh")
	labels := make([]string, len(got))
	for i, c := range got {
		labels[i] = c.Label
	}
	want := []string{"Trà xanh", "Bánh", "Bánh đậu xanh Rồng Vàng", "Hộp quà bánh đậu xanh"}
	if !slices.Equal(labels, want) {
		t.Errorf("order = %v, want %v", labels, want)
	}
}

func TestSearchIndexerFoldsEverySearchableText(t *testing.T) {
	text := SearchIndexer{}.Text(SkuSearchText{Code: "BDX-01", DisplayName: "Bánh đậu xanh – Hộp 250g",
		Barcodes: []string{"8935001234567"}, FlavorLabels: []string{"Sầu riêng"}, AttributeTexts: []string{"Rồng Vàng"}})
	for _, part := range []string{"bdx 01", "banh dau xanh", "hop 250g", "8935001234567", "sau rieng", "rong vang"} {
		if !strings.Contains(text, part) {
			t.Errorf("search text %q lacks %q", text, part)
		}
	}
}
