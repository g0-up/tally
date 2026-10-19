//go:build integration

package partner

import (
	"context"
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

func newHarness(t *testing.T) harness {
	t.Helper()
	clk := testkit.Clock()
	svc := NewService(Deps{DB: testkit.DB(t), Clock: clk, Audit: audit.NewRecorder(clk)})
	return harness{svc: svc, seeder: NewSeeder(svc), d: testkit.DB(t)}
}

func systemActor(tenantID uuid.UUID) audit.Actor {
	return audit.Actor{TenantID: tenantID, Type: audit.ActorSystem, Source: audit.SourceCLI}
}

// tenant creates a tenant with the built-in customer groups and returns an owner actor for it.
func (h harness) tenant(t *testing.T) audit.Actor {
	t.Helper()
	tn := testkit.NewTenant(t, h.d)
	testkit.InTenant(t, h.d, tn.ID, func(q db.Querier) error {
		return h.seeder.SeedPartnerDefaults(context.Background(), q,
			SeedContext{TenantID: tn.ID, Now: testkit.Epoch, Actor: systemActor(tn.ID)})
	})
	return audit.Actor{TenantID: tn.ID, Type: audit.ActorOwner, ID: ids.New(), Source: audit.SourceWeb}
}

func scopeOf(a audit.Actor) Scope { return Scope{TenantID: a.TenantID} }

func wantErr(t *testing.T, err error, kind apperr.Kind, code string) {
	t.Helper()
	if apperr.KindOf(err) != kind {
		t.Fatalf("err = %v, want kind %v", err, kind)
	}
	if code != "" && !apperr.Is(err, code) {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

func mustCustomer(t *testing.T, h harness, a audit.Actor, in CustomerInput) CustomerResult {
	t.Helper()
	res, _, err := h.svc.CreateCustomer(context.Background(), a, nil, in)
	if err != nil {
		t.Fatalf("create customer: %v", err)
	}
	return res
}

func mustSupplier(t *testing.T, h harness, a audit.Actor, in SupplierInput) SupplierResult {
	t.Helper()
	res, err := h.svc.CreateSupplier(context.Background(), a, in)
	if err != nil {
		t.Fatalf("create supplier: %v", err)
	}
	return res
}

func TestCustomerCreateDefaultsAndDuplicateWarnings(t *testing.T) {
	h := newHarness(t)
	a := h.tenant(t)
	ctx := context.Background()

	first := mustCustomer(t, h, a, CustomerInput{PartyInput: PartyInput{Name: ptr("Tạp hóa Bích Ngọc"), Phone: Some("0912 418 226")}})
	if first.Customer.GroupName != "Khách lẻ" || first.Customer.Profile.Status != StatusActive || len(first.Warnings) != 0 {
		t.Fatalf("first customer: %+v", first)
	}
	// Same phone written differently and the accent-free name: saved, with a warning.
	second := mustCustomer(t, h, a, CustomerInput{PartyInput: PartyInput{Name: ptr("tap hoa bich ngoc"), Phone: Some("0912.418.226")}})
	if len(second.Warnings) != 1 || second.Warnings[0].PartyID != first.Customer.ID ||
		len(second.Warnings[0].Matches) != 2 || !second.Warnings[0].IsCustomer {
		t.Fatalf("warnings = %+v", second.Warnings)
	}
	// A supplier with the same tax code warns too, across roles.
	mustCustomer(t, h, a, CustomerInput{PartyInput: PartyInput{Name: ptr("Công ty TNHH Minh An"), TaxCode: Some("0109 245 781")}})
	sup := mustSupplier(t, h, a, SupplierInput{PartyInput: PartyInput{Name: ptr("Minh An Trading"), TaxCode: Some("0109245781")}})
	if len(sup.Warnings) != 1 || sup.Warnings[0].Matches[0] != MatchTaxCode {
		t.Fatalf("supplier warnings = %+v", sup.Warnings)
	}

	// An inactive or unknown group is refused; a status cannot be given on create.
	_, _, err := h.svc.CreateCustomer(ctx, a, nil, CustomerInput{PartyInput: PartyInput{Name: ptr("X")}, CustomerGroupID: ptr(ids.New())})
	wantErr(t, err, apperr.Validation, "")
	_, _, err = h.svc.CreateCustomer(ctx, a, nil, CustomerInput{PartyInput: PartyInput{Name: ptr("X")}, Status: ptr(StatusInactive)})
	wantErr(t, err, apperr.Validation, "")
	g, err := h.svc.CreateCustomerGroup(ctx, a, GroupInput{Name: ptr("Ngừng"), BasePriceKind: ptr(PriceRetail), Active: ptr(false)})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = h.svc.CreateCustomer(ctx, a, nil, CustomerInput{PartyInput: PartyInput{Name: ptr("X")}, CustomerGroupID: &g.ID})
	wantErr(t, err, apperr.Validation, "")
}

func TestCustomerUpdateUsesTheVersion(t *testing.T) {
	h := newHarness(t)
	a := h.tenant(t)
	ctx := context.Background()
	c := mustCustomer(t, h, a, CustomerInput{PartyInput: PartyInput{Name: ptr("Cửa hàng Thanh Hương")}}).Customer

	limit := money.MustMoney("50000000")
	updated, err := h.svc.UpdateCustomer(ctx, a, c.ID, c.Version, CustomerInput{
		CreditLimit: Some(limit), PaymentTermsDays: ptr(30), Status: ptr(StatusInactive), PartyInput: PartyInput{Address: Some("140 Phùng Hưng")},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := updated.Customer
	if got.Version != c.Version+1 || got.Profile.Status != StatusInactive || got.Profile.PaymentTermsDays != 30 ||
		got.Profile.CreditLimit == nil || got.Profile.CreditLimit.Cmp(limit) != 0 || deref(got.Address) != "140 Phùng Hưng" {
		t.Fatalf("updated: %+v", got)
	}
	_, err = h.svc.UpdateCustomer(ctx, a, c.ID, c.Version, CustomerInput{PaymentTermsDays: ptr(7)})
	wantErr(t, err, apperr.PreconditionFailed, "")
	// Clearing the limit means no limit.
	cleared, err := h.svc.UpdateCustomer(ctx, a, c.ID, got.Version, CustomerInput{CreditLimit: Opt[money.Money]{Set: true}})
	if err != nil || cleared.Customer.Profile.CreditLimit != nil {
		t.Fatalf("cleared: %+v, %v", cleared, err)
	}
}

func TestPartyWithBothRoles(t *testing.T) {
	h := newHarness(t)
	a := h.tenant(t)
	ctx := context.Background()
	sup := mustSupplier(t, h, a, SupplierInput{
		PartyInput: PartyInput{Name: ptr("Bao bì Hồng Phát"), Phone: Some("0936 210 774")}, SuppliedGoodsNote: Some("Giỏ quà, nơ"),
	}).Supplier

	// Party fields cannot be given with party_id.
	_, _, err := h.svc.CreateCustomer(ctx, a, nil, CustomerInput{PartyID: &sup.ID, PartyInput: PartyInput{Name: ptr("Other")}})
	wantErr(t, err, apperr.Validation, "")

	cust := mustCustomer(t, h, a, CustomerInput{PartyID: &sup.ID}).Customer
	if cust.ID != sup.ID || !cust.IsSupplier || cust.Version != sup.Version+1 {
		t.Fatalf("customer of the supplier: %+v", cust)
	}
	_, _, err = h.svc.CreateCustomer(ctx, a, nil, CustomerInput{PartyID: &sup.ID})
	wantErr(t, err, apperr.Conflict, "already_customer")
	_, err = h.svc.CreateSupplier(ctx, a, SupplierInput{PartyID: &sup.ID})
	wantErr(t, err, apperr.Conflict, "already_supplier")

	again, err := h.svc.Supplier(ctx, scopeOf(a), sup.ID)
	if err != nil || !again.IsCustomer {
		t.Fatalf("supplier reads is_customer: %+v, %v", again, err)
	}
	// Dropping the supplier role keeps the party as a customer and drops the goods from search.
	if err := h.svc.DeleteSupplier(ctx, a, sup.ID); err != nil {
		t.Fatal(err)
	}
	left, err := h.svc.Customer(ctx, scopeOf(a), sup.ID)
	if err != nil || left.IsSupplier {
		t.Fatalf("customer after supplier delete: %+v, %v", left, err)
	}
	if found, _ := h.svc.Customers(ctx, scopeOf(a), CustomerFilter{Q: "gio qua", Limit: 10}); len(found) != 0 {
		t.Fatalf("goods note still searchable: %+v", found)
	}
	if err := h.svc.DeleteCustomer(ctx, a, sup.ID); err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.Customer(ctx, scopeOf(a), sup.ID)
	wantErr(t, err, apperr.NotFound, "")
}

func TestDeleteOnlyUnusedPartners(t *testing.T) {
	h := newHarness(t)
	a := h.tenant(t)
	ctx := context.Background()
	c := mustCustomer(t, h, a, CustomerInput{PartyInput: PartyInput{Name: ptr("Siêu thị mini Hoa Mai")}}).Customer
	s := mustSupplier(t, h, a, SupplierInput{PartyInput: PartyInput{Name: ptr("Vang Lâm Viên")}}).Supplier

	testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO customer_activities (id, tenant_id, party_id, kind, summary, created_at)
			VALUES ($1, $2, $3, 'note', 'Gọi chào Tết', $4)`, ids.New(), a.TenantID, c.ID, testkit.Epoch); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `
			INSERT INTO supplier_documents (id, tenant_id, supplier_party_id, document_no, document_date, goods_amount, total_amount,
			                                created_at, updated_at)
			VALUES ($1, $2, $3, 'HD-0001', '2026-03-15', 100, 100, $4, $4)`, ids.New(), a.TenantID, s.ID, testkit.Epoch)
		return err
	})
	wantErr(t, h.svc.DeleteCustomer(ctx, a, c.ID), apperr.Conflict, "customer_in_use")
	wantErr(t, h.svc.DeleteSupplier(ctx, a, s.ID), apperr.Conflict, "supplier_in_use")

	// A partner in use can still stop trading.
	stopped, err := h.svc.SetSupplierStatus(ctx, a, s.ID, StatusInactive)
	if err != nil || stopped.Profile.Status != StatusInactive || stopped.Version != s.Version+1 {
		t.Fatalf("stop: %+v, %v", stopped, err)
	}
	again, err := h.svc.SetSupplierStatus(ctx, a, s.ID, StatusInactive)
	if err != nil || again.Version != stopped.Version {
		t.Fatalf("stop is idempotent: %+v, %v", again, err)
	}
	active, _ := h.svc.Suppliers(ctx, scopeOf(a), SupplierFilter{Status: StatusActive, Limit: 10})
	if len(active) != 0 {
		t.Fatalf("stopped supplier listed as active: %+v", active)
	}
	if reopened, err := h.svc.SetSupplierStatus(ctx, a, s.ID, StatusActive); err != nil || reopened.Profile.Status != StatusActive {
		t.Fatalf("reopen: %+v, %v", reopened, err)
	}
}

func TestCustomerGroupRules(t *testing.T) {
	h := newHarness(t)
	a := h.tenant(t)
	ctx := context.Background()
	groups, err := h.svc.CustomerGroups(ctx, scopeOf(a), true)
	if err != nil || len(groups) != 2 {
		t.Fatalf("built-in groups: %+v, %v", groups, err)
	}
	var retail CustomerGroup
	for _, g := range groups {
		if g.BasePriceKind == PriceRetail {
			retail = g
		}
	}
	if !retail.IsSystem {
		t.Fatalf("retail group: %+v", retail)
	}
	_, err = h.svc.UpdateCustomerGroup(ctx, a, retail.ID, GroupInput{BasePriceKind: ptr(PriceWholesale)})
	wantErr(t, err, apperr.Forbidden, "read_only")
	_, err = h.svc.UpdateCustomerGroup(ctx, a, retail.ID, GroupInput{Active: ptr(false)})
	wantErr(t, err, apperr.Forbidden, "read_only")
	wantErr(t, h.svc.DeleteCustomerGroup(ctx, a, retail.ID), apperr.Forbidden, "read_only")
	if renamed, err := h.svc.UpdateCustomerGroup(ctx, a, retail.ID, GroupInput{Name: ptr("Khách vãng lai")}); err != nil || renamed.Name != "Khách vãng lai" {
		t.Fatalf("rename a built-in group: %+v, %v", renamed, err)
	}

	agents, err := h.svc.CreateCustomerGroup(ctx, a, GroupInput{Name: ptr("Đại lý cấp 2"), BasePriceKind: ptr(PriceWholesale)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.CreateCustomerGroup(ctx, a, GroupInput{Name: ptr("dai ly cap 2"), BasePriceKind: ptr(PriceRetail)})
	wantErr(t, err, apperr.Conflict, "name_taken")

	mustCustomer(t, h, a, CustomerInput{PartyInput: PartyInput{Name: ptr("Tạp hóa A")}, CustomerGroupID: &agents.ID})
	wantErr(t, h.svc.DeleteCustomerGroup(ctx, a, agents.ID), apperr.Conflict, "customer_group_in_use")
	if off, err := h.svc.UpdateCustomerGroup(ctx, a, agents.ID, GroupInput{Active: ptr(false)}); err != nil || off.Active {
		t.Fatalf("deactivate: %+v, %v", off, err)
	}
	if active, _ := h.svc.CustomerGroups(ctx, scopeOf(a), true); len(active) != 2 {
		t.Fatalf("active groups: %+v", active)
	}
	unused, err := h.svc.CreateCustomerGroup(ctx, a, GroupInput{Name: ptr("Tạm"), BasePriceKind: ptr(PriceRetail)})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DeleteCustomerGroup(ctx, a, unused.ID); err != nil {
		t.Fatal(err)
	}
}

func TestContactsAndAddressesKeepOnePrimary(t *testing.T) {
	h := newHarness(t)
	a := h.tenant(t)
	ctx := context.Background()
	c := mustCustomer(t, h, a, CustomerInput{PartyInput: PartyInput{Name: ptr("Công ty TNHH Minh An")}}).Customer

	first, err := h.svc.CreateContact(ctx, a, c.ID, ContactInput{Name: ptr("Chị Lan"), Email: Some("Lan@MinhAn.vn"), IsPrimary: ptr(true)})
	if err != nil || deref(first.Email) != "lan@minhan.vn" {
		t.Fatalf("first contact: %+v, %v", first, err)
	}
	second, err := h.svc.CreateContact(ctx, a, c.ID, ContactInput{Name: ptr("Anh Tuấn"), IsPrimary: ptr(true)})
	if err != nil {
		t.Fatal(err)
	}
	contacts, err := h.svc.Contacts(ctx, scopeOf(a), c.ID)
	if err != nil || len(contacts) != 2 || contacts[0].ID != second.ID || contacts[1].IsPrimary {
		t.Fatalf("contacts: %+v, %v", contacts, err)
	}
	if _, err := h.svc.UpdateContact(ctx, a, c.ID, first.ID, ContactInput{IsPrimary: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	if contacts, _ = h.svc.Contacts(ctx, scopeOf(a), c.ID); contacts[0].ID != first.ID || contacts[1].IsPrimary {
		t.Fatalf("primary switch: %+v", contacts)
	}
	_, err = h.svc.CreateContact(ctx, a, c.ID, ContactInput{Name: ptr("")})
	wantErr(t, err, apperr.Validation, "")

	office, err := h.svc.CreateAddress(ctx, a, c.ID, AddressInput{Label: ptr("Văn phòng"), AddressLine: ptr("52 Duy Tân"), IsDefault: ptr(true)})
	if err != nil {
		t.Fatal(err)
	}
	store, err := h.svc.CreateAddress(ctx, a, c.ID, AddressInput{Label: ptr("Kho"), AddressLine: ptr("12 Ngọc Lâm"), IsDefault: ptr(true)})
	if err != nil {
		t.Fatal(err)
	}
	addresses, err := h.svc.Addresses(ctx, scopeOf(a), c.ID)
	if err != nil || len(addresses) != 2 || addresses[0].ID != store.ID || addresses[1].IsDefault {
		t.Fatalf("addresses: %+v, %v", addresses, err)
	}
	if err := h.svc.DeleteAddress(ctx, a, c.ID, office.ID); err != nil {
		t.Fatal(err)
	}
	// A detail is addressed through its own party only.
	other := mustCustomer(t, h, a, CustomerInput{PartyInput: PartyInput{Name: ptr("Other")}}).Customer
	wantErr(t, h.svc.DeleteContact(ctx, a, other.ID, first.ID), apperr.NotFound, "")
	_, err = h.svc.UpdateAddress(ctx, a, other.ID, store.ID, AddressInput{Label: ptr("x")})
	wantErr(t, err, apperr.NotFound, "")

	// Deleting the customer takes its details with the party.
	if err := h.svc.DeleteCustomer(ctx, a, c.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSupplierSearchMatchesSuppliedGoods(t *testing.T) {
	h := newHarness(t)
	a := h.tenant(t)
	ctx := context.Background()
	mustSupplier(t, h, a, SupplierInput{PartyInput: PartyInput{Name: ptr("Hạt điều Bình Phước Xanh")}, SuppliedGoodsNote: Some("Hạt điều rang muối")})
	wine := mustSupplier(t, h, a, SupplierInput{PartyInput: PartyInput{Name: ptr("Lâm Viên"), Phone: Some("0263 382 1468")}, SuppliedGoodsNote: Some("Rượu vang Đà Lạt")}).Supplier

	for _, q := range []string{"ruou vang", "Rượu", "3821468", "lam vien"} {
		got, err := h.svc.Suppliers(ctx, scopeOf(a), SupplierFilter{Q: q, Limit: 10})
		if err != nil || len(got) != 1 || got[0].ID != wine.ID {
			t.Fatalf("q=%q: %+v, %v", q, got, err)
		}
	}
	// Editing the goods note moves the search text.
	if _, err := h.svc.UpdateSupplier(ctx, a, wine.ID, wine.Version, SupplierInput{SuppliedGoodsNote: Some("Nho khô")}); err != nil {
		t.Fatal(err)
	}
	if got, _ := h.svc.Suppliers(ctx, scopeOf(a), SupplierFilter{Q: "nho kho", Limit: 10}); len(got) != 1 {
		t.Fatalf("after note change: %+v", got)
	}
	if got, _ := h.svc.Suppliers(ctx, scopeOf(a), SupplierFilter{Q: "ruou", Limit: 10}); len(got) != 0 {
		t.Fatalf("old note still matches: %+v", got)
	}
}

func TestSeedersAreIdempotent(t *testing.T) {
	h := newHarness(t)
	tn := testkit.NewTenant(t, h.d)
	sc := SeedContext{TenantID: tn.ID, Now: testkit.Epoch, Actor: systemActor(tn.ID)}
	ctx := context.Background()
	count := func(table string) int {
		var n int
		testkit.InTenant(t, h.d, tn.ID, func(q db.Querier) error {
			return q.QueryRowxContext(ctx, `SELECT count(*) FROM `+table).Scan(&n)
		})
		return n
	}
	for range 2 {
		testkit.InTenant(t, h.d, tn.ID, func(q db.Querier) error {
			if err := h.seeder.SeedPartnerDefaults(ctx, q, sc); err != nil {
				return err
			}
			return h.seeder.SeedDemoSample(ctx, q, sc)
		})
	}
	if n := count("customer_groups"); n != 5 {
		t.Fatalf("groups = %d, want 2 built-in + 3 sample", n)
	}
	if n := count("customer_profiles"); n != 6 {
		t.Fatalf("customers = %d, want 6", n)
	}
	if n := count("supplier_profiles"); n != 4 {
		t.Fatalf("suppliers = %d, want 4", n)
	}
	if n := count("party_addresses WHERE is_default"); n != 1 {
		t.Fatalf("default addresses = %d, want 1", n)
	}
}

func TestPartnersAreTenantIsolated(t *testing.T) {
	h := newHarness(t)
	a, b := h.tenant(t), h.tenant(t)
	ctx := context.Background()
	c := mustCustomer(t, h, a, CustomerInput{PartyInput: PartyInput{Name: ptr("Tạp hóa Bích Ngọc"), Phone: Some("0912418226")}}).Customer
	s := mustSupplier(t, h, a, SupplierInput{PartyInput: PartyInput{Name: ptr("Vang Lâm Viên")}}).Supplier
	contact, err := h.svc.CreateContact(ctx, a, c.ID, ContactInput{Name: ptr("Chị Ngọc")})
	if err != nil {
		t.Fatal(err)
	}
	groups, _ := h.svc.CustomerGroups(ctx, scopeOf(a), false)

	_, err = h.svc.Customer(ctx, scopeOf(b), c.ID)
	wantErr(t, err, apperr.NotFound, "")
	_, err = h.svc.Supplier(ctx, scopeOf(b), s.ID)
	wantErr(t, err, apperr.NotFound, "")
	_, err = h.svc.UpdateCustomer(ctx, b, c.ID, c.Version, CustomerInput{PaymentTermsDays: ptr(1)})
	wantErr(t, err, apperr.NotFound, "")
	_, err = h.svc.SetSupplierStatus(ctx, b, s.ID, StatusInactive)
	wantErr(t, err, apperr.NotFound, "")
	wantErr(t, h.svc.DeleteCustomer(ctx, b, c.ID), apperr.NotFound, "")
	wantErr(t, h.svc.DeleteSupplier(ctx, b, s.ID), apperr.NotFound, "")
	_, err = h.svc.Contacts(ctx, scopeOf(b), c.ID)
	wantErr(t, err, apperr.NotFound, "")
	wantErr(t, h.svc.DeleteContact(ctx, b, c.ID, contact.ID), apperr.NotFound, "")
	_, _, err = h.svc.CreateCustomer(ctx, b, nil, CustomerInput{PartyID: &s.ID})
	wantErr(t, err, apperr.Validation, "")
	_, _, err = h.svc.CreateCustomer(ctx, b, nil, CustomerInput{PartyInput: PartyInput{Name: ptr("X")}, CustomerGroupID: &groups[0].ID})
	wantErr(t, err, apperr.Validation, "")
	wantErr(t, h.svc.DeleteCustomerGroup(ctx, b, groups[0].ID), apperr.NotFound, "")

	// Another tenant's identical partner raises no duplicate warning.
	res := mustCustomer(t, h, b, CustomerInput{PartyInput: PartyInput{Name: ptr("Tạp hóa Bích Ngọc"), Phone: Some("0912418226")}})
	if len(res.Warnings) != 0 {
		t.Fatalf("cross-tenant warning: %+v", res.Warnings)
	}
	if list, _ := h.svc.Customers(ctx, scopeOf(b), CustomerFilter{Limit: 10}); len(list) != 1 {
		t.Fatalf("tenant b customers: %+v", list)
	}
}

func TestSupplierListShowsTradingActivity(t *testing.T) {
	h := newHarness(t)
	a := h.tenant(t)
	ctx := context.Background()
	s := mustSupplier(t, h, a, SupplierInput{PartyInput: PartyInput{Name: ptr("Bia Sài Gòn")}}).Supplier
	if _, err := h.svc.CreateContact(ctx, a, s.ID, ContactInput{Name: ptr("Anh Tùng")}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.CreateContact(ctx, a, s.ID, ContactInput{Name: ptr("Chị Lan"), IsPrimary: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	list := func() SupplierActivity {
		t.Helper()
		got, err := h.svc.Suppliers(ctx, scopeOf(a), SupplierFilter{Limit: 10})
		if err != nil || len(got) != 1 || got[0].Activity == nil {
			t.Fatalf("suppliers = %+v, %v", got, err)
		}
		return *got[0].Activity
	}
	fresh := list()
	if fresh.ContactName == nil || *fresh.ContactName != "Chị Lan" || fresh.OrderCount != 0 || fresh.LastReceiptDate != nil ||
		!fresh.PayableOpen.IsZero() || fresh.HasOpenOrders || fresh.InUse {
		t.Errorf("new supplier activity = %+v", fresh)
	}
	testkit.InTenant(t, h.d, a.TenantID, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `
			INSERT INTO supplier_documents (id, tenant_id, supplier_party_id, document_no, document_date, goods_amount, total_amount,
			                                created_at, updated_at)
			VALUES ($1, $2, $3, 'HD-0002', '2026-03-15', 100, 100, $4, $4)`, ids.New(), a.TenantID, s.ID, testkit.Epoch)
		return err
	})
	if used := list(); !used.InUse || used.HasOpenOrders {
		t.Errorf("supplier with a document = %+v, want in use and stoppable", used)
	}
}
