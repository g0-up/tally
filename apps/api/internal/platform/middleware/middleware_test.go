package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/metrics"
)

const root = "tally.vn"

var (
	tenantA = uuid.MustParse("01920000-0000-7000-8000-00000000000a")
	tenantB = uuid.MustParse("01920000-0000-7000-8000-00000000000b")
)

// mapResolver resolves slugs from a map and counts lookups.
type mapResolver struct {
	mu    sync.Mutex
	slugs map[string]uuid.UUID
	calls int
	err   error
}

func (r *mapResolver) ResolveSlug(_ context.Context, slug string) (uuid.UUID, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.err != nil {
		return uuid.Nil, false, r.err
	}
	id, ok := r.slugs[slug]
	return id, ok, nil
}

type mapSessions map[string]Principal

func (m mapSessions) ResolveSession(_ context.Context, token string) (Principal, bool, error) {
	if token == "broken" {
		return Principal{}, false, errors.New("db down")
	}
	p, ok := m[token]
	return p, ok, nil
}

func newEngine(t *testing.T) *gin.Engine {
	t.Helper()
	e, err := httpx.NewEngine(httpx.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

type request struct {
	method, host, path string
	cookies            map[string]string
	header             http.Header
}

func do(h http.Handler, r request) *httptest.ResponseRecorder {
	if r.method == "" {
		r.method = http.MethodGet
	}
	if r.path == "" {
		r.path = "/"
	}
	req := httptest.NewRequest(r.method, r.path, nil)
	req.Host = r.host
	for k, values := range r.header {
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	for name, value := range r.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHostContextClassifyHost(t *testing.T) {
	cases := []struct {
		host string
		kind httpx.HostKind
		slug string
		ok   bool
	}{
		{"tally.vn", httpx.HostRoot, "", true},
		{"TALLY.VN:443", httpx.HostRoot, "", true},
		{"tally.vn.", httpx.HostRoot, "", true},
		{"www.tally.vn", httpx.HostRoot, "", true},
		{"admin.tally.vn", httpx.HostAdmin, "", true},
		{"demo.tally.vn", httpx.HostTenant, "demo", true},
		{"Demo.Tally.VN:8080", httpx.HostTenant, "demo", true},
		{"x.demo.tally.vn", "", "", false},
		{"evil-tally.vn", "", "", false},
		{"tally.vn.evil.com", "", "", false},
		{"example.com", "", "", false},
		{".tally.vn", "", "", false},
		{"127.0.0.1", "", "", false},
		{"127.0.0.1:8080", "", "", false},
		{"[::1]:8080", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		kind, slug, ok := ClassifyHost(tc.host, root)
		if kind != tc.kind || slug != tc.slug || ok != tc.ok {
			t.Errorf("ClassifyHost(%q) = %q %q %v, want %q %q %v", tc.host, kind, slug, ok, tc.kind, tc.slug, tc.ok)
		}
	}
	if kind, _, ok := ClassifyHost("demo.tally.localhost:5173", "tally.localhost"); !ok || kind != httpx.HostTenant {
		t.Error("dev root with port")
	}
}

func TestHostContextMiddleware(t *testing.T) {
	resolver := &mapResolver{slugs: map[string]uuid.UUID{"demo": tenantA}}
	e := newEngine(t)
	e.Use(HostContext(root, resolver))
	e.GET("/", func(c *gin.Context) {
		h, _ := httpx.HostOf(c)
		c.JSON(200, gin.H{"kind": h.Kind, "slug": h.Slug, "tenant": h.TenantID, "known": h.KnownTenant()})
	})
	for host, want := range map[string]string{
		"tally.vn":       `{"kind":"root","known":false,"slug":"","tenant":"00000000-0000-0000-0000-000000000000"}`,
		"admin.tally.vn": `{"kind":"admin","known":false,"slug":"","tenant":"00000000-0000-0000-0000-000000000000"}`,
		"demo.tally.vn":  `{"kind":"tenant","known":true,"slug":"demo","tenant":"` + tenantA.String() + `"}`,
		"ghost.tally.vn": `{"kind":"tenant","known":false,"slug":"ghost","tenant":"00000000-0000-0000-0000-000000000000"}`,
		"a--b.tally.vn":  `{"kind":"tenant","known":false,"slug":"a--b","tenant":"00000000-0000-0000-0000-000000000000"}`,
		"x_y.tally.vn":   `{"kind":"tenant","known":false,"slug":"x_y","tenant":"00000000-0000-0000-0000-000000000000"}`,
	} {
		if rec := do(e, request{host: host}); rec.Body.String() != want {
			t.Errorf("%s: %s", host, rec.Body.String())
		}
	}
	if resolver.calls != 2 {
		t.Errorf("resolver calls = %d; malformed slugs must not be looked up", resolver.calls)
	}
	rec := do(e, request{host: "attacker.example"})
	if rec.Code != http.StatusMisdirectedRequest || !strings.Contains(rec.Body.String(), "misdirected_request") {
		t.Errorf("foreign host: %d %s", rec.Code, rec.Body.String())
	}

	resolver.err = errors.New("db down")
	if rec := do(e, request{host: "other.tally.vn"}); rec.Code != 500 {
		t.Errorf("resolver failure: %d", rec.Code)
	}
}

func TestHostContextCache(t *testing.T) {
	next := &mapResolver{slugs: map[string]uuid.UUID{"demo": tenantA}}
	cache := NewTenantHostCache(next)
	now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	ctx := context.Background()

	for range 3 {
		if id, ok, err := cache.ResolveSlug(ctx, "demo"); err != nil || !ok || id != tenantA {
			t.Fatalf("demo: %v %v %v", id, ok, err)
		}
		if _, ok, _ := cache.ResolveSlug(ctx, "ghost"); ok {
			t.Fatal("ghost resolved")
		}
	}
	if next.calls != 2 {
		t.Fatalf("calls = %d, want one per slug", next.calls)
	}
	next.slugs["ghost"] = tenantB
	now = now.Add(negativeTTL)
	if id, ok, _ := cache.ResolveSlug(ctx, "ghost"); !ok || id != tenantB {
		t.Fatal("negative entry did not expire")
	}

	next.slugs["fresh"] = tenantB
	cache.negative.put("fresh", struct{}{}, now)
	cache.Purge("fresh")
	if _, ok, _ := cache.ResolveSlug(ctx, "fresh"); !ok {
		t.Fatal("purge did not evict the negative entry")
	}

	next.err = errors.New("db down")
	if _, _, err := cache.ResolveSlug(ctx, "boom"); err == nil {
		t.Fatal("resolver error swallowed")
	}
	next.err = nil
}

// The negative cache stays bounded and keeps caching new unknown slugs when full: the oldest
// entry is evicted instead of the cache refusing writes.
func TestHostContextNegativeCacheEvictsOldest(t *testing.T) {
	next := &mapResolver{slugs: map[string]uuid.UUID{}}
	cache := NewTenantHostCache(next)
	now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	cache.now = func() time.Time { return now }
	ctx := context.Background()

	for i := range maxNegativeEntries {
		if _, ok, _ := cache.ResolveSlug(ctx, fmt.Sprintf("ghost-%05d", i)); ok {
			t.Fatal("unknown slug resolved")
		}
	}
	now = now.Add(time.Second)
	if _, ok, _ := cache.ResolveSlug(ctx, "newest"); ok {
		t.Fatal("unknown slug resolved")
	}
	if n := cache.negative.len(); n != maxNegativeEntries {
		t.Fatalf("negative entries = %d, want %d", n, maxNegativeEntries)
	}
	if _, kept := cache.negative.get("ghost-00000"); kept {
		t.Error("oldest entry kept when the cache was full")
	}
	calls := next.calls
	for _, slug := range []string{"newest", "ghost-00001"} {
		if _, ok, _ := cache.ResolveSlug(ctx, slug); ok {
			t.Fatal("unknown slug resolved")
		}
	}
	if next.calls != calls {
		t.Errorf("cached unknown slugs hit the resolver %d times", next.calls-calls)
	}
	// Expired entries are dropped before new ones are added; "newest" is still fresh.
	now = now.Add(negativeTTL - time.Millisecond)
	if _, ok, _ := cache.ResolveSlug(ctx, "later"); ok {
		t.Fatal("unknown slug resolved")
	}
	if n := cache.negative.len(); n != 2 {
		t.Errorf("negative entries after expiry = %d, want newest and later", n)
	}
}

// tenantEngine is the tenant route chain: host context, session, Tenant guard.
func tenantEngine(t *testing.T) *gin.Engine {
	t.Helper()
	cookies := CookieNames(true)
	sessions := mapSessions{
		"owner-a":   {Kind: PrincipalOwner, TenantID: tenantA, UserID: uuid.New()},
		"owner-b":   {Kind: PrincipalOwner, TenantID: tenantB, UserID: uuid.New()},
		"support-a": {Kind: PrincipalSupport, TenantID: tenantA, UserID: uuid.New()},
		"admin":     {Kind: PrincipalPlatformAdmin, UserID: uuid.New()},
		"admin-a":   {Kind: PrincipalPlatformAdmin, TenantID: tenantA, UserID: uuid.New()},
	}
	resolver := NewTenantHostCache(&mapResolver{slugs: map[string]uuid.UUID{"store-a": tenantA, "store-b": tenantB}})
	e := newEngine(t)
	e.Use(RequestID(), HostContext(root, resolver), Session(sessions, cookies.Session))
	api := e.Group("/api/v1", httpx.OnHosts(httpx.HostTenant), Tenant())
	api.GET("/me", func(c *gin.Context) {
		a, err := Actor(c)
		if err != nil {
			httpx.RenderError(c, err)
			return
		}
		c.JSON(200, gin.H{"tenant": TenantID(c), "type": a.Type, "source": a.Source, "request_id": a.RequestID != ""})
	})
	return e
}

func TestTenantHostGuard(t *testing.T) {
	e := tenantEngine(t)
	session := CookieNames(true).Session
	cookie := func(token string) map[string]string { return map[string]string{session: token} }

	ok := do(e, request{host: "store-a.tally.vn", path: "/api/v1/me", cookies: cookie("owner-a")})
	if ok.Code != 200 || !strings.Contains(ok.Body.String(), tenantA.String()) || !strings.Contains(ok.Body.String(), `"type":"owner"`) {
		t.Fatalf("own store: %d %s", ok.Code, ok.Body.String())
	}
	support := do(e, request{host: "store-a.tally.vn", path: "/api/v1/me", cookies: cookie("support-a")})
	if support.Code != 200 || !strings.Contains(support.Body.String(), `"source":"support"`) {
		t.Fatalf("support session: %d %s", support.Code, support.Body.String())
	}

	noCookie := do(e, request{host: "store-b.tally.vn", path: "/api/v1/me"})
	if noCookie.Code != http.StatusUnauthorized {
		t.Fatalf("no cookie: %d", noCookie.Code)
	}
	want := noCookie.Body.String()
	for name, r := range map[string]request{
		"cookie of A on host B":   {host: "store-b.tally.vn", cookies: cookie("owner-a")},
		"unknown slug":            {host: "ghost.tally.vn", cookies: cookie("owner-a")},
		"unknown slug, no cookie": {host: "ghost.tally.vn"},
		"unknown token":           {host: "store-a.tally.vn", cookies: cookie("forged")},
		"empty token":             {host: "store-a.tally.vn", cookies: cookie("")},
		"admin session":           {host: "store-a.tally.vn", cookies: cookie("admin")},
		"admin session w/ tenant": {host: "store-a.tally.vn", cookies: cookie("admin-a")},
		"dev cookie name":         {host: "store-a.tally.vn", cookies: map[string]string{"tally_session": "owner-a"}},
	} {
		r.path = "/api/v1/me"
		rec := do(e, r)
		if rec.Code != http.StatusUnauthorized || rec.Body.String() != want {
			t.Errorf("%s: %d %s, want the no-cookie 401 %s", name, rec.Code, rec.Body.String(), want)
		}
	}
	if !strings.Contains(want, `"code":"unauthenticated"`) {
		t.Errorf("401 body %s", want)
	}

	if rec := do(e, request{host: "store-a.tally.vn", path: "/api/v1/me", cookies: cookie("broken")}); rec.Code != 500 {
		t.Errorf("session resolver failure: %d", rec.Code)
	}
}

func TestTenantHostGuardGroups(t *testing.T) {
	e := newEngine(t)
	resolver := &mapResolver{slugs: map[string]uuid.UUID{"store-a": tenantA}}
	e.Use(HostContext(root, resolver))
	e.Group("/api/v1", httpx.OnHosts(httpx.HostTenant)).GET("/items", func(c *gin.Context) { c.Status(204) })
	e.Group("/api/v1", httpx.OnHosts(httpx.HostRoot, httpx.HostTenant)).GET("/host-context", func(c *gin.Context) { c.Status(204) })
	e.Group("/api/admin/v1", httpx.OnHosts(httpx.HostAdmin)).GET("/tenants", func(c *gin.Context) { c.Status(204) })
	for _, tc := range []struct {
		host, path string
		want       int
	}{
		{"store-a.tally.vn", "/api/v1/items", 204},
		{"tally.vn", "/api/v1/items", 404},
		{"admin.tally.vn", "/api/v1/items", 404},
		{"tally.vn", "/api/v1/host-context", 204},
		{"admin.tally.vn", "/api/v1/host-context", 404},
		{"admin.tally.vn", "/api/admin/v1/tenants", 204},
		{"store-a.tally.vn", "/api/admin/v1/tenants", 404},
		{"ghost.tally.vn", "/api/admin/v1/tenants", 404},
		{"tally.vn", "/api/admin/v1/tenants", 404},
	} {
		if rec := do(e, request{host: tc.host, path: tc.path}); rec.Code != tc.want {
			t.Errorf("%s%s = %d, want %d", tc.host, tc.path, rec.Code, tc.want)
		}
	}
}

func TestActorAndTenantIDOutsideTenantRoutes(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/", nil)
	if TenantID(c) != uuid.Nil {
		t.Error("tenant outside tenant routes")
	}
	if _, err := Actor(c); apperr.KindOf(err) != apperr.Unauthenticated {
		t.Errorf("actor: %v", err)
	}
	if _, ok := PrincipalOf(c); ok {
		t.Error("principal without session")
	}
	c.Set(principalKey, "x")
	c.Set(tenantKey, "x")
	if _, ok := PrincipalOf(c); ok || TenantID(c) != uuid.Nil {
		t.Error("wrong types accepted")
	}
	if a := (audit.Actor{}); a.Validate() == nil {
		t.Error("empty actor valid")
	}
}

func TestCookieNames(t *testing.T) {
	prod, dev := CookieNames(true), CookieNames(false)
	for _, name := range []string{prod.Session, prod.CSRF, prod.AdminSession, prod.AdminCSRF, prod.Scan} {
		if !strings.HasPrefix(name, "__Host-") {
			t.Errorf("production cookie %s lacks __Host-", name)
		}
	}
	if !prod.Secure || dev.Secure || strings.HasPrefix(dev.Session, "__Host-") || prod.Session == prod.AdminSession {
		t.Errorf("cookies %+v %+v", prod, dev)
	}
}

func TestCSRFTokens(t *testing.T) {
	tokens := NewCSRFTokens([]byte("0123456789abcdef0123456789abcdef"))
	tok := tokens.Issue("session:owner:a")
	if !tokens.Valid("session:owner:a", tok) {
		t.Fatal("issued token rejected")
	}
	if tokens.Issue("session:owner:a") == tok {
		t.Error("tokens are not randomised")
	}
	if tokens.Valid("session:owner:b", tok) {
		t.Error("token valid for another session")
	}
	if NewCSRFTokens([]byte("another key of thirty-two bytes!")).Valid("session:owner:a", tok) {
		t.Error("token valid under another key")
	}
	nonce, mac, _ := strings.Cut(tok, ".")
	for _, bad := range []string{"", "tok123", nonce, nonce + ".", "." + mac, nonce + "." + mac + "x", "AAAA." + mac, nonce + ".!!"} {
		if tokens.Valid("session:owner:a", bad) {
			t.Errorf("malformed token %q accepted", bad)
		}
	}
	if random := NewCSRFTokens(nil); !random.Valid("x", random.Issue("x")) {
		t.Error("random-key issuer rejects its own token")
	}
}

func TestCSRF(t *testing.T) {
	tokens := NewCSRFTokens([]byte("0123456789abcdef0123456789abcdef"))
	sessionA, sessionB := uuid.New(), uuid.New()
	e := newEngine(t)
	e.Use(func(c *gin.Context) {
		httpx.SetHost(c, httpx.Host{Kind: httpx.HostTenant, Slug: "a", TenantID: tenantA})
		switch c.GetHeader("X-Session") {
		case "a":
			c.Set(principalKey, Principal{Kind: PrincipalOwner, SessionID: sessionA, TenantID: tenantA})
		case "b":
			c.Set(principalKey, Principal{Kind: PrincipalOwner, SessionID: sessionB, TenantID: tenantA})
		}
	}, CSRF(tokens, "tally_csrf"))
	e.Any("/x", func(c *gin.Context) { c.Status(204) })

	forA := tokens.Issue("session:owner:" + sessionA.String())
	anon := tokens.Issue("anon:tenant:a")
	anonOtherHost := tokens.Issue("anon:tenant:b")
	submit := func(cookie, header, session string) request {
		h := http.Header{"X-Session": {session}}
		if header != "" {
			h.Set(CSRFHeader, header)
		}
		return request{method: "POST", cookies: map[string]string{"tally_csrf": cookie}, header: h}
	}
	for name, tc := range map[string]struct {
		r    request
		want int
	}{
		"safe without token":         {request{method: "GET"}, 204},
		"head":                       {request{method: "HEAD"}, 204},
		"session token":              {submit(forA, forA, "a"), 204},
		"anonymous token":            {submit(anon, anon, ""), 204},
		"token of another session":   {submit(forA, forA, "b"), 403},
		"anonymous token in session": {submit(anon, anon, "a"), 403},
		"session token anonymously":  {submit(forA, forA, ""), 403},
		"token of another host":      {submit(anonOtherHost, anonOtherHost, ""), 403},
		"unsigned token":             {submit("tok123", "tok123", "a"), 403},
		"header differs from cookie": {submit(forA, tokens.Issue("session:owner:"+sessionA.String()), "a"), 403},
		"no header":                  {submit(forA, "", "a"), 403},
		"no cookie":                  {request{method: "PATCH", header: http.Header{CSRFHeader: {forA}, "X-Session": {"a"}}}, 403},
		"empty both":                 {submit("", "", "a"), 403},
	} {
		tc.r.path = "/x"
		rec := do(e, tc.r)
		if rec.Code != tc.want {
			t.Errorf("%s: %d, want %d", name, rec.Code, tc.want)
		}
		if tc.want == 403 && !strings.Contains(rec.Body.String(), "csrf_failed") {
			t.Errorf("%s: body %s", name, rec.Body.String())
		}
	}
}

func TestCookieHelpers(t *testing.T) {
	tokens := NewCSRFTokens(nil)
	e := newEngine(t)
	var issued string
	e.GET("/set", func(c *gin.Context) {
		issued = SetCSRFCookie(c, tokens, CookieNames(true), "__Host-tally_csrf", "anon:root:")
		SetSessionCookie(c, CookieNames(true), "__Host-tally_session", "opaque", 3600)
		ClearCookie(c, CookieNames(true), "__Host-tally_admin_session", true)
		c.Status(204)
	})
	rec := do(e, request{path: "/set"})
	cookies := map[string]*http.Cookie{}
	for _, ck := range rec.Result().Cookies() {
		cookies[ck.Name] = ck
	}
	csrf, session, cleared := cookies["__Host-tally_csrf"], cookies["__Host-tally_session"], cookies["__Host-tally_admin_session"]
	if csrf == nil || csrf.Value != issued || csrf.HttpOnly || !csrf.Secure || csrf.Path != "/" || csrf.Domain != "" || !tokens.Valid("anon:root:", issued) {
		t.Errorf("csrf cookie = %+v", csrf)
	}
	if session == nil || session.Value != "opaque" || !session.HttpOnly || !session.Secure || session.Path != "/" || session.Domain != "" ||
		session.SameSite != http.SameSiteLaxMode || session.MaxAge != 3600 {
		t.Errorf("session cookie = %+v", session)
	}
	if cleared == nil || cleared.MaxAge >= 0 || cleared.Value != "" {
		t.Errorf("cleared cookie = %+v", cleared)
	}
}

func TestRequireRecentAuth(t *testing.T) {
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	e := newEngine(t)
	e.Use(func(c *gin.Context) {
		switch c.GetHeader("X-Reauth") {
		case "none":
			c.Set(principalKey, Principal{Kind: PrincipalOwner, TenantID: tenantA})
		case "fresh":
			c.Set(principalKey, Principal{Kind: PrincipalOwner, TenantID: tenantA, ReauthenticatedAt: at(-4 * time.Minute)})
		case "stale":
			c.Set(principalKey, Principal{Kind: PrincipalOwner, TenantID: tenantA, ReauthenticatedAt: at(-6 * time.Minute)})
		case "future":
			c.Set(principalKey, Principal{Kind: PrincipalOwner, TenantID: tenantA, ReauthenticatedAt: at(time.Hour)})
		}
	}, RequireRecentAuth(ReauthWindow, func() time.Time { return now }))
	e.POST("/x", func(c *gin.Context) { c.Status(204) })
	for kind, want := range map[string]int{"fresh": 204, "none": 403, "stale": 403, "future": 403, "": 401} {
		rec := do(e, request{method: "POST", path: "/x", header: http.Header{"X-Reauth": {kind}}})
		if rec.Code != want {
			t.Errorf("%q: %d, want %d", kind, rec.Code, want)
		}
		if want == 403 && !strings.Contains(rec.Body.String(), "reauth_required") {
			t.Errorf("%q: body %s", kind, rec.Body.String())
		}
	}
}

type entitlementFunc func(uuid.UUID) (bool, error)

func (f entitlementFunc) CanWrite(_ context.Context, id uuid.UUID) (bool, error) { return f(id) }

func TestEntitlement(t *testing.T) {
	e := newEngine(t)
	e.Use(func(c *gin.Context) { c.Set(tenantKey, uuid.MustParse(c.GetHeader("X-Tenant"))) })
	e.Use(Entitlement(entitlementFunc(func(id uuid.UUID) (bool, error) {
		switch id {
		case tenantA:
			return true, nil
		case tenantB:
			return false, nil
		default:
			return false, errors.New("db down")
		}
	})))
	e.Any("/x", func(c *gin.Context) { c.Status(204) })
	h := func(id uuid.UUID) http.Header { return http.Header{"X-Tenant": {id.String()}} }
	if rec := do(e, request{method: "POST", path: "/x", header: h(tenantA)}); rec.Code != 204 {
		t.Errorf("writable: %d", rec.Code)
	}
	rec := do(e, request{method: "POST", path: "/x", header: h(tenantB)})
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "subscription_read_only") {
		t.Errorf("read-only write: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(e, request{method: "GET", path: "/x", header: h(tenantB)}); rec.Code != 204 {
		t.Errorf("read-only read: %d", rec.Code)
	}
	if rec := do(e, request{method: "POST", path: "/x", header: h(uuid.New())}); rec.Code != 500 {
		t.Errorf("reader failure: %d", rec.Code)
	}
}

func TestSupportReadOnly(t *testing.T) {
	e := newEngine(t)
	e.Use(func(c *gin.Context) {
		if kind := c.GetHeader("X-Kind"); kind != "" {
			c.Set(principalKey, Principal{Kind: PrincipalKind(kind), TenantID: tenantA})
		}
	}, SupportReadOnly())
	e.Any("/x", func(c *gin.Context) { c.Status(204) })
	for _, tc := range []struct {
		method, kind string
		want         int
	}{
		{"POST", "support", 403}, {"DELETE", "support", 403}, {"GET", "support", 204},
		{"POST", "owner", 204}, {"POST", "", 204},
	} {
		rec := do(e, request{method: tc.method, path: "/x", header: http.Header{"X-Kind": {tc.kind}}})
		if rec.Code != tc.want {
			t.Errorf("%s as %q: %d, want %d", tc.method, tc.kind, rec.Code, tc.want)
		}
		if tc.want == 403 && !strings.Contains(rec.Body.String(), "support_read_only") {
			t.Errorf("body %s", rec.Body.String())
		}
	}
}

func TestRateLimiter(t *testing.T) {
	r := NewRateLimiter(1, 2)
	now := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	for i := range 2 {
		if ok, _ := r.Allow("ip1"); !ok {
			t.Fatalf("burst request %d denied", i)
		}
	}
	ok, wait := r.Allow("ip1")
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("over burst: %v %v", ok, wait)
	}
	if ok, _ := r.Allow("ip2"); !ok {
		t.Fatal("keys share a bucket")
	}
	now = now.Add(time.Second)
	if ok, _ := r.Allow("ip1"); !ok {
		t.Fatal("token not refilled")
	}
	now = now.Add(limiterIdleTTL)
	r.Allow("ip3")
	r.mu.Lock()
	_, kept := r.buckets.get("ip1")
	r.mu.Unlock()
	if kept {
		t.Error("idle bucket not swept")
	}

	// A full limiter evicts its least recently used bucket instead of growing.
	small := NewRateLimiter(1, 1)
	small.now = func() time.Time { return now }
	small.buckets = newLRU[*rate.Limiter](2)
	small.Allow("a")
	small.Allow("b")
	small.Allow("a")
	small.Allow("c")
	if _, ok := small.buckets.get("b"); ok || small.buckets.len() != 2 {
		t.Errorf("full limiter kept %d buckets, b present = %v", small.buckets.len(), ok)
	}
	if ok, _ := small.Allow("a"); ok {
		t.Error("recently used bucket was evicted")
	}

	e := newEngine(t)
	e.Use(RateLimitByIP(NewRateLimiter(0.5, 1)))
	e.GET("/x", func(c *gin.Context) { c.Status(204) })
	if rec := do(e, request{path: "/x"}); rec.Code != 204 {
		t.Fatalf("first: %d", rec.Code)
	}
	rec := do(e, request{path: "/x"})
	if rec.Code != 429 || rec.Header().Get("Retry-After") != "2" || !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("limited: %d %q %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
}

func TestClientKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.7":                    "203.0.113.7",
		"::ffff:203.0.113.7":             "203.0.113.7",
		"2001:db8:1:2:aaaa:bbbb:cccc:1":  "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff:ffff:ff": "2001:db8:1:2::/64",
		"2001:db8:1:3::1":                "2001:db8:1:3::/64",
		"fe80::1%eth0":                   "fe80::/64",
		"not-an-ip":                      "not-an-ip",
	} {
		if got := ClientKey(in); got != want {
			t.Errorf("ClientKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRequestID(t *testing.T) {
	e := newEngine(t)
	e.Use(RequestID())
	e.GET("/x", func(c *gin.Context) { c.String(200, RequestIDFrom(c.Request.Context())) })
	rec := do(e, request{path: "/x", header: http.Header{RequestIDHeader: {"edge-req-12345"}}})
	if rec.Body.String() != "edge-req-12345" || rec.Header().Get(RequestIDHeader) != "edge-req-12345" {
		t.Errorf("reused id: %s %v", rec.Body.String(), rec.Header())
	}
	for _, bad := range []string{"", "short", "has space in it", strings.Repeat("a", 65), "inject\nheader"} {
		rec := do(e, request{path: "/x", header: http.Header{RequestIDHeader: {bad}}})
		if _, err := uuid.Parse(rec.Body.String()); err != nil || rec.Header().Get(RequestIDHeader) != rec.Body.String() {
			t.Errorf("incoming %q: got %q", bad, rec.Body.String())
		}
	}
	if RequestIDFrom(context.Background()) != "" {
		t.Error("request id outside a request")
	}
}

func TestRecoverAndLogger(t *testing.T) {
	var logs bytes.Buffer
	m := metrics.New()
	e := newEngine(t)
	e.Use(RequestID(), Logger(slog.New(slog.NewJSONHandler(&logs, nil)), m), Recover(), SecurityHeaders())
	e.GET("/panic", func(*gin.Context) { panic("kaboom") })
	e.GET("/late-panic", func(c *gin.Context) {
		c.String(200, "partial")
		panic("after write")
	})
	e.GET("/abort", func(*gin.Context) { panic(http.ErrAbortHandler) })
	e.GET("/ok", func(c *gin.Context) { c.Status(204) })
	e.GET("/missing", func(c *gin.Context) { httpx.RenderError(c, apperr.NewNotFound("not_found", "x")) })

	rec := do(e, request{path: "/panic?token=secret"})
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "internal_error") || strings.Contains(rec.Body.String(), "kaboom") {
		t.Fatalf("panic: %d %s", rec.Code, rec.Body.String())
	}
	for k, v := range map[string]string{"X-Content-Type-Options": "nosniff", "Cache-Control": "no-store", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer"} {
		if rec.Header().Get(k) != v {
			t.Errorf("header %s = %q", k, rec.Header().Get(k))
		}
	}
	if rec := do(e, request{path: "/late-panic"}); rec.Code != 200 || rec.Body.String() != "partial" {
		t.Errorf("late panic: %d %s", rec.Code, rec.Body.String())
	}
	func() {
		defer func() {
			if p := recover(); p != http.ErrAbortHandler {
				t.Errorf("ErrAbortHandler not re-raised: %v", p)
			}
		}()
		do(e, request{path: "/abort"})
	}()
	do(e, request{path: "/ok"})
	do(e, request{path: "/missing"})

	out := logs.String()
	for _, want := range []string{`"msg":"panic recovered"`, `"panic":"kaboom"`, `"level":"ERROR","msg":"http request"`, `"route":"/panic"`, `"status":204`, `"level":"WARN"`, `"request_id":"`} {
		if !strings.Contains(out, want) {
			t.Errorf("logs lack %s", want)
		}
	}
	if strings.Contains(out, "secret") {
		t.Error("query string logged")
	}
	scrape := httptest.NewRecorder()
	m.Handler().ServeHTTP(scrape, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(scrape.Body)
	if !strings.Contains(string(body), `route="/ok"`) || !strings.Contains(string(body), `status="500"`) {
		t.Errorf("http metrics missing:\n%s", body)
	}
}
