//go:build integration

package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

type harness struct {
	svc    *Service
	seeder *Seeder
	d      *db.DB
}

func newHarness(t testing.TB) harness {
	t.Helper()
	clk := testkit.Clock()
	svc := NewService(Deps{DB: testkit.DB(t), Clock: clk, Audit: audit.NewRecorder(clk)})
	return harness{svc: svc, seeder: NewSeeder(svc), d: testkit.DB(t)}
}

// tenant creates a tenant with the default catalog seeded and returns an owner actor for it.
func (h harness) tenant(t testing.TB) audit.Actor {
	t.Helper()
	tn := testkit.NewTenant(t, h.d)
	actor := audit.Actor{TenantID: tn.ID, Type: audit.ActorOwner, ID: ids.New(), Source: audit.SourceWeb}
	sc := SeedContext{TenantID: tn.ID, Now: testkit.Epoch, Actor: audit.Actor{TenantID: tn.ID, Type: audit.ActorSystem, Source: audit.SourceCLI}}
	testkit.InTenant(t, h.d, tn.ID, func(q db.Querier) error {
		ctx := context.Background()
		for _, seed := range []func(context.Context, db.Querier, SeedContext) error{
			h.seeder.SeedColors, h.seeder.SeedUnits, h.seeder.SeedTaxRates, h.seeder.SeedAttributeSets,
		} {
			if err := seed(ctx, q, sc); err != nil {
				return err
			}
		}
		return nil
	})
	return actor
}

func scopeOfActor(a audit.Actor) Scope { return Scope{TenantID: a.TenantID} }

func (h harness) unitID(t testing.TB, a audit.Actor, name string) uuid.UUID {
	t.Helper()
	units, err := h.svc.Units(context.Background(), scopeOfActor(a), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range units {
		if u.Name == name {
			return u.ID
		}
	}
	t.Fatalf("no unit %q", name)
	return uuid.Nil
}

func (h harness) count(t *testing.T, a audit.Actor, table string) int {
	t.Helper()
	var n int
	testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&n) //nolint:gosec // test-only constant table names
	})
	return n
}

func ptr2[T any](v T) *T { return &v }

func (h harness) product(t *testing.T, a audit.Actor, name, code string) ProductDetail {
	t.Helper()
	d, err := h.svc.CreateProduct(context.Background(), a, ProductInput{Name: name,
		Sku: &SkuInput{Code: code, BaseUnitID: h.unitID(t, a, "hộp")}})
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	return d
}

func TestSeedersAreIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	sc := SeedContext{TenantID: a.TenantID, Now: testkit.Epoch, Actor: audit.Actor{TenantID: a.TenantID, Type: audit.ActorSystem, Source: audit.SourceCLI}}
	testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error {
		for _, seed := range []func(context.Context, db.Querier, SeedContext) error{
			h.seeder.SeedColors, h.seeder.SeedUnits, h.seeder.SeedTaxRates, h.seeder.SeedAttributeSets,
		} {
			if err := seed(ctx, q, sc); err != nil {
				return err
			}
		}
		return nil
	})
	if c, u, r, s := h.count(t, a, "colors"), h.count(t, a, "units"), h.count(t, a, "tax_rates"), h.count(t, a, "attribute_sets"); c != 7 || u != 9 || r != 3 || s != 3 {
		t.Fatalf("seeded colors %d, units %d, tax rates %d, sets %d", c, u, r, s)
	}

	for range 2 {
		testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error { return h.seeder.SeedDemoSample(ctx, q, sc) })
	}
	products, skus := h.count(t, a, "products"), h.count(t, a, "skus")
	if products != 15 || skus != 13+5+4 || h.count(t, a, "mix_groups") != 2 {
		t.Fatalf("sample: %d products, %d skus, %d mix groups", products, skus, h.count(t, a, "mix_groups"))
	}
	items, err := h.svc.Search(ctx, scopeOfActor(a), SearchRequest{Kind: SearchSku, Q: "sua tuoi dau"})
	if err != nil || len(items) == 0 || items[0].Code != "TH-DAU" {
		t.Errorf("sample search = %+v, %v", items, err)
	}
}

func TestAttributeEditRulesOnceUsed(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	text := DataText
	res, err := h.svc.CreateAttribute(ctx, a, AttributeInput{Code: ptr2("material"), Name: ptr2("Chất liệu"), DataType: &text})
	if err != nil {
		t.Fatal(err)
	}
	attr := res.Attribute
	set, err := h.svc.CreateAttributeSet(ctx, a, AttributeSetInput{Name: ptr2("Bao bì"), Items: []SetItem{{AttributeID: attr.ID, IsRequired: true}}})
	if err != nil {
		t.Fatal(err)
	}

	// Unused: the type may change.
	number := DataNumber
	res, err = h.svc.UpdateAttribute(ctx, a, attr.ID, attr.Version, AttributeInput{DataType: &number})
	if err != nil {
		t.Fatal(err)
	}
	res, err = h.svc.UpdateAttribute(ctx, a, attr.ID, res.Attribute.Version, AttributeInput{DataType: &text})
	if err != nil {
		t.Fatal(err)
	}
	attr = res.Attribute

	_, err = h.svc.CreateProduct(ctx, a, ProductInput{Name: "Túi giấy", AttributeSetID: Some(set.ID),
		Sku: &SkuInput{Code: "TG-1", BaseUnitID: h.unitID(t, a, "cái")}})
	if f := fieldsOf(err); f["attributes.material"] != "required" {
		t.Fatalf("required attribute: %v", err)
	}
	p, err := h.svc.CreateProduct(ctx, a, ProductInput{Name: "Túi giấy", AttributeSetID: Some(set.ID),
		Attributes: json.RawMessage(`{"material":"Giấy kraft"}`), Sku: &SkuInput{Code: "TG-1", BaseUnitID: h.unitID(t, a, "cái")}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = h.svc.UpdateAttribute(ctx, a, attr.ID, attr.Version, AttributeInput{DataType: &number})
	wantCode(t, err, "attribute_in_use")
	if apperr.KindOf(err) != apperr.Conflict {
		t.Errorf("attribute_in_use is a conflict, got %v", apperr.KindOf(err))
	}
	wantCode(t, h.svc.DeleteAttribute(ctx, a, attr.ID), "attribute_in_use")
	_, err = h.svc.UpdateAttribute(ctx, a, attr.ID, attr.Version+5, AttributeInput{Name: ptr2("X")})
	if apperr.KindOf(err) != apperr.PreconditionFailed {
		t.Errorf("stale version = %v", err)
	}
	inactive := StatusInactive
	res, err = h.svc.UpdateAttribute(ctx, a, attr.ID, attr.Version, AttributeInput{Name: ptr2("Vật liệu"), Status: &inactive})
	if err != nil || res.Attribute.Status != StatusInactive || res.Attribute.Name != "Vật liệu" {
		t.Fatalf("deactivate = %+v, %v", res.Attribute, err)
	}
	// The stored value stays; the product can still be saved with it.
	detail, err := h.svc.UpdateProduct(ctx, a, p.Product.ID, p.Product.Version, ProductPatch{Attributes: json.RawMessage(`{"material":"Giấy kraft"}`)})
	if err != nil || string(detail.Product.Attributes) != `{"material": "Giấy kraft"}` && string(detail.Product.Attributes) != `{"material":"Giấy kraft"}` {
		t.Errorf("product after deactivation = %s, %v", detail.Product.Attributes, err)
	}

	// Select options: a used option cannot be deleted, an unused one can.
	sel := DataSelect
	flavor, err := h.svc.CreateAttribute(ctx, a, AttributeInput{Code: ptr2("taste"), Name: ptr2("Hương"), DataType: &sel, Options: []string{"Dâu", "Cam"}})
	if err != nil || len(flavor.Attribute.Options) != 2 {
		t.Fatalf("select = %+v, %v", flavor, err)
	}
	_, err = h.svc.AddOption(ctx, a, flavor.Attribute.ID, OptionInput{Label: ptr2(" dâu ")})
	wantCode(t, err, "option_label_taken")
	v, err := h.svc.CreateProduct(ctx, a, ProductInput{Name: "Kẹo hương", Sku: &SkuInput{Code: "KH-1", BaseUnitID: h.unitID(t, a, "gói")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ReplaceVariantAxes(ctx, a, v.Product.ID, []uuid.UUID{flavor.Attribute.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.GenerateVariants(ctx, a, v.Product.ID, VariantRequest{OptionIDs: []uuid.UUID{flavor.Attribute.Options[0].ID},
		CodePrefix: "KH", Defaults: &SkuInput{BaseUnitID: h.unitID(t, a, "gói")}}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, h.svc.DeleteOption(ctx, a, flavor.Attribute.ID, flavor.Attribute.Options[0].ID), "option_in_use")
	if err := h.svc.DeleteOption(ctx, a, flavor.Attribute.ID, flavor.Attribute.Options[1].ID); err != nil {
		t.Errorf("delete unused option: %v", err)
	}
}

func TestVariantPreviewEqualsGenerationAndRegenerationIsIdempotent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	sel := DataSelect
	mk := func(code string, labels ...string) Attribute {
		r, err := h.svc.CreateAttribute(ctx, a, AttributeInput{Code: &code, Name: &code, DataType: &sel, Options: labels})
		if err != nil {
			t.Fatal(err)
		}
		return r.Attribute
	}
	size, taste, sugar := mk("size", "250g", "500g"), mk("taste", "Dâu", "Cam", "Nho"), mk("sugar", "Có đường", "Không đường")
	p, err := h.svc.CreateProduct(ctx, a, ProductInput{Name: "Kẹo dẻo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ReplaceVariantAxes(ctx, a, p.Product.ID, []uuid.UUID{size.ID, taste.ID, sugar.ID}); err != nil {
		t.Fatal(err)
	}
	req := VariantRequest{CodePrefix: "KD", Defaults: &SkuInput{BaseUnitID: h.unitID(t, a, "gói")}}
	firstKey := VariantKeyOf([]uuid.UUID{size.Options[0].ID, taste.Options[0].ID, sugar.Options[0].ID})
	req.Codes = map[string]string{firstKey: "KD-DAU"}

	_, err = h.svc.PreviewVariants(ctx, scopeOfActor(a), p.Product.ID, VariantRequest{Codes: req.Codes})
	wantField(t, err, "code_prefix", "required")

	preview, err := h.svc.PreviewVariants(ctx, scopeOfActor(a), p.Product.ID, req)
	if err != nil || len(preview) != 12 {
		t.Fatalf("preview = %d rows, %v", len(preview), err)
	}
	if preview[0].Code != "KD-DAU" || preview[1].Code != "KD-1" || preview[11].Code != "KD-11" {
		t.Errorf("codes = %s %s %s", preview[0].Code, preview[1].Code, preview[11].Code)
	}
	res, err := h.svc.GenerateVariants(ctx, a, p.Product.ID, req)
	if err != nil || len(res.Created) != 12 || res.Existing != 0 {
		t.Fatalf("generate = %d created, %d existing, %v", len(res.Created), res.Existing, err)
	}
	for i, row := range preview {
		got := res.Created[i].Sku
		if got.VariantKey != row.VariantKey || got.Code != row.Code || got.DisplayName != row.DisplayName {
			t.Errorf("row %d: preview %+v, created %+v", i, row, got)
		}
	}

	again, err := h.svc.GenerateVariants(ctx, a, p.Product.ID, req)
	if err != nil || len(again.Created) != 0 || again.Existing != 12 {
		t.Fatalf("regenerate = %d created, %d existing, %v", len(again.Created), again.Existing, err)
	}
	rows, err := h.svc.PreviewVariants(ctx, scopeOfActor(a), p.Product.ID, req)
	if err != nil || slices.ContainsFunc(rows, func(r VariantRow) bool { return r.SkuID == nil }) {
		t.Errorf("every variant now exists: %v", err)
	}

	// A new option only adds the missing variants.
	if _, err := h.svc.AddOption(ctx, a, sugar.ID, OptionInput{Label: ptr2("Ít đường")}); err != nil {
		t.Fatal(err)
	}
	more, err := h.svc.GenerateVariants(ctx, a, p.Product.ID, req)
	if err != nil || len(more.Created) != 6 || more.Existing != 12 {
		t.Fatalf("new option = %d created, %d existing, %v", len(more.Created), more.Existing, err)
	}

	// Only select attributes can be axes; a SKU code already used elsewhere is taken.
	text := DataText
	note, _ := h.svc.CreateAttribute(ctx, a, AttributeInput{Code: ptr2("note"), Name: ptr2("Ghi chú"), DataType: &text})
	_, err = h.svc.ReplaceVariantAxes(ctx, a, p.Product.ID, []uuid.UUID{note.Attribute.ID})
	wantField(t, err, "axes[0].attribute_id", "not_select")
	other, _ := h.svc.CreateProduct(ctx, a, ProductInput{Name: "Kẹo khác"})
	if _, err := h.svc.ReplaceVariantAxes(ctx, a, other.Product.ID, []uuid.UUID{taste.ID}); err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.GenerateVariants(ctx, a, other.Product.ID, VariantRequest{Codes: map[string]string{
		VariantKeyOf([]uuid.UUID{taste.Options[0].ID}): "kd-dau"}, OptionIDs: []uuid.UUID{taste.Options[0].ID},
		Defaults: &SkuInput{BaseUnitID: h.unitID(t, a, "gói")}})
	wantCode(t, err, "sku_code_taken")
}

func TestProductNamesCollideAcrossDiacriticsAndCase(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	h.product(t, a, "Kẹo Dừa", "KD-01")
	_, err := h.svc.CreateProduct(ctx, a, ProductInput{Name: "keo dua", Sku: &SkuInput{Code: "KD-02", BaseUnitID: h.unitID(t, a, "hộp")}})
	wantCode(t, err, "product_name_taken")
	_, err = h.svc.CreateProduct(ctx, a, ProductInput{Name: "Kẹo dừa sáp", Sku: &SkuInput{Code: "kd-01", BaseUnitID: h.unitID(t, a, "hộp")}})
	wantCode(t, err, "sku_code_taken")
	_, err = h.svc.CreateProduct(ctx, a, ProductInput{Name: " ", Sku: &SkuInput{BaseUnitID: h.unitID(t, a, "hộp")}})
	wantCode(t, err, "code_and_name_required")
	_, err = h.svc.CreateUnit(ctx, a, UnitInput{Name: ptr2("HỘP")})
	wantCode(t, err, "name_taken")

	// Another tenant may use the same names and codes.
	b := h.tenant(t)
	h.product(t, b, "Kẹo Dừa", "KD-01")
}

func TestSearchFindsAccentInsensitiveAndExactBarcodeFirst(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	h.product(t, a, "Bánh đậu xanh Rồng Vàng", "BDX-01")
	h.product(t, a, "Hộp quà bánh đậu xanh", "HQ-02")
	target := h.product(t, a, "Trà xanh hộp", "TX-03")
	if _, err := h.svc.AddBarcode(ctx, a, target.Skus[0].Sku.ID, BarcodeInput{Code: "8935001234567"}); err != nil {
		t.Fatal(err)
	}
	_, err := h.svc.AddBarcode(ctx, a, target.Skus[0].Sku.ID, BarcodeInput{Code: "8935001234567"})
	wantCode(t, err, "barcode_taken")
	scope := scopeOfActor(a)

	items, err := h.svc.Search(ctx, scope, SearchRequest{Kind: SearchSku, Q: "banh dau xanh"})
	if err != nil || len(items) != 2 || items[0].Label != "Bánh đậu xanh Rồng Vàng" {
		t.Fatalf("accent-insensitive search = %+v, %v", items, err)
	}
	items, err = h.svc.Search(ctx, scope, SearchRequest{Kind: SearchSku, Q: "8935001234567"})
	if err != nil || len(items) != 1 || items[0].ID != target.Skus[0].Sku.ID {
		t.Fatalf("barcode search = %+v, %v", items, err)
	}
	items, err = h.svc.Search(ctx, scope, SearchRequest{Kind: SearchSku, Q: "xanh"})
	if err != nil || len(items) != 3 {
		t.Fatalf("word search = %+v, %v", items, err)
	}
	items, err = h.svc.Search(ctx, scope, SearchRequest{Kind: SearchSku, Q: "tx-03"})
	if err != nil || len(items) == 0 || items[0].Code != "TX-03" {
		t.Errorf("code search = %+v, %v", items, err)
	}
	_, err = h.svc.Search(ctx, scope, SearchRequest{Kind: "product"})
	wantField(t, err, "type", "invalid")

	// Inactive SKUs leave the picker.
	inactive := StatusInactive
	if _, err := h.svc.UpdateSku(ctx, a, target.Skus[0].Sku.ID, target.Skus[0].Sku.Version, SkuPatch{Status: &inactive}); err != nil {
		t.Fatal(err)
	}
	if items, _ := h.svc.Search(ctx, scope, SearchRequest{Kind: SearchSku, Q: "8935001234567"}); len(items) != 0 {
		t.Errorf("inactive sku found: %+v", items)
	}
	// Other tenants never see these SKUs.
	if items, _ := h.svc.Search(ctx, scopeOfActor(h.tenant(t)), SearchRequest{Kind: SearchSku, Q: "banh"}); len(items) != 0 {
		t.Errorf("cross-tenant search = %+v", items)
	}
}

// The party tables belong to the partner domain; rows are written directly so the catalog search
// can be tested on its own.
func TestPartySearchMatchesPartiesWithoutPhoneOrTaxCode(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	lan, xanh := ids.New(), ids.New()
	testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error {
		group := ids.New()
		_, err := q.ExecContext(ctx, `
			INSERT INTO customer_groups (id, tenant_id, name, name_key, base_price_kind, created_at, updated_at)
			VALUES ($1, $2, 'Khách lẻ', 'khach le', 'retail', $3, $3)`, group, a.TenantID, testkit.Epoch)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `
			INSERT INTO parties (id, tenant_id, kind, name, name_key, tax_code, search_norm, created_at, updated_at)
			VALUES ($1, $3, 'person', 'Chị Lan', 'chi lan', NULL, 'chi lan', $4, $4),
			       ($2, $3, 'organization', 'Công ty Đậu Xanh', 'cong ty dau xanh', '0101234567', 'cong ty dau xanh 0101234567', $4, $4)`,
			lan, xanh, a.TenantID, testkit.Epoch)
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `
			INSERT INTO customer_profiles (tenant_id, party_id, customer_group_id, created_at, updated_at)
			VALUES ($1, $2, $4, $5, $5), ($1, $3, $4, $5, $5)`, a.TenantID, lan, xanh, group, testkit.Epoch)
		return err
	})
	scope := scopeOfActor(a)
	items, err := h.svc.Search(ctx, scope, SearchRequest{Kind: SearchCustomer, Q: "chi lan"})
	if err != nil || len(items) != 1 || items[0].ID != lan || items[0].Code != "" {
		t.Fatalf("name search = %+v, %v", items, err)
	}
	items, err = h.svc.Search(ctx, scope, SearchRequest{Kind: SearchCustomer, Q: "0101234567"})
	if err != nil || len(items) != 1 || items[0].ID != xanh {
		t.Fatalf("tax code search = %+v, %v", items, err)
	}
	if items, err := h.svc.Search(ctx, scope, SearchRequest{Kind: SearchSupplier, Q: "chi lan"}); err != nil || len(items) != 0 {
		t.Errorf("supplier search finds customers: %+v, %v", items, err)
	}
}

func TestSkuUnitsBarcodesAndDeleteRules(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	p := h.product(t, a, "Sữa hộp", "SH-1")
	sku := p.Skus[0].Sku
	thung := h.unitID(t, a, "thùng")

	_, err := h.svc.AddSkuUnit(ctx, a, sku.ID, SkuUnitInput{UnitID: thung, Factor: ptr2("0")})
	wantCode(t, err, "factor_not_positive")
	u, err := h.svc.AddSkuUnit(ctx, a, sku.ID, SkuUnitInput{UnitID: thung, Factor: ptr2("48"), IsDefaultPurchase: ptr2(true)})
	if err != nil || !u.Factor.Equal(money.MustFactor("48")) || !u.IsDefaultPurchase {
		t.Fatalf("add unit = %+v, %v", u, err)
	}
	if _, err := h.svc.AddBarcode(ctx, a, sku.ID, BarcodeInput{Code: "18935001234564", UnitID: &thung}); err != nil {
		t.Fatal(err)
	}
	detail, err := h.svc.Sku(ctx, scopeOfActor(a), sku.ID)
	if err != nil || len(detail.Units) != 2 || len(detail.Barcodes) != 1 {
		t.Fatalf("sku = %+v, %v", detail, err)
	}
	if _, err := h.svc.UpdateSkuUnit(ctx, a, sku.ID, thung, SkuUnitInput{Factor: ptr2("24")}); err != nil {
		t.Errorf("factors may change: %v", err)
	}
	wantCode(t, h.svc.DeleteSkuUnit(ctx, a, sku.ID, thung), "unit_in_use")
	if err := h.svc.DeleteBarcode(ctx, a, sku.ID, detail.Barcodes[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DeleteSkuUnit(ctx, a, sku.ID, thung); err != nil {
		t.Errorf("delete unused unit: %v", err)
	}
	if err := h.svc.DeleteProduct(ctx, a, p.Product.ID); err != nil {
		t.Errorf("delete unused product: %v", err)
	}
	if _, err := h.svc.Product(ctx, scopeOfActor(a), p.Product.ID); apperr.KindOf(err) != apperr.NotFound {
		t.Errorf("deleted product = %v", err)
	}
}

func TestCategoriesFormATreeOfAtMostFourLevels(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	var parent *uuid.UUID
	var chain []Category
	for i := range MaxCategoryDepth {
		c, err := h.svc.CreateCategory(ctx, a, CategoryInput{Name: ptr2(fmt.Sprintf("Cấp %d", i+1)), ParentID: optOf(parent)})
		if err != nil {
			t.Fatal(err)
		}
		chain = append(chain, c)
		parent = &c.ID
	}
	_, err := h.svc.CreateCategory(ctx, a, CategoryInput{Name: ptr2("Cấp 5"), ParentID: Some(chain[3].ID)})
	wantCode(t, err, "category_too_deep")
	_, err = h.svc.UpdateCategory(ctx, a, chain[0].ID, CategoryInput{ParentID: Some(chain[2].ID)})
	wantCode(t, err, "category_cycle")
	wantCode(t, h.svc.DeleteCategory(ctx, a, chain[2].ID), "category_in_use")
	if err := h.svc.DeleteCategory(ctx, a, chain[3].ID); err != nil {
		t.Errorf("delete leaf: %v", err)
	}
	_, err = h.svc.CreateCategory(ctx, a, CategoryInput{Name: ptr2("cap 1")})
	wantCode(t, err, "name_taken")
}

func optOf[T any](v *T) Opt[T] {
	if v == nil {
		return Opt[T]{}
	}
	return Some(*v)
}

func TestMixGroupsAndQuickCreate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	cola, cam := h.product(t, a, "Nước ngọt Cola", "NG-1"), h.product(t, a, "Nước ngọt Cam", "NG-2")
	in := MixGroupInput{Name: "Nước ngọt lon", PackUnitID: h.unitID(t, a, "thùng"), UnitsPerPack: 24,
		PartUnitID: h.unitID(t, a, "lốc"), UnitsPerPart: 6, Items: []MixGroupItemDraft{
			{SkuID: cola.Skus[0].Sku.ID, FlavorLabel: "Cola", Active: true}, {SkuID: cam.Skus[0].Sku.ID, FlavorLabel: "Cam", Active: true}}}
	g, err := h.svc.CreateMixGroup(ctx, a, in)
	if err != nil || g.PieceUnitID != h.unitID(t, a, "hộp") || len(g.Items) != 2 {
		t.Fatalf("mix group = %+v, %v", g, err)
	}
	_, err = h.svc.CreateMixGroup(ctx, a, in)
	wantMessage(t, err, "mix_group_name_taken", "Tên nhóm mix đã tồn tại.")
	in.Name, in.UnitsPerPack = "Khác", 25
	_, err = h.svc.CreateMixGroup(ctx, a, in)
	wantMessage(t, err, "mix_group_pack_not_divisible", "1 thùng (25) phải chia hết cho 1 lốc (6).")
	// Flavor labels are searchable.
	if items, _ := h.svc.Search(ctx, scopeOfActor(a), SearchRequest{Kind: SearchSku, Q: "cola"}); len(items) != 1 {
		t.Errorf("flavor search = %+v", items)
	}
	off, err := h.svc.SetMixGroupStatus(ctx, a, g.ID, StatusInactive)
	if err != nil || off.Status != StatusInactive {
		t.Errorf("deactivate = %+v, %v", off, err)
	}
	if err := h.svc.DeleteMixGroup(ctx, a, g.ID); err != nil {
		t.Errorf("delete unused group: %v", err)
	}

	price := money.MustMoney("480000")
	sale := money.MustMoney("12000")
	thung := h.unitID(t, a, "thùng")
	res, err := h.svc.QuickCreateProduct(ctx, a, QuickCreateInput{Name: "Bia lon 330ml", BaseUnitID: h.unitID(t, a, "chai"),
		PurchaseUnitID: &thung, Factor: ptr2("24"), PurchasePrice: &price, SalePrice: &sale})
	if err != nil {
		t.Fatal(err)
	}
	sku := res.Product.Skus[0]
	if sku.Sku.Code != "SP-1" || sku.Sku.RefCost == nil || sku.Sku.RefCost.String() != "20000" || len(sku.Units) != 2 || res.SalePriceSaved {
		t.Errorf("quick create = %+v / %+v, saved %v", sku.Sku, sku.Units, res.SalePriceSaved)
	}
	_, err = h.svc.QuickCreateProduct(ctx, a, QuickCreateInput{Name: "bia lon 330ML", BaseUnitID: h.unitID(t, a, "chai")})
	wantCode(t, err, "product_name_taken")
	_, err = h.svc.QuickCreateProduct(ctx, a, QuickCreateInput{Name: "Bia chai", BaseUnitID: h.unitID(t, a, "chai"), PurchaseUnitID: &thung})
	wantField(t, err, "factor", "required")
}

func TestTenantsCannotReachEachOthersCatalog(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, b := h.tenant(t), h.tenant(t)
	p := h.product(t, a, "Bánh quy", "BQ-1")
	sku := p.Skus[0].Sku
	sb := scopeOfActor(b)
	notFound := func(name string, err error) {
		t.Helper()
		if apperr.KindOf(err) != apperr.NotFound {
			t.Errorf("%s across tenants = %v, want not found", name, err)
		}
	}
	_, err := h.svc.Product(ctx, sb, p.Product.ID)
	notFound("product", err)
	_, err = h.svc.Sku(ctx, sb, sku.ID)
	notFound("sku", err)
	_, err = h.svc.UpdateProduct(ctx, b, p.Product.ID, p.Product.Version, ProductPatch{Name: ptr2("X")})
	notFound("update product", err)
	notFound("delete product", h.svc.DeleteProduct(ctx, b, p.Product.ID))
	_, err = h.svc.AddBarcode(ctx, b, sku.ID, BarcodeInput{Code: "8930000000001"})
	notFound("add barcode", err)
	_, err = h.svc.AddSkuUnit(ctx, b, sku.ID, SkuUnitInput{UnitID: h.unitID(t, b, "thùng"), Factor: ptr2("10")})
	notFound("add sku unit", err)
	// A unit of tenant A cannot be used by tenant B.
	_, err = h.svc.CreateProduct(ctx, b, ProductInput{Name: "Bánh", Sku: &SkuInput{Code: "B-1", BaseUnitID: h.unitID(t, a, "hộp")}})
	if apperr.KindOf(err) != apperr.Validation {
		t.Errorf("foreign unit = %v", err)
	}
	if list, err := h.svc.Products(ctx, sb, ProductFilter{Limit: 50}); err != nil || len(list) != 0 {
		t.Errorf("products of b = %d, %v", len(list), err)
	}
}

func TestSkuListCarriesScreenColumns(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	chai, thung := h.unitID(t, a, "chai"), h.unitID(t, a, "thùng")
	cat, err := h.svc.CreateCategory(ctx, a, CategoryInput{Name: ptr2("Đồ uống")})
	if err != nil {
		t.Fatal(err)
	}
	d, err := h.svc.CreateProduct(ctx, a, ProductInput{Name: "Nước suối 500ml", CategoryID: &cat.ID,
		Sku:       &SkuInput{Code: "NS-500", BaseUnitID: chai, TrackLot: true, TrackExpiry: true, Barcode: ptr2("8930000000500")},
		SkuExtras: SkuExtras{PurchaseUnitID: &thung, Factor: ptr2("24")}})
	if err != nil {
		t.Fatal(err)
	}
	if units := d.Skus[0].Units; len(units) != 2 {
		t.Fatalf("units after create = %+v, want base and purchase unit", units)
	}
	h.product(t, a, "Bánh quy", "BQ-1")

	list, err := h.svc.Skus(ctx, scopeOfActor(a), SkuFilter{CategoryID: &cat.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("category filter = %d rows, want 1", len(list))
	}
	row := list[0]
	if row.CategoryName == nil || *row.CategoryName != "Đồ uống" || row.Barcode == nil || *row.Barcode != "8930000000500" ||
		row.ProductType != ProductGoods || len(row.Units) != 2 || !row.TrackLot || row.InMixGroup || row.RetailPrice != nil {
		t.Errorf("sku row = %+v", row)
	}
	if !row.OnHand.IsZero() || row.LastPurchasePrice != nil {
		t.Errorf("stock columns of a new SKU = on hand %s, last purchase %v", row.OnHand, row.LastPurchasePrice)
	}
	all, err := h.svc.Skus(ctx, scopeOfActor(a), SkuFilter{Type: ProductGoods, Q: "banh"})
	if err != nil || len(all) != 1 || all[0].Code != "BQ-1" {
		t.Errorf("type + search filter = %+v, %v", all, err)
	}
	_, err = h.svc.Skus(ctx, scopeOfActor(a), SkuFilter{Type: "bogus"})
	wantField(t, err, "type", "invalid")
	_, err = h.svc.CreateProduct(ctx, a, ProductInput{Name: "Bia", Sku: &SkuInput{Code: "BIA", BaseUnitID: chai},
		SkuExtras: SkuExtras{PurchaseUnitID: &thung, Factor: ptr2("0")}})
	wantCode(t, err, "factor_not_positive")
}

// The mix group list says which documents use a group and which flavors they sold, so the screen
// locks what an edit can no longer change; the SKU list can be read for chosen ids.
func TestMixGroupsCarryTheirUsage(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	cola, cam := h.product(t, a, "Nước ngọt Cola", "NG-1"), h.product(t, a, "Nước ngọt Cam", "NG-2")
	h.product(t, a, "Bánh quy", "BQ-1")
	colaID, camID := cola.Skus[0].Sku.ID, cam.Skus[0].Sku.ID
	g, err := h.svc.CreateMixGroup(ctx, a, MixGroupInput{Name: "Nước ngọt lon", PackUnitID: h.unitID(t, a, "thùng"), UnitsPerPack: 24,
		PartUnitID: h.unitID(t, a, "lốc"), UnitsPerPart: 6, Items: []MixGroupItemDraft{
			{SkuID: colaID, FlavorLabel: "Cola", Active: true}, {SkuID: camID, FlavorLabel: "Cam", Active: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Usage) != 0 || g.UsedSkuIDs == nil || len(g.UsedSkuIDs) != 0 {
		t.Errorf("new group usage = %+v / %v, want none", g.Usage, g.UsedSkuIDs)
	}

	used := NewService(Deps{DB: h.d, Clock: testkit.Clock(), Audit: audit.NewRecorder(testkit.Clock()),
		Usage: usageStub{mixUsage: []UsageCount{{Label: "hóa đơn", Count: 2}}, mixUsedSkus: []uuid.UUID{colaID}}})
	list, err := used.MixGroups(ctx, scopeOfActor(a), "")
	if err != nil || len(list) != 1 {
		t.Fatalf("mix groups = %+v, %v", list, err)
	}
	if u := list[0].Usage; len(u) != 1 || u[0] != (UsageCount{Label: "hóa đơn", Count: 2}) || len(list[0].UsedSkuIDs) != 1 || list[0].UsedSkuIDs[0] != colaID {
		t.Errorf("listed usage = %+v / %v", u, list[0].UsedSkuIDs)
	}
	one, err := used.MixGroup(ctx, scopeOfActor(a), g.ID)
	if err != nil || len(one.Usage) != 1 || len(one.UsedSkuIDs) != 1 {
		t.Errorf("group usage = %+v / %v, %v", one.Usage, one.UsedSkuIDs, err)
	}

	skus, err := h.svc.Skus(ctx, scopeOfActor(a), SkuFilter{IDs: []uuid.UUID{camID, colaID}})
	if err != nil || len(skus) != 2 || skus[0].ID != colaID || skus[1].ID != camID {
		t.Errorf("skus by id = %+v, %v", skus, err)
	}
	_, err = h.svc.Skus(ctx, scopeOfActor(a), SkuFilter{IDs: make([]uuid.UUID, maxSkuIDs+1)})
	wantField(t, err, "id", "too_long")
}

// unitOf finds the unit of a SKU detail, failing the test when the SKU lacks it.
func unitOf(t *testing.T, d SkuDetail, unitID uuid.UUID) SkuUnit {
	t.Helper()
	for _, u := range d.Units {
		if u.UnitID == unitID {
			return u
		}
	}
	t.Fatalf("units %+v lack %s", d.Units, unitID)
	return SkuUnit{}
}

func TestSkuUnitTableSavesAtomically(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.tenant(t)
	cai, day, thung, loc := h.unitID(t, a, "cái"), h.unitID(t, a, "dây"), h.unitID(t, a, "thùng"), h.unitID(t, a, "lốc")

	_, err := h.svc.CreateProduct(ctx, a, ProductInput{Name: "Kẹo lỗi", Sku: &SkuInput{Code: "KEO-0", BaseUnitID: cai},
		SkuExtras: SkuExtras{Units: []UnitPriceInput{{UnitID: day, Factor: ptr2("50")}, {UnitID: day, Factor: ptr2("60")},
			{UnitID: cai, Factor: ptr2("1")}, {UnitID: thung, Factor: ptr2("0")}}}})
	for field, reason := range map[string]string{"sku.units[1].unit_id": "duplicate", "sku.units[2].unit_id": "is_base",
		"sku.units[3].factor": "not_positive"} {
		wantField(t, err, field, reason)
	}
	_, err = h.svc.CreateProduct(ctx, a, ProductInput{Name: "Kẹo lạ", Sku: &SkuInput{Code: "KEO-0", BaseUnitID: cai},
		SkuExtras: SkuExtras{Units: []UnitPriceInput{{UnitID: ids.New(), Factor: ptr2("50")}}}})
	wantField(t, err, "sku.units[0].unit_id", "unknown_unit")
	_, err = h.svc.CreateProduct(ctx, a, ProductInput{Name: "Kẹo đôi", Sku: &SkuInput{Code: "KEO-0", BaseUnitID: cai},
		SkuExtras: SkuExtras{PurchaseUnitID: &thung, Factor: ptr2("500"), Units: []UnitPriceInput{{UnitID: day, Factor: ptr2("50")}}}})
	wantField(t, err, "sku.units", "with_purchase_unit")
	if n := h.count(t, a, "products"); n != 0 {
		t.Fatalf("refused creates left %d products", n)
	}

	p, err := h.svc.CreateProduct(ctx, a, ProductInput{Name: "Kẹo sữa", Sku: &SkuInput{Code: "KEO-1", BaseUnitID: cai},
		SkuExtras: SkuExtras{Units: []UnitPriceInput{{UnitID: day, Factor: ptr2("50")}, {UnitID: thung, Factor: ptr2("500")}}}})
	if err != nil {
		t.Fatal(err)
	}
	sku := p.Skus[0]
	if len(sku.Units) != 3 || !unitOf(t, sku, thung).IsDefaultPurchase || unitOf(t, sku, day).IsDefaultPurchase ||
		!unitOf(t, sku, day).Factor.Equal(money.MustFactor("50")) {
		t.Fatalf("created units = %+v, want cái, dây×50 and thùng×500 bought by the thùng", sku.Units)
	}
	for _, u := range sku.Units {
		if u.LockedBy != nil {
			t.Errorf("fresh unit %s locked by %s", u.UnitName, *u.LockedBy)
		}
	}

	table := func(rows ...SkuUnitRow) SkuUnitsInput { return SkuUnitsInput{Units: rows} }
	dayRow := SkuUnitRow{UnitID: day, Factor: ptr2("50"), Active: true}
	thungRow := SkuUnitRow{UnitID: thung, Factor: ptr2("500"), Active: true}
	locRow := SkuUnitRow{UnitID: loc, Factor: ptr2("10"), Active: true}

	_, err = h.svc.ReplaceSkuUnits(ctx, a, sku.Sku.ID, sku.Sku.Version+1, table(dayRow, thungRow, locRow))
	if apperr.KindOf(err) != apperr.PreconditionFailed {
		t.Fatalf("stale version = %v, want precondition failed", err)
	}
	_, err = h.svc.ReplaceSkuUnits(ctx, a, sku.Sku.ID, sku.Sku.Version, table(dayRow, dayRow,
		SkuUnitRow{UnitID: cai, Factor: ptr2("1"), Active: true}, SkuUnitRow{UnitID: loc, Factor: ptr2("0"), Active: true}))
	for field, reason := range map[string]string{"units[1].unit_id": "duplicate", "units[2].unit_id": "is_base",
		"units[3].factor": "not_positive"} {
		wantField(t, err, field, reason)
	}

	d, err := h.svc.ReplaceSkuUnits(ctx, a, sku.Sku.ID, sku.Sku.Version, table(dayRow, thungRow, locRow))
	if err != nil || len(d.Units) != 4 || d.Sku.Version != sku.Sku.Version+1 || unitOf(t, d, loc).LockedBy != nil {
		t.Fatalf("add lốc = %+v, %v", d, err)
	}
	if _, err := h.svc.AddBarcode(ctx, a, sku.Sku.ID, BarcodeInput{Code: "8936000999018", UnitID: &thung}); err != nil {
		t.Fatal(err)
	}
	if d, err = h.svc.Sku(ctx, scopeOfActor(a), sku.Sku.ID); err != nil {
		t.Fatal(err)
	}
	if got := unitOf(t, d, thung).LockedBy; got == nil || *got != UnitLockedByBarcodes {
		t.Errorf("thùng with a barcode locked by %v, want barcodes", got)
	}

	// Deleting the thùng with its barcode refuses the whole table: the dây factor stays.
	_, err = h.svc.ReplaceSkuUnits(ctx, a, sku.Sku.ID, d.Sku.Version, table(SkuUnitRow{UnitID: day, Factor: ptr2("40"), Active: true}))
	wantCode(t, err, "unit_in_use")
	wantField(t, err, "units["+thung.String()+"]", UnitLockedByBarcodes)
	after, err := h.svc.Sku(ctx, scopeOfActor(a), sku.Sku.ID)
	if err != nil || after.Sku.Version != d.Sku.Version || len(after.Units) != 4 || !unitOf(t, after, day).Factor.Equal(money.MustFactor("50")) {
		t.Fatalf("after the refused table = %+v, %v", after, err)
	}

	// An unlocked unit is deleted; the default thùng goes inactive and dây becomes the purchase unit.
	thungRow.Active = false
	d, err = h.svc.ReplaceSkuUnits(ctx, a, sku.Sku.ID, d.Sku.Version, table(dayRow, thungRow))
	if err != nil || len(d.Units) != 3 {
		t.Fatalf("drop lốc, deactivate thùng = %+v, %v", d, err)
	}
	if th, dy := unitOf(t, d, thung), unitOf(t, d, day); th.Active || th.IsDefaultPurchase || !dy.IsDefaultPurchase {
		t.Errorf("thùng %+v, dây %+v, want dây the purchase unit", th, dy)
	}
	if _, err := h.svc.LookupCode(ctx, scopeOfActor(a), "8936000999018"); apperr.KindOf(err) != apperr.NotFound {
		t.Errorf("barcode of the inactive thùng = %v, want not found", err)
	}
	var events int
	testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error {
		return q.QueryRowxContext(ctx, `SELECT count(*) FROM audit_events WHERE action = 'sku.units_replace' AND entity_id = $1`,
			sku.Sku.ID).Scan(&events)
	})
	if events != 2 {
		t.Errorf("sku.units_replace events = %d, want one per saved table", events)
	}

	// Without units the dây goes back to base: the base unit is the purchase unit.
	dayRow.Active = false
	if d, err = h.svc.ReplaceSkuUnits(ctx, a, sku.Sku.ID, d.Sku.Version, table(dayRow, thungRow)); err != nil {
		t.Fatal(err)
	}
	if !unitOf(t, d, cai).IsDefaultPurchase || unitOf(t, d, day).IsDefaultPurchase {
		t.Errorf("all units inactive = %+v, want the base unit as purchase unit", d.Units)
	}

	// A unit added on its own moves the version, so a table read before it cannot delete it.
	if _, err := h.svc.AddSkuUnit(ctx, a, sku.Sku.ID, SkuUnitInput{UnitID: loc, Factor: ptr2("10")}); err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.ReplaceSkuUnits(ctx, a, sku.Sku.ID, d.Sku.Version, table(dayRow, thungRow))
	if apperr.KindOf(err) != apperr.PreconditionFailed {
		t.Errorf("table read before the lốc was added = %v, want precondition failed", err)
	}

	// Quick-create takes units too; a chosen purchase unit must be one of them.
	_, err = h.svc.QuickCreateProduct(ctx, a, QuickCreateInput{Name: "Kẹo dừa", BaseUnitID: cai, PurchaseUnitID: &loc,
		Units: []UnitPriceInput{{UnitID: day, Factor: ptr2("50")}}})
	wantField(t, err, "purchase_unit_id", "not_a_unit")
	price := money.MustMoney("200000")
	res, err := h.svc.QuickCreateProduct(ctx, a, QuickCreateInput{Name: "Kẹo dừa", BaseUnitID: cai, PurchasePrice: &price,
		Units: []UnitPriceInput{{UnitID: day, Factor: ptr2("50")}, {UnitID: thung, Factor: ptr2("500")}}})
	if err != nil {
		t.Fatal(err)
	}
	q := res.Product.Skus[0]
	if len(q.Units) != 3 || !unitOf(t, q, thung).IsDefaultPurchase || q.Sku.RefCost == nil || q.Sku.RefCost.String() != "400" {
		t.Errorf("quick create = %+v / %+v, want thùng bought at 200000 / 500", q.Sku, q.Units)
	}
}
