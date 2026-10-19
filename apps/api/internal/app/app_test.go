package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

type fakeSessions map[string]middleware.Principal

func (f fakeSessions) ResolveSession(_ context.Context, token string) (middleware.Principal, bool, error) {
	p, ok := f[token]
	return p, ok, nil
}

type fakeHosts map[string]uuid.UUID

func (f fakeHosts) ResolveSlug(_ context.Context, slug string) (uuid.UUID, bool, error) {
	id, ok := f[slug]
	return id, ok, nil
}

// fakeEntitlements lists the tenants allowed to write.
type fakeEntitlements map[uuid.UUID]bool

func (f fakeEntitlements) CanWrite(_ context.Context, tenantID uuid.UUID) (bool, error) {
	return f[tenantID], nil
}

var quiet = slog.New(slog.DiscardHandler)

type call struct {
	method, host, path, cookie, csrf string
}

func serve(h http.Handler, c call) *httptest.ResponseRecorder {
	req := httptest.NewRequest(c.method, c.path, nil)
	req.Host = c.host
	if c.cookie != "" {
		req.Header.Add("Cookie", c.cookie)
	}
	if c.csrf != "" {
		req.Header.Add("X-CSRF-Token", c.csrf)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestNewValidatesOptions(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Error("nil base URL: want error")
	}
	if _, err := New(Options{PublicBaseURL: mustURL(t, "https://tally.test"), TrustedProxyCIDRs: []string{"not-a-cidr"}}); err == nil {
		t.Error("bad proxy CIDR: want error")
	}
	a, err := New(Options{PublicBaseURL: mustURL(t, "https://tally.test"), SecureCookies: true})
	if err != nil {
		t.Fatal(err)
	}
	if a.URLs().Host() != "tally.test" || a.Cookies().Session != "__Host-tally_session" || a.TenantHostCache() == nil || a.Commands() == nil {
		t.Errorf("accessors: host=%q cookies=%+v", a.URLs().Host(), a.Cookies())
	}
}

func TestProbes(t *testing.T) {
	var logs bytes.Buffer
	readyErr := errors.New("postgres down: password=secret")
	a, err := New(Options{
		PublicBaseURL: mustURL(t, "http://localhost:8080"),
		Logger:        slog.New(slog.NewJSONHandler(&logs, nil)),
		Ready:         func(context.Context) error { return readyErr },
	})
	if err != nil {
		t.Fatal(err)
	}
	// Probes answer on any host, including the loopback address the container healthcheck uses.
	if w := serve(a.Handler(), call{method: "GET", host: "127.0.0.1:8080", path: "/healthz"}); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok"`) {
		t.Errorf("healthz = %d %s", w.Code, w.Body)
	}
	w := serve(a.Handler(), call{method: "GET", host: "127.0.0.1:8080", path: "/readyz"})
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "secret") {
		t.Errorf("readyz not ready = %d %s", w.Code, w.Body)
	}
	if !strings.Contains(logs.String(), "not ready") {
		t.Errorf("readiness failure not logged: %s", logs.String())
	}
	readyErr = nil
	if w := serve(a.Handler(), call{method: "GET", host: "127.0.0.1", path: "/readyz"}); w.Code != http.StatusOK {
		t.Errorf("readyz ready = %d", w.Code)
	}

	noCheck, err := New(Options{PublicBaseURL: mustURL(t, "http://localhost:8080"), Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	if w := serve(noCheck.Handler(), call{method: "GET", host: "localhost", path: "/readyz"}); w.Code != http.StatusOK {
		t.Errorf("readyz without check = %d", w.Code)
	}
}

var testCSRFKey = []byte("0123456789abcdef0123456789abcdef")

func TestRouteGroups(t *testing.T) {
	tenantA, tenantB := uuid.New(), uuid.New()
	sidA, sidB, sidAdmin := uuid.New(), uuid.New(), uuid.New()
	sessions := fakeSessions{
		"owner-a": {Kind: middleware.PrincipalOwner, TenantID: tenantA, SessionID: sidA},
		"owner-b": {Kind: middleware.PrincipalOwner, TenantID: tenantB, SessionID: sidB},
		"admin":   {Kind: middleware.PrincipalPlatformAdmin, SessionID: sidAdmin},
	}
	a, err := New(Options{
		PublicBaseURL: mustURL(t, "http://localhost:8080"),
		Logger:        quiet,
		CSRFKey:       testCSRFKey,
		Ports: Ports{
			Sessions:      sessions,
			TenantHosts:   fakeHosts{"store-a": tenantA, "store-b": tenantB},
			Entitlements:  fakeEntitlements{tenantB: true},
			AdminSessions: sessions,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ok := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }
	a.API(httpx.HostRoot, httpx.HostTenant).GET("/public", ok)
	a.API(httpx.HostRoot, httpx.HostTenant).POST("/public", ok)
	tenant := a.TenantAPI()
	tenant.GET("/things", ok)
	tenant.POST("/things", ok)
	a.CommandRouter(tenant, "things").Item("confirm", ok)
	a.TenantPostingAPI().POST("/postings", ok)
	a.TenantAccountAPI().POST("/account", ok)
	admin := a.AdminAPI()
	admin.GET("/tenants", ok)
	admin.POST("/tenants", ok)

	tokens := middleware.NewCSRFTokens(testCSRFKey)
	tokA := tokens.Issue(middleware.SessionBinding(middleware.PrincipalOwner, sidA))
	tokB := tokens.Issue(middleware.SessionBinding(middleware.PrincipalOwner, sidB))
	tokAdmin := tokens.Issue(middleware.SessionBinding(middleware.PrincipalPlatformAdmin, sidAdmin))
	tokAnonA := tokens.Issue(middleware.AnonymousBinding(httpx.Host{Kind: httpx.HostTenant, Slug: "store-a"}))
	const (
		rootHost   = "localhost:8080"
		tenantHost = "store-a.localhost:8080"
		otherHost  = "store-b.localhost:8080"
		adminHost  = "admin.localhost:8080"
	)
	ownerA := "tally_session=owner-a; tally_csrf=" + tokA
	ownerB := "tally_session=owner-b; tally_csrf=" + tokB
	adminCk := "tally_admin_session=admin; tally_admin_csrf=" + tokAdmin
	// A token of session A replayed with session B's cookie: same cookie and header, wrong binding.
	ownerBWithA := "tally_session=owner-b; tally_csrf=" + tokA
	anonA := "tally_csrf=" + tokAnonA
	cases := []struct {
		name string
		call call
		want int
	}{
		{"public on root", call{method: "GET", host: rootHost, path: "/api/v1/public"}, 200},
		{"public on tenant", call{method: "GET", host: tenantHost, path: "/api/v1/public"}, 200},
		{"public on admin host", call{method: "GET", host: adminHost, path: "/api/v1/public"}, 404},
		{"foreign host", call{method: "GET", host: "evil.example", path: "/api/v1/public"}, 421},
		{"tenant read", call{method: "GET", host: tenantHost, path: "/api/v1/things", cookie: ownerA}, 200},
		{"tenant without session", call{method: "GET", host: tenantHost, path: "/api/v1/things"}, 401},
		{"tenant cookie on other store", call{method: "GET", host: otherHost, path: "/api/v1/things", cookie: ownerA}, 401},
		{"tenant group on root host", call{method: "GET", host: rootHost, path: "/api/v1/things", cookie: ownerA}, 404},
		{"tenant group on admin host", call{method: "GET", host: adminHost, path: "/api/v1/things", cookie: ownerA}, 404},
		{"tenant write without csrf", call{method: "POST", host: tenantHost, path: "/api/v1/things", cookie: ownerA}, 403},
		{"tenant write read-only subscription", call{method: "POST", host: tenantHost, path: "/api/v1/things", cookie: ownerA, csrf: tokA}, 403},
		{"tenant write", call{method: "POST", host: otherHost, path: "/api/v1/things", cookie: ownerB, csrf: tokB}, 200},
		{"posting read-only subscription reaches the coordinator", call{method: "POST", host: tenantHost, path: "/api/v1/postings", cookie: ownerA, csrf: tokA}, 200},
		{"posting without csrf", call{method: "POST", host: tenantHost, path: "/api/v1/postings", cookie: ownerA}, 403},
		{"posting without session", call{method: "POST", host: tenantHost, path: "/api/v1/postings", csrf: tokA}, 401},
		{"posting on root host", call{method: "POST", host: rootHost, path: "/api/v1/postings", cookie: ownerA, csrf: tokA}, 404},
		{"command", call{method: "POST", host: otherHost, path: "/api/v1/things/" + uuid.NewString() + ":confirm", cookie: ownerB, csrf: tokB}, 200},
		{"command unknown verb", call{method: "POST", host: otherHost, path: "/api/v1/things/" + uuid.NewString() + ":nope", cookie: ownerB, csrf: tokB}, 404},
		{"tenant write with another session's token", call{method: "POST", host: otherHost, path: "/api/v1/things", cookie: ownerBWithA, csrf: tokA}, 403},
		{"account write read-only subscription", call{method: "POST", host: tenantHost, path: "/api/v1/account", cookie: ownerA, csrf: tokA}, 200},
		{"anonymous write with host token", call{method: "POST", host: tenantHost, path: "/api/v1/public", cookie: anonA, csrf: tokAnonA}, 200},
		{"anonymous write with another host's token", call{method: "POST", host: otherHost, path: "/api/v1/public", cookie: anonA, csrf: tokAnonA}, 403},
		{"anonymous write without csrf", call{method: "POST", host: tenantHost, path: "/api/v1/public"}, 403},
		{"anonymous token after sign-in", call{method: "POST", host: tenantHost, path: "/api/v1/public", cookie: "tally_session=owner-a; " + anonA, csrf: tokAnonA}, 403},
		{"admin read", call{method: "GET", host: adminHost, path: "/api/admin/v1/tenants", cookie: adminCk}, 200},
		{"admin write", call{method: "POST", host: adminHost, path: "/api/admin/v1/tenants", cookie: adminCk, csrf: tokAdmin}, 200},
		{"admin write without csrf", call{method: "POST", host: adminHost, path: "/api/admin/v1/tenants", cookie: adminCk}, 403},
		{"admin with owner session", call{method: "GET", host: adminHost, path: "/api/admin/v1/tenants", cookie: "tally_admin_session=owner-a"}, 401},
		{"admin without session", call{method: "GET", host: adminHost, path: "/api/admin/v1/tenants"}, 401},
		{"admin group on tenant host", call{method: "GET", host: tenantHost, path: "/api/admin/v1/tenants", cookie: adminCk}, 404},
		{"unknown route", call{method: "GET", host: rootHost, path: "/nope"}, 404},
		{"unknown route on foreign host", call{method: "GET", host: "evil.example", path: "/nope"}, 421},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serve(a.Handler(), tc.call)
			if w.Code != tc.want {
				t.Errorf("%s %s on %s = %d %s, want %d", tc.call.method, tc.call.path, tc.call.host, w.Code, w.Body, tc.want)
			}
		})
	}
	if routes := a.Commands().Routes(); len(routes) != 1 || routes[0].Path != "/api/v1/things/{id}:confirm" {
		t.Errorf("command index = %+v", routes)
	}
}

// countingHosts knows no slug and counts lookups (each would be a database query).
type countingHosts struct{ calls atomic.Int64 }

func (h *countingHosts) ResolveSlug(context.Context, string) (uuid.UUID, bool, error) {
	h.calls.Add(1)
	return uuid.Nil, false, nil
}

// Random tenant slugs from one client are throttled before their host is looked up.
func TestRateLimitRunsBeforeSlugLookup(t *testing.T) {
	hosts := &countingHosts{}
	a, err := New(Options{PublicBaseURL: mustURL(t, "http://localhost:8080"), Logger: quiet, Ports: Ports{TenantHosts: hosts}})
	if err != nil {
		t.Fatal(err)
	}
	served, limited := 0, 0
	for i := range apiRateBurst + 50 {
		w := serve(a.Handler(), call{method: "GET", host: fmt.Sprintf("ghost-%d.localhost:8080", i), path: "/api/v1/things"})
		if w.Code == http.StatusTooManyRequests {
			limited++
		} else {
			served++
		}
	}
	if limited == 0 {
		t.Fatal("no request was rate limited")
	}
	if n := hosts.calls.Load(); n != int64(served) {
		t.Fatalf("slug lookups = %d, want one per request that passed the limiter (%d)", n, served)
	}
}

func TestAPIRateLimitOverridesTheDefault(t *testing.T) {
	for _, c := range []struct {
		limit        RateLimit
		requests     int
		wantServed   int
		wantRejected int
	}{
		{RateLimit{PerSecond: 0.001, Burst: 3}, 10, 3, 7},
		{RateLimit{PerSecond: 1000, Burst: apiRateBurst * 10}, apiRateBurst + 50, apiRateBurst + 50, 0},
	} {
		a, err := New(Options{PublicBaseURL: mustURL(t, "http://localhost:8080"), Logger: quiet, APIRateLimit: c.limit})
		if err != nil {
			t.Fatal(err)
		}
		served, limited := 0, 0
		for range c.requests {
			if serve(a.Handler(), call{method: "GET", host: "localhost:8080", path: "/api/v1/things"}).Code == http.StatusTooManyRequests {
				limited++
			} else {
				served++
			}
		}
		if served != c.wantServed || limited != c.wantRejected {
			t.Errorf("limit %+v: served %d, limited %d, want %d and %d", c.limit, served, limited, c.wantServed, c.wantRejected)
		}
	}
}

func TestDefaultPorts(t *testing.T) {
	a, err := New(Options{PublicBaseURL: mustURL(t, "http://localhost:8080"), Logger: quiet})
	if err != nil {
		t.Fatal(err)
	}
	a.TenantAPI().GET("/things", func(c *gin.Context) { c.Status(http.StatusOK) })
	// No session resolver and no tenant hosts: every tenant request is unauthenticated.
	if w := serve(a.Handler(), call{method: "GET", host: "store.localhost", path: "/api/v1/things", cookie: "tally_session=x"}); w.Code != http.StatusUnauthorized {
		t.Errorf("default ports = %d", w.Code)
	}
	if ok, err := (writesAllowed{}).CanWrite(context.Background(), uuid.New()); !ok || err != nil {
		t.Errorf("writesAllowed = %v %v", ok, err)
	}
	if _, found, err := (noSessions{}).ResolveSession(context.Background(), "x"); found || err != nil {
		t.Errorf("noSessions = %v %v", found, err)
	}
	if _, found, err := (unknownTenantHosts{}).ResolveSlug(context.Background(), "x"); found || err != nil {
		t.Errorf("unknownTenantHosts = %v %v", found, err)
	}
}

// fakeEntitlementError is a subscription gate that cannot be read.
type fakeEntitlementError struct{ err error }

func (f fakeEntitlementError) CanWrite(context.Context, uuid.UUID) (bool, error) { return false, f.err }

func TestPostingEntitlements(t *testing.T) {
	ctx := context.Background()
	active, readOnly := uuid.New(), uuid.New()
	gate := postingEntitlements{r: fakeEntitlements{active: true}}
	if err := gate.CanPost(ctx, nil, active); err != nil {
		t.Errorf("active tenant = %v", err)
	}
	err := gate.CanPost(ctx, nil, readOnly)
	if e, ok := apperr.As(err); !ok || e.Kind != apperr.ReadOnlyTenant || e.Code != "subscription_read_only" {
		t.Errorf("read-only tenant = %v, want 403 subscription_read_only", err)
	}
	down := errors.New("connection refused")
	if err := (postingEntitlements{r: fakeEntitlementError{down}}).CanPost(ctx, nil, active); !errors.Is(err, down) {
		t.Errorf("unreadable gate = %v, want the read error", err)
	}
	if err := (postingEntitlements{r: writesAllowed{}}).CanPost(ctx, nil, readOnly); err != nil {
		t.Errorf("default gate = %v", err)
	}

	tx := &sqlx.Tx{}
	guard := &fakePostingGuard{err: apperr.NewReadOnlyTenant()}
	err = (postingEntitlements{r: guard}).CanPost(ctx, tx, readOnly)
	if !errors.Is(err, guard.err) || guard.q != db.Querier(tx) || guard.tenantID != readOnly {
		t.Errorf("posting guard = %v (q %v, tenant %s), want it asked in the posting transaction", err, guard.q, guard.tenantID)
	}
}

// fakePostingGuard is a subscription gate that can read in the posting transaction. Its own
// connection is never the one to ask during a posting.
type fakePostingGuard struct {
	err      error
	q        db.Querier
	tenantID uuid.UUID
}

func (f *fakePostingGuard) CanWrite(context.Context, uuid.UUID) (bool, error) {
	return false, errors.New("read outside the posting transaction")
}

func (f *fakePostingGuard) CanPost(_ context.Context, q db.Querier, tenantID uuid.UUID) error {
	f.q, f.tenantID = q, tenantID
	return f.err
}

func TestAttachmentOwnersRegistered(t *testing.T) {
	owners := Domains{}.AttachmentOwners()
	for _, ownerType := range []string{"product", "party", "receipt", "supplier_document", "support_ticket"} {
		if owners[ownerType] == nil {
			t.Errorf("owner type %q is not registered", ownerType)
		}
	}
	if len(owners) != 5 {
		t.Errorf("owner types = %d, want 5", len(owners))
	}
}

func TestHostContext(t *testing.T) {
	tenantA, sidA := uuid.New(), uuid.New()
	a, err := New(Options{
		PublicBaseURL: mustURL(t, "http://localhost:8080"),
		Logger:        quiet,
		CSRFKey:       testCSRFKey,
		Ports: Ports{
			Sessions:    fakeSessions{"owner-a": {Kind: middleware.PrincipalOwner, TenantID: tenantA, SessionID: sidA}},
			TenantHosts: fakeHosts{"store-a": tenantA},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	tokens := middleware.NewCSRFTokens(testCSRFKey)
	cases := []struct {
		name, host, cookie, kind, cookieName, binding string
		admin                                         bool
	}{
		{"root", "localhost:8080", "", "root", "tally_csrf", "anon:root:", false},
		{"tenant signed out", "store-a.localhost:8080", "", "tenant", "tally_csrf", "anon:tenant:store-a", false},
		{"tenant signed in", "store-a.localhost:8080", "tally_session=owner-a",
			"tenant", "tally_csrf", middleware.SessionBinding(middleware.PrincipalOwner, sidA), false},
		{"unknown store answers like a real one", "ghost.localhost:8080", "", "tenant", "tally_csrf", "anon:tenant:ghost", false},
		{"admin", "admin.localhost:8080", "", "admin", "tally_admin_csrf", "anon:admin:", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serve(a.Handler(), call{method: "GET", host: tc.host, path: "/api/v1/host-context", cookie: tc.cookie})
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d %s", w.Code, w.Body)
			}
			var body struct {
				Kind      string  `json:"kind"`
				RootURL   string  `json:"root_url"`
				AdminURL  *string `json:"admin_url"`
				CSRFToken string  `json:"csrf_token"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Kind != tc.kind || body.RootURL != "http://localhost:8080/" || (body.AdminURL != nil) != tc.admin {
				t.Errorf("body = %+v", body)
			}
			if !tokens.Valid(tc.binding, body.CSRFToken) {
				t.Errorf("csrf token not bound to %q", tc.binding)
			}
			var cookie *http.Cookie
			for _, ck := range w.Result().Cookies() {
				if ck.Name == tc.cookieName {
					cookie = ck
				}
			}
			if cookie == nil || cookie.Value != body.CSRFToken || cookie.HttpOnly || cookie.Domain != "" {
				t.Errorf("csrf cookie = %+v", cookie)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Error("host context must not be cached")
			}
		})
	}
}

// Handlers without services register every route without touching them (the contract test
// relies on it).
func TestModulesRegister(t *testing.T) {
	a, err := New(Options{
		PublicBaseURL: mustURL(t, "http://localhost:8080"),
		Logger:        quiet,
		Modules:       Modules{Identity: identity.NewHandler(nil), Org: org.NewHandler(nil)},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"POST /api/v1/auth/signup": false, "POST /api/v1/auth/login": false, "GET /api/v1/me": false,
		"GET /api/v1/tenant": false, "POST /api/v1/period-locks/reopen": false, `POST /api/v1/sessions\:revoke-all`: false,
	}
	for _, r := range a.Handler().Routes() {
		if _, ok := want[r.Method+" "+r.Path]; ok {
			want[r.Method+" "+r.Path] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("route %s not registered", route)
		}
	}
	// Account routes are tenant-host only.
	if w := serve(a.Handler(), call{method: "GET", host: "localhost:8080", path: "/api/v1/me"}); w.Code != http.StatusNotFound {
		t.Errorf("GET /me on the root host = %d, want 404", w.Code)
	}
	if w := serve(a.Handler(), call{method: "POST", host: "store.localhost:8080", path: "/api/v1/auth/signup"}); w.Code != http.StatusNotFound {
		t.Errorf("sign-up on a tenant host = %d, want 404", w.Code)
	}
}

// A consented support session reads the store but never acts as its owner.
func TestSupportSessionIsNotOwner(t *testing.T) {
	tenantA := uuid.New()
	a, err := New(Options{
		PublicBaseURL: mustURL(t, "http://localhost:8080"),
		Logger:        quiet,
		CSRFKey:       testCSRFKey,
		Modules:       Modules{Identity: identity.NewHandler(nil), Org: org.NewHandler(nil)},
		Ports: Ports{
			Sessions:    fakeSessions{"support-a": {Kind: middleware.PrincipalSupport, TenantID: tenantA, SessionID: uuid.New()}},
			TenantHosts: fakeHosts{"store-a": tenantA},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := serve(a.Handler(), call{method: "GET", host: "store-a.localhost:8080", path: "/api/v1/me", cookie: "tally_session=support-a"})
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"forbidden"`) {
		t.Errorf("GET /me as support = %d %s, want 403 forbidden", w.Code, w.Body)
	}
}
