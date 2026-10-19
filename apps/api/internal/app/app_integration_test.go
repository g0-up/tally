//go:build integration

package app_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/partner"
	"github.com/g0-up/tally/apps/api/internal/platform/exception"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/pricing"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// testAPIRateLimit lifts the per-client limit of every request for the test stacks: a scenario
// runs hundreds of requests from one browser faster than the production refill. The credential
// endpoints keep their own limiter, and the limit itself is covered by the unit tests.
var testAPIRateLimit = app.RateLimit{PerSecond: 1000, Burst: 10000}

type stack struct {
	handler  http.Handler
	identity *identity.Service
	mail     *captureMailer
}

// newStack wires the identity, organisation, catalog, partner, pricing and inventory modules;
// opts adjust the identity dependencies, such as the password hasher.
func newStack(t *testing.T, opts ...func(*identity.Deps)) stack {
	t.Helper()
	clk := testkit.Clock()
	capture := &captureMailer{}
	deps := identityDeps(t, capture, clk)
	for _, opt := range opts {
		opt(&deps)
	}
	var hosts *middleware.TenantHostCache
	deps.TenantCreated = func(slug string) {
		if hosts != nil {
			hosts.Purge(slug)
		}
	}
	idSvc, err := identity.NewService(deps)
	if err != nil {
		t.Fatal(err)
	}
	// Presigning needs no connection; the cross-tenant checks never reach the object store.
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
		AttachmentOwners: domains.AttachmentOwners(),
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
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts = a.TenantHostCache()
	return stack{handler: a.Handler(), identity: idSvc, mail: capture}
}

// browser keeps the cookies of one host, the way a browser keeps host-only cookies. Every
// browser has its own client address, so the per-address limit of the credential endpoints
// only trips where a test means it to.
type browser struct {
	t       *testing.T
	h       http.Handler
	host    string
	cookies map[string]*http.Cookie
	csrf    string
}

var clients atomic.Uint32

func (s stack) browser(t *testing.T, host string) *browser {
	n := clients.Add(1)
	addr := fmt.Sprintf("10.%d.%d.%d:40000", n>>16&0xff, n>>8&0xff, n&0xff)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.RemoteAddr = addr
		s.handler.ServeHTTP(w, r)
	})
	return &browser{t: t, h: h, host: host, cookies: map[string]*http.Cookie{}}
}

func (b *browser) do(method, path string, body any) *httptest.ResponseRecorder {
	b.t.Helper()
	return b.doWith(method, path, body, nil)
}

// doWith is do with extra request headers, such as If-Match.
func (b *browser) doWith(method, path string, body any, extra http.Header) *httptest.ResponseRecorder {
	b.t.Helper()
	header := extra.Clone()
	if header == nil {
		header = http.Header{}
	}
	if b.csrf != "" {
		header.Set(middleware.CSRFHeader, b.csrf)
	}
	var jar []*http.Cookie
	for _, c := range b.cookies {
		jar = append(jar, c)
	}
	rec := testkit.Do(b.t, b.h, testkit.Request{Method: method, URL: "http://" + b.host + path, Body: body, Header: header, Cookies: jar})
	for _, c := range rec.Result().Cookies() {
		if c.Domain != "" {
			b.t.Errorf("cookie %s has Domain=%s; every cookie must be host-only", c.Name, c.Domain)
		}
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
			continue
		}
		b.cookies[c.Name] = &http.Cookie{Name: c.Name, Value: c.Value}
	}
	return rec
}

// hostContext fetches the CSRF token the SPA reads on start.
func (b *browser) hostContext() {
	b.t.Helper()
	rec := b.do(http.MethodGet, "/api/v1/host-context", nil)
	if rec.Code != http.StatusOK {
		b.t.Fatalf("host-context = %d %s", rec.Code, rec.Body)
	}
	b.csrf = testkit.DecodeJSON[struct {
		CSRFToken string `json:"csrf_token"`
	}](b.t, rec).CSRFToken
}

func expect(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status || (code != "" && !strings.Contains(rec.Body.String(), `"`+code+`"`)) {
		t.Errorf("response = %d %s, want %d %s", rec.Code, rec.Body, status, code)
	}
}

type owner struct {
	slug, email string
	host        string
}

// signUp creates a verified store through the HTTP API.
func (s stack) signUp(t *testing.T) owner {
	t.Helper()
	o := owner{slug: "s-" + unique(), email: "owner-" + unique() + "@example.test"}
	o.host = o.slug + ".tally.test"
	root := s.browser(t, "tally.test")
	root.hostContext()
	expect(t, root.do(http.MethodPost, "/api/v1/auth/signup", map[string]string{
		"email": o.email, "password": password, "full_name": "Chủ cửa hàng", "store_name": "Cửa hàng " + o.slug, "slug": o.slug,
	}), http.StatusAccepted, "")
	s.identity.WaitMail()
	msgs := s.mail.to(o.email)
	if len(msgs) == 0 {
		t.Fatal("no verification mail")
	}
	token := tokenRE.FindStringSubmatch(msgs[0].Text)[1]
	tenant := s.browser(t, o.host)
	tenant.hostContext()
	expect(t, tenant.do(http.MethodPost, "/api/v1/auth/verify", map[string]string{"token": token}), http.StatusNoContent, "")
	return o
}

// signIn returns a browser signed in on the owner's host.
func (s stack) signIn(t *testing.T, o owner) *browser {
	t.Helper()
	b := s.browser(t, o.host)
	b.hostContext()
	rec := b.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"login": o.email, "password": password})
	if rec.Code != http.StatusOK {
		t.Fatalf("login = %d %s", rec.Code, rec.Body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "tally_session" && (!c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/") {
			t.Errorf("session cookie = %+v", c)
		}
	}
	b.csrf = testkit.DecodeJSON[struct {
		CSRFToken string `json:"csrf_token"`
	}](t, rec).CSRFToken
	if b.cookies["tally_session"] == nil || b.cookies["tally_csrf"].Value != b.csrf {
		t.Fatalf("cookies after login = %v", b.cookies)
	}
	return b
}

func TestAccountFlowOverHTTP(t *testing.T) {
	s := newStack(t)
	a, b := s.signUp(t), s.signUp(t)
	signedIn := s.signIn(t, a)
	me := signedIn.do(http.MethodGet, "/api/v1/me", nil)
	expect(t, me, http.StatusOK, "")
	// The server's clock is reported in the store's time zone, Asia/Ho_Chi_Minh by default.
	if want := testkit.Epoch.In(time.FixedZone("", 7*3600)).Format(time.RFC3339); testkit.DecodeJSON[struct {
		ServerTime string `json:"server_time"`
	}](t, me).ServerTime != want {
		t.Errorf("GET /me server_time = %s, want %s", me.Body, want)
	}

	t.Run("owner of A on host B", func(t *testing.T) {
		other := s.browser(t, b.host)
		other.hostContext()
		wrongStore := other.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"login": a.email, "password": password})
		wrongPassword := other.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"login": b.email, "password": "sai-mat-khau"})
		expect(t, wrongStore, http.StatusUnauthorized, "invalid_credentials")
		if wrongStore.Body.String() != wrongPassword.Body.String() {
			t.Errorf("answers differ: %s vs %s", wrongStore.Body, wrongPassword.Body)
		}
		// A's session cookie replayed on B's host is no session there.
		other.cookies["tally_session"] = signedIn.cookies["tally_session"]
		expect(t, other.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "")
	})

	t.Run("CSRF tokens are bound to the session", func(t *testing.T) {
		anonymous := s.browser(t, a.host)
		anonymous.hostContext()
		forged := s.signIn(t, a)
		forged.csrf, forged.cookies["tally_csrf"] = anonymous.csrf, anonymous.cookies["tally_csrf"]
		expect(t, forged.do(http.MethodPost, "/api/v1/sessions:revoke-all", nil), http.StatusForbidden, "csrf_failed")

		another := s.signIn(t, a)
		stolen := s.signIn(t, a)
		stolen.csrf, stolen.cookies["tally_csrf"] = another.csrf, another.cookies["tally_csrf"]
		expect(t, stolen.do(http.MethodPost, "/api/v1/sessions:revoke-all", nil), http.StatusForbidden, "csrf_failed")

		noHeader := s.signIn(t, a)
		noHeader.csrf = ""
		expect(t, noHeader.do(http.MethodPost, "/api/v1/sessions:revoke-all", nil), http.StatusForbidden, "csrf_failed")

		// The host context of a signed-in browser hands out a token bound to its session.
		refreshed := s.signIn(t, a)
		refreshed.hostContext()
		expect(t, refreshed.do(http.MethodPost, "/api/v1/sessions:revoke-all", nil), http.StatusOK, "")
		expect(t, signedIn.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "")
	})

	t.Run("cross-tenant ids answer 404", func(t *testing.T) {
		mine := s.signIn(t, a)
		theirs := s.signIn(t, b)
		events := testkit.DecodeJSON[struct {
			Data []struct {
				ID uuid.UUID `json:"id"`
			} `json:"data"`
		}](t, theirs.do(http.MethodGet, "/api/v1/audit-events?limit=5", nil))
		if len(events.Data) == 0 {
			t.Fatal("B has no audit events")
		}
		expect(t, theirs.do(http.MethodGet, "/api/v1/audit-events/"+events.Data[0].ID.String(), nil), http.StatusOK, "")
		expect(t, mine.do(http.MethodGet, "/api/v1/audit-events/"+events.Data[0].ID.String(), nil), http.StatusNotFound, "not_found")
		expect(t, mine.do(http.MethodGet, "/api/v1/attachments/"+ids.New().String()+"/url", nil), http.StatusNotFound, "not_found")
		sessions := testkit.DecodeJSON[struct {
			Data []struct {
				ID uuid.UUID `json:"id"`
			} `json:"data"`
		}](t, theirs.do(http.MethodGet, "/api/v1/sessions", nil))
		if len(sessions.Data) == 0 {
			t.Fatal("B has no sessions")
		}
		expect(t, mine.do(http.MethodDelete, "/api/v1/sessions/"+sessions.Data[0].ID.String(), nil), http.StatusNotFound, "not_found")
	})

	t.Run("store lookup and sign-up never reveal an account", func(t *testing.T) {
		root := s.browser(t, "tally.test")
		root.hostContext()
		unknown := "nobody-" + unique() + "@example.test"
		expect(t, root.do(http.MethodPost, "/api/v1/store-lookup", map[string]string{"email": unknown}), http.StatusAccepted, "")
		slug := "s-" + unique()
		fresh := root.do(http.MethodPost, "/api/v1/auth/signup", map[string]string{
			"email": "new-" + unique() + "@example.test", "password": password, "full_name": "Người mới", "store_name": "Mới", "slug": slug,
		})
		slug2 := "s-" + unique()
		existing := root.do(http.MethodPost, "/api/v1/auth/signup", map[string]string{
			"email": a.email, "password": password, "full_name": "Người lạ", "store_name": "Lạ", "slug": slug2,
		})
		if fresh.Code != http.StatusAccepted || existing.Code != http.StatusAccepted ||
			strings.ReplaceAll(fresh.Body.String(), slug, "X") != strings.ReplaceAll(existing.Body.String(), slug2, "X") {
			t.Errorf("sign-up answers differ: %d %s vs %d %s", fresh.Code, fresh.Body, existing.Code, existing.Body)
		}
		s.identity.WaitMail()
		if got := s.mail.to(unknown); len(got) != 0 {
			t.Errorf("mails to an unknown address = %d", len(got))
		}
		mails := s.mail.to(a.email)
		if last := mails[len(mails)-1]; last.Subject != "E-mail của bạn đã có tài khoản Tally" {
			t.Errorf("last mail to the registered address = %q", last.Subject)
		}
		ghost := s.browser(t, slug2+".tally.test")
		ghost.hostContext()
		expect(t, ghost.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"login": a.email, "password": password}),
			http.StatusUnauthorized, "invalid_credentials")
	})

	t.Run("throttled login answers 429 with Retry-After", func(t *testing.T) {
		c := s.browser(t, b.host)
		c.hostContext()
		for range identity.MaxFailedLogins {
			c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"login": b.email, "password": "sai-mat-khau"})
		}
		rec := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"login": b.email, "password": password})
		expect(t, rec, http.StatusTooManyRequests, "rate_limited")
		if rec.Header().Get("Retry-After") == "" {
			t.Error("a throttled login must say when to retry")
		}
	})

	t.Run("logout ends the session", func(t *testing.T) {
		c := s.signIn(t, a)
		rec := c.do(http.MethodPost, "/api/v1/auth/logout", nil)
		expect(t, rec, http.StatusOK, "")
		if c.cookies["tally_session"] != nil {
			t.Error("logout must clear the session cookie")
		}
		expect(t, c.do(http.MethodGet, "/api/v1/me", nil), http.StatusUnauthorized, "")
	})
}
