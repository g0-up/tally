package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

// SeedContext is what the catalog seeders need of a tenant being provisioned. They run inside the
// provisioning tenant transaction and are idempotent: a retried provisioning runs them again.
type SeedContext struct {
	TenantID uuid.UUID
	Now      time.Time
	// Actor is who provisions the tenant, for the audit trail of the sample records.
	Actor audit.Actor
}

// Seeder creates the default catalog data of a new tenant and the sample catalog of a sandbox.
type Seeder struct {
	svc *Service
}

// NewSeeder returns the seeder of svc's catalog.
func NewSeeder(svc *Service) *Seeder { return &Seeder{svc: svc} }

type seedColor struct{ code, name, hex string }

// defaultColors are the seven fixed colors with the hex tokens of the UI.
var defaultColors = []seedColor{
	{"xanh", "Xanh", "#1f6fd1"}, {"do", "Đỏ", "#d62828"}, {"tim", "Tím", "#7b3fbf"}, {"vang", "Vàng", "#f2c200"},
	{"nau", "Nâu", "#8b5a2b"}, {"den", "Đen", "#1f1f1f"}, {"trang", "Trắng", "#ffffff"},
}

// defaultUnits are the base units of a new store.
var defaultUnits = []string{"cái", "hộp", "thùng", "dây", "lốc", "chai", "gói", "kg", "bó"}

type seedTaxRate struct{ name, rate string }

var defaultTaxRates = []seedTaxRate{{"Không chịu thuế", "0"}, {"VAT 8%", "8"}, {"VAT 10%", "10"}}

type seedAttribute struct {
	code, name string
	dataType   DataType
	unitLabel  string
	filterable bool
	searchable bool
}

var defaultAttributes = []seedAttribute{
	{code: "brand", name: "Thương hiệu", dataType: DataText, filterable: true, searchable: true},
	{code: "origin", name: "Xuất xứ", dataType: DataText, filterable: true},
	{code: "flavor", name: "Vị", dataType: DataSelect, filterable: true, searchable: true},
	{code: "net_weight", name: "Khối lượng tịnh", dataType: DataNumber, unitLabel: "g"},
	{code: "volume", name: "Dung tích", dataType: DataNumber, unitLabel: "ml"},
}

// defaultSets are the "mẫu ngành" with the codes of their attributes.
var defaultSets = []struct {
	name  string
	attrs []string
}{
	{"Bánh kẹo", []string{"brand", "origin", "flavor", "net_weight"}},
	{"Đồ uống", []string{"brand", "origin", "flavor", "volume"}},
	{"Giỏ quà", []string{"brand", "origin"}},
}

// SeedColors creates the seven fixed colors.
func (s *Seeder) SeedColors(ctx context.Context, q db.Querier, sc SeedContext) error {
	for i, c := range defaultColors {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO colors (id, tenant_id, code, name, hex, sort_order, is_system, active, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, true, true, $7) ON CONFLICT (tenant_id, code) DO NOTHING`,
			ids.New(), sc.TenantID, c.code, c.name, c.hex, i+1, sc.Now); err != nil {
			return fmt.Errorf("catalog: seed colors: %w", err)
		}
	}
	return nil
}

// SeedUnits creates the base units.
func (s *Seeder) SeedUnits(ctx context.Context, q db.Querier, sc SeedContext) error {
	return seedUnits(ctx, q, sc, defaultUnits)
}

func seedUnits(ctx context.Context, q db.Querier, sc SeedContext, names []string) error {
	for _, name := range names {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO units (id, tenant_id, name, name_key, active, created_at) VALUES ($1, $2, $3, $4, true, $5)
			ON CONFLICT (tenant_id, name_key) DO NOTHING`, ids.New(), sc.TenantID, name, nameKey(name), sc.Now); err != nil {
			return fmt.Errorf("catalog: seed units: %w", err)
		}
	}
	return nil
}

// SeedTaxRates creates the tax rates.
func (s *Seeder) SeedTaxRates(ctx context.Context, q db.Querier, sc SeedContext) error {
	for _, t := range defaultTaxRates {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO tax_rates (id, tenant_id, name, name_key, rate_percent, active, created_at) VALUES ($1, $2, $3, $4, $5, true, $6)
			ON CONFLICT (tenant_id, name_key) DO NOTHING`, ids.New(), sc.TenantID, t.name, nameKey(t.name), t.rate, sc.Now); err != nil {
			return fmt.Errorf("catalog: seed tax rates: %w", err)
		}
	}
	return nil
}

// SeedAttributeSets creates the base attributes and the attribute sets built on them.
func (s *Seeder) SeedAttributeSets(ctx context.Context, q db.Querier, sc SeedContext) error {
	for _, a := range defaultAttributes {
		var unit *string
		if a.unitLabel != "" {
			unit = &a.unitLabel
		}
		if _, err := q.ExecContext(ctx, `
			INSERT INTO attributes (id, tenant_id, code, name, data_type, unit_label, is_filterable, is_searchable, status, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'active', $9, $9) ON CONFLICT (tenant_id, code) DO NOTHING`,
			ids.New(), sc.TenantID, a.code, a.name, a.dataType, unit, a.filterable, a.searchable, sc.Now); err != nil {
			return fmt.Errorf("catalog: seed attributes: %w", err)
		}
	}
	for _, set := range defaultSets {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO attribute_sets (id, tenant_id, name, active, created_at, updated_at) VALUES ($1, $2, $3, true, $4, $4)
			ON CONFLICT (tenant_id, name) DO NOTHING`, ids.New(), sc.TenantID, set.name, sc.Now); err != nil {
			return fmt.Errorf("catalog: seed attribute sets: %w", err)
		}
		for i, code := range set.attrs {
			if _, err := q.ExecContext(ctx, `
				INSERT INTO attribute_set_items (tenant_id, attribute_set_id, attribute_id, is_required, sort_order)
				SELECT $1, s.id, a.id, false, $4 FROM attribute_sets s, attributes a WHERE s.name = $2 AND a.code = $3
				ON CONFLICT DO NOTHING`, sc.TenantID, set.name, code, (i+1)*10); err != nil {
				return fmt.Errorf("catalog: seed attribute set items: %w", err)
			}
		}
	}
	return nil
}

// demoUnits are the units the sample catalog needs beyond the base units.
var demoUnits = []string{"hũ", "túi", "bao", "lon"}

// demoCategory is a category of the sample catalog and the attribute set its products take.
type demoCategory struct{ name, set string }

var demoCategories = []demoCategory{
	{"Bánh", "Bánh kẹo"}, {"Kẹo", "Bánh kẹo"}, {"Hạt", "Bánh kẹo"}, {"Mứt", "Bánh kẹo"}, {"Trà", "Bánh kẹo"},
	{"Đồ uống", "Đồ uống"}, {"Sữa", "Đồ uống"}, {"Vật tư", ""},
}

type demoUnit struct {
	unit   string
	factor int
}

// demoProduct is a single-SKU product of the sample (the Tết gift shop of the prototype).
type demoProduct struct {
	code, name, category, base string
	units                      []demoUnit
	cost, minStock             int64
	tracked                    bool
	material                   bool
	attrs                      map[string]any
}

var demoProducts = []demoProduct{
	{"BQ454", "Bánh quy bơ hộp thiếc 454g", "Bánh", "hộp", []demoUnit{{"thùng", 12}}, 118000, 60, true, false, map[string]any{"origin": "Việt Nam", "net_weight": 454}},
	{"CK681", "Bánh cookies hộp 681g", "Bánh", "hộp", []demoUnit{{"thùng", 12}}, 146000, 40, true, false, map[string]any{"origin": "Việt Nam", "net_weight": 681}},
	{"KD350", "Kẹo dẻo trái cây gói 350g", "Kẹo", "gói", []demoUnit{{"thùng", 40}}, 27000, 80, true, false, map[string]any{"origin": "Việt Nam", "net_weight": 350}},
	{"HD500", "Hạt điều rang muối hũ 500g", "Hạt", "hũ", []demoUnit{{"thùng", 20}}, 168000, 60, true, false, map[string]any{"origin": "Bình Phước", "net_weight": 500}},
	{"HDU200", "Hạt dưa túi 200g", "Hạt", "túi", []demoUnit{{"bao", 50}}, 22000, 100, true, false, map[string]any{"origin": "Việt Nam", "net_weight": 200}},
	{"MUT500", "Mứt thập cẩm hộp 500g", "Mứt", "hộp", []demoUnit{{"thùng", 24}}, 56000, 60, true, false, map[string]any{"origin": "Việt Nam", "net_weight": 500}},
	{"TRA200", "Trà ô long hộp 200g", "Trà", "hộp", []demoUnit{{"thùng", 30}}, 80000, 40, true, false, map[string]any{"origin": "Lâm Đồng", "net_weight": 200}},
	{"VANG75", "Vang đỏ Đà Lạt 750ml", "Đồ uống", "chai", []demoUnit{{"thùng", 6}}, 104000, 48, true, false, map[string]any{"origin": "Lâm Đồng", "volume": 750}},
	{"BIA330", "Bia lon 330ml", "Đồ uống", "lon", []demoUnit{{"thùng", 24}}, 13900, 480, true, false, map[string]any{"origin": "Việt Nam", "volume": 330}},
	{"NGOT330", "Nước ngọt lon 330ml", "Đồ uống", "lon", []demoUnit{{"thùng", 24}}, 7600, 480, true, false, map[string]any{"origin": "Việt Nam", "volume": 330}},
	{"VT-HOP", "Hộp quà giấy đỏ cỡ lớn", "Vật tư", "cái", nil, 22000, 100, false, true, nil},
	{"VT-GIO", "Giỏ mây đan cỡ vừa", "Vật tư", "cái", []demoUnit{{"bó", 10}}, 38000, 80, false, true, nil},
	{"VT-NO", "Nơ ruy băng đỏ", "Vật tư", "cái", []demoUnit{{"túi", 50}}, 3000, 200, false, true, nil},
}

type demoFlavor struct{ code, label string }

// demoMixProduct is a product whose flavors are variants sold together in a mixed pack.
type demoMixProduct struct {
	name, category, base string
	units                []demoUnit
	cost, minPrice       int64
	minStock             int64
	volume               int
	barcodePrefix        string
	flavors              []demoFlavor
	group                string
	pack, part           demoUnit
}

var demoMixProducts = []demoMixProduct{
	{
		name: "Sữa tươi TH true MILK 180ml", category: "Sữa", base: "hộp", units: []demoUnit{{"dây", 4}, {"thùng", 48}},
		cost: 6900, minPrice: 7400, minStock: 96, volume: 180, barcodePrefix: "89352870100",
		flavors: []demoFlavor{{"TH-CD", "Có đường"}, {"TH-ID", "Ít đường"}, {"TH-KD", "Không đường"}, {"TH-DAU", "Dâu"}, {"TH-SCL", "Sô-cô-la"}},
		group:   "Sữa tươi TH true MILK 180ml", pack: demoUnit{"thùng", 48}, part: demoUnit{"dây", 4},
	},
	{
		name: "Nước ngọt lon 330ml theo vị", category: "Đồ uống", base: "lon", units: []demoUnit{{"lốc", 6}, {"thùng", 24}},
		cost: 7600, minPrice: 8200, minStock: 96, volume: 330, barcodePrefix: "89352870200",
		flavors: []demoFlavor{{"NG-COLA", "Cola"}, {"NG-CAM", "Cam"}, {"NG-XAXI", "Xá xị"}, {"NG-CHANH", "Chanh"}},
		group:   "Nước ngọt lon 330ml", pack: demoUnit{"thùng", 24}, part: demoUnit{"lốc", 6},
	},
}

// demoMinPrice is the floor price of a sample SKU: cost plus 8 %, rounded to hundreds.
func demoMinPrice(cost int64) money.Money {
	return mustMoney((cost*108 + 5000) / 10000 * 100)
}

func mustMoney(v int64) money.Money {
	m, err := money.NewMoney(fmt.Sprint(v))
	if err != nil {
		panic(err) // sample constants are always valid amounts
	}
	return m
}

func mustQty(v int64) money.Qty {
	m, err := money.NewQty(fmt.Sprint(v))
	if err != nil {
		panic(err) // sample constants are always valid quantities
	}
	return m
}

// SeedDemoSample fills a sandbox tenant with the sample catalog through the catalog use cases, so
// the sample obeys every rule real data does. It does nothing when the tenant has products.
func (s *Seeder) SeedDemoSample(ctx context.Context, q db.Querier, sc SeedContext) error {
	has, err := exists(ctx, q, `SELECT 1 FROM products`)
	if err != nil || has {
		return err
	}
	if err := seedUnits(ctx, q, sc, demoUnits); err != nil {
		return err
	}
	d := demo{svc: s.svc.within(q), q: q, actor: sc.Actor}
	if err := d.lookups(ctx); err != nil {
		return err
	}
	for i, p := range demoProducts {
		if err := d.product(ctx, i, p); err != nil {
			return fmt.Errorf("catalog: seed sample %s: %w", p.code, err)
		}
	}
	for _, p := range demoMixProducts {
		if err := d.mixProduct(ctx, p); err != nil {
			return fmt.Errorf("catalog: seed sample %s: %w", p.name, err)
		}
	}
	return nil
}

// demo creates the sample in the provisioning transaction.
type demo struct {
	svc        *Service
	q          db.Querier
	actor      audit.Actor
	units      map[string]uuid.UUID
	categories map[string]uuid.UUID
	flavor     uuid.UUID
}

func (d *demo) lookups(ctx context.Context) error {
	var units []Unit
	if err := sqlx.SelectContext(ctx, d.q, &units, `SELECT `+unitColumns+` FROM units`); err != nil {
		return fmt.Errorf("catalog: read units: %w", err)
	}
	d.units = map[string]uuid.UUID{}
	for _, u := range units {
		d.units[u.Name] = u.ID
	}
	var sets []struct {
		ID   uuid.UUID `db:"id"`
		Name string    `db:"name"`
	}
	if err := sqlx.SelectContext(ctx, d.q, &sets, `SELECT id, name FROM attribute_sets`); err != nil {
		return fmt.Errorf("catalog: read attribute sets: %w", err)
	}
	setID := map[string]uuid.UUID{}
	for _, st := range sets {
		setID[st.Name] = st.ID
	}
	d.categories = map[string]uuid.UUID{}
	for i, c := range demoCategories {
		in := CategoryInput{Name: &c.name, SortOrder: ptr((i + 1) * 10)}
		if id, ok := setID[c.set]; ok {
			in.AttributeSetID = Some(id)
		}
		cat, err := d.svc.CreateCategory(ctx, d.actor, in)
		if err != nil {
			return fmt.Errorf("catalog: seed sample category %s: %w", c.name, err)
		}
		d.categories[c.name] = cat.ID
	}
	return d.q.QueryRowxContext(ctx, `SELECT id FROM attributes WHERE code = 'flavor'`).Scan(&d.flavor)
}

func ptr[T any](v T) *T { return &v }

func (d *demo) unit(name string) uuid.UUID {
	id, ok := d.units[name]
	if !ok {
		panic("catalog: sample unit " + name + " is not seeded") // demoUnits and defaultUnits cover the sample
	}
	return id
}

func (d *demo) addUnits(ctx context.Context, skuID uuid.UUID, units []demoUnit) error {
	for _, u := range units {
		factor := fmt.Sprint(u.factor)
		if _, err := d.svc.AddSkuUnit(ctx, d.actor, skuID, SkuUnitInput{UnitID: d.unit(u.unit), Factor: &factor}); err != nil {
			return err
		}
	}
	return nil
}

func (d *demo) product(ctx context.Context, i int, p demoProduct) error {
	typ := ProductGoods
	if p.material {
		typ = ProductMaterial
	}
	attrs := json.RawMessage("{}")
	if p.attrs != nil {
		raw, err := json.Marshal(p.attrs)
		if err != nil {
			return err
		}
		attrs = raw
	}
	barcode := fmt.Sprintf("89350%05d%03d", 10000+i*137, i)
	cost, minStock := mustMoney(p.cost), mustQty(p.minStock)
	sku := &SkuInput{Code: p.code, BaseUnitID: d.unit(p.base), TrackLot: p.tracked, TrackExpiry: p.tracked, RefCost: &cost,
		MinStock: &minStock, Barcode: &barcode}
	if !p.material {
		floor := demoMinPrice(p.cost)
		sku.MinPrice = &floor
	}
	category := d.categories[p.category]
	detail, err := d.svc.CreateProduct(ctx, d.actor, ProductInput{Type: typ, Name: p.name, CategoryID: &category, Attributes: attrs, Sku: sku})
	if err != nil {
		return err
	}
	return d.addUnits(ctx, detail.Skus[0].Sku.ID, p.units)
}

func (d *demo) mixProduct(ctx context.Context, p demoMixProduct) error {
	optionOf := map[string]uuid.UUID{}
	for i, f := range p.flavors {
		o, err := d.svc.AddOption(ctx, d.actor, d.flavor, OptionInput{Label: &f.label, SortOrder: ptr((i + 1) * 10)})
		if err != nil {
			return err
		}
		optionOf[f.code] = o.ID
	}
	attrs, err := json.Marshal(map[string]any{"volume": p.volume})
	if err != nil {
		return err
	}
	category := d.categories[p.category]
	detail, err := d.svc.CreateProduct(ctx, d.actor, ProductInput{Name: p.name, CategoryID: &category, Attributes: attrs})
	if err != nil {
		return err
	}
	productID := detail.Product.ID
	if _, err := d.svc.ReplaceVariantAxes(ctx, d.actor, productID, []uuid.UUID{d.flavor}); err != nil {
		return err
	}
	req := VariantRequest{Codes: map[string]string{}}
	for _, f := range p.flavors {
		req.OptionIDs = append(req.OptionIDs, optionOf[f.code])
		req.Codes[VariantKeyOf([]uuid.UUID{optionOf[f.code]})] = f.code
	}
	cost, floor, minStock := mustMoney(p.cost), mustMoney(p.minPrice), mustQty(p.minStock)
	req.Defaults = &SkuInput{BaseUnitID: d.unit(p.base), TrackLot: true, TrackExpiry: true, RefCost: &cost, MinPrice: &floor, MinStock: &minStock}
	res, err := d.svc.GenerateVariants(ctx, d.actor, productID, req)
	if err != nil {
		return err
	}
	byCode := map[string]uuid.UUID{}
	for _, s := range res.Created {
		byCode[s.Sku.Code] = s.Sku.ID
	}
	group := MixGroupInput{Name: p.group, PackUnitID: d.unit(p.pack.unit), UnitsPerPack: p.pack.factor,
		PartUnitID: d.unit(p.part.unit), UnitsPerPart: p.part.factor, SourceProductID: &productID}
	for i, f := range p.flavors {
		skuID := byCode[f.code]
		if err := d.addUnits(ctx, skuID, p.units); err != nil {
			return err
		}
		if _, err := d.svc.AddBarcode(ctx, d.actor, skuID, BarcodeInput{Code: fmt.Sprintf("%s%d", p.barcodePrefix, i+1)}); err != nil {
			return err
		}
		group.Items = append(group.Items, MixGroupItemDraft{SkuID: skuID, FlavorLabel: f.label, Active: true})
	}
	_, err = d.svc.CreateMixGroup(ctx, d.actor, group)
	return err
}
