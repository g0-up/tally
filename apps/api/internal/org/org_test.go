package org

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
)

func TestSlugPolicyReason(t *testing.T) {
	p := NewSlugPolicy([]string{" Grafana ", "", "shop"})
	cases := map[string]string{
		"cua-hang-a":            "",
		"abc":                   "",
		"tap-hoa-co-ba-2":       "",
		strings.Repeat("a", 30): "",
		"ab":                    SlugInvalid, // too short
		strings.Repeat("a", 31): SlugInvalid, // too long
		"Cua-Hang":              SlugInvalid,
		"cửa-hàng":              SlugInvalid,
		"-abc":                  SlugInvalid,
		"abc-":                  SlugInvalid,
		"cua--hang":             SlugInvalid,
		"xn--cua-hng-bva":       SlugInvalid, // punycode
		"cua-hang-demo":         SlugInvalid, // kept for sandbox tenants
		"demo":                  "",          // only "-demo" is held back; "demo" itself is a plain label
		"cua_hang":              SlugInvalid,
		"www":                   SlugReserved,
		"admin":                 SlugReserved,
		"tally":                 SlugReserved,
		"grafana":               SlugReserved, // RESERVED_SLUGS, trimmed and lowercased
		"shop":                  SlugReserved,
		"api":                   SlugReserved,
		"staging":               SlugReserved,
	}
	for slug, want := range cases {
		if got := p.Reason(slug); got != want {
			t.Errorf("Reason(%q) = %q, want %q", slug, got, want)
		}
	}
	if err := p.Validate("www"); !apperr.Is(err, "slug_reserved") {
		t.Errorf("Validate(www) = %v", err)
	}
	if err := p.Validate("a"); !apperr.Is(err, "slug_invalid") {
		t.Errorf("Validate(a) = %v", err)
	}
	if err := p.Validate("cua-hang-a"); err != nil {
		t.Errorf("Validate(cua-hang-a) = %v", err)
	}
}

func TestSlugPolicyValidateSandbox(t *testing.T) {
	p := NewSlugPolicy(nil)
	if err := p.ValidateSandbox("demo", ""); err != nil {
		t.Errorf("standalone demo = %v", err)
	}
	if err := p.ValidateSandbox("cua-hang-a-demo", "cua-hang-a"); err != nil {
		t.Errorf("parent demo = %v", err)
	}
	for _, tc := range [][2]string{{"cua-hang-a", "cua-hang-a"}, {"other-demo", "cua-hang-a"}, {"cua-hang-a-demo", ""}} {
		if err := p.ValidateSandbox(tc[0], tc[1]); !apperr.Is(err, "slug_invalid") {
			t.Errorf("ValidateSandbox(%q, %q) = %v", tc[0], tc[1], err)
		}
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"Cửa hàng A", "Tạp hoá Cô Ba", strings.Repeat("ữ", maxNameLen)} {
		if err := ValidateName("display_name", ok); err != nil {
			t.Errorf("ValidateName(%q) = %v", ok, err)
		}
	}
	for v, reason := range map[string]string{
		"": "required", "   ": "required", "a\nb": "invalid", "a\x00b": "invalid", strings.Repeat("a", maxNameLen+1): "invalid",
	} {
		ae, ok := apperr.As(ValidateName("display_name", v))
		if !ok || ae.Fields["display_name"] != reason {
			t.Errorf("ValidateName(%q) = %v, want %s", v, ae, reason)
		}
	}
}

func TestProvisionerValidate(t *testing.T) {
	p := NewProvisioner(NewSlugPolicy(nil), nil, nil, Seeders{})
	parent := uuid.New()
	ok := []TenantSpec{
		{ID: uuid.New(), Slug: "cua-hang-a", DisplayName: "Cửa hàng A", LegalName: "Cửa hàng A"},
		{ID: uuid.New(), Slug: "demo", DisplayName: "Cửa hàng demo", LegalName: "Cửa hàng demo", IsSandbox: true},
		{ID: uuid.New(), Slug: "cua-hang-a-demo", DisplayName: "A (demo)", LegalName: "A (demo)", IsSandbox: true,
			SandboxOf: &parent, ParentSlug: "cua-hang-a"},
	}
	for _, spec := range ok {
		if err := p.validate(spec); err != nil {
			t.Errorf("validate(%s) = %v", spec.Slug, err)
		}
	}
	bad := []TenantSpec{
		{Slug: "cua-hang-a", DisplayName: "A", LegalName: "A"},                                                  // no id
		{ID: uuid.New(), Slug: "cua-hang-a", DisplayName: "", LegalName: "A"},                                   // no name
		{ID: uuid.New(), Slug: "x-demo", DisplayName: "A", LegalName: "A", SandboxOf: &parent},                  // parent on a real store
		{ID: uuid.New(), Slug: "a-demo", DisplayName: "A", LegalName: "A", IsSandbox: true, SandboxOf: &parent}, // parent id without slug
		{ID: uuid.New(), Slug: "www", DisplayName: "A", LegalName: "A"},                                         // reserved
	}
	for i, spec := range bad {
		if err := p.validate(spec); err == nil {
			t.Errorf("validate(bad[%d] %s) passed", i, spec.Slug)
		}
	}
}

func TestParseDateAndHash(t *testing.T) {
	d, err := parseDate("from", "2026-03-15")
	if err != nil || !d.Equal(time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("parseDate = %v, %v", d, err)
	}
	for _, bad := range []string{"15/03/2026", "2026-02-30", ""} {
		if _, err := parseDate("from", bad); err == nil {
			t.Errorf("parseDate(%q) passed", bad)
		}
	}
	h, err := parseHash("sha256", strings.Repeat("aB", 32))
	if err != nil || len(h) != 32 {
		t.Errorf("parseHash = %x, %v", h, err)
	}
	for _, bad := range []string{"", "zz", strings.Repeat("a", 62)} {
		ae, ok := apperr.As(func() error { _, err := parseHash("sha256", bad); return err }())
		if !ok || ae.Fields["sha256"] != "invalid_sha256" {
			t.Errorf("parseHash(%q) = %v", bad, ae)
		}
	}
}

func TestParseOptionalDate(t *testing.T) {
	if d, err := parseOptionalDate("locked_through", json.RawMessage(` null `)); err != nil || d != nil {
		t.Errorf("null = %v, %v; want nil", d, err)
	}
	if d, err := parseOptionalDate("locked_through", json.RawMessage(`"2026-03-09"`)); err != nil || d == nil || d.Format(time.DateOnly) != "2026-03-09" {
		t.Errorf("date = %v, %v", d, err)
	}
	for _, raw := range []string{`"2026-13-01"`, `20260309`, `""`, `{}`} {
		if _, err := parseOptionalDate("locked_through", json.RawMessage(raw)); err == nil {
			t.Errorf("%s accepted", raw)
		}
	}
}

func TestReopenRequestNeedsLockedThroughPresent(t *testing.T) {
	var req reopenRequest
	err := httpx.DecodeJSON([]byte(`{"locked_through":null,"reason":"x","exception_confirmation_id":"0192f0c4-1c2d-7a3b-8c4d-5e6f7a8b9c0d"}`), &req)
	if err != nil || string(req.LockedThrough) != "null" {
		t.Fatalf("explicit null = %q, %v", req.LockedThrough, err)
	}
	var missing reopenRequest
	err = httpx.DecodeJSON([]byte(`{"reason":"x","exception_confirmation_id":"0192f0c4-1c2d-7a3b-8c4d-5e6f7a8b9c0d"}`), &missing)
	if ae, ok := apperr.As(err); !ok || ae.Fields["locked_through"] == "" {
		t.Fatalf("missing locked_through = %v, want a field error", err)
	}
}

func TestAuditFilterOf(t *testing.T) {
	gin.SetMode(gin.TestMode)
	filter := func(query string) (AuditFilter, error) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/api/v1/audit-events?"+query, nil)
		return auditFilterOf(c)
	}
	id := uuid.New()
	f, err := filter("entity_type=tenant&entity_id=" + id.String() + "&action=+tenant.update+&from=2026-03-01&to=2026-03-15")
	if err != nil {
		t.Fatal(err)
	}
	if f.EntityType != "tenant" || f.EntityID != id || f.Action != "tenant.update" || f.ActorID != uuid.Nil ||
		f.From.Day() != 1 || f.To.Day() != 15 {
		t.Errorf("filter = %+v", f)
	}
	if f, err := filter("q=+HD-0042++"); err != nil || f.Query != "HD-0042" {
		t.Errorf("q filter = %+v, %v", f, err)
	}
	_, err = filter("q=" + strings.Repeat("ạ", AuditQueryMaxLen+1))
	if ae, ok := apperr.As(err); !ok || ae.Fields["q"] != "too_long" {
		t.Errorf("long q = %v", err)
	}
	if f, err := filter("q=" + strings.Repeat("ạ", AuditQueryMaxLen)); err != nil || f.Query == "" {
		t.Errorf("q of the longest length = %+v, %v", f, err)
	}
	_, err = filter("entity_id=nope&actor_id=1&from=yesterday")
	ae, ok := apperr.As(err)
	if !ok || ae.Fields["entity_id"] != "invalid_uuid" || ae.Fields["actor_id"] != "invalid_uuid" || ae.Fields["from"] != "invalid_date" {
		t.Errorf("invalid filter = %v", ae)
	}
}

func TestTenantPatchNormalise(t *testing.T) {
	s := func(v string) *string { return &v }
	p := TenantPatch{DisplayName: s("  Cửa hàng B "), TaxCode: s("0101234567-001"), Phone: s("0901 234 567"), Timezone: s("Asia/Ho_Chi_Minh")}
	if err := p.normalise(); err != nil || *p.DisplayName != "Cửa hàng B" {
		t.Fatalf("normalise = %v (%q)", err, *p.DisplayName)
	}
	bad := TenantPatch{LegalName: s(""), TaxCode: s("123"), Phone: s("call me"), Timezone: s("Mars/Base"), Address: s("a\x00")}
	ae, ok := apperr.As(bad.normalise())
	if !ok {
		t.Fatal("invalid patch passed")
	}
	for field, want := range map[string]string{"legal_name": "required", "tax_code": "invalid", "phone": "invalid", "timezone": "invalid", "address": "invalid"} {
		if ae.Fields[field] != want {
			t.Errorf("%s = %q, want %q", field, ae.Fields[field], want)
		}
	}
	empty := TenantPatch{TaxCode: s(""), Phone: s("")}
	if err := empty.normalise(); err != nil {
		t.Errorf("clearing optional fields = %v", err)
	}
}

func TestSettingsPatchValidate(t *testing.T) {
	i := func(v int) *int { return &v }
	s := func(v string) *string { return &v }
	neg := money.MustMoney("-1")
	if err := (SettingsPatch{RoundingMode: s("document"), DefaultPaymentTermsDays: i(30), OfflinePOSMaxHours: i(72)}).validate(); err != nil {
		t.Errorf("valid patch = %v", err)
	}
	ae, ok := apperr.As(SettingsPatch{
		RoundingMode: s("bankers"), DefaultPaymentTermsDays: i(-1), ExpiryWarningDays: i(366), OfflinePOSMaxAmount: &neg,
		OfflinePOSMaxHours: i(73),
	}.validate())
	if !ok || len(ae.Fields) != 5 {
		t.Errorf("invalid patch = %v", ae)
	}
}

func TestNewAttachmentValidate(t *testing.T) {
	owners := AttachmentOwners{"receipt": func(context.Context, db.Querier, uuid.UUID) (bool, error) { return true, nil }}
	ok := NewAttachment{OwnerType: " receipt ", OwnerID: uuid.New(), FileName: " hoá đơn.pdf ", SHA256: make([]byte, 32)}
	if err := ok.validate(owners); err != nil || ok.OwnerType != "receipt" || ok.FileName != "hoá đơn.pdf" {
		t.Errorf("valid attachment = %v (%+v)", err, ok)
	}
	bad := NewAttachment{OwnerType: "Receipt", FileName: "../x", SHA256: make([]byte, 31)}
	ae, isErr := apperr.As(bad.validate(owners))
	if !isErr || len(ae.Fields) != 4 || ae.Fields["owner_type"] != "invalid" {
		t.Errorf("invalid attachment = %v", ae)
	}
	// A well-formed type no domain registered is refused like a malformed one, and so is every
	// type when nothing is registered.
	for _, reg := range []AttachmentOwners{owners, nil} {
		unknown := NewAttachment{OwnerType: "purchase_receipt", OwnerID: uuid.New(), FileName: "a.pdf", SHA256: make([]byte, 32)}
		if ae, isErr := apperr.As(unknown.validate(reg)); !isErr || len(ae.Fields) != 1 || ae.Fields["owner_type"] != "invalid" {
			t.Errorf("unregistered owner type with %d owners = %v", len(reg), ae)
		}
	}
	for name, want := range map[string]bool{"a.png": true, "": false, ".": false, "..": false, "a/b": false, "a\\b": false, "a\nb": false} {
		if validFileName(name) != want {
			t.Errorf("validFileName(%q) != %v", name, want)
		}
	}
	if mediaType("image/png; charset=binary") != "image/png" || mediaType(";;") != "" {
		t.Error("mediaType must strip parameters and reject garbage")
	}
}

func TestRegisterNeedsEveryGroup(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Register without route groups must panic")
		}
	}()
	NewHandler(nil).Register(Routes{})
}

// recordingSeeder notes the order seeders run in.
type recordingSeeder struct{ calls *[]string }

func (r recordingSeeder) note(name string) error { *r.calls = append(*r.calls, name); return nil }
func (r recordingSeeder) SeedColors(context.Context, db.Querier, SeedContext) error {
	return r.note("colors")
}
func (r recordingSeeder) SeedUnits(context.Context, db.Querier, SeedContext) error {
	return r.note("units")
}
func (r recordingSeeder) SeedTaxRates(context.Context, db.Querier, SeedContext) error {
	return r.note("tax_rates")
}
func (r recordingSeeder) SeedAttributeSets(context.Context, db.Querier, SeedContext) error {
	return r.note("attribute_sets")
}
func (r recordingSeeder) SeedPriceLists(context.Context, db.Querier, SeedContext) error {
	return r.note("price_lists")
}
func (r recordingSeeder) SeedPartnerDefaults(context.Context, db.Querier, SeedContext) error {
	return r.note("partners")
}
func (r recordingSeeder) SeedFunds(context.Context, db.Querier, SeedContext) error {
	return r.note("funds")
}
func (r recordingSeeder) SeedReasons(context.Context, db.Querier, SeedContext) error {
	return r.note("reasons")
}
func (r recordingSeeder) SeedDemoSample(context.Context, db.Querier, SeedContext) error {
	return r.note("demo_sample")
}

func TestSeedersRunInDependencyOrder(t *testing.T) {
	var calls []string
	r := recordingSeeder{calls: &calls}
	s := Seeders{Colors: r, Units: r, TaxRates: r, AttributeSets: r, PriceLists: r, Partners: r, Funds: r, Reasons: r, DemoSample: r}
	if err := s.run(context.Background(), nil, SeedContext{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"colors", "units", "tax_rates", "attribute_sets", "price_lists", "partners", "funds", "reasons"}
	if !slices.Equal(calls, want) {
		t.Errorf("real tenant seeders = %v", calls)
	}
	calls = nil
	if err := s.run(context.Background(), nil, SeedContext{IsSandbox: true}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, append(want, "demo_sample")) {
		t.Errorf("sandbox seeders = %v", calls)
	}
	if err := (Seeders{}).run(context.Background(), nil, SeedContext{IsSandbox: true}); err != nil {
		t.Errorf("no seeders = %v", err)
	}
}
