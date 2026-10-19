//go:build integration

package app_test

import (
	"context"
	"encoding/base32"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/cash"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/debt"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// pausingHasher holds a login between reading the account and opening its session, so a test can
// run a sandbox reset in that window.
type pausingHasher struct {
	identity.PasswordHasher
	mu      sync.Mutex
	reached chan struct{}
	resume  chan struct{}
}

// pauseNextVerify makes the next Verify close reached and wait until resume is called.
func (h *pausingHasher) pauseNextVerify() (reached <-chan struct{}, resume func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reached, h.resume = make(chan struct{}), make(chan struct{})
	release := h.resume
	return h.reached, func() { close(release) }
}

func (h *pausingHasher) Verify(encoded, password string) (bool, error) {
	ok, err := h.PasswordHasher.Verify(encoded, password)
	h.mu.Lock()
	reached, resume := h.reached, h.resume
	h.reached, h.resume = nil, nil
	h.mu.Unlock()
	if reached != nil {
		close(reached)
		<-resume
	}
	return ok, err
}

// newSandboxStack is the HTTP stack with every ledger domain, whose host cache forgets an address
// once a sandbox reset moves it to a new tenant (as serve wires it), its clock and its hasher.
func newSandboxStack(t *testing.T) (stack, *clock.Fixed, *pausingHasher) {
	t.Helper()
	clk := testkit.Clock()
	capture := &captureMailer{}
	deps := identityDeps(t, capture, clk)
	hasher := &pausingHasher{PasswordHasher: deps.Hasher}
	deps.Hasher = hasher
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
	objects, err := objectstore.NewS3(objectstore.S3Config{
		Endpoint: &url.URL{Scheme: "http", Host: "127.0.0.1:9000"}, Region: "us-east-1", Bucket: "tally-dev",
		AccessKey: "tally-dev", SecretKey: "tally-dev-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	domains := app.NewDomains(deps.DB, clk, deps.Audit)
	orgSvc := org.NewService(org.Deps{
		DB: deps.DB, Clock: clk, Audit: deps.Audit, Exceptions: exception.NewGuard(clk),
		Provisioner: org.NewProvisioner(org.NewSlugPolicy(nil), deps.Audit, clk, domains.Seeders()), Objects: objects,
		AttachmentOwners: domains.AttachmentOwners(), AddressMoved: purge,
	})
	a, err := app.New(app.Options{
		PublicBaseURL: testBaseURL,
		Logger:        slog.New(slog.DiscardHandler),
		Ports:         app.Ports{Sessions: idSvc, TenantHosts: idSvc},
		CSRFKey:       []byte("0123456789abcdef0123456789abcdef"),
		Now:           clk.Now,
		APIRateLimit:  testAPIRateLimit,
		Modules: app.Modules{
			Identity: identity.NewHandler(idSvc), Org: org.NewHandler(orgSvc), Reasons: org.NewReasonHandler(orgSvc),
			Catalog: catalog.NewHandler(domains.Catalog), Partner: partner.NewHandler(domains.Partner),
			Pricing: pricing.NewHandler(domains.Pricing), Inventory: inventory.NewHandler(domains.Inventory),
			Debt: debt.NewHandler(domains.Debt), Cash: cash.NewHandler(domains.Cash),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts = a.TenantHostCache()
	return stack{handler: a.Handler(), identity: idSvc, mail: capture}, clk, hasher
}

// demoOf creates the sandbox of the real store parent the way `tally seed demo --parent` does,
// and lets its owner choose a password from the verification link.
func (s stack) demoOf(t *testing.T, parent owner, parentID uuid.UUID) owner {
	t.Helper()
	o := owner{slug: parent.slug + org.SandboxSuffix, email: "demo-" + unique() + "@example.test"}
	o.host = o.slug + ".tally.test"
	created, err := s.identity.CreateTenantWithOwner(context.Background(), identity.OwnerSpec{
		Tenant: identity.TenantSpec{
			ID: ids.New(), Slug: o.slug, DisplayName: "Cửa hàng demo", IsSandbox: true, SandboxOf: &parentID, ParentSlug: parent.slug,
		},
		Email: o.email, FullName: "Chủ demo", Source: audit.SourceCLI,
	})
	if err != nil {
		t.Fatal(err)
	}
	b := s.browser(t, o.host)
	b.hostContext()
	token := tokenRE.FindStringSubmatch(created.VerifyURL)[1]
	expect(t, b.do(http.MethodPost, v1+"/auth/verify", map[string]string{"token": token, "password": password}), http.StatusNoContent, "")
	return o
}

type tenantOut struct {
	ID        uuid.UUID  `json:"id"`
	Slug      string     `json:"slug"`
	Status    string     `json:"status"`
	IsSandbox bool       `json:"is_sandbox"`
	SandboxOf *uuid.UUID `json:"sandbox_of"`
}

// ledgerRows counts what a tenant holds in its append-only ledgers and the documents they post.
func ledgerRows(t *testing.T, tenantID uuid.UUID) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range []string{
		"price_changes", "stock_moves", "cash_entries", "payments", "obligations", "obligation_settlements",
		"audit_events", "products", "parties",
	} {
		out[table] = count(t, `SELECT count(*) FROM tally.`+table+` WHERE tenant_id = $1`, tenantID)
	}
	return out
}

// verifyLedgers is `tally ledger verify --tenant` for one tenant: every projection must agree
// with its ledger.
func verifyLedgers(t *testing.T, tenantID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	err := testkit.DB(t).WithReadOnlyTenantTx(ctx, tenantID, func(q db.Querier) error {
		for name, verify := range map[string]func(context.Context, db.Querier) ([]string, error){
			"debt": debt.Verify, "cash": cash.Verify, "inventory": inventory.Verify,
		} {
			lines, err := verify(ctx, q)
			if err != nil {
				return err
			}
			for _, line := range lines {
				t.Errorf("tenant %s %s: %s", tenantID, name, line)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify tenant %s: %v", tenantID, err)
	}
}

func TestSandboxResetProvisionsANewTenant(t *testing.T) {
	s, clk, _ := newSandboxStack(t)
	parent := s.signUp(t)
	store := s.signIn(t, parent)
	parentID := tenantOf(t, store)
	demo := s.demoOf(t, parent, parentID)
	b := s.signIn(t, demo)
	oldID := tenantOf(t, b)

	// The sandbox posts through the real services: a price change, opening stock, an opening debt
	// and a cash payment against it.
	prices := buildPricingStore(t, b, "SBX-1")
	expect(t, post(b, "/stock-adjustments", map[string]any{
		"kind": "opening", "lines": []map[string]any{{"sku_id": prices.sku, "qty": "24", "unit_cost": "8000"}}}),
		http.StatusCreated, "")
	ledger := buildLedgerStore(t, b)
	expect(t, post(b, "/payments", map[string]any{"direction": "in", "party_id": ledger.customer, "purpose": "settlement",
		"payment_method_id": ledger.cashMethod, "amount": "1000000",
		"allocations": []map[string]any{{"obligation_id": ledger.opening, "amount": "1000000"}}}), http.StatusCreated, "")
	before := ledgerRows(t, oldID)
	for _, table := range []string{"price_changes", "stock_moves", "cash_entries", "payments", "obligation_settlements"} {
		if before[table] == 0 {
			t.Fatalf("the sandbox has no %s before the reset: %v", table, before)
		}
	}

	// A sandbox younger than the reset interval refuses with Retry-After.
	rec := b.do(http.MethodPost, v1+"/tenant/sandbox/reset", nil)
	expect(t, rec, http.StatusTooManyRequests, "sandbox_reset_too_soon")
	if wait, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || wait < 1 || wait > int(org.SandboxResetInterval.Seconds()) {
		t.Fatalf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
	// Once old enough, the reset still needs a fresh password.
	clk.Advance(org.SandboxResetInterval)
	expect(t, b.do(http.MethodPost, v1+"/tenant/sandbox/reset", nil), http.StatusForbidden, "reauth_required")
	if got := tenantOf(t, b); got != oldID {
		t.Fatalf("a refused reset moved the session to %s", got)
	}
	expect(t, b.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
	before = ledgerRows(t, oldID) // the refused attempts and the re-auth may be audited

	rec = b.do(http.MethodPost, v1+"/tenant/sandbox/reset", nil)
	expect(t, rec, http.StatusOK, "")
	first := testkit.DecodeJSON[tenantOut](t, rec)
	if first.ID == oldID || first.Slug != demo.slug || first.Status != org.TenantActive || !first.IsSandbox ||
		first.SandboxOf == nil || *first.SandboxOf != parentID {
		t.Fatalf("reset = %+v", first)
	}

	t.Run("the old tenant keeps every row and is unreachable", func(t *testing.T) {
		after := ledgerRows(t, oldID)
		after["audit_events"]-- // the tenant.replace event
		for table, n := range before {
			if after[table] != n {
				t.Errorf("old %s = %d, want %d", table, after[table], n)
			}
		}
		var slug, status string
		if err := testkit.SuperDB(t).QueryRowx(`SELECT slug, status FROM tally.tenants WHERE id = $1`, oldID).Scan(&slug, &status); err != nil {
			t.Fatal(err)
		}
		if slug == demo.slug || status != org.TenantReplaced {
			t.Errorf("old tenant = %s %s", slug, status)
		}
		if _, found, err := s.identity.ResolveSlug(context.Background(), slug); err != nil || found {
			t.Errorf("the replaced address resolves: %v, %v", found, err)
		}
		if n := count(t, `SELECT count(*) FROM tally.users WHERE tenant_id = $1`, oldID) +
			count(t, `SELECT count(*) FROM tally.sessions WHERE tenant_id = $1`, oldID); n != 0 {
			t.Errorf("the old tenant keeps %d accounts and sessions", n)
		}
		if n := count(t, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND action = 'tenant.replace'`, oldID); n != 1 {
			t.Errorf("tenant.replace events = %d", n)
		}
	})

	t.Run("the session moved to the new tenant with fresh seeds", func(t *testing.T) {
		me := send[struct {
			Tenant tenantOut `json:"tenant"`
		}](t, b, http.MethodGet, "/me", nil).Tenant
		if me.ID != first.ID || me.Slug != demo.slug {
			t.Fatalf("me after reset = %+v", me)
		}
		fresh := ledgerRows(t, first.ID)
		if fresh["products"] == 0 || fresh["parties"] == 0 {
			t.Errorf("the new sandbox has no sample: %v", fresh)
		}
		for _, table := range []string{"stock_moves", "cash_entries", "payments", "obligation_settlements"} {
			if fresh[table] != 0 {
				t.Errorf("the new sandbox starts with %d %s", fresh[table], table)
			}
		}
		if n := count(t, `SELECT count(*) FROM tally.skus WHERE tenant_id = $1 AND code = 'SBX-1'`, first.ID); n != 0 {
			t.Error("the new sandbox has the SKU of the old one")
		}
		if n := count(t, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND action = 'tenant.sandbox_reset'`, first.ID); n != 1 {
			t.Errorf("tenant.sandbox_reset events = %d", n)
		}
		// Writing works on the new tenant under the moved session and its CSRF token.
		send[withID](t, b, http.MethodPost, "/customers", map[string]any{"kind": "person", "name": "Anh Minh"})
		// A fresh login on the address reaches the new tenant too.
		if got := tenantOf(t, s.signIn(t, demo)); got != first.ID {
			t.Errorf("login after reset reaches %s, want %s", got, first.ID)
		}
	})

	t.Run("a second reset replaces the new tenant", func(t *testing.T) {
		// A retry of the first reset reaches the new sandbox and is refused, not repeated.
		expect(t, b.do(http.MethodPost, v1+"/tenant/sandbox/reset", nil), http.StatusTooManyRequests, "sandbox_reset_too_soon")
		clk.Advance(org.SandboxResetInterval)
		expect(t, b.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
		rec := b.do(http.MethodPost, v1+"/tenant/sandbox/reset", nil)
		expect(t, rec, http.StatusOK, "")
		second := testkit.DecodeJSON[tenantOut](t, rec)
		if second.ID == first.ID || second.ID == oldID || second.Slug != demo.slug {
			t.Fatalf("second reset = %+v", second)
		}
		if got := tenantOf(t, b); got != second.ID {
			t.Errorf("session after the second reset is on %s", got)
		}
		if n := count(t, `SELECT count(*) FROM tally.tenants WHERE sandbox_of = $1 AND status = 'replaced'`, parentID); n != 2 {
			t.Errorf("replaced sandboxes = %d, want 2", n)
		}
		if n := count(t, `SELECT count(*) FROM tally.retired_tenant_slugs WHERE slug = $1`, demo.slug); n != 0 {
			t.Error("the sandbox address was retired")
		}
		for _, id := range []uuid.UUID{oldID, first.ID, second.ID} {
			verifyLedgers(t, id)
		}
	})

	t.Run("a real store cannot be reset", func(t *testing.T) {
		expect(t, store.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
		expect(t, store.do(http.MethodPost, v1+"/tenant/sandbox/reset", nil), http.StatusForbidden, "not_sandbox")
		if got := tenantOf(t, store); got != parentID {
			t.Errorf("the real store moved to %s", got)
		}
		verifyLedgers(t, parentID)
	})
}

// within fails the test unless ch is closed within a few seconds.
func within(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestLoginRacingASandboxReset(t *testing.T) {
	s, clk, hasher := newSandboxStack(t)
	parent := s.signUp(t)
	demo := s.demoOf(t, parent, tenantOf(t, s.signIn(t, parent)))
	clk.Advance(org.SandboxResetInterval)
	b := s.signIn(t, demo)
	oldID := tenantOf(t, b)

	// The login has read the account of the old tenant when the reset moves it.
	l := s.browser(t, demo.host)
	l.hostContext()
	reached, resume := hasher.pauseNextVerify()
	done := make(chan struct{})
	var login *httptest.ResponseRecorder
	go func() {
		defer close(done)
		login = l.do(http.MethodPost, v1+"/auth/login", map[string]string{"login": demo.email, "password": password})
	}()
	within(t, reached, "the login to verify the password")
	rec := b.do(http.MethodPost, v1+"/tenant/sandbox/reset", nil)
	resume()
	within(t, done, "the login")
	expect(t, rec, http.StatusOK, "")
	expect(t, login, http.StatusConflict, "sandbox_replaced")
	if n := count(t, `SELECT count(*) FROM tally.sessions WHERE tenant_id = $1`, oldID); n != 0 {
		t.Errorf("the replaced tenant has %d sessions", n)
	}
	// Signing in again reaches the new sandbox.
	if got := tenantOf(t, s.signIn(t, demo)); got == oldID {
		t.Error("a login after the reset reached the replaced tenant")
	}
}

// replaceTenant marks a store as a sandbox a reset has replaced: the state a request that raced the
// reset finds once it locks the tenant row.
func replaceTenant(t *testing.T, tenantID uuid.UUID, replaced bool) {
	t.Helper()
	status := "active"
	if replaced {
		status = "replaced"
	}
	testkit.SuperDB(t).MustExec(`UPDATE tally.tenants SET is_sandbox = true, status = $2 WHERE id = $1`, tenantID, status)
}

func TestAccountChangesRefuseAReplacedSandbox(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	plain := h.newAccount(t)
	_, p := h.login(t, plain, desktop)
	_, other := h.login(t, plain, desktop)
	if err := h.svc.RequestPasswordReset(ctx, plain.tenantID, plain.email); err != nil {
		t.Fatal(err)
	}
	resetToken := h.lastToken(t, plain.email, "Đặt lại mật khẩu")
	heir := uniqueEmail("heir")
	if err := h.svc.RequestOwnerTransfer(ctx, p, identity.TransferInput{Email: heir, FullName: "Chủ mới", Client: desktop}); err != nil {
		t.Fatal(err)
	}
	transferToken := h.lastToken(t, heir, "Bạn được mời nhận quyền chủ cửa hàng")

	// An owner with two-step verification, stopped at the MFA step of a login.
	secure := h.newAccount(t)
	_, sp := h.login(t, secure, desktop)
	enrol, err := h.svc.EnrolTOTP(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrol.Secret)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := h.svc.ConfirmTOTP(ctx, sp, authn.TOTPCode(secret, h.clk.Now()), desktop)
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.Login(ctx, identity.LoginInput{HostTenant: secure.tenantID, Login: secure.email, Password: password, Client: desktop})
	if err != nil || res.MFAChallenge == "" {
		t.Fatalf("login with MFA = %+v, %v", res, err)
	}
	challenge := res.MFAChallenge
	h.clk.Advance(authn.TOTPPeriod)
	code := authn.TOTPCode(secret, h.clk.Now())

	replaceTenant(t, plain.tenantID, true)
	replaceTenant(t, secure.tenantID, true)

	// Every account change of identity answers sandbox_replaced on a replaced tenant and spends
	// nothing.
	for _, c := range []struct {
		name string
		call func() error
	}{
		{"verify e-mail", func() error {
			return h.svc.VerifyEmail(ctx, identity.VerifyInput{HostTenant: plain.tenantID, Token: resetToken})
		}},
		{"request password reset", func() error { return h.svc.RequestPasswordReset(ctx, plain.tenantID, plain.email) }},
		{"reset password", func() error {
			return h.svc.ResetPassword(ctx, identity.ResetInput{HostTenant: plain.tenantID, Token: resetToken, Password: "mat-khau-moi-2"})
		}},
		{"login", func() error {
			_, err := h.svc.Login(ctx, identity.LoginInput{HostTenant: plain.tenantID, Login: plain.email, Password: password, Client: desktop})
			return err
		}},
		{"MFA step with a code", func() error {
			_, err := h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: secure.tenantID, Challenge: challenge, Code: code, Client: desktop})
			return err
		}},
		{"MFA step with a recovery code", func() error {
			_, err := h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: secure.tenantID, Challenge: challenge, RecoveryCode: codes[0], Client: desktop})
			return err
		}},
		{"re-authenticate", func() error { _, err := h.svc.Reauth(ctx, p, password, desktop); return err }},
		{"re-authenticate with a wrong password", func() error {
			_, err := h.svc.Reauth(ctx, p, "not-the-password", desktop)
			return err
		}},
		{"change password", func() error { _, err := h.svc.ChangePassword(ctx, p, "mat-khau-moi-2", desktop); return err }},
		{"enrol TOTP", func() error { _, err := h.svc.EnrolTOTP(ctx, p); return err }},
		{"confirm TOTP", func() error { _, err := h.svc.ConfirmTOTP(ctx, p, "000000", desktop); return err }},
		{"disable TOTP", func() error { return h.svc.DisableTOTP(ctx, sp, desktop) }},
		{"request transfer", func() error {
			return h.svc.RequestOwnerTransfer(ctx, p, identity.TransferInput{Email: uniqueEmail("heir"), FullName: "Chủ khác", Client: desktop})
		}},
		{"accept transfer", func() error {
			return h.svc.AcceptOwnerTransfer(ctx, identity.AcceptInput{HostTenant: plain.tenantID, Token: transferToken, Password: password})
		}},
		{"revoke a session", func() error { return h.svc.RevokeSession(ctx, p, other.SessionID, desktop) }},
		{"revoke the others", func() error { _, err := h.svc.RevokeOtherSessions(ctx, p, desktop); return err }},
		{"revoke every session", func() error { _, err := h.svc.RevokeTenantSessions(ctx, plain.tenantID, "incident"); return err }},
		{"grant support consent", func() error {
			_, err := h.svc.GrantSupportConsent(ctx, p, h.clk.Now().Add(time.Hour), func(db.Querier, uuid.UUID) error { return nil })
			return err
		}},
		{"revoke support consent", func() error {
			return h.svc.RevokeSupportConsent(ctx, p, uuid.New(), func(db.Querier) error { return nil })
		}},
		{"issue a closure link", func() error {
			return h.svc.IssueClosureToken(ctx, p, func(db.Querier) (uuid.UUID, error) { return uuid.New(), nil })
		}},
		{"consume a closure link", func() error {
			return h.svc.ConsumeClosureToken(ctx, p, "closure-token", func(db.Querier, uuid.UUID) error { return nil })
		}},
	} {
		if err := c.call(); !apperr.Is(err, "sandbox_replaced") {
			t.Errorf("%s = %v, want sandbox_replaced", c.name, err)
		}
	}

	// A wrong password says nothing about the tenant.
	_, err = h.svc.Login(ctx, identity.LoginInput{HostTenant: plain.tenantID, Login: plain.email, Password: "not-the-password", Client: desktop})
	wantCode(t, err, "invalid_credentials")
	if n := count(t, `SELECT count(*) FROM tally.sessions WHERE tenant_id = $1 AND revoked_at IS NULL`, plain.tenantID); n != 2 {
		t.Errorf("open sessions of the replaced tenant = %d, want 2", n)
	}
	// Signing out needs no tenant: the session ends wherever it is.
	if err := h.svc.Logout(ctx, p); err != nil {
		t.Fatalf("logout = %v", err)
	}
	if n := count(t, `SELECT count(*) FROM tally.sessions WHERE id = $1 AND revoked_at IS NULL`, p.SessionID); n != 0 {
		t.Error("logout left the session open")
	}

	// Nothing was spent: the same token, TOTP code and recovery code still work once the tenant is
	// back.
	replaceTenant(t, plain.tenantID, false)
	replaceTenant(t, secure.tenantID, false)
	if err := h.svc.ResetPassword(ctx, identity.ResetInput{HostTenant: plain.tenantID, Token: resetToken, Password: "mat-khau-moi-2"}); err != nil {
		t.Errorf("reset password after the refusal = %v", err)
	}
	if _, err := h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: secure.tenantID, Challenge: challenge, Code: code, Client: desktop}); err != nil {
		t.Errorf("TOTP code after the refusal = %v", err)
	}
	if _, err := h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: secure.tenantID, Challenge: challenge, RecoveryCode: codes[0], Client: desktop}); err != nil {
		t.Errorf("recovery code after the refusal = %v", err)
	}
}

func TestSessionCallsAfterASandboxResetMovedTheSession(t *testing.T) {
	s, clk, _ := newSandboxStack(t)
	parent := s.signUp(t)
	demo := s.demoOf(t, parent, tenantOf(t, s.signIn(t, parent)))
	clk.Advance(org.SandboxResetInterval)
	b := s.signIn(t, demo)
	ctx := context.Background()
	// The principal of a request that resolved its session just before the reset moved it.
	p, found, err := s.identity.ResolveSession(ctx, b.cookies["tally_session"].Value)
	if err != nil || !found {
		t.Fatalf("resolve session = %v, %v", found, err)
	}
	expect(t, b.do(http.MethodPost, v1+"/tenant/sandbox/reset", nil), http.StatusOK, "")
	if n := count(t, `SELECT count(*) FROM tally.sessions WHERE id = $1 AND tenant_id <> $2`, p.SessionID, p.TenantID); n != 1 {
		t.Fatal("the reset did not move the session")
	}

	client := identity.Client{IP: "203.0.113.9", UserAgent: "Mozilla/5.0"}
	_, err = s.identity.Reauth(ctx, p, password, client)
	wantCode(t, err, "sandbox_replaced")
	if err := s.identity.Logout(ctx, p); err != nil {
		t.Fatalf("logout = %v", err)
	}
	if n := count(t, `SELECT count(*) FROM tally.sessions WHERE id = $1 AND revoked_at IS NULL`, p.SessionID); n != 0 {
		t.Error("logout left the moved session open")
	}
	expect(t, b.do(http.MethodGet, v1+"/me", nil), http.StatusUnauthorized, "unauthenticated")
}
