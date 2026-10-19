//go:build integration

package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/admin"
	"github.com/g0-up/tally/apps/api/internal/analytics"
	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/campaign"
	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/crm"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/featureflag"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/kits"
	"github.com/g0-up/tally/apps/api/internal/notification"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/printing"
	"github.com/g0-up/tally/apps/api/internal/procurement"
	"github.com/g0-up/tally/apps/api/internal/reporting"
	"github.com/g0-up/tally/apps/api/internal/saas"
	"github.com/g0-up/tally/apps/api/internal/saas/provider"
	"github.com/g0-up/tally/apps/api/internal/sales"
	"github.com/g0-up/tally/apps/api/internal/support"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

const (
	adminV1      = "/api/admin/v1"
	adminHost    = "admin.tally.test"
	storePlanID  = "00000000-0000-7000-8000-000000000002"
	testProvider = "testpay"
)

// controlStack is the HTTP stack wired the way the server wires it: the subscription gate,
// plan limits, billing with a signed webhook provider, support, onboarding and the admin API.
type controlStack struct {
	stack
	clk     *clock.Fixed
	domains app.Domains
	objects *memStore
	auth    *admin.AuthService
	webhook provider.SignedWebhook
}

func newControlStack(t *testing.T) controlStack {
	t.Helper()
	clk := testkit.Clock()
	capture := &captureMailer{}
	objects := newMemStore()
	hook, err := provider.NewSignedWebhook(testProvider, []byte(strings.Repeat("s", 32)))
	if err != nil {
		t.Fatal(err)
	}
	deps := identityDeps(t, capture, clk)
	domains := app.NewDomains(deps.DB, clk, deps.Audit, app.WithMailer(capture), app.WithSubscriptionGate(),
		app.WithPaymentProviders(hook), app.WithObjectStore(objects))
	prov := org.NewProvisioner(org.NewSlugPolicy(nil), deps.Audit, clk, domains.Seeders())
	deps.Provisioner = app.NewTenantProvisioner(prov)
	var hosts *middleware.TenantHostCache
	purge := func(slug string) {
		if hosts != nil {
			hosts.Purge(slug)
		}
	}
	deps.TenantCreated = purge
	idSvc, err := identity.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	domains.SaaS.SetClosureTokens(idSvc)

	keys, err := admin.NewKeys("test-session-hash-key", "test-encryption-key")
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := authn.NewHasher(testHasherParams)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := admin.NewAuthService(admin.AuthDeps{DB: deps.DB, Clock: clk, Hasher: hasher, Keys: keys,
		Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	sa := domains.SaaS
	supportSvc := support.NewService(support.Deps{DB: deps.DB, Clock: clk, Audit: deps.Audit, Consent: idSvc, SLA: sa})
	incidents, box := admin.NewIncidents(deps.DB, clk), admin.NewFeedbackBox(deps.DB, clk)
	adminHandler := admin.NewHandler(admin.HandlerDeps{
		Auth: auth, Directory: admin.NewDirectory(deps.DB, sa), SaaS: sa,
		Flags:     featureflag.NewWriter(deps.DB, clk, domains.Flags),
		Incidents: incidents, Partners: admin.NewPartners(deps.DB, clk), Feedback: box, Logger: slog.New(slog.DiscardHandler),
	})
	orgSvc := org.NewService(org.Deps{
		DB: deps.DB, Clock: clk, Audit: deps.Audit, Exceptions: exception.NewGuard(clk), Provisioner: prov, Objects: objects,
		AttachmentOwners: domains.AttachmentOwners(), AddressMoved: purge, Limits: domains.OrgLimits(),
	})
	entitlements := sa.Reader()
	a, err := app.New(app.Options{
		PublicBaseURL: testBaseURL,
		Logger:        slog.New(slog.DiscardHandler),
		Ports: app.Ports{
			Sessions: idSvc, TenantHosts: idSvc, Entitlements: entitlements, Modules: entitlements, AdminSessions: auth,
			Maintenance: entitlements,
		},
		CSRFKey:      []byte("0123456789abcdef0123456789abcdef"),
		Now:          clk.Now,
		APIRateLimit: testAPIRateLimit,
		Modules: app.Modules{
			Identity: identity.NewHandler(idSvc), Org: org.NewHandler(orgSvc), Reasons: org.NewReasonHandler(orgSvc),
			Catalog: catalog.NewHandler(domains.Catalog), Partner: partner.NewHandler(domains.Partner),
			Pricing: pricing.NewHandler(domains.Pricing), Inventory: inventory.NewHandler(domains.Inventory),
			Debt: debt.NewHandler(domains.Debt), Cash: cash.NewHandler(domains.Cash),
			Procurement: procurement.NewHandler(domains.Procurement), Sales: sales.NewHandler(domains.Sales),
			Kits: kits.NewHandler(domains.Kits), Campaign: campaign.NewHandler(domains.Campaign),
			CRM: crm.NewHandler(domains.CRM), Notification: notification.NewHandler(domains.Notification),
			Reporting: reporting.NewHandler(domains.Reporting), Analytics: analytics.NewHandler(domains.Analytics),
			Printing: printing.NewHandler(domains.Printing),

			SaaS: saas.NewHandler(sa), Support: support.NewHandler(supportSvc), SupportAdmin: supportSvc,
			Onboarding: org.NewOnboardingHandler(orgSvc), Feedback: admin.NewFeedbackHandler(box),
			Status: admin.NewStatusHandler(incidents), Admin: adminHandler,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts = a.TenantHostCache()
	return controlStack{
		stack: stack{handler: a.Handler(), identity: idSvc, mail: capture}, clk: clk, domains: domains, objects: objects,
		auth: auth, webhook: hook,
	}
}

// adminSignIn enrols a new platform admin and signs it in on the admin host with password and
// TOTP; the browser carries the admin session and CSRF token.
const adminPassword = "mat-khau-quan-tri-1"

// adminAccount creates an operator account; it returns its e-mail, TOTP secret and id.
func (s controlStack) adminAccount(t *testing.T) (string, []byte, uuid.UUID) {
	t.Helper()
	enrol, err := s.auth.CreateAdmin(context.Background(), admin.NewAdmin{
		Email: "admin-" + unique() + "@example.test", FullName: "Quản trị viên", Password: adminPassword,
	})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrol.Secret)
	if err != nil {
		t.Fatal(err)
	}
	return enrol.Email, secret, enrol.AdminID
}

// adminBrowser is a new client of the admin host.
func (s controlStack) adminBrowser(t *testing.T) *browser {
	t.Helper()
	b := s.browser(t, adminHost)
	b.hostContext()
	return b
}

// adminChallenge passes the password step from b and returns the TOTP challenge.
func adminChallenge(t *testing.T, b *browser, email string) string {
	t.Helper()
	rec := b.do(http.MethodPost, adminV1+"/auth/login", map[string]string{"email": email, "password": adminPassword})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login = %d %s", rec.Code, rec.Body)
	}
	login := testkit.DecodeJSON[struct {
		Status    string `json:"status"`
		Challenge string `json:"challenge"`
	}](t, rec)
	if login.Status != "mfa_required" || login.Challenge == "" {
		t.Fatalf("admin login = %+v, want a TOTP challenge", login)
	}
	return login.Challenge
}

func adminTOTP(b *browser, challenge, code string) *httptest.ResponseRecorder {
	return b.do(http.MethodPost, adminV1+"/auth/totp", map[string]string{"challenge": challenge, "code": code})
}

func (s controlStack) adminSignIn(t *testing.T) (*browser, uuid.UUID) {
	t.Helper()
	email, secret, adminID := s.adminAccount(t)
	b := s.adminBrowser(t)
	rec := adminTOTP(b, adminChallenge(t, b, email), authn.TOTPCode(secret, s.clk.Now()))
	if rec.Code != http.StatusOK {
		t.Fatalf("admin totp = %d %s", rec.Code, rec.Body)
	}
	b.csrf = testkit.DecodeJSON[struct {
		CSRFToken string `json:"csrf_token"`
	}](t, rec).CSRFToken
	return b, adminID
}

// adminSend makes an admin request that must succeed and decodes its answer.
func adminSend[T any](t *testing.T, b *browser, method, path string, body any) T {
	t.Helper()
	rec := b.do(method, adminV1+path, body)
	if rec.Code < 200 || rec.Code > 299 {
		t.Fatalf("admin %s %s = %d %s", method, path, rec.Code, rec.Body)
	}
	return testkit.DecodeJSON[T](t, rec)
}

func subscriptionOf(t *testing.T, b *browser) saas.SubscriptionViewDTO {
	t.Helper()
	return send[saas.SubscriptionViewDTO](t, b, http.MethodGet, "/subscription", nil)
}

func TestReadOnlySubscriptionRefusesPostingsButKeepsReadsAndExports(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	st := buildSalesStore(t, b)
	sku := st.newSku(t, b, "RO-1", "50000")
	st.receive(t, b, sku, nil, "5", "30000")
	checkout(t, b, st.cashSale("50000", saleLine(sku, "1", nil)))

	adm, _ := s.adminSignIn(t)
	sub := adminSend[saas.SubscriptionDTO](t, adm, http.MethodPost,
		"/tenants/"+st.tenant.String()+"/subscription:set-status", map[string]any{"status": saas.StatusReadOnly})
	if sub.Status != saas.StatusReadOnly || sub.ReadOnlyEndsOn == nil {
		t.Fatalf("subscription after set-status = %+v", sub)
	}

	sales := countRows(t, st.tenant, `SELECT count(*) FROM sales_documents`)
	expect(t, post(b, "/pos/checkout", st.cashSale("50000", saleLine(sku, "1", nil))), http.StatusForbidden, "subscription_read_only")
	expect(t, post(b, "/receipts", map[string]any{"supplier_party_id": st.supplier,
		"lines": []map[string]any{{"sku_id": sku, "qty": "1", "unit_price": "1000"}}}), http.StatusForbidden, "subscription_read_only")
	if n := countRows(t, st.tenant, `SELECT count(*) FROM sales_documents`); n != sales {
		t.Errorf("sales documents = %d after a refused checkout, want %d", n, sales)
	}

	exp := b.do(http.MethodPost, v1+"/data-exports", nil)
	expect(t, exp, http.StatusAccepted, "queued")
	if got := subscriptionOf(t, b); got.Subscription == nil || got.Subscription.Status != saas.StatusReadOnly ||
		got.Entitlements.CanPost || !got.Entitlements.CanExport {
		t.Errorf("subscription view = %+v, want read-only without posting and with exports", got)
	}
	expect(t, b.do(http.MethodGet, v1+"/reports/dashboard?period=month&date=2026-03-15", nil), http.StatusOK, "")
	reports := send[dataOf[struct {
		Key string `json:"key"`
	}]](t, b, http.MethodGet, "/reports", nil).Data
	if len(reports) == 0 {
		t.Fatal("empty report catalogue")
	}
	for _, r := range reports {
		rec := b.do(http.MethodGet, v1+"/reports/"+r.Key, nil)
		if rec.Code == http.StatusForbidden || rec.Code >= http.StatusInternalServerError {
			t.Errorf("GET /reports/%s on a read-only store = %d %s", r.Key, rec.Code, rec.Body)
		}
	}
	for _, path := range []string{"/products", "/customers", "/sales-documents", "/receipts", "/stock"} {
		if rec := b.do(http.MethodGet, v1+path, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s on a read-only store = %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestPaymentWebhookExtendsThePeriodOnce(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	tenant := tenantOf(t, b)
	before := subscriptionOf(t, b)
	if before.Subscription == nil || before.Subscription.Status != saas.StatusTrialing {
		t.Fatalf("new store subscription = %+v, want a trial", before)
	}
	if before.Entitlements.Modules[saas.ModuleAIQA] {
		t.Error("the trial must not include the AI Q&A")
	}

	adm, _ := s.adminSignIn(t)
	inv := adminSend[saas.InvoiceDTO](t, adm, http.MethodPost, "/saas-invoices",
		map[string]any{"tenant_id": tenant, "plan_id": storePlanID, "amount": "250000"})
	if inv.Status != saas.InvoiceOpen || inv.Purpose != saas.PurposeNewPeriod {
		t.Fatalf("issued invoice = %+v", inv)
	}
	listed := send[dataOf[saas.InvoiceDTO]](t, b, http.MethodGet, "/saas-invoices", nil).Data
	if len(listed) == 0 || listed[0].Number != inv.Number {
		t.Fatalf("store invoices = %+v, want %s first", listed, inv.Number)
	}

	callback := func(body []byte, signature string) *httptest.ResponseRecorder {
		t.Helper()
		return testkit.Do(t, s.handler, testkit.Request{
			Method: http.MethodPost, URL: "http://tally.test/api/v1/webhooks/saas-payments/" + testProvider,
			Body: json.RawMessage(body), Header: http.Header{provider.SignatureHeader: {signature}},
		})
	}
	eventID := "evt-" + unique()
	paid := []byte(`{"event_id":"` + eventID + `","invoice_number":"` + inv.Number + `","amount":"250000","reference":"FT` + unique() + `"}`)

	expect(t, callback(paid, "sha256=00"), http.StatusForbidden, saas.CodeSignatureInvalid)
	duplicate := func(rec *httptest.ResponseRecorder) bool {
		t.Helper()
		if rec.Code != http.StatusOK {
			t.Fatalf("webhook = %d %s", rec.Code, rec.Body)
		}
		return testkit.DecodeJSON[struct {
			Duplicate bool `json:"duplicate"`
		}](t, rec).Duplicate
	}
	if duplicate(callback(paid, s.webhook.Sign(paid))) {
		t.Fatal("the first callback was reported as a duplicate")
	}
	first := subscriptionOf(t, b)
	if first.Subscription.Status != saas.StatusActive || first.Subscription.CurrentPeriodEnd == before.Subscription.CurrentPeriodEnd ||
		first.Subscription.PlanID.String() != storePlanID {
		t.Fatalf("subscription after payment = %+v, before %+v", first.Subscription, before.Subscription)
	}

	s.clk.Advance(time.Hour)
	if !duplicate(callback(paid, s.webhook.Sign(paid))) {
		t.Error("the repeated callback was not reported as a duplicate")
	}
	// Another event for the paid invoice is a second payment: refused and reported once for a
	// refund, however often the provider retries it.
	otherID := eventID + "-again"
	other := []byte(strings.Replace(string(paid), eventID, otherID, 1))
	for range 2 {
		expect(t, callback(other, s.webhook.Sign(other)), http.StatusConflict, saas.CodeInvoiceNotOpen)
		if n := count(t, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND action = 'saas_invoice.payment_refused'
			AND entity_id = $2 AND actor_type = 'system' AND after->>'event_id' = $3 AND after->>'invoice_status' = 'paid'`,
			tenant, inv.ID, otherID); n != 1 {
			t.Errorf("refused second payment audits = %d, want 1", n)
		}
	}
	if n := count(t, `SELECT count(*) FROM tally.saas_payment_events WHERE provider = $1 AND event_id = $2`, testProvider, otherID); n != 0 {
		t.Errorf("saas_payment_events rows for the refused event = %d, want none", n)
	}
	if again := subscriptionOf(t, b); again.Subscription.CurrentPeriodEnd != first.Subscription.CurrentPeriodEnd ||
		again.Subscription.Version != first.Subscription.Version {
		t.Errorf("subscription after duplicates = %+v, want %+v", again.Subscription, first.Subscription)
	}
	events := count(t, `SELECT count(*) FROM tally.saas_payment_events WHERE provider = $1 AND event_id = $2`, testProvider, eventID)
	if events != 1 {
		t.Errorf("saas_payment_events rows for the event = %d, want 1", events)
	}
	if n := countRows(t, tenant, `SELECT count(*) FROM audit_events WHERE action = 'saas_invoice.paid' AND entity_id = $1`, inv.ID); n != 1 {
		t.Errorf("paid audit events = %d, want 1", n)
	}
}

func TestPlanSkuLimitRefusesTheSkuPastIt(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	tenant := tenantOf(t, b)
	units := unitsOf(t, b)
	skus := countRows(t, tenant, `SELECT count(*) FROM skus`)

	adm, _ := s.adminSignIn(t)
	plan := adminSend[saas.PlanDTO](t, adm, http.MethodPost, "/plans", map[string]any{
		"code": "limit_" + unique(), "name": "Gói giới hạn", "kind": saas.KindYearRound, "billing_period": saas.PeriodYear,
		"price": "0", "modules": []string{saas.ModulePOS}, "limits": map[string]int64{saas.LimitSKUs: int64(skus + 1)},
	})
	adminSend[saas.SubscriptionDTO](t, adm, http.MethodPost, "/tenants/"+tenant.String()+"/subscription:change",
		map[string]any{"plan_id": plan.ID})
	if got := send[saas.Entitlements](t, b, http.MethodGet, "/entitlements", nil); got.Limits[saas.LimitSKUs] != int64(skus+1) {
		t.Fatalf("entitlements = %+v, want the SKU limit %d", got, skus+1)
	}

	product := func(code string) *httptest.ResponseRecorder {
		return b.do(http.MethodPost, v1+"/products", map[string]any{
			"type": "goods", "name": "Hàng " + code, "sku": map[string]any{"code": code, "base_unit_id": units["cái"]},
		})
	}
	expect(t, product("GH-1"), http.StatusCreated, "")
	expect(t, product("GH-2"), http.StatusConflict, saas.CodePlanLimitReached)
	if n := countRows(t, tenant, `SELECT count(*) FROM skus`); n != skus+1 {
		t.Errorf("skus = %d, want %d", n, skus+1)
	}
}

func TestSupportSessionIsReadOnlyScopedAndAudited(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	st := buildSalesStore(t, b)
	st.newSku(t, b, "HT-1", "10000")
	adm, adminID := s.adminSignIn(t)

	ticket := send[support.TicketDTO](t, b, http.MethodPost, "/support-tickets",
		map[string]any{"subject": "Không in được hóa đơn", "body": "Máy in báo lỗi khi in hóa đơn bán lẻ."})
	expect(t, b.do(http.MethodPost, v1+"/support-sessions", map[string]any{
		"ticket_id": ticket.ID, "modules": []string{"catalog"}, "hours": 1,
	}), http.StatusConflict, support.CodeNoAssignedAdmin)
	adminSend[support.TicketDTO](t, adm, http.MethodPatch, "/support-tickets/"+ticket.ID.String(),
		map[string]any{"assigned_admin_id": adminID, "status": "in_progress"})

	grant := func() support.SessionDTO {
		t.Helper()
		return send[support.SessionDTO](t, b, http.MethodPost, "/support-sessions", map[string]any{
			"ticket_id": ticket.ID, "modules": []string{"catalog"}, "hours": 1,
		})
	}
	view := func(method, rest string) *httptest.ResponseRecorder {
		t.Helper()
		return adm.do(method, adminV1+"/tenants/"+st.tenant.String()+"/support-view/"+rest, nil)
	}
	accesses := func() int {
		return countRows(t, st.tenant, `SELECT count(*) FROM audit_events WHERE action = 'support_session.access' AND source = 'support'`)
	}

	expect(t, view(http.MethodGet, "catalog/products"), http.StatusForbidden, support.CodeSessionInactive)
	session := grant()
	if session.AdminID != adminID || session.Scope != "read_only" {
		t.Fatalf("granted session = %+v", session)
	}
	rec := view(http.MethodGet, "catalog/products")
	expect(t, rec, http.StatusOK, "")
	if got := testkit.DecodeJSON[dataOf[withID]](t, rec).Data; len(got) == 0 {
		t.Error("the support view lists no products")
	}
	expect(t, view(http.MethodGet, "catalog/skus"), http.StatusOK, "")
	if n := accesses(); n != 2 {
		t.Errorf("support access audit events = %d, want 2", n)
	}
	for _, method := range []string{http.MethodPost, http.MethodPatch, http.MethodDelete} {
		expect(t, view(method, "catalog/products"), http.StatusForbidden, "support_read_only")
	}
	expect(t, view(http.MethodGet, "partners/customers"), http.StatusForbidden, support.CodeSessionInactive)
	expect(t, view(http.MethodGet, "catalog/customers"), http.StatusForbidden, support.CodeSessionInactive)
	if n := accesses(); n != 2 {
		t.Errorf("refused reads were audited as accesses: %d, want 2", n)
	}

	t.Run("the database refuses writes in a support transaction", func(t *testing.T) {
		ctx := context.Background()
		err := testkit.DB(t).WithReadOnlyTenantTx(ctx, st.tenant, func(q db.Querier) error {
			_, err := q.ExecContext(ctx, `UPDATE parties SET name = name || ' (sửa)' WHERE id = $1`, st.customer)
			return err
		})
		if !db.IsReadOnlyViolation(err) {
			t.Fatalf("update in a read-only tenant transaction: err = %v, want a read-only violation", err)
		}
		err = testkit.DB(t).WithReadOnlyTenantTx(ctx, st.tenant, func(q db.Querier) error {
			_, err := q.ExecContext(ctx, `INSERT INTO audit_events (id, tenant_id, actor_type, action, entity_type, source, created_at)
				VALUES (gen_random_uuid(), $1, 'system', 'support_session.write', 'tenant', 'support', now())`, st.tenant)
			return err
		})
		if !db.IsReadOnlyViolation(err) {
			t.Fatalf("insert in a read-only tenant transaction: err = %v, want a read-only violation", err)
		}
	})

	t.Run("a revoked session no longer opens", func(t *testing.T) {
		send[support.SessionDTO](t, b, http.MethodPost, "/support-sessions/"+session.ID.String()+":revoke", nil)
		expect(t, view(http.MethodGet, "catalog/products"), http.StatusForbidden, support.CodeSessionInactive)
		if n := countRows(t, st.tenant, `SELECT count(*) FROM audit_events WHERE action = 'support_session.revoke'`); n != 1 {
			t.Errorf("revoke audit events = %d, want 1", n)
		}
	})

	t.Run("an expired session no longer opens", func(t *testing.T) {
		grant()
		expect(t, view(http.MethodGet, "catalog/products"), http.StatusOK, "")
		s.clk.Advance(time.Hour + time.Minute)
		expect(t, view(http.MethodGet, "catalog/products"), http.StatusForbidden, support.CodeSessionInactive)
	})
}

func TestSupportViewOpensEveryGrantedModule(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	st := buildSalesStore(t, b)
	sku := st.newSku(t, b, "HT-1", "10000")
	adm, adminID := s.adminSignIn(t)

	ticket := send[support.TicketDTO](t, b, http.MethodPost, "/support-tickets",
		map[string]any{"subject": "Báo cáo công nợ lỗi", "body": "Trang công nợ không mở được."})
	adminSend[support.TicketDTO](t, adm, http.MethodPatch, "/support-tickets/"+ticket.ID.String(),
		map[string]any{"assigned_admin_id": adminID, "status": "in_progress"})
	modules := make([]string, 0, len(support.ViewModules))
	for m := range support.ViewModules {
		modules = append(modules, m)
	}
	send[support.SessionDTO](t, b, http.MethodPost, "/support-sessions", map[string]any{
		"ticket_id": ticket.ID, "modules": modules, "hours": 1,
	})

	// One GET per segment a module opens; a path with an id the store lacks may answer 404.
	today := testkit.Clock().Now().Format(time.DateOnly)
	reads := map[string][]string{
		"catalog": {"products", "skus", "categories", "colors", "units", "tax-rates", "attributes", "attribute-sets",
			"mix-groups", "search?type=sku&q=HT", "price-lists", "price-changes", "promotions"},
		"partners":  {"parties/" + st.customer.String() + "/contacts", "customers", "customer-groups", "suppliers"},
		"inventory": {"stock", "stock-card?sku_id=" + sku.String(), "lots", "reservations", "stock-adjustments", "stock-counts", "cost-recalculations"},
		"procurement": {"procurement/late-orders", "purchase-orders", "receipts", "supplier-documents", "purchase-returns",
			"purchase-price-adjustments"},
		"sales": {"pos/bootstrap", "quotes", "invoices", "stock-outs/" + uuid.NewString(), "sales-documents", "sales-returns", "exchanges"},
		"debt_cash": {"debts/parties?direction=receivable", "debts/aging?direction=payable&as_of=" + today,
			"debts/forecast?from=" + today + "&to=" + today, "payments/pending-verification", "statements", "funds", "payment-methods"},
		"kits":     {"kits", "assembly-orders", "disassemblies"},
		"campaign": {"campaigns"},
		"crm":      {"care-tasks", "loyalty/program"},
		"reports":  {"reports", "saved-reports"},
		"settings": {"store", "tenant", "reasons", "period-locks", "audit-events"},
	}
	for module, segments := range support.ViewModules {
		for _, segment := range segments {
			covered := false
			for _, rest := range reads[module] {
				covered = covered || strings.SplitN(strings.SplitN(rest, "?", 2)[0], "/", 2)[0] == segment
			}
			if !covered {
				t.Errorf("no support view read covers %s/%s", module, segment)
			}
		}
	}
	for module, rests := range reads {
		for _, rest := range rests {
			rec := adm.do(http.MethodGet, adminV1+"/tenants/"+st.tenant.String()+"/support-view/"+module+"/"+rest, nil)
			if rec.Code >= http.StatusInternalServerError || rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
				t.Errorf("support view GET %s/%s = %d: %s", module, rest, rec.Code, rec.Body.String())
			}
		}
	}

	rec := adm.do(http.MethodGet, adminV1+"/tenants/"+st.tenant.String()+"/support-view/catalog/price-changes", nil)
	expect(t, rec, http.StatusOK, "")
	changes := testkit.DecodeJSON[dataOf[struct {
		ActorName *string `json:"actor_name"`
	}]](t, rec).Data
	if len(changes) == 0 || changes[0].ActorName == nil || *changes[0].ActorName == "" {
		t.Errorf("support view price changes = %+v, want the actor's name", changes)
	}

	// The audit search by a person's name reads the store's users, which the read-only role cannot.
	type auditRow struct {
		ActorType string     `json:"actor_type"`
		ActorID   *uuid.UUID `json:"actor_id"`
	}
	rec = adm.do(http.MethodGet, adminV1+"/tenants/"+st.tenant.String()+"/support-view/settings/audit-events?q=chu+cua", nil)
	expect(t, rec, http.StatusOK, "")
	found := testkit.DecodeJSON[dataOf[auditRow]](t, rec).Data
	if len(found) == 0 {
		t.Fatal("the support view audit search by the owner's name finds nothing")
	}
	for _, ev := range found {
		if ev.ActorType != "owner" || ev.ActorID == nil ||
			countRows(t, st.tenant, `SELECT count(*) FROM users WHERE id = $1 AND full_name = 'Chủ cửa hàng'`, *ev.ActorID) != 1 {
			t.Errorf("audit search by the owner's name returned %+v", ev)
		}
	}
	rec = adm.do(http.MethodGet, adminV1+"/tenants/"+st.tenant.String()+"/support-view/settings/audit-events?q=khong+ai+ten+vay", nil)
	expect(t, rec, http.StatusOK, "")
	if got := testkit.DecodeJSON[dataOf[auditRow]](t, rec).Data; len(got) != 0 {
		t.Errorf("audit search by an unknown name = %+v, want none", got)
	}
}

func TestAdminTenantListCarriesNoBusinessData(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	st := buildSalesStore(t, b)
	sku := st.newSku(t, b, "QT-1", "10000")
	st.receive(t, b, sku, nil, "2", "5000")
	adm, _ := s.adminSignIn(t)

	forbidden := []string{"product", "sku_code", "party", "customer", "supplier", "document", "amount", "price", "total",
		"invoice", "receivable", "payable", "stock_value", "revenue", "phone", "address"}
	check := func(where string, keys map[string]json.RawMessage) {
		t.Helper()
		for k := range keys {
			for _, f := range forbidden {
				if strings.Contains(k, f) {
					t.Errorf("%s has the field %q", where, k)
				}
			}
		}
	}
	all := adminSend[dataOf[map[string]json.RawMessage]](t, adm, http.MethodGet, "/tenants", nil).Data
	if len(all) == 0 {
		t.Fatal("empty tenant list")
	}
	for _, row := range all {
		check("tenant list row", row)
		var status string
		if err := json.Unmarshal(row["status"], &status); err != nil || status == "replaced" {
			t.Errorf("tenant list row status = %s", row["status"])
		}
	}
	mine := adminSend[dataOf[map[string]json.RawMessage]](t, adm, http.MethodGet, "/tenants?q="+o.slug, nil).Data
	if len(mine) != 1 || !strings.Contains(string(mine[0]["slug"]), o.slug) {
		t.Fatalf("tenant search for %s = %v", o.slug, mine)
	}
	detail := adminSend[map[string]json.RawMessage](t, adm, http.MethodGet, "/tenants/"+st.tenant.String(), nil)
	delete(detail, "subscription") // the plan's own price, not store data
	check("tenant detail", detail)
	var usage []map[string]json.RawMessage
	if err := json.Unmarshal(detail["usage"], &usage); err != nil {
		t.Fatalf("tenant detail usage = %s: %v", detail["usage"], err)
	}
	for _, u := range usage {
		check("tenant usage", u)
	}
}

func TestAdminTOTPLocksTheAccountAfterFiveWrongCodes(t *testing.T) {
	s := newControlStack(t)
	email, secret, adminID := s.adminAccount(t)
	b := s.adminBrowser(t)
	challenge := adminChallenge(t, b, email)
	near := map[string]bool{}
	for d := -2; d <= 2; d++ {
		near[authn.TOTPCode(secret, s.clk.Now().Add(time.Duration(d)*authn.TOTPPeriod))] = true
	}
	wrong := "000000"
	for i := 1; near[wrong]; i++ {
		wrong = fmt.Sprintf("%06d", i)
	}

	for range 5 {
		expect(t, adminTOTP(b, challenge, wrong), http.StatusUnauthorized, "invalid_code")
	}
	rec := adminTOTP(b, challenge, authn.TOTPCode(secret, s.clk.Now()))
	expect(t, rec, http.StatusTooManyRequests, "rate_limited")
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a locked sign-in has no Retry-After")
	}
	expect(t, b.do(http.MethodPost, adminV1+"/auth/login", map[string]string{"email": email, "password": adminPassword}),
		http.StatusTooManyRequests, "rate_limited")

	s.clk.Advance(admin.TOTPLockDuration)
	b = s.adminBrowser(t)
	expect(t, adminTOTP(b, challenge, authn.TOTPCode(secret, s.clk.Now())), http.StatusUnauthorized, "challenge_invalid")
	expect(t, adminTOTP(b, adminChallenge(t, b, email), authn.TOTPCode(secret, s.clk.Now())), http.StatusOK, "")
	if n := count(t, `SELECT count(*) FROM tally.platform_admins WHERE id = $1 AND totp_failed_count = 0 AND locked_until IS NULL`,
		adminID); n != 1 {
		t.Error("a signed-in admin keeps its failed TOTP count or lock")
	}
}

func TestAdminLoginIsLimitedPerEmail(t *testing.T) {
	s := newControlStack(t)
	email, _, _ := s.adminAccount(t)
	login := func(email string) *httptest.ResponseRecorder {
		return s.adminBrowser(t).do(http.MethodPost, adminV1+"/auth/login",
			map[string]string{"email": email, "password": "khong-phai-mat-khau"})
	}
	for range 10 {
		expect(t, login(email), http.StatusUnauthorized, "invalid_credentials")
	}
	expect(t, login(email), http.StatusTooManyRequests, "rate_limited")
	expect(t, login("khac-"+email), http.StatusUnauthorized, "invalid_credentials")
}

func TestAdminAPIOnlyAnswersOnTheAdminHost(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	for _, host := range []string{o.host, "tally.test"} {
		rec := testkit.Do(t, s.handler, testkit.Request{Method: http.MethodGet, URL: "http://" + host + adminV1 + "/tenants"})
		expect(t, rec, http.StatusNotFound, "")
	}
	expect(t, b.do(http.MethodGet, adminV1+"/tenants", nil), http.StatusNotFound, "")

	anon := s.browser(t, adminHost)
	anon.hostContext()
	expect(t, anon.do(http.MethodGet, adminV1+"/tenants", nil), http.StatusUnauthorized, "")
	// A store session cookie means nothing on the admin host.
	stolen := s.browser(t, adminHost)
	stolen.cookies = b.cookies
	expect(t, stolen.do(http.MethodGet, adminV1+"/tenants", nil), http.StatusUnauthorized, "")
}

type closureAnswer struct {
	Status      string     `json:"status"`
	ExportJobID *uuid.UUID `json:"export_job_id"`
}

// confirmClosure requests the closure of o's store and verifies it with the mailed token (a
// tampered token is refused first).
func (s controlStack) confirmClosure(t *testing.T, b *browser, o owner) closureAnswer {
	t.Helper()
	expect(t, b.do(http.MethodPost, v1+"/account-closure", nil), http.StatusAccepted, "requested")
	s.identity.WaitMail()
	var token string
	for _, m := range s.mail.to(o.email) {
		if strings.Contains(m.Text, "/settings/account-closure?") {
			if found := tokenRE.FindStringSubmatch(m.Text); found != nil {
				token = found[1]
			}
		}
	}
	if token == "" {
		t.Fatal("no closure confirmation mail")
	}
	// The tampered token differs in its first character whatever that character is.
	tampered := "x" + token[1:]
	if token[0] == 'x' {
		tampered = "y" + token[1:]
	}
	expect(t, b.do(http.MethodPost, v1+"/account-closure:verify", map[string]string{"token": tampered}),
		http.StatusUnprocessableEntity, "token_invalid")
	rec := b.do(http.MethodPost, v1+"/account-closure:verify", map[string]string{"token": token})
	expect(t, rec, http.StatusOK, "waiting")
	return testkit.DecodeJSON[closureAnswer](t, rec)
}

// closeStore confirms the closure of o's store, runs its export, and after the wait takes its
// final export and completes it.
func (s controlStack) closeStore(t *testing.T, b *browser, o owner) {
	t.Helper()
	tenant := tenantOf(t, b)
	s.confirmClosure(t, b, o)
	ctx := context.Background()
	if _, err := s.domains.SaaS.RunDataExports(ctx); err != nil {
		t.Fatal(err)
	}
	s.clk.Advance((saas.ClosureWaitDays + 1) * 24 * time.Hour)
	for range 2 {
		if _, err := s.domains.SaaS.CompleteClosures(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.domains.SaaS.RunDataExports(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, `SELECT count(*) FROM tally.tenants WHERE id = $1 AND status = 'closed'`, tenant); n != 1 {
		t.Fatal("the store is not closed after its final export")
	}
}

// closureLink returns the token of the latest closure export link mailed to o.
func (s controlStack) closureLink(t *testing.T, o owner) string {
	t.Helper()
	s.identity.WaitMail()
	var token string
	for _, m := range s.mail.to(o.email) {
		if strings.Contains(m.Text, "/api/v1/account-closure/export?") {
			if found := tokenRE.FindStringSubmatch(m.Text); found != nil {
				token = found[1]
			}
		}
	}
	if token == "" {
		t.Fatal("no closure export link mailed")
	}
	return token
}

// keepPosting extends the store's subscription past the closure's wait, so the subscription
// never stops it from posting there.
func (s controlStack) keepPosting(t *testing.T, tenant uuid.UUID) {
	t.Helper()
	adm, _ := s.adminSignIn(t)
	adminSend[saas.SubscriptionDTO](t, adm, http.MethodPost, "/tenants/"+tenant.String()+"/subscription:extend",
		map[string]any{"days": 2 * saas.ClosureWaitDays})
}

func TestAClosedStoreStaysClosed(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	tenant := tenantOf(t, b)
	adm, _ := s.adminSignIn(t)
	old := adminSend[saas.InvoiceDTO](t, adm, http.MethodPost, "/saas-invoices",
		map[string]any{"tenant_id": tenant, "plan_id": storePlanID, "amount": "250000"})

	s.closeStore(t, b, o)
	closed := func(when string) {
		t.Helper()
		if n := count(t, `SELECT count(*) FROM tally.tenants WHERE id = $1 AND status = 'closed'`, tenant); n != 1 {
			t.Errorf("the store is not closed %s", when)
		}
	}
	closed("after its closure")
	if n := count(t, `SELECT count(*) FROM tally.saas_invoices WHERE id = $1 AND status = 'void'`, old.ID); n != 1 {
		t.Error("the closure left the store's open invoice payable")
	}
	period := func() string {
		t.Helper()
		var v string
		if err := testkit.SuperDB(t).Get(&v, `SELECT concat_ws(' ', plan_id, status, current_period_start, current_period_end, version)
			FROM tally.subscriptions WHERE tenant_id = $1`, tenant); err != nil {
			t.Fatal(err)
		}
		return v
	}
	beforeLate := period()

	paid := []byte(`{"event_id":"evt-` + unique() + `","invoice_number":"` + old.Number + `","amount":"250000","reference":"FT` + unique() + `"}`)
	refused := func() int {
		t.Helper()
		return count(t, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND action = 'saas_invoice.payment_refused'
			AND entity_id = $2`, tenant, old.ID)
	}
	// The provider retries a refused callback; the operator who refunds it by hand hears of it once.
	for range 2 {
		expect(t, testkit.Do(t, s.handler, testkit.Request{
			Method: http.MethodPost, URL: "http://tally.test/api/v1/webhooks/saas-payments/" + testProvider,
			Body: json.RawMessage(paid), Header: http.Header{provider.SignatureHeader: {s.webhook.Sign(paid)}},
		}), http.StatusConflict, saas.CodeInvoiceNotOpen)
		if n := refused(); n != 1 {
			t.Errorf("refused payment audits = %d, want 1", n)
		}
	}
	closed("after a late payment of its old invoice")
	if n := count(t, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND action = 'saas_invoice.payment_refused'
		AND entity_id = $2 AND actor_type = 'system' AND after->>'invoice_status' = 'void'`, tenant, old.ID); n != 1 {
		t.Errorf("refused payment audits by the system = %d, want 1", n)
	}
	if n := count(t, `SELECT count(*) FROM tally.saas_invoices WHERE id = $1 AND status = 'void' AND paid_at IS NULL
		AND provider IS NULL`, old.ID); n != 1 {
		t.Error("the late payment changed the void invoice")
	}
	if got := period(); got != beforeLate {
		t.Errorf("subscription after the late payment = %s, want %s", got, beforeLate)
	}

	adm, _ = s.adminSignIn(t)
	expect(t, adm.do(http.MethodPost, adminV1+"/tenants/"+tenant.String()+"/subscription:change",
		map[string]any{"plan_id": storePlanID}), http.StatusConflict, saas.CodeTenantClosed)
	expect(t, adm.do(http.MethodPost, adminV1+"/saas-invoices",
		map[string]any{"tenant_id": tenant, "plan_id": storePlanID, "amount": "250000"}), http.StatusConflict, saas.CodeTenantClosed)
	if _, err := s.domains.SaaS.RunLifecycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	closed("after an admin plan change, a new invoice and the lifecycle job")
}

func TestMaintenanceRefusesEveryChangeAndHoldsTheJobs(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	st := buildSalesStore(t, b)
	sku := st.newSku(t, b, "MT-1", "40000")
	st.receive(t, b, sku, nil, "4", "20000")
	hold := func(on bool) {
		t.Helper()
		if _, err := testkit.SuperDB(t).Exec(`UPDATE tally.tenants SET maintenance = $2 WHERE id = $1`, st.tenant, on); err != nil {
			t.Fatal(err)
		}
	}

	hold(true)
	sales := countRows(t, st.tenant, `SELECT count(*) FROM sales_documents`)
	expect(t, post(b, "/pos/checkout", st.cashSale("40000", saleLine(sku, "1", nil))), http.StatusLocked, "tenant_maintenance")
	for _, req := range []struct{ method, path string }{
		{http.MethodPost, "/customers"},
		{http.MethodPatch, "/tenant"},
		{http.MethodPost, "/feedback"},
		{http.MethodPost, "/support-tickets"},
		{http.MethodPost, "/data-exports"},
		{http.MethodPost, "/account-closure"},
		{http.MethodPost, "/onboarding/steps/opening_stock:skip"},
	} {
		rec := b.do(req.method, v1+req.path, map[string]any{"name": "Chị Ngọc"})
		if rec.Code != http.StatusLocked || !strings.Contains(rec.Body.String(), "tenant_maintenance") {
			t.Errorf("%s %s in maintenance = %d %s, want 423 %s", req.method, req.path, rec.Code, rec.Body, "tenant_maintenance")
		}
	}
	if n := countRows(t, st.tenant, `SELECT count(*) FROM sales_documents`); n != sales {
		t.Errorf("sales documents = %d after a checkout in maintenance, want %d", n, sales)
	}
	for _, path := range []string{"/products", "/subscription", "/me", "/onboarding"} {
		if rec := b.do(http.MethodGet, v1+path, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s in maintenance = %d %s", path, rec.Code, rec.Body)
		}
	}
	// Securing the account never waits for an operator: its rows are platform rows a copy
	// never writes.
	expect(t, b.do(http.MethodDelete, v1+"/sessions/"+uuid.NewString(), nil), http.StatusNotFound, "")

	hold(false)
	checkout(t, b, st.cashSale("40000", saleLine(sku, "1", nil)))

	// The jobs run for every store, so each check reads this store's rows.
	ctx := context.Background()
	verified := s.confirmClosure(t, b, o)
	if verified.ExportJobID == nil {
		t.Fatal("verified closure has no export job")
	}
	var subStatus string
	if err := testkit.SuperDB(t).Get(&subStatus, `SELECT status FROM tally.subscriptions WHERE tenant_id = $1`, st.tenant); err != nil {
		t.Fatal(err)
	}
	state := func() (export, closure, sub string) {
		t.Helper()
		super := testkit.SuperDB(t)
		if err := super.Get(&export, `SELECT status FROM tally.export_jobs WHERE id = $1`, *verified.ExportJobID); err != nil {
			t.Fatal(err)
		}
		if err := super.Get(&closure, `SELECT status FROM tally.account_closure_requests WHERE tenant_id = $1
			ORDER BY created_at DESC, id DESC LIMIT 1`, st.tenant); err != nil {
			t.Fatal(err)
		}
		if err := super.Get(&sub, `SELECT status FROM tally.subscriptions WHERE tenant_id = $1`, st.tenant); err != nil {
			t.Fatal(err)
		}
		return export, closure, sub
	}
	runJobs := func() {
		t.Helper()
		if _, err := s.domains.SaaS.RunDataExports(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.domains.SaaS.RunLifecycle(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.domains.SaaS.CompleteClosures(ctx); err != nil {
			t.Fatal(err)
		}
	}
	hold(true)
	s.clk.Advance((saas.ClosureWaitDays + 1) * 24 * time.Hour)
	runJobs()
	if export, req, sub := state(); export != "queued" || req != saas.ClosureWaiting || sub != subStatus {
		t.Errorf("after the jobs in maintenance: export %s, closure %s, subscription %s; want queued, waiting, %s",
			export, req, sub, subStatus)
	}
	hold(false)
	runJobs()
	if export, req, _ := state(); export != "succeeded" || req != saas.ClosureExporting {
		t.Errorf("after the jobs on release: export %s, closure %s; want succeeded, exporting", export, req)
	}
	if final := closureExportOf(t, st.tenant); final == *verified.ExportJobID {
		t.Error("after the jobs on release the closure did not move on to its final export")
	}
}

func TestAccountClosureQueuesTheFullDataExport(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	st := buildSalesStore(t, b)
	sku := st.newSku(t, b, "DL-1", "10000")
	st.receive(t, b, sku, nil, "3", "6000")

	closure := s.confirmClosure(t, b, o)
	if closure.ExportJobID == nil {
		t.Fatal("verified closure has no export job")
	}
	if n := countRows(t, st.tenant, `SELECT count(*) FROM export_jobs WHERE id = $1 AND kind = $2 AND status = 'queued'`,
		*closure.ExportJobID, saas.DataExportKind); n != 1 {
		t.Fatalf("queued full data export jobs = %d, want 1", n)
	}
	if got := send[map[string]json.RawMessage](t, b, http.MethodGet, "/tenant", nil); !strings.Contains(string(got["status"]), "closing") {
		t.Errorf("tenant after verification = %v, want closing", got)
	}

	if _, err := s.domains.SaaS.RunDataExports(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st.tenant, `SELECT count(*) FROM export_jobs WHERE id = $1 AND status = 'succeeded'`, *closure.ExportJobID); n != 1 {
		t.Fatal("the full data export did not succeed")
	}
	data, ok := s.objects.object(objectstore.TenantKey(st.tenant, "exports", closure.ExportJobID.String()+".zip"))
	if !ok {
		t.Fatal("the export zip is not in the object store")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var tables []db.TenantTable
	testkit.InTenant(t, testkit.DB(t), st.tenant, func(q db.Querier) error {
		var err error
		tables, err = db.TenantTables(context.Background(), q)
		return err
	})
	want := map[string]bool{"README.csv": true}
	for _, tb := range tables {
		want[tb.Name+".csv"] = true
	}
	if len(zr.File) != len(want) {
		t.Errorf("zip entries = %d, want one per tenant table plus README (%d)", len(zr.File), len(want))
	}
	contents := map[string]string{}
	for _, f := range zr.File {
		if !want[f.Name] {
			t.Errorf("unexpected zip entry %s", f.Name)
			continue
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasPrefix(body, []byte("\xef\xbb\xbf")) {
			t.Errorf("%s does not start with the UTF-8 BOM", f.Name)
		}
		contents[f.Name] = string(body)
	}
	if !strings.Contains(contents["parties.csv"], "Chị Lan") || !strings.Contains(contents["parties.csv"], "Xưởng may Bình Tân") {
		t.Errorf("parties.csv lacks the Vietnamese party names:\n%s", contents["parties.csv"])
	}
	if !strings.Contains(contents["units.csv"], "cái") {
		t.Error("units.csv lacks the unit cái")
	}
	if !strings.Contains(contents["README.csv"], "bảng") {
		t.Error("README.csv lacks the data dictionary header")
	}
}

// closureState is the owner's view of the store's closure.
type closureState struct {
	Request *struct {
		Status      string     `json:"status"`
		ExportJobID *uuid.UUID `json:"export_job_id"`
	} `json:"request"`
	Export *struct {
		ID             uuid.UUID  `json:"id"`
		Status         string     `json:"status"`
		Final          bool       `json:"final"`
		FailedAttempts int        `json:"failed_attempts"`
		ExpiresAt      *time.Time `json:"expires_at"`
	} `json:"export"`
}

// closureExportOf returns the export job the store's latest closure waits for.
func closureExportOf(t *testing.T, tenant uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := testkit.SuperDB(t).Get(&id, `SELECT export_job_id FROM tally.account_closure_requests WHERE tenant_id = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`, tenant); err != nil {
		t.Fatal(err)
	}
	return id
}

// closureStatusOf returns the status of the store's latest closure.
func closureStatusOf(t *testing.T, tenant uuid.UUID) string {
	t.Helper()
	var status string
	if err := testkit.SuperDB(t).Get(&status, `SELECT status FROM tally.account_closure_requests WHERE tenant_id = $1
		ORDER BY created_at DESC, id DESC LIMIT 1`, tenant); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestAClosureWhoseFinalExportKeepsFailingWaitsForAnOperator(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	tenant := tenantOf(t, b)
	s.keepPosting(t, tenant)
	ctx := context.Background()
	s.confirmClosure(t, b, o)
	if _, err := s.domains.SaaS.RunDataExports(ctx); err != nil {
		t.Fatal(err)
	}
	s.clk.Advance((saas.ClosureWaitDays + 1) * 24 * time.Hour)
	complete := func() {
		t.Helper()
		if _, err := s.domains.SaaS.CompleteClosures(ctx); err != nil {
			t.Fatal(err)
		}
	}
	complete()
	if got := closureStatusOf(t, tenant); got != saas.ClosureExporting {
		t.Fatalf("closure after the wait = %s, want exporting", got)
	}
	fail := func(id uuid.UUID) {
		t.Helper()
		if _, err := testkit.SuperDB(t).Exec(`UPDATE tally.export_jobs SET status = 'failed', error = 'disk full', finished_at = now()
			WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	audits := func(action string) int {
		t.Helper()
		return count(t, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND action = $2`, tenant, action)
	}
	finals := func(round int) int {
		t.Helper()
		return countRows(t, tenant, `SELECT count(*) FROM export_jobs WHERE kind = $1 AND params->>'reason' = 'closure_final'
			AND (params->>'round')::int = $2`, saas.DataExportKind, round)
	}

	failed := []uuid.UUID{}
	for attempt := 1; attempt < saas.ClosureExportAttempts; attempt++ {
		failed = append(failed, closureExportOf(t, tenant))
		fail(failed[len(failed)-1])
		complete()
		next := closureExportOf(t, tenant)
		if slices.Contains(failed, next) {
			t.Fatalf("failure %d: the closure still waits for the failed export %s", attempt, next)
		}
		if n := countRows(t, tenant, `SELECT count(*) FROM export_jobs WHERE id = $1 AND status = 'queued'`, next); n != 1 {
			t.Fatalf("failure %d: export %s is not queued", attempt, next)
		}
		if got := closureStatusOf(t, tenant); got != saas.ClosureExporting {
			t.Fatalf("failure %d: closure = %s, want exporting", attempt, got)
		}
	}
	if n := audits("account_closure.export_requeued"); n != saas.ClosureExportAttempts-1 {
		t.Errorf("requeue audits = %d, want %d", n, saas.ClosureExportAttempts-1)
	}

	// The last allowed failure stops the closure for an operator, reported once.
	fail(closureExportOf(t, tenant))
	complete()
	complete()
	if got := closureStatusOf(t, tenant); got != saas.ClosureExportFailed {
		t.Fatalf("closure after %d failed final exports = %s, want export_failed", saas.ClosureExportAttempts, got)
	}
	if n := audits("account_closure.export_failed"); n != 1 {
		t.Errorf("export_failed audits = %d, want 1", n)
	}
	if n := finals(1); n != saas.ClosureExportAttempts {
		t.Errorf("final exports of the first round = %d, want %d", n, saas.ClosureExportAttempts)
	}
	b = s.signIn(t, o)
	view := send[closureState](t, b, http.MethodGet, "/account-closure", nil)
	if view.Request == nil || view.Request.Status != saas.ClosureExportFailed || view.Export == nil ||
		view.Export.Status != "failed" || view.Export.FailedAttempts != saas.ClosureExportAttempts {
		t.Errorf("owner's closure = %+v %+v, want export_failed after %d failed exports", view.Request, view.Export,
			saas.ClosureExportAttempts)
	}
	// The store still only reads and the owner cannot back out of it.
	expect(t, b.do(http.MethodPost, v1+"/customers", map[string]any{"kind": "person", "name": "Chị Hằng", "phone": "0903334445"}),
		http.StatusForbidden, "subscription_read_only")
	expect(t, b.do(http.MethodPost, v1+"/account-closure:cancel", nil), http.StatusConflict, saas.CodeClosureState)

	adm, _ := s.adminSignIn(t)
	detail := adminSend[struct {
		Closure *closureState `json:"closure"`
	}](t, adm, http.MethodGet, "/tenants/"+tenant.String(), nil)
	if detail.Closure == nil || detail.Closure.Request == nil || detail.Closure.Request.Status != saas.ClosureExportFailed {
		t.Errorf("admin tenant closure = %+v, want export_failed", detail.Closure)
	}
	retry := adminV1 + "/tenants/" + tenant.String() + "/account-closure:retry-export"
	retried := testkit.DecodeJSON[closureState](t, adm.do(http.MethodPost, retry, nil))
	if retried.Request == nil || retried.Request.Status != saas.ClosureExporting || retried.Export == nil ||
		retried.Export.Status != "queued" || retried.Export.FailedAttempts != 0 {
		t.Fatalf("closure after the operator's retry = %+v %+v, want exporting a queued export", retried.Request, retried.Export)
	}
	if n := finals(2); n != 1 {
		t.Errorf("final exports of the retried round = %d, want 1", n)
	}
	if n := audits("account_closure.export_retried"); n != 1 {
		t.Errorf("retry audits = %d, want 1", n)
	}
	expect(t, adm.do(http.MethodPost, retry, nil), http.StatusConflict, saas.CodeClosureState)

	if _, err := s.domains.SaaS.RunDataExports(ctx); err != nil {
		t.Fatal(err)
	}
	complete()
	if n := count(t, `SELECT count(*) FROM tally.tenants WHERE id = $1 AND status = 'closed'`, tenant); n != 1 {
		t.Error("the store is not closed after the retried final export succeeded")
	}
	_ = s.closureLink(t, o)
}

func TestClosureEndsWithAFinalExportTheOwnerCanDownload(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	st := buildSalesStore(t, b)
	s.keepPosting(t, st.tenant)
	ctx := context.Background()
	first := *s.confirmClosure(t, b, o).ExportJobID
	if _, err := s.domains.SaaS.RunDataExports(ctx); err != nil {
		t.Fatal(err)
	}
	complete := func() {
		t.Helper()
		if _, err := s.domains.SaaS.CompleteClosures(ctx); err != nil {
			t.Fatal(err)
		}
	}
	partiesOf := func(job uuid.UUID) string {
		t.Helper()
		data, ok := s.objects.object(objectstore.TenantKey(st.tenant, "exports", job.String()+".zip"))
		if !ok {
			t.Fatalf("export %s has no zip", job)
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range zr.File {
			if f.Name != "parties.csv" {
				continue
			}
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(r)
			_ = r.Close()
			if err != nil {
				t.Fatal(err)
			}
			return string(body)
		}
		t.Fatalf("export %s has no parties.csv", job)
		return ""
	}

	// The store keeps working while it waits; the first export does not hold that work.
	s.clk.Advance(15 * 24 * time.Hour)
	b = s.signIn(t, o)
	send[withID](t, b, http.MethodPost, "/customers", map[string]any{"kind": "person", "name": "Anh Tư Cuối Kỳ", "phone": "0907654321"})
	if strings.Contains(partiesOf(first), "Anh Tư Cuối Kỳ") {
		t.Fatal("the first export holds a customer created after it")
	}

	// The store freezes when the closure takes its final export, not on the date the wait ends.
	s.clk.Advance((saas.ClosureWaitDays - 15 + 1) * 24 * time.Hour)
	b = s.signIn(t, o)
	send[withID](t, b, http.MethodPost, "/customers", map[string]any{"kind": "person", "name": "Chị Kịp Giờ", "phone": "0905556667"})
	// From then on the store only reads, so that export holds everything; the owner can no
	// longer back out.
	complete()
	if got := closureStatusOf(t, st.tenant); got != saas.ClosureExporting {
		t.Fatalf("closure after the wait = %s, want exporting", got)
	}
	expect(t, b.do(http.MethodPost, v1+"/customers", map[string]any{"kind": "person", "name": "Chị Muộn", "phone": "0901112223"}),
		http.StatusForbidden, "subscription_read_only")
	for _, path := range []string{"/products", "/account-closure", "/me"} {
		if rec := b.do(http.MethodGet, v1+path, nil); rec.Code != http.StatusOK {
			t.Errorf("GET %s while exporting = %d %s", path, rec.Code, rec.Body)
		}
	}
	expect(t, b.do(http.MethodPost, v1+"/account-closure:cancel", nil), http.StatusConflict, saas.CodeClosureState)
	final := closureExportOf(t, st.tenant)
	if final == first {
		t.Fatal("the closure did not queue a final export when the wait ended")
	}
	// The read-only store cannot cancel it either.
	expect(t, b.do(http.MethodPost, v1+"/export-jobs/"+final.String()+":cancel", nil),
		http.StatusForbidden, "subscription_read_only")

	// The store stays open until the final export succeeded.
	complete()
	if _, err := testkit.SuperDB(t).Exec(`UPDATE tally.export_jobs SET status = 'running' WHERE id = $1`, final); err != nil {
		t.Fatal(err)
	}
	complete()
	if _, err := testkit.SuperDB(t).Exec(`UPDATE tally.export_jobs SET status = 'queued' WHERE id = $1`, final); err != nil {
		t.Fatal(err)
	}
	if n := count(t, `SELECT count(*) FROM tally.tenants WHERE id = $1 AND status = 'closing'`, st.tenant); n != 1 ||
		closureStatusOf(t, st.tenant) != saas.ClosureExporting {
		t.Fatal("the store closed before its final export succeeded")
	}
	if _, err := s.domains.SaaS.RunDataExports(ctx); err != nil {
		t.Fatal(err)
	}
	if parties := partiesOf(final); !strings.Contains(parties, "Anh Tư Cuối Kỳ") || !strings.Contains(parties, "Chị Kịp Giờ") {
		t.Error("the final export lacks the customers created during the wait")
	}
	view := send[closureState](t, b, http.MethodGet, "/account-closure", nil)
	if view.Export == nil || view.Export.ID != final || !view.Export.Final || view.Export.Status != "succeeded" || view.Export.ExpiresAt == nil {
		t.Fatalf("owner's closure export = %+v, want the succeeded final export %s", view.Export, final)
	}
	complete()
	if n := count(t, `SELECT count(*) FROM tally.tenants WHERE id = $1 AND status = 'closed'`, st.tenant); n != 1 {
		t.Fatal("the store is not closed after its final export succeeded")
	}

	// The owner downloads the final export of the closed store through the mailed link, with no
	// session, as often as needed while the export lasts.
	anon := s.browser(t, o.host)
	download := func(token string) *httptest.ResponseRecorder {
		t.Helper()
		return anon.do(http.MethodGet, v1+"/account-closure/export?token="+token, nil)
	}
	link := s.closureLink(t, o)
	for range 2 {
		rec := download(link)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") == "" {
			t.Fatalf("closure export link = %d %s, want a redirect to the file", rec.Code, rec.Body)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("closure export link Cache-Control = %q, want no-store", got)
		}
	}
	expect(t, download("x"+link[1:]), http.StatusNotFound, saas.CodeClosureLinkNotFound)
	expect(t, download(""), http.StatusNotFound, saas.CodeClosureLinkNotFound)
	// The link opens only on its own store's host.
	other := s.signUp(t)
	expect(t, s.browser(t, other.host).do(http.MethodGet, v1+"/account-closure/export?token="+link, nil),
		http.StatusNotFound, saas.CodeClosureLinkNotFound)
	// The closed store has no sessions, which is why the link needs none.
	expect(t, b.do(http.MethodGet, v1+"/me", nil), http.StatusUnauthorized, "")

	// An operator mails a new link when the owner lost the mail; the earlier one stops working.
	adm, _ := s.adminSignIn(t)
	retry := adminV1 + "/tenants/" + st.tenant.String() + "/account-closure:retry-export"
	resent := testkit.DecodeJSON[closureState](t, adm.do(http.MethodPost, retry, nil))
	if resent.Request == nil || resent.Request.Status != saas.ClosureCompleted {
		t.Errorf("closure after the operator's resend = %+v, want completed", resent.Request)
	}
	again := s.closureLink(t, o)
	if again == link {
		t.Fatal("the resend mailed the earlier link")
	}
	expect(t, download(link), http.StatusNotFound, saas.CodeClosureLinkNotFound)
	if rec := download(again); rec.Code != http.StatusFound {
		t.Errorf("resent closure export link = %d %s", rec.Code, rec.Body)
	}

	// An export that is no longer available is refused even while its link lasts.
	if _, err := testkit.SuperDB(t).Exec(`UPDATE tally.export_jobs SET status = 'expired' WHERE id = $1`, final); err != nil {
		t.Fatal(err)
	}
	expect(t, download(again), http.StatusConflict, saas.CodeExportNotReady)
	expect(t, adm.do(http.MethodPost, retry, nil), http.StatusConflict, saas.CodeClosureState)
	if _, err := testkit.SuperDB(t).Exec(`UPDATE tally.export_jobs SET status = 'succeeded' WHERE id = $1`, final); err != nil {
		t.Fatal(err)
	}
	// The link ends with the export.
	s.clk.Set(view.Export.ExpiresAt.Add(time.Minute))
	expect(t, download(again), http.StatusNotFound, saas.CodeClosureLinkNotFound)

	// A guessing client is throttled.
	throttled := false
	for range 12 {
		if download("x"+again[1:]).Code == http.StatusTooManyRequests {
			throttled = true
			break
		}
	}
	if !throttled {
		t.Error("the closure export link is not rate limited")
	}
}

func TestOpeningBalancesConfirmationLocksThePeriod(t *testing.T) {
	s := newControlStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	st := buildSalesStore(t, b)
	sku := st.newSku(t, b, "MO-1", "10000")

	setCutoff := func(date string) *httptest.ResponseRecorder {
		t.Helper()
		cur := b.do(http.MethodGet, v1+"/tenant", nil)
		expect(t, cur, http.StatusOK, "")
		return b.doWith(http.MethodPatch, v1+"/tenant", map[string]any{"opening_cutoff_date": date},
			http.Header{"If-Match": {cur.Header().Get("ETag")}})
	}
	confirm := func() *httptest.ResponseRecorder {
		return b.do(http.MethodPost, v1+"/onboarding/steps/confirm_opening_balances:complete", nil)
	}
	expect(t, confirm(), http.StatusUnprocessableEntity, "opening_cutoff_required")
	expect(t, setCutoff("2026-03-20"), http.StatusUnprocessableEntity, "")
	expect(t, setCutoff("2026-03-10"), http.StatusOK, "2026-03-10")
	expect(t, confirm(), http.StatusConflict, "onboarding_steps_pending")
	for _, step := range []string{"opening_stock", "opening_debt", "opening_cash"} {
		expect(t, b.do(http.MethodPost, v1+"/onboarding/steps/"+step+":complete", nil), http.StatusOK, "completed")
	}

	s.clk.Advance(middleware.ReauthWindow + time.Minute)
	expect(t, confirm(), http.StatusForbidden, "reauth_required")
	if n := countRows(t, st.tenant, `SELECT count(*) FROM period_locks`); n != 0 {
		t.Fatalf("period locks before confirmation = %d", n)
	}
	expect(t, b.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
	rec := confirm()
	expect(t, rec, http.StatusOK, "completed")
	if got := testkit.DecodeJSON[struct {
		Totals *struct {
			Cutoff string `json:"cutoff"`
		} `json:"totals"`
	}](t, rec); got.Totals == nil || got.Totals.Cutoff != "2026-03-10" {
		t.Errorf("confirmation = %s, want the totals at the cutoff", rec.Body)
	}
	onboarding := send[struct {
		LockedThrough *string `json:"locked_through"`
	}](t, b, http.MethodGet, "/onboarding", nil)
	if onboarding.LockedThrough == nil || *onboarding.LockedThrough != "2026-03-10" {
		t.Errorf("onboarding locked_through = %v, want 2026-03-10", onboarding.LockedThrough)
	}

	receipt := func(date string) map[string]any {
		return map[string]any{"supplier_party_id": st.supplier, "business_date": date,
			"lines": []map[string]any{{"sku_id": sku, "qty": "1", "unit_price": "1000"}}}
	}
	expect(t, post(b, "/receipts", receipt("2026-03-08")), http.StatusLocked, "period_locked")
	expect(t, post(b, "/receipts", receipt("2026-03-10")), http.StatusLocked, "period_locked")
	postReceipt(t, b, receipt("2026-03-12"))
	expect(t, setCutoff("2026-03-05"), http.StatusConflict, "opening_balances_confirmed")
}
