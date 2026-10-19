//go:build integration

package app_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/admin"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

type adminMe struct {
	Admin struct {
		ID       uuid.UUID `json:"id"`
		FullName string    `json:"full_name"`
		Email    string    `json:"email"`
	} `json:"admin"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// cookieNamed is the name of b's cookie whose name ends in suffix.
func cookieNamed(t *testing.T, b *browser, suffix string) string {
	t.Helper()
	for name := range b.cookies {
		if strings.HasSuffix(name, suffix) {
			return name
		}
	}
	t.Fatalf("no cookie ending in %s among %v", suffix, b.cookies)
	return ""
}

func TestAdminMeAnswersTheSignedInAdminAndItsSession(t *testing.T) {
	s := newControlStack(t)
	b, adminID := s.adminSignIn(t)
	signedInAt := s.clk.Now()

	rec := b.do(http.MethodGet, adminV1+"/me", nil)
	expect(t, rec, http.StatusOK, "")
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	me := testkit.DecodeJSON[adminMe](t, rec)
	if me.Admin.ID != adminID || me.Admin.FullName != "Quản trị viên" || !strings.HasPrefix(me.Admin.Email, "admin-") {
		t.Errorf("me admin = %+v, want admin %s", me.Admin, adminID)
	}
	if !me.ExpiresAt.Equal(signedInAt.Add(admin.SessionTTL)) {
		t.Errorf("expires_at = %s, want %s", me.ExpiresAt, signedInAt.Add(admin.SessionTTL))
	}
	// The token of the sign-in stays current, so another tab holding it keeps working.
	if me.CSRFToken != b.csrf || len(rec.Result().Cookies()) != 0 {
		t.Errorf("me csrf_token = %q with cookies %v, want the sign-in token %q unchanged", me.CSRFToken, rec.Result().Cookies(), b.csrf)
	}

	// Without the CSRF cookie a new token is issued, written to the cookie and accepted.
	delete(b.cookies, cookieNamed(t, b, "admin_csrf"))
	b.csrf = ""
	rec = b.do(http.MethodGet, adminV1+"/me", nil)
	expect(t, rec, http.StatusOK, "")
	b.csrf = testkit.DecodeJSON[adminMe](t, rec).CSRFToken
	if b.csrf == "" || b.cookies[cookieNamed(t, b, "admin_csrf")].Value != b.csrf {
		t.Fatalf("me without a CSRF cookie answered %q and the cookie %v", b.csrf, b.cookies)
	}
	expect(t, b.do(http.MethodPost, adminV1+"/auth/logout", nil), http.StatusOK, "signed_out")
	expect(t, b.do(http.MethodGet, adminV1+"/me", nil), http.StatusUnauthorized, "unauthenticated")

	other, _ := s.adminSignIn(t)
	s.clk.Advance(admin.SessionTTL)
	expect(t, other.do(http.MethodGet, adminV1+"/me", nil), http.StatusUnauthorized, "unauthenticated")
}

func TestAdminMeRefusesWithoutASession(t *testing.T) {
	s := newControlStack(t)
	anon := s.adminBrowser(t)
	expect(t, anon.do(http.MethodGet, adminV1+"/me", nil), http.StatusUnauthorized, "unauthenticated")

	// The password step alone opens no session, whatever the client sends as its cookie.
	email, _, _ := s.adminAccount(t)
	b := s.adminBrowser(t)
	challenge := adminChallenge(t, b, email)
	expect(t, b.do(http.MethodGet, adminV1+"/me", nil), http.StatusUnauthorized, "unauthenticated")
	name := middleware.CookieNames(false).AdminSession
	b.cookies[name] = &http.Cookie{Name: name, Value: challenge}
	expect(t, b.do(http.MethodGet, adminV1+"/me", nil), http.StatusUnauthorized, "unauthenticated")
}
