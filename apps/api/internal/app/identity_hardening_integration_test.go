//go:build integration

package app_test

import (
	"context"
	"encoding/base32"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// countingHasher counts the password work of a service.
type countingHasher struct {
	identity.PasswordHasher
	hash, verify, dummy atomic.Int32
}

func (c *countingHasher) Hash(pw string) (string, error) {
	c.hash.Add(1)
	return c.PasswordHasher.Hash(pw)
}

func (c *countingHasher) Verify(encoded, pw string) (bool, error) {
	c.verify.Add(1)
	return c.PasswordHasher.Verify(encoded, pw)
}

func (c *countingHasher) DummyVerify(pw string) {
	c.dummy.Add(1)
	c.PasswordHasher.DummyVerify(pw)
}

// countingProvisioner counts the tenants a service provisions (committed or rolled back).
type countingProvisioner struct {
	identity.TenantProvisioner
	provisions atomic.Int32
}

func (c *countingProvisioner) Provision(ctx context.Context, q db.Querier, spec identity.TenantSpec, actor audit.Actor) (identity.TenantInfo, bool, error) {
	c.provisions.Add(1)
	return c.TenantProvisioner.Provision(ctx, q, spec, actor)
}

// A sign-up with a registered e-mail must not be told apart from a new one, not even by how long
// it takes: both run the same password hashing and tenant provisioning and send one mail.
func TestSignupDoesTheSameWorkForARegisteredEmail(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	registered := h.newAccount(t)

	deps := identityDeps(t, h.mail, h.clk)
	hasher := &countingHasher{PasswordHasher: deps.Hasher}
	prov := &countingProvisioner{TenantProvisioner: deps.Provisioner}
	deps.Hasher, deps.Provisioner = hasher, prov
	svc, err := identity.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}

	type work struct {
		hash, verify, dummy, provisions int32
		mails                           int
		storeURL                        string
	}
	signup := func(email, slug string) work {
		t.Helper()
		before := len(h.mail.to(email))
		hasher.hash.Store(0)
		hasher.verify.Store(0)
		hasher.dummy.Store(0)
		prov.provisions.Store(0)
		res, err := svc.Signup(ctx, identity.SignupInput{
			Email: email, Password: password, FullName: "Người đăng ký", StoreName: "Cửa hàng " + slug, Slug: slug, Client: desktop,
		})
		if err != nil {
			t.Fatalf("signup %s = %v", email, err)
		}
		svc.WaitMail()
		return work{
			hash: hasher.hash.Load(), verify: hasher.verify.Load(), dummy: hasher.dummy.Load(), provisions: prov.provisions.Load(),
			mails: len(h.mail.to(email)) - before, storeURL: res.StoreURL,
		}
	}

	freshSlug, takenSlug := "s-"+unique(), "s-"+unique()
	fresh := signup(uniqueEmail("fresh"), freshSlug)
	existing := signup(registered.email, takenSlug)
	if fresh.storeURL != "http://"+freshSlug+".tally.test/" || existing.storeURL != "http://"+takenSlug+".tally.test/" {
		t.Errorf("store URLs = %q, %q", fresh.storeURL, existing.storeURL)
	}
	fresh.storeURL, existing.storeURL = "", ""
	if fresh != existing {
		t.Errorf("work for a new e-mail = %+v, for a registered one = %+v", fresh, existing)
	}
	if want := (work{hash: 1, provisions: 1, mails: 1}); fresh != want {
		t.Errorf("work = %+v, want %+v", fresh, want)
	}
	if n := count(t, `SELECT count(*) FROM tally.tenants WHERE slug = $1`, takenSlug); n != 0 {
		t.Error("the provisioning of a registered e-mail must roll back")
	}
}

func TestEnablingMFASignsOutTheOtherSessions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.newAccount(t)
	current, p := h.login(t, a, desktop)
	other, _ := h.login(t, a, desktop)

	enrol, err := h.svc.EnrolTOTP(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrol.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ConfirmTOTP(ctx, p, authn.TOTPCode(secret, h.clk.Now()), desktop); err != nil {
		t.Fatalf("confirm = %v", err)
	}
	h.gone(t, other)
	if mp := h.resolve(t, current); mp.MfaVerifiedAt == nil {
		t.Error("the session that turned MFA on stays signed in and verified")
	}
	if n := count(t, `SELECT count(*) FROM tally.audit_events
		WHERE tenant_id = $1 AND action = 'user.enable_mfa' AND (after->>'sessions_revoked')::int = 1`, a.tenantID); n != 1 {
		t.Errorf("user.enable_mfa audit events with sessions_revoked = 1: %d", n)
	}
}

func TestChangePasswordSignsOutTheOtherSessions(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.newAccount(t)
	current, p := h.login(t, a, desktop)
	other, _ := h.login(t, a, desktop)

	_, err := h.svc.ChangePassword(ctx, p, "short", desktop)
	if ae, ok := apperr.As(err); !ok || ae.Fields["password"] != "too_short" {
		t.Errorf("short password = %v", err)
	}
	h.resolve(t, other)

	const newPassword = "mat-khau-moi-doi"
	n, err := h.svc.ChangePassword(ctx, p, newPassword, desktop)
	if err != nil || n != 1 {
		t.Fatalf("change password = %d, %v", n, err)
	}
	h.gone(t, other)
	h.resolve(t, current)
	_, err = h.svc.Login(ctx, identity.LoginInput{HostTenant: a.tenantID, Login: a.email, Password: password, Client: desktop})
	wantCode(t, err, "invalid_credentials")
	if res, err := h.svc.Login(ctx, identity.LoginInput{HostTenant: a.tenantID, Login: a.email, Password: newPassword, Client: desktop}); err != nil || res.Session == nil {
		t.Errorf("login with the new password = %v", err)
	}
	if n := count(t, `SELECT count(*) FROM tally.audit_events
		WHERE tenant_id = $1 AND action = 'user.change_password' AND actor_id = $2 AND (after->>'sessions_revoked')::int = 1`,
		a.tenantID, p.UserID); n != 1 {
		t.Errorf("user.change_password audit events = %d", n)
	}
}

func TestFailedReauthIsAuditedAndThrottled(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.newAccount(t)
	_, p := h.login(t, a, desktop)
	h.clk.Advance(time.Second) // failures count after the last success

	for range identity.MaxFailedLogins {
		_, err := h.svc.Reauth(ctx, p, "not-the-password", desktop)
		wantCode(t, err, "invalid_password")
		h.clk.Advance(time.Second)
	}
	if n := count(t, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND action = 'user.reject_reauth' AND actor_id = $2`,
		a.tenantID, p.UserID); n != identity.MaxFailedLogins {
		t.Errorf("user.reject_reauth audit events = %d, want %d", n, identity.MaxFailedLogins)
	}
	_, err := h.svc.Reauth(ctx, p, password, desktop)
	var throttled *identity.ThrottledError
	if !errors.As(err, &throttled) {
		t.Fatalf("re-authentication while locked = %v", err)
	}
	wantCode(t, err, "rate_limited")
	// Re-authentication and login share the throttle.
	_, err = h.svc.Login(ctx, identity.LoginInput{HostTenant: a.tenantID, Login: a.email, Password: password, Client: desktop})
	wantCode(t, err, "rate_limited")
}

func TestLoginWithPhoneNumber(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	slug, email := "s-"+unique(), uniqueEmail("phone")

	_, err := h.svc.Signup(ctx, identity.SignupInput{
		Email: email, Phone: "12345", Password: password, FullName: "Chủ có số", StoreName: "Cửa hàng " + slug, Slug: slug,
	})
	if ae, ok := apperr.As(err); !ok || ae.Fields["phone"] != "invalid" {
		t.Fatalf("sign-up with an invalid phone = %v", err)
	}
	if _, err := h.svc.Signup(ctx, identity.SignupInput{
		Email: email, Phone: "+84 912.418.226", Password: password, FullName: "Chủ có số", StoreName: "Cửa hàng " + slug, Slug: slug,
	}); err != nil {
		t.Fatalf("signup = %v", err)
	}
	tenantID, found, err := h.svc.ResolveSlug(ctx, slug)
	if err != nil || !found {
		t.Fatalf("resolve slug = %v, %v", found, err)
	}
	if n := count(t, `SELECT count(*) FROM tally.users WHERE tenant_id = $1 AND phone = '0912418226'`, tenantID); n != 1 {
		t.Fatalf("users with the normalised phone = %d", n)
	}
	if err := h.svc.VerifyEmail(ctx, identity.VerifyInput{HostTenant: tenantID, Token: h.lastToken(t, email, "Xác nhận e-mail")}); err != nil {
		t.Fatal(err)
	}

	for _, typed := range []string{"0912 418 226", "+84912418226", "84-912-418-226"} {
		res, err := h.svc.Login(ctx, identity.LoginInput{HostTenant: tenantID, Login: typed, Password: password, Client: desktop})
		if err != nil || res.Session == nil {
			t.Fatalf("login with %q = %+v, %v", typed, res, err)
		}
		if p := h.resolve(t, res.Session.Token); p.TenantID != tenantID {
			t.Errorf("principal = %+v", p)
		}
	}

	other := h.newAccount(t)
	for name, in := range map[string]identity.LoginInput{
		"wrong password":      {HostTenant: tenantID, Login: "0912418226", Password: "not-the-password"},
		"phone on other host": {HostTenant: other.tenantID, Login: "0912418226", Password: password},
		"unknown phone":       {HostTenant: tenantID, Login: "0987654321", Password: password},
		"not a phone":         {HostTenant: tenantID, Login: "12345", Password: password},
	} {
		in.Client = identity.Client{IP: "198.51.100.2"}
		if _, err := h.svc.Login(ctx, in); !apperr.Is(err, "invalid_credentials") {
			t.Errorf("%s = %v, want invalid_credentials", name, err)
		}
	}

	// The throttle counts the identifier as typed: a locked phone leaves the e-mail usable.
	wrong := identity.LoginInput{HostTenant: tenantID, Login: "0912418226", Password: "sai-mat-khau", Client: desktop}
	h.clk.Advance(time.Second) // failures count after the last success
	for range identity.MaxFailedLogins {
		_, _ = h.svc.Login(ctx, wrong)
		h.clk.Advance(time.Second)
	}
	right := wrong
	right.Password = password
	_, err = h.svc.Login(ctx, right)
	wantCode(t, err, "rate_limited")
	if res, err := h.svc.Login(ctx, identity.LoginInput{HostTenant: tenantID, Login: email, Password: password, Client: desktop}); err != nil || res.Session == nil {
		t.Errorf("login with the e-mail while the phone is locked = %v", err)
	}
}

// The signed-in password change answers how many other sessions it signed out.
func TestChangePasswordEndpoint(t *testing.T) {
	s := newStack(t)
	o := s.signUp(t)
	b := s.signIn(t, o)
	other := s.signIn(t, o)
	rec := b.do(http.MethodPost, "/api/v1/auth/password", map[string]string{"password": "mat-khau-moi-http"})
	expect(t, rec, http.StatusOK, "")
	if body := rec.Body.String(); body != `{"revoked":1}` {
		t.Errorf("change password body = %s", body)
	}
	expect(t, other.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "")
	expect(t, b.do(http.MethodGet, "/api/v1/me", nil), http.StatusOK, "")
}
