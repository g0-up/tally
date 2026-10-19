package partner

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/textnorm"
)

// SeedContext is what the partner seeders need of a tenant being provisioned. They run inside
// the provisioning tenant transaction and are idempotent: a retried provisioning runs them again.
type SeedContext struct {
	TenantID uuid.UUID
	Now      time.Time
	// Actor is who provisions the tenant, for the audit trail of the sample records.
	Actor audit.Actor
}

// Seeder creates the default customer groups of a new tenant and the sample partners of a
// sandbox.
type Seeder struct {
	svc *Service
}

// NewSeeder returns the seeder of svc's partners.
func NewSeeder(svc *Service) *Seeder { return &Seeder{svc: svc} }

// Built-in customer groups: walk-in customers buy at retail, wholesale customers at wholesale.
var defaultGroups = []struct{ name, kind string }{
	{"Khách lẻ", PriceRetail},
	{"Khách sỉ", PriceWholesale},
}

// SeedPartnerDefaults creates the built-in customer groups.
func (s *Seeder) SeedPartnerDefaults(ctx context.Context, q db.Querier, sc SeedContext) error {
	for _, g := range defaultGroups {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO customer_groups (id, tenant_id, name, name_key, base_price_kind, is_system, active, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, true, true, $6, $6)
			ON CONFLICT (tenant_id, name_key) DO NOTHING`,
			ids.New(), sc.TenantID, g.name, textnorm.Key(g.name), g.kind, sc.Now); err != nil {
			return fmt.Errorf("partner: seed customer group %s: %w", g.name, err)
		}
	}
	return nil
}

// demoGroups are the customer groups of the sample.
var demoGroups = []struct{ name, kind string }{
	{"Đại lý cấp 2", PriceWholesale},
	{"Doanh nghiệp quà tặng", PriceWholesale},
	{"Bán lẻ", PriceRetail},
}

// demoAddress is a delivery address of a sample customer.
type demoAddress struct{ label, line string }

// demoCustomer is a customer of the sample (the Tết gift shop of the prototype).
type demoCustomer struct {
	name, group, phone, address, taxCode string
	limit                                int64
	terms                                int
	addresses                            []demoAddress
}

var demoCustomers = []demoCustomer{
	{name: "Tạp hóa Bích Ngọc", group: "Đại lý cấp 2", limit: 80000000, terms: 15, phone: "0912 418 226",
		address: "22 ngõ 165 Cầu Giấy, Hà Nội"},
	{name: "Cửa hàng Thanh Hương", group: "Đại lý cấp 2", limit: 50000000, terms: 30, phone: "0983 552 101",
		address: "140 Phùng Hưng, Hà Đông, Hà Nội"},
	{name: "Công ty TNHH Minh An", group: "Doanh nghiệp quà tặng", limit: 200000000, terms: 30, phone: "024 3795 6612",
		address: "Tầng 9, 52 Duy Tân, Cầu Giấy, Hà Nội", taxCode: "0109 245 781",
		addresses: []demoAddress{
			{"Văn phòng Minh An – Duy Tân", "Tầng 9, 52 Duy Tân, Cầu Giấy, Hà Nội"},
			{"Kho Minh An – Long Biên", "12 Ngọc Lâm, Long Biên, Hà Nội"},
		}},
	{name: "Siêu thị mini Hoa Mai", group: "Bán lẻ", limit: 60000000, terms: 15, phone: "0904 771 365",
		address: "7 Nguyễn Khuyến, Hà Đông, Hà Nội"},
	{name: "Văn phòng Luật Tâm Đức", group: "Doanh nghiệp quà tặng", limit: 0, terms: 0, phone: "024 3562 1180",
		address: "18 Lý Thường Kiệt, Hoàn Kiếm, Hà Nội"},
}

// demoSupplier is a supplier of the sample; asCustomer gives it a customer profile too.
type demoSupplier struct {
	name, phone, address, goods string
	terms                       int
	asCustomer                  *demoCustomer
}

var demoSuppliers = []demoSupplier{
	{name: "Công ty Phân phối Phú Thịnh", terms: 30, phone: "024 3863 2299", address: "KCN Quang Minh, Mê Linh, Hà Nội",
		goods: "Bánh quy bơ, chocolate, kẹo dừa"},
	{name: "Vang Lâm Viên", terms: 15, phone: "0263 382 1468", address: "Phường 7, Đà Lạt, Lâm Đồng",
		goods: "Rượu vang Đà Lạt"},
	{name: "Hạt điều Bình Phước Xanh", terms: 20, phone: "0271 388 2140", address: "Đồng Xoài, Bình Phước",
		goods: "Hạt điều rang muối"},
	{name: "Bao bì Hồng Phát", terms: 7, phone: "0936 210 774", address: "Ninh Hiệp, Gia Lâm, Hà Nội",
		goods:      "Giỏ quà, nơ, túi vải nhung",
		asCustomer: &demoCustomer{group: "Khách sỉ", limit: 20000000}},
}

// SeedDemoSample creates the sample customers and suppliers of a sandbox. It does nothing when
// the tenant already has partners, so a retried provisioning does not duplicate them.
func (s *Seeder) SeedDemoSample(ctx context.Context, q db.Querier, sc SeedContext) error {
	has, err := exists(ctx, q, "sample partners", `SELECT EXISTS (SELECT 1 FROM parties)`)
	if err != nil || has {
		return err
	}
	svc := s.svc.within(q)
	groups := map[string]uuid.UUID{}
	for _, g := range demoGroups {
		name, kind := g.name, g.kind
		out, err := svc.CreateCustomerGroup(ctx, sc.Actor, GroupInput{Name: &name, BasePriceKind: &kind})
		if err != nil {
			return fmt.Errorf("partner: seed sample group %s: %w", g.name, err)
		}
		groups[textnorm.Key(g.name)] = out.ID
	}
	var system []CustomerGroup
	if system, err = svc.CustomerGroups(ctx, Scope{TenantID: sc.TenantID}, false); err != nil {
		return err
	}
	for _, g := range system {
		groups[g.NameKey] = g.ID
	}
	customerInput := func(c demoCustomer) (CustomerInput, error) {
		id, ok := groups[textnorm.Key(c.group)]
		if !ok {
			return CustomerInput{}, fmt.Errorf("partner: sample group %q is missing", c.group)
		}
		terms := c.terms
		return CustomerInput{CustomerGroupID: &id, CreditLimit: Some(money.NewMoneyFromInt(c.limit)), PaymentTermsDays: &terms}, nil
	}
	for _, c := range demoCustomers {
		in, err := customerInput(c)
		if err != nil {
			return err
		}
		in.PartyInput = PartyInput{Name: &c.name, Phone: Some(c.phone), Address: Some(c.address)}
		if c.taxCode != "" {
			in.TaxCode = Some(c.taxCode)
		}
		res, _, err := svc.CreateCustomer(ctx, sc.Actor, nil, in)
		if err != nil {
			return fmt.Errorf("partner: seed sample customer %s: %w", c.name, err)
		}
		for i, a := range c.addresses {
			label, line, isDefault := a.label, a.line, i == 0
			if _, err := svc.CreateAddress(ctx, sc.Actor, res.Customer.ID, AddressInput{Label: &label, AddressLine: &line, IsDefault: &isDefault}); err != nil {
				return fmt.Errorf("partner: seed sample address %s: %w", a.label, err)
			}
		}
	}
	for _, sp := range demoSuppliers {
		terms := sp.terms
		res, err := svc.CreateSupplier(ctx, sc.Actor, SupplierInput{
			PartyInput:       PartyInput{Name: &sp.name, Phone: Some(sp.phone), Address: Some(sp.address)},
			PaymentTermsDays: &terms, SuppliedGoodsNote: Some(sp.goods),
		})
		if err != nil {
			return fmt.Errorf("partner: seed sample supplier %s: %w", sp.name, err)
		}
		if sp.asCustomer == nil {
			continue
		}
		in, err := customerInput(*sp.asCustomer)
		if err != nil {
			return err
		}
		partyID := res.Supplier.ID
		in.PartyID = &partyID
		if _, _, err := svc.CreateCustomer(ctx, sc.Actor, nil, in); err != nil {
			return fmt.Errorf("partner: seed sample customer %s: %w", sp.name, err)
		}
	}
	return nil
}
