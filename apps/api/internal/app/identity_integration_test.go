//go:build integration

package app_test

import (
	"context"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/authn"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/mailer"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/urls"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// captureMailer keeps every message it is handed.
type captureMailer struct {
	mu   sync.Mutex
	msgs []mailer.Message
}

func (c *captureMailer) Send(_ context.Context, msg mailer.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, msg)
	return nil
}

// to returns the messages sent to addr, oldest first.
func (c *captureMailer) to(addr string) []mailer.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []mailer.Message
	for _, m := range c.msgs {
		for _, a := range m.To {
			if strings.EqualFold(a.Address, addr) {
				out = append(out, m)
			}
		}
	}
	return out
}

type harness struct {
	svc  *identity.Service
	clk  *clock.Fixed
	mail *captureMailer
}

const password = "mat-khau-dung-1"

var testHasherParams = authn.Params{Memory: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}

// testBaseURL is the apex of the test hosts.
var testBaseURL = &url.URL{Scheme: "http", Host: "tally.test"}

// identityDeps are the collaborators of an identity service on the test database: cheap
// password hashing, the real organisation provisioner without default data seeders, and m.
func identityDeps(t *testing.T, m mailer.Mailer, clk clock.Clock) identity.Deps {
	t.Helper()
	keys, err := identity.NewKeys("test-session-hash-key", "test-encryption-key")
	if err != nil {
		t.Fatal(err)
	}
	hasher, err := authn.NewHasher(testHasherParams)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := urls.New(testBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	rec := audit.NewRecorder(clk)
	return identity.Deps{
		DB: testkit.DB(t), Clock: clk, Audit: rec, Hasher: hasher, Keys: keys,
		Provisioner: app.NewTenantProvisioner(org.NewProvisioner(org.NewSlugPolicy(nil), rec, clk, app.NewDomains(testkit.DB(t), clk, rec).Seeders())),
		URLs:        builder, Mailer: m, Renderer: mailer.NewRenderer(mailer.Templates),
	}
}

func newService(t *testing.T, m mailer.Mailer, clk clock.Clock) *identity.Service {
	t.Helper()
	svc, err := identity.NewService(identityDeps(t, m, clk))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func newHarness(t *testing.T) harness {
	t.Helper()
	h := harness{clk: testkit.Clock(), mail: &captureMailer{}}
	h.svc = newService(t, h.mail, h.clk)
	return h
}

// unique returns a short random label for slugs and addresses.
func unique() string { return strings.ReplaceAll(ids.New().String(), "-", "")[20:] }

func uniqueEmail(prefix string) string { return prefix + "-" + unique() + "@example.test" }

var tokenRE = regexp.MustCompile(`token=([A-Za-z0-9_-]{43})`)

// lastToken returns the token of the newest mail to addr whose subject contains subject.
func (h harness) lastToken(t *testing.T, addr, subject string) string {
	t.Helper()
	h.svc.WaitMail()
	msgs := h.mail.to(addr)
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.Contains(msgs[i].Subject, subject) {
			if m := tokenRE.FindStringSubmatch(msgs[i].Text); m != nil {
				return m[1]
			}
		}
	}
	t.Fatalf("no %q mail with a token to %s", subject, addr)
	return ""
}

type account struct {
	tenantID uuid.UUID
	slug     string
	email    string
}

// newAccount signs up a store and verifies its owner.
func (h harness) newAccount(t *testing.T) account {
	t.Helper()
	ctx := context.Background()
	a := account{slug: "s-" + unique(), email: uniqueEmail("owner")}
	if _, err := h.svc.Signup(ctx, identity.SignupInput{
		Email: a.email, Password: password, FullName: "Nguyễn Văn A", StoreName: "Cửa hàng " + a.slug, Slug: a.slug,
	}); err != nil {
		t.Fatalf("signup: %v", err)
	}
	id, found, err := h.svc.ResolveSlug(ctx, a.slug)
	if err != nil || !found {
		t.Fatalf("resolve slug: %v %v", found, err)
	}
	a.tenantID = id
	token := h.lastToken(t, a.email, "Xác nhận e-mail")
	if err := h.svc.VerifyEmail(ctx, identity.VerifyInput{HostTenant: id, Token: token}); err != nil {
		t.Fatalf("verify: %v", err)
	}
	return a
}

// desktop is the client of the account actions.
var desktop = identity.Client{IP: "203.0.113.7", UserAgent: "Mozilla/5.0 (Windows NT 10.0) Chrome/129.0 Safari/537.36"}

// login signs in and resolves the new session.
func (h harness) login(t *testing.T, a account, c identity.Client) (string, middleware.Principal) {
	t.Helper()
	res, err := h.svc.Login(context.Background(), identity.LoginInput{HostTenant: a.tenantID, Login: a.email, Password: password, Client: c})
	if err != nil || res.Session == nil {
		t.Fatalf("login = %+v, %v", res, err)
	}
	return res.Session.Token, h.resolve(t, res.Session.Token)
}

func (h harness) resolve(t *testing.T, token string) middleware.Principal {
	t.Helper()
	p, found, err := h.svc.ResolveSession(context.Background(), token)
	if err != nil || !found {
		t.Fatalf("resolve session = %v, %v", found, err)
	}
	return p
}

func (h harness) gone(t *testing.T, token string) {
	t.Helper()
	if _, found, err := h.svc.ResolveSession(context.Background(), token); err != nil || found {
		t.Errorf("session still resolves (%v, %v)", found, err)
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if !apperr.Is(err, code) {
		t.Errorf("error = %v, want %s", err, code)
	}
}

func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := testkit.SuperDB(t).Get(&n, query, args...); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSignupVerifyAndLogin(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	slug, email := "s-"+unique(), uniqueEmail("chu")
	res, err := h.svc.Signup(ctx, identity.SignupInput{
		Email: email, Password: password, FullName: "Trần Thị B", StoreName: "Tạp hoá Cô Ba", Slug: slug, Client: desktop,
	})
	if err != nil || res.StoreURL != "http://"+slug+".tally.test/" {
		t.Fatalf("signup = %+v, %v", res, err)
	}
	tenantID, found, err := h.svc.ResolveSlug(ctx, slug)
	if err != nil || !found {
		t.Fatalf("resolve slug = %v, %v", found, err)
	}
	for table, want := range map[string]int{"tally.stores": 1, "tally.warehouses": 1, "tally.tenant_settings": 1, "tally.users": 1} {
		if n := count(t, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tenantID); n != want {
			t.Errorf("%s rows = %d, want %d", table, n, want)
		}
	}
	if n := count(t, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND action = 'tenant.create'`, tenantID); n != 1 {
		t.Errorf("tenant.create audit events = %d", n)
	}

	_, err = h.svc.Login(ctx, identity.LoginInput{HostTenant: tenantID, Login: email, Password: password, Client: desktop})
	wantCode(t, err, "email_not_verified")

	token := h.lastToken(t, email, "Xác nhận e-mail cho cửa hàng Tạp hoá Cô Ba")
	if err := h.svc.VerifyEmail(ctx, identity.VerifyInput{HostTenant: uuid.New(), Token: token}); !apperr.Is(err, "token_invalid") {
		t.Errorf("token on another host = %v", err)
	}
	if err := h.svc.VerifyEmail(ctx, identity.VerifyInput{HostTenant: tenantID, Token: token, Client: desktop}); err != nil {
		t.Fatalf("verify = %v", err)
	}
	wantCode(t, h.svc.VerifyEmail(ctx, identity.VerifyInput{HostTenant: tenantID, Token: token}), "token_invalid")

	sessionToken, p := h.login(t, account{tenantID: tenantID, slug: slug, email: strings.ToUpper(email)}, desktop)
	if p.Kind != middleware.PrincipalOwner || p.TenantID != tenantID || p.MfaVerifiedAt != nil || p.ReauthenticatedAt == nil {
		t.Errorf("principal = %+v", p)
	}
	me, err := h.svc.Me(ctx, p)
	if err != nil || me.User.Email != email || me.Tenant.Slug != slug || me.Session.ID != p.SessionID {
		t.Errorf("me = %+v, %v", me, err)
	}
	if err := h.svc.Logout(ctx, p); err != nil {
		t.Fatal(err)
	}
	h.gone(t, sessionToken)
}

func TestSignupWithRegisteredEmailCreatesNothing(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.newAccount(t)
	slug := "s-" + unique()
	res, err := h.svc.Signup(ctx, identity.SignupInput{
		Email: a.email, Password: "another-password", FullName: "Kẻ lạ", StoreName: "Cửa hàng khác", Slug: slug,
	})
	if err != nil || res.StoreURL != "http://"+slug+".tally.test/" {
		t.Fatalf("signup = %+v, %v; want the answer of a new account", res, err)
	}
	if n := count(t, `SELECT count(*) FROM tally.tenants WHERE slug = $1`, slug); n != 0 {
		t.Errorf("tenant created for a registered e-mail")
	}
	h.svc.WaitMail()
	msgs := h.mail.to(a.email)
	last := msgs[len(msgs)-1]
	if last.Subject != "E-mail của bạn đã có tài khoản Tally" || !strings.Contains(last.Text, "http://"+a.slug+".tally.test/") {
		t.Errorf("last mail = %q %q", last.Subject, last.Text)
	}

	// A taken store address is reported: the live check reveals it anyway.
	_, err = h.svc.Signup(ctx, identity.SignupInput{
		Email: uniqueEmail("new"), Password: password, FullName: "Người mới", StoreName: "Trùng địa chỉ", Slug: a.slug,
	})
	wantCode(t, err, "slug_taken")
}

func TestLoginOnlyAcceptsTheOwnerOfTheHostStore(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, b := h.newAccount(t), h.newAccount(t)
	attempts := map[string]identity.LoginInput{
		"owner of A on host B": {HostTenant: b.tenantID, Login: a.email, Password: password},
		"wrong password":       {HostTenant: b.tenantID, Login: b.email, Password: "not-the-password"},
		"unknown e-mail":       {HostTenant: b.tenantID, Login: uniqueEmail("ghost"), Password: password},
		"unknown host":         {HostTenant: uuid.Nil, Login: a.email, Password: password},
	}
	for name, in := range attempts {
		in.Client = identity.Client{IP: "198.51.100.1"}
		_, err := h.svc.Login(ctx, in)
		ae, ok := apperr.As(err)
		if !ok || ae.Kind != apperr.Unauthenticated || ae.Code != "invalid_credentials" || ae.Message != "The e-mail or password is not correct" {
			t.Errorf("%s = %v, want the single invalid_credentials answer", name, err)
		}
	}
}

func TestLoginThrottleLocksAfterRepeatedFailures(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.newAccount(t)
	wrong := identity.LoginInput{HostTenant: a.tenantID, Login: a.email, Password: "sai-mat-khau", Client: desktop}
	for range identity.MaxFailedLogins {
		_, err := h.svc.Login(ctx, wrong)
		wantCode(t, err, "invalid_credentials")
		h.clk.Advance(time.Second)
	}
	right := wrong
	right.Password = password
	_, err := h.svc.Login(ctx, right)
	var throttled *identity.ThrottledError
	if !errors.As(err, &throttled) || throttled.RetryAfter != identity.LockDuration-time.Second {
		t.Fatalf("login while locked = %v", err)
	}
	wantCode(t, err, "rate_limited")

	// The lock is per e-mail and client address.
	other := right
	other.Client.IP = "203.0.113.99"
	if res, err := h.svc.Login(ctx, other); err != nil || res.Session == nil {
		t.Errorf("login from another address = %v", err)
	}

	h.clk.Advance(identity.LockDuration)
	if res, err := h.svc.Login(ctx, right); err != nil || res.Session == nil {
		t.Errorf("login after the lock = %v", err)
	}
	if n := count(t, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND action = 'user.reject_login'`, a.tenantID); n != identity.MaxFailedLogins {
		t.Errorf("user.reject_login audit events = %d", n)
	}
}

func TestExpiredAndReusedTokens(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.newAccount(t)
	oldSession, _ := h.login(t, a, desktop)

	if err := h.svc.RequestPasswordReset(ctx, a.tenantID, a.email); err != nil {
		t.Fatal(err)
	}
	expired := h.lastToken(t, a.email, "Đặt lại mật khẩu")
	h.clk.Advance(identity.ResetTokenTTL + time.Second)
	wantCode(t, h.svc.ResetPassword(ctx, identity.ResetInput{HostTenant: a.tenantID, Token: expired, Password: "mat-khau-moi-1"}), "token_invalid")

	if err := h.svc.RequestPasswordReset(ctx, a.tenantID, a.email); err != nil {
		t.Fatal(err)
	}
	fresh := h.lastToken(t, a.email, "Đặt lại mật khẩu")
	if fresh == expired {
		t.Fatal("a new reset link must carry a new token")
	}
	if err := h.svc.ResetPassword(ctx, identity.ResetInput{HostTenant: a.tenantID, Token: fresh, Password: "mat-khau-moi-1"}); err != nil {
		t.Fatalf("reset = %v", err)
	}
	h.gone(t, oldSession)
	wantCode(t, h.svc.ResetPassword(ctx, identity.ResetInput{HostTenant: a.tenantID, Token: fresh, Password: "mat-khau-moi-2"}), "token_invalid")
	if res, err := h.svc.Login(ctx, identity.LoginInput{HostTenant: a.tenantID, Login: a.email, Password: "mat-khau-moi-1", Client: desktop}); err != nil || res.Session == nil {
		t.Errorf("login with the new password = %v", err)
	}

	// A reset for an address that is not the owner of the host store sends nothing.
	stranger := uniqueEmail("stranger")
	if err := h.svc.RequestPasswordReset(ctx, a.tenantID, stranger); err != nil {
		t.Fatal(err)
	}

	// An unused verification link expires after VerifyTokenTTL.
	created, err := h.svc.CreateTenantWithOwner(ctx, identity.OwnerSpec{
		Tenant: identity.TenantSpec{Slug: "s-" + unique(), DisplayName: "Cửa hàng chậm"}, Email: uniqueEmail("late"), FullName: "Chủ chậm",
		Source: audit.SourceCLI,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.clk.Advance(identity.VerifyTokenTTL + time.Second)
	token := tokenRE.FindStringSubmatch(created.VerifyURL)[1]
	wantCode(t, h.svc.VerifyEmail(ctx, identity.VerifyInput{HostTenant: created.Tenant.ID, Token: token, Password: password}), "token_invalid")

	h.svc.WaitMail()
	if got := h.mail.to(stranger); len(got) != 0 {
		t.Errorf("mails to a stranger = %d", len(got))
	}
}

func TestVerifyWithoutPasswordNeedsOne(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	email := uniqueEmail("cli")
	created, err := h.svc.CreateTenantWithOwner(ctx, identity.OwnerSpec{
		Tenant: identity.TenantSpec{Slug: "s-" + unique(), DisplayName: "Cửa hàng CLI"}, Email: email, FullName: "Chủ CLI", Source: audit.SourceCLI,
	})
	if err != nil {
		t.Fatal(err)
	}
	token := tokenRE.FindStringSubmatch(created.VerifyURL)[1]
	err = h.svc.VerifyEmail(ctx, identity.VerifyInput{HostTenant: created.Tenant.ID, Token: token})
	if ae, ok := apperr.As(err); !ok || ae.Fields["password"] != "required" {
		t.Fatalf("verify without password = %v", err)
	}
	// The failed attempt rolled back: the link still works once.
	if err := h.svc.VerifyEmail(ctx, identity.VerifyInput{HostTenant: created.Tenant.ID, Token: token, Password: password}); err != nil {
		t.Fatalf("verify with password = %v", err)
	}
	h.login(t, account{tenantID: created.Tenant.ID, email: email}, desktop)
}

func TestTOTPAndRecoveryCodes(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.newAccount(t)
	_, p := h.login(t, a, desktop)

	enrol, err := h.svc.EnrolTOTP(ctx, p)
	if err != nil || !strings.HasPrefix(enrol.URI, "otpauth://totp/") {
		t.Fatalf("enrol = %+v, %v", enrol, err)
	}
	secret, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(enrol.Secret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.svc.ConfirmTOTP(ctx, p, "000000", desktop)
	if authn.TOTPCode(secret, h.clk.Now()) != "000000" {
		wantCode(t, err, "invalid_code")
	}
	codes, err := h.svc.ConfirmTOTP(ctx, p, authn.TOTPCode(secret, h.clk.Now()), desktop)
	if err != nil || len(codes) != 10 {
		t.Fatalf("confirm = %d codes, %v", len(codes), err)
	}
	if _, err := h.svc.EnrolTOTP(ctx, p); !apperr.Is(err, "mfa_already_enabled") {
		t.Errorf("enrol twice = %v", err)
	}

	challenge := func() string {
		t.Helper()
		res, err := h.svc.Login(ctx, identity.LoginInput{HostTenant: a.tenantID, Login: a.email, Password: password, Client: desktop})
		if err != nil || res.Session != nil || res.MFAChallenge == "" {
			t.Fatalf("login with MFA = %+v, %v", res, err)
		}
		return res.MFAChallenge
	}

	ch := challenge()
	other := h.newAccount(t)
	_, err = h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: other.tenantID, Challenge: ch, Code: authn.TOTPCode(secret, h.clk.Now())})
	wantCode(t, err, "challenge_invalid")
	_, err = h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: a.tenantID, Challenge: ch})
	wantCode(t, err, "validation_failed")
	wrong := "123456"
	if authn.TOTPCode(secret, h.clk.Now()) == wrong {
		wrong = "654321"
	}
	_, err = h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: a.tenantID, Challenge: ch, Code: wrong, Client: desktop})
	wantCode(t, err, "invalid_code")
	// The code that confirmed the set-up is spent: it cannot also complete a sign-in.
	_, err = h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: a.tenantID, Challenge: ch, Code: authn.TOTPCode(secret, h.clk.Now()), Client: desktop})
	wantCode(t, err, "invalid_code")
	h.clk.Advance(authn.TOTPPeriod)
	code := authn.TOTPCode(secret, h.clk.Now())
	issued, err := h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: a.tenantID, Challenge: ch, Code: code, Client: desktop})
	if err != nil {
		t.Fatalf("verify TOTP = %v", err)
	}
	if mp := h.resolve(t, issued.Token); mp.MfaVerifiedAt == nil {
		t.Error("an MFA login must mark the session verified")
	}
	// A code works once, even with the same challenge and inside its time window, and an older
	// code that is still inside the skew window is refused once a newer one was accepted.
	_, err = h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: a.tenantID, Challenge: ch, Code: code, Client: desktop})
	wantCode(t, err, "invalid_code")
	_, err = h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: a.tenantID, Challenge: challenge(), Code: authn.TOTPCode(secret, h.clk.Now().Add(-authn.TOTPPeriod)), Client: desktop})
	wantCode(t, err, "invalid_code")

	ch = challenge()
	if _, err := h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: a.tenantID, Challenge: ch, RecoveryCode: strings.ToLower(codes[0]), Client: desktop}); err != nil {
		t.Fatalf("recovery code = %v", err)
	}
	ch = challenge()
	_, err = h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: a.tenantID, Challenge: ch, RecoveryCode: codes[0], Client: desktop})
	wantCode(t, err, "invalid_code")

	h.clk.Advance(identity.ChallengeTTL + time.Second)
	_, err = h.svc.VerifyChallenge(ctx, identity.ChallengeInput{HostTenant: a.tenantID, Challenge: ch, RecoveryCode: codes[1], Client: desktop})
	wantCode(t, err, "challenge_invalid")

	if err := h.svc.DisableTOTP(ctx, p, desktop); err != nil {
		t.Fatal(err)
	}
	if n := count(t, `SELECT count(*) FROM tally.mfa_recovery_codes c JOIN tally.users u ON u.id = c.user_id WHERE u.tenant_id = $1`, a.tenantID); n != 0 {
		t.Errorf("recovery codes left after disabling MFA = %d", n)
	}
	h.login(t, a, desktop)
}

func TestRevokeSessionsTakesEffectOnTheNextRequest(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.newAccount(t)
	current, p := h.login(t, a, desktop)
	phone := identity.Client{IP: "203.0.113.8", UserAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) Version/17.5 Safari/604.1"}
	other, op := h.login(t, a, phone)
	third, _ := h.login(t, a, phone)

	list, err := h.svc.Sessions(ctx, p)
	if err != nil || len(list) != 3 {
		t.Fatalf("sessions = %d, %v", len(list), err)
	}
	if err := h.svc.RevokeSession(ctx, p, op.SessionID, desktop); err != nil {
		t.Fatal(err)
	}
	h.gone(t, other)
	wantCode(t, h.svc.RevokeSession(ctx, p, op.SessionID, desktop), "not_found")

	n, err := h.svc.RevokeOtherSessions(ctx, p, desktop)
	if err != nil || n != 1 {
		t.Fatalf("revoke others = %d, %v", n, err)
	}
	h.gone(t, third)
	h.resolve(t, current)

	n, err = h.svc.RevokeTenantSessions(ctx, a.tenantID, "incident")
	if err != nil || n != 1 {
		t.Fatalf("revoke tenant = %d, %v", n, err)
	}
	h.gone(t, current)

	// Idle and absolute expiry.
	idle, _ := h.login(t, a, desktop)
	h.clk.Advance(identity.SessionIdleTTL + time.Second)
	h.gone(t, idle)
}

func TestReauthRotatesTheSessionToken(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.newAccount(t)
	token, p := h.login(t, a, desktop)
	_, err := h.svc.Reauth(ctx, p, "not-the-password", desktop)
	if ae, ok := apperr.As(err); !ok || ae.Kind != apperr.Forbidden || ae.Code != "invalid_password" {
		t.Errorf("wrong password = %v", err)
	}
	h.clk.Advance(time.Minute)
	issued, err := h.svc.Reauth(ctx, p, password, desktop)
	if err != nil || issued.ID != p.SessionID || issued.Token == token {
		t.Fatalf("reauth = %+v, %v", issued, err)
	}
	h.gone(t, token)
	if np := h.resolve(t, issued.Token); np.ReauthenticatedAt == nil || !np.ReauthenticatedAt.Equal(h.clk.Now()) {
		t.Errorf("re-authenticated at = %v", np.ReauthenticatedAt)
	}
}

func TestOwnershipTransfer(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a, b := h.newAccount(t), h.newAccount(t)
	oldToken, p := h.login(t, a, desktop)
	oldOwner := p.UserID
	actorEvents := func() int {
		return count(t, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND actor_id = $2`, a.tenantID, oldOwner)
	}

	err := h.svc.RequestOwnerTransfer(ctx, p, identity.TransferInput{Email: b.email, FullName: "Chủ B", Client: desktop})
	if ae, ok := apperr.As(err); !ok || ae.Kind != apperr.Validation || ae.Code != "email_in_use" {
		t.Fatalf("transfer to a registered e-mail = %v", err)
	}

	newEmail := uniqueEmail("next")
	if err := h.svc.RequestOwnerTransfer(ctx, p, identity.TransferInput{Email: newEmail, FullName: "Chủ mới", Client: desktop}); err != nil {
		t.Fatal(err)
	}
	token := h.lastToken(t, newEmail, "Bạn được mời nhận quyền chủ cửa hàng")
	before := actorEvents()
	if before == 0 {
		t.Fatal("the old owner has no audit events")
	}
	wantCode(t, h.svc.AcceptOwnerTransfer(ctx, identity.AcceptInput{HostTenant: b.tenantID, Token: token, Password: password}), "token_invalid")
	if err := h.svc.AcceptOwnerTransfer(ctx, identity.AcceptInput{HostTenant: a.tenantID, Token: token, Password: password, Client: desktop}); err != nil {
		t.Fatalf("accept = %v", err)
	}
	wantCode(t, h.svc.AcceptOwnerTransfer(ctx, identity.AcceptInput{HostTenant: a.tenantID, Token: token, Password: password}), "token_invalid")

	if after := actorEvents(); after != before {
		t.Errorf("audit events of the old owner = %d, was %d", after, before)
	}
	h.gone(t, oldToken)
	_, err = h.svc.Login(ctx, identity.LoginInput{HostTenant: a.tenantID, Login: a.email, Password: password, Client: desktop})
	wantCode(t, err, "invalid_credentials")
	_, np := h.login(t, account{tenantID: a.tenantID, email: newEmail}, desktop)
	if np.UserID == oldOwner {
		t.Error("the new owner must be a new user")
	}
	if n := count(t, `SELECT count(*) FROM tally.users WHERE tenant_id = $1 AND status <> 'disabled'`, a.tenantID); n != 1 {
		t.Errorf("non-disabled owners = %d", n)
	}
}

func TestStoreLookup(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.newAccount(t)
	unknown := uniqueEmail("nobody")
	if err := h.svc.StoreLookup(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.StoreLookup(ctx, " "+strings.ToUpper(a.email)+" "); err != nil {
		t.Fatal(err)
	}
	wantCode(t, h.svc.StoreLookup(ctx, "not an e-mail"), "validation_failed")
	h.svc.WaitMail()
	if got := h.mail.to(unknown); len(got) != 0 {
		t.Errorf("mails to an unknown address = %d", len(got))
	}
	msgs := h.mail.to(a.email)
	last := msgs[len(msgs)-1]
	if last.Subject != "Địa chỉ cửa hàng Tally của bạn" || !strings.Contains(last.Text, "http://"+a.slug+".tally.test/") {
		t.Errorf("lookup mail = %q %q", last.Subject, last.Text)
	}
}

func TestCheckSlugAgainstExistingStores(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.newAccount(t)
	taken, err := h.svc.CheckSlug(ctx, a.slug)
	if err != nil || taken.Available || taken.Reason != org.SlugTaken || taken.Suggestion != a.slug+"-2" {
		t.Fatalf("taken slug = %+v, %v", taken, err)
	}
	named, err := h.svc.CheckSlug(ctx, "Cửa Hàng "+a.slug)
	if err != nil || named.Available || named.Reason != org.SlugInvalid || named.Suggestion != "cua-hang-"+a.slug {
		t.Errorf("store name typed as slug = %+v, %v", named, err)
	}
	free, err := h.svc.CheckSlug(ctx, "s-"+unique())
	if err != nil || !free.Available {
		t.Errorf("free slug = %+v, %v", free, err)
	}
	reserved, err := h.svc.CheckSlug(ctx, "admin")
	if err != nil || reserved.Available || reserved.Reason != org.SlugReserved || reserved.Suggestion == "" || reserved.Suggestion == "admin" {
		t.Errorf("reserved slug = %+v, %v", reserved, err)
	}
}

// The account mails go through the real SMTP relay of the dev stack (Mailpit).
func TestAccountMailsReachMailpit(t *testing.T) {
	smtp, err := mailer.NewSMTP(mailer.SMTPConfig{
		Host: envOr("SMTP_HOST", "localhost"), Port: 1025,
		From: &mail.Address{Name: "Tally", Address: "no-reply@tally.localhost"}, Timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	clk := testkit.Clock()
	svc := newService(t, smtp, clk)
	ctx := context.Background()
	email, slug := uniqueEmail("mailpit"), "s-"+unique()
	created, err := svc.CreateTenantWithOwner(ctx, identity.OwnerSpec{
		Tenant: identity.TenantSpec{Slug: slug, DisplayName: "Cửa hàng " + slug}, Email: email, FullName: "Chủ Mailpit", Source: audit.SourceCLI,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.RequestPasswordReset(ctx, created.Tenant.ID, email); err != nil {
		t.Fatal(err)
	}
	svc.WaitMail()
	verify := fetchFromMailpit(t, email, "Xác nhận e-mail cho cửa hàng Cửa hàng "+slug+" trên Tally")
	if !strings.Contains(verify.Text, created.VerifyURL) {
		t.Errorf("verify mail does not carry the link: %q", verify.Text)
	}
	reset := fetchFromMailpit(t, email, "Đặt lại mật khẩu cửa hàng Cửa hàng "+slug)
	if !strings.Contains(reset.Text, "http://"+slug+".tally.test/reset-password?token=") {
		t.Errorf("reset mail does not carry the link: %q", reset.Text)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type mailpitMessage struct {
	Subject string
	Text    string
}

// fetchFromMailpit waits for the message to addr with exactly subject in the Mailpit API of the
// dev stack (MAILPIT_URL, default http://localhost:8025).
func fetchFromMailpit(t *testing.T, addr, subject string) mailpitMessage {
	t.Helper()
	base := envOr("MAILPIT_URL", "http://localhost:8025")
	client := &http.Client{Timeout: 5 * time.Second}
	getJSON := func(u string, v any) error {
		resp, err := client.Get(u) //nolint:noctx // test helper
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET %s: %d", u, resp.StatusCode)
		}
		return json.NewDecoder(resp.Body).Decode(v)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var list struct {
			Messages []struct {
				ID      string
				Subject string
			}
		}
		err := getJSON(base+"/api/v1/search?query="+url.QueryEscape("to:"+addr), &list)
		if err == nil {
			for _, m := range list.Messages {
				if m.Subject != subject {
					continue
				}
				var msg mailpitMessage
				if err := getJSON(base+"/api/v1/message/"+m.ID, &msg); err != nil {
					t.Fatal(err)
				}
				return msg
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("mail %q to %s not found in Mailpit (start it with `make db-up`): %v", subject, addr, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
