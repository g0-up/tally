//go:build integration

package scanner_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/catalog"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/scanner"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

const (
	iPhoneUA  = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1"
	androidUA = "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Mobile Safari/537.36"
	// samplePrice and sampleStock are set on the sample SKU and must never reach the phone.
	samplePrice = "98765"
	sampleStock = "4321"
)

// fakeSessions resolves the login session cookies of the test users; revoke signs one out and
// rotate replaces a cookie as a re-authentication does, keeping the login session.
type fakeSessions struct {
	mu sync.Mutex
	m  map[string]middleware.Principal
}

func (f *fakeSessions) ResolveSession(_ context.Context, token string) (middleware.Principal, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.m[token]
	return p, ok, nil
}

func (f *fakeSessions) SessionActive(_ context.Context, tenantID, userID, sessionID uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.m {
		if p.TenantID == tenantID && p.UserID == userID && p.SessionID == sessionID {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeSessions) add(p middleware.Principal) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	token := rand.Text()
	f.m[token] = p
	return token
}

func (f *fakeSessions) revoke(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, token)
}

func (f *fakeSessions) rotate(token string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	next := rand.Text()
	f.m[next] = f.m[token]
	delete(f.m, token)
	return next
}

// fakeHosts maps the store slugs of the test to their tenants.
type fakeHosts struct {
	mu sync.Mutex
	m  map[string]uuid.UUID
}

func (f *fakeHosts) ResolveSlug(_ context.Context, slug string) (uuid.UUID, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.m[slug]
	return id, ok, nil
}

// heldTenants is the maintenance switch of the test tenants.
type heldTenants struct {
	mu sync.Mutex
	m  map[uuid.UUID]bool
}

func (h *heldTenants) InMaintenance(_ context.Context, tenantID uuid.UUID) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.m[tenantID], nil
}

func (h *heldTenants) hold(tenantID uuid.UUID, held bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.m[tenantID] = held
}

// readOnlyTenants is the subscription switch of the test tenants: a listed tenant may not write.
type readOnlyTenants struct {
	mu sync.Mutex
	m  map[uuid.UUID]bool
}

func (r *readOnlyTenants) CanWrite(_ context.Context, tenantID uuid.UUID) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.m[tenantID], nil
}

func (r *readOnlyTenants) set(tenantID uuid.UUID, readOnly bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[tenantID] = readOnly
}

// stackConfig tunes one test stack; the zero value has generous limits, short stream intervals
// and no server timeouts.
type stackConfig struct {
	limits       *scanner.RateLimits
	writeTimeout time.Duration
	ping, tick   time.Duration
}

var generousLimits = scanner.RateLimits{
	Claim: scanner.RateLimit{PerSecond: 1000, Burst: 1000}, Scans: scanner.RateLimit{PerSecond: 1000, Burst: 1000},
	Events: scanner.RateLimit{PerSecond: 1000, Burst: 1000}, Commands: scanner.RateLimit{PerSecond: 1000, Burst: 1000},
}

// stack is the HTTP API with the scanner routes on a real listener, so event streams flow.
type stack struct {
	t        *testing.T
	db       *db.DB
	clk      *clock.Fixed
	svc      *scanner.Service
	catalog  *catalog.Service
	sessions *fakeSessions
	hosts    *fakeHosts
	held     *heldTenants
	readOnly *readOnlyTenants
	url      string
	http     *http.Client
}

func newStack(t *testing.T, cfg stackConfig) *stack {
	t.Helper()
	if cfg.limits == nil {
		cfg.limits = &generousLimits
	}
	if cfg.ping == 0 {
		cfg.ping = 200 * time.Millisecond
	}
	if cfg.tick == 0 {
		cfg.tick = 50 * time.Millisecond
	}
	d, clk := testkit.DB(t), testkit.Clock()
	domains := app.NewDomains(d, clk, audit.NewRecorder(clk))
	s := &stack{
		t: t, db: d, clk: clk, svc: domains.Scanner, catalog: domains.Catalog,
		sessions: &fakeSessions{m: map[string]middleware.Principal{}},
		hosts:    &fakeHosts{m: map[string]uuid.UUID{}},
		held:     &heldTenants{m: map[uuid.UUID]bool{}},
		readOnly: &readOnlyTenants{m: map[uuid.UUID]bool{}},
		http:     &http.Client{Transport: &http.Transport{}},
	}
	a, err := app.New(app.Options{
		PublicBaseURL: &url.URL{Scheme: "http", Host: "tally.test"},
		Logger:        slog.New(slog.DiscardHandler),
		Ports: app.Ports{
			Sessions: s.sessions, TenantHosts: s.hosts, Maintenance: s.held, Entitlements: s.readOnly, LoginSessions: s.sessions,
		},
		CSRFKey:      []byte("0123456789abcdef0123456789abcdef"),
		Now:          clk.Now,
		APIRateLimit: app.RateLimit{PerSecond: 1000, Burst: 10000},
		Modules: app.Modules{Scanner: scanner.NewHandler(domains.Scanner,
			scanner.WithRateLimits(*cfg.limits), scanner.WithStreamTiming(cfg.ping, cfg.tick))},
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(a.Handler())
	srv.Config.WriteTimeout, srv.Config.ReadTimeout = cfg.writeTimeout, cfg.writeTimeout
	srv.Start()
	s.url = srv.URL
	// Cleanups run last-in first-out: the hub tells every stream to go before the server waits.
	t.Cleanup(srv.Close)
	t.Cleanup(s.http.CloseIdleConnections)
	t.Cleanup(domains.Scanner.Hub().CloseAll)
	return s
}

// store is a tenant with the default units and a host the stack resolves.
type store struct {
	id   uuid.UUID
	host string
}

func (s *stack) newStore() store {
	s.t.Helper()
	tn := testkit.NewTenant(s.t, s.db)
	sc := catalog.SeedContext{TenantID: tn.ID, Now: testkit.Epoch,
		Actor: audit.Actor{TenantID: tn.ID, Type: audit.ActorSystem, Source: audit.SourceCLI}}
	testkit.InTenant(s.t, s.db, tn.ID, func(q db.Querier) error {
		return catalog.NewSeeder(s.catalog).SeedUnits(context.Background(), q, sc)
	})
	s.hosts.mu.Lock()
	s.hosts.m[tn.Slug] = tn.ID
	s.hosts.mu.Unlock()
	return store{id: tn.ID, host: tn.Slug + ".tally.test"}
}

// newSku creates a SKU in "chai" with a price, a cost and a minimum stock the phone must not see.
func (s *stack) newSku(st store, name, code string) (skuID, unitID uuid.UUID) {
	s.t.Helper()
	ctx := context.Background()
	units, err := s.catalog.Units(ctx, catalog.Scope{TenantID: st.id}, false)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, u := range units {
		if u.Name == "chai" {
			unitID = u.ID
		}
	}
	price, stock := money.MustMoney(samplePrice), money.MustQty(sampleStock)
	actor := audit.Actor{TenantID: st.id, Type: audit.ActorOwner, ID: ids.New(), Source: audit.SourceWeb}
	d, err := s.catalog.CreateProduct(ctx, actor, catalog.ProductInput{Name: name, Sku: &catalog.SkuInput{
		Code: code, BaseUnitID: unitID, RefCost: &price, MinPrice: &price, MinStock: &stock,
	}})
	if err != nil {
		s.t.Fatal(err)
	}
	return d.Skus[0].Sku.ID, unitID
}

// client keeps the cookies of one browser on one host.
type client struct {
	t     *testing.T
	s     *stack
	host  string
	ua    string
	jar   map[string]string
	extra []*http.Cookie // sent before the jar, such as a stray parent-domain cookie
	csrf  string
	token string // the login session of a desktop
	p     middleware.Principal
}

// desktop signs a new user of st in and loads the CSRF token of that session.
func (s *stack) desktop(st store) *client {
	s.t.Helper()
	return s.desktopOf(st, ids.New())
}

func (s *stack) desktopOf(st store, userID uuid.UUID) *client {
	s.t.Helper()
	p := middleware.Principal{Kind: middleware.PrincipalOwner, SessionID: ids.New(), UserID: userID, TenantID: st.id}
	c := &client{t: s.t, s: s, host: st.host, jar: map[string]string{}, token: s.sessions.add(p), p: p}
	c.jar["tally_session"] = c.token
	c.hostContext()
	return c
}

// phone is a signed-out browser of st.
func (s *stack) phone(st store, ua string) *client {
	s.t.Helper()
	c := &client{t: s.t, s: s, host: st.host, ua: ua, jar: map[string]string{}}
	c.hostContext()
	return c
}

func (c *client) hostContext() {
	c.t.Helper()
	r := c.do(http.MethodGet, "/host-context", nil)
	if r.code != http.StatusOK {
		c.t.Fatalf("host-context = %d %s", r.code, r.body)
	}
	c.csrf = decode[struct {
		CSRFToken string `json:"csrf_token"`
	}](c.t, r.body).CSRFToken
}

type response struct {
	code   int
	body   []byte
	header http.Header
}

func (c *client) request(ctx context.Context, method, path string, body any, header http.Header) *http.Request {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.s.url+"/api/v1"+path, rd)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Host = c.host
	for k, vs := range header {
		req.Header[k] = vs
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.csrf != "" {
		req.Header.Set(middleware.CSRFHeader, c.csrf)
	}
	if c.ua != "" {
		req.Header.Set("User-Agent", c.ua)
	}
	for _, ck := range c.extra {
		req.AddCookie(ck)
	}
	for name, value := range c.jar {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	return req
}

func (c *client) do(method, path string, body any) response {
	c.t.Helper()
	return c.doWith(method, path, body, nil)
}

func (c *client) doWith(method, path string, body any, header http.Header) response {
	c.t.Helper()
	res, err := c.s.http.Do(c.request(context.Background(), method, path, body, header))
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		c.t.Fatalf("%s %s: read body: %v", method, path, err)
	}
	for _, ck := range res.Cookies() {
		if ck.MaxAge < 0 {
			delete(c.jar, ck.Name)
			continue
		}
		c.jar[ck.Name] = ck.Value
	}
	return response{code: res.StatusCode, body: raw, header: res.Header}
}

func decode[T any](t *testing.T, raw []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return v
}

func expect(t *testing.T, r response, status int, code string) {
	t.Helper()
	if r.code != status || (code != "" && !strings.Contains(string(r.body), `"`+code+`"`)) {
		t.Errorf("response = %d %s, want %d %s", r.code, r.body, status, code)
	}
}

type created struct {
	ID             uuid.UUID `json:"id"`
	ClaimCode      string    `json:"claim_code"`
	ClaimExpiresAt time.Time `json:"claim_expires_at"`
}

type status struct {
	State       string     `json:"state"`
	DeviceLabel string     `json:"device_label"`
	VerifyCode  string     `json:"verify_code"`
	ClaimedAt   *time.Time `json:"claimed_at"`
	ApprovedAt  *time.Time `json:"approved_at"`
}

type phoneStatus struct {
	State      string `json:"state"`
	VerifyCode string `json:"verify_code"`
}

type scanResult struct {
	Outcome     string `json:"outcome"`
	ProductName string `json:"product_name"`
	UnitName    string `json:"unit_name"`
}

func (c *client) create() created {
	c.t.Helper()
	r := c.do(http.MethodPost, "/scanner-sessions", nil)
	if r.code != http.StatusCreated {
		c.t.Fatalf("create = %d %s", r.code, r.body)
	}
	return decode[created](c.t, r.body)
}

func (c *client) status(id uuid.UUID) status {
	c.t.Helper()
	r := c.do(http.MethodGet, "/scanner-sessions/"+id.String(), nil)
	if r.code != http.StatusOK {
		c.t.Fatalf("status = %d %s", r.code, r.body)
	}
	return decode[status](c.t, r.body)
}

func (c *client) command(id uuid.UUID, verb string) response {
	c.t.Helper()
	return c.do(http.MethodPost, "/scanner-sessions/"+id.String()+":"+verb, nil)
}

func (c *client) claim(code string) phoneStatus {
	c.t.Helper()
	r := c.do(http.MethodPost, "/scan/claim", map[string]string{"code": code})
	if r.code != http.StatusOK {
		c.t.Fatalf("claim = %d %s", r.code, r.body)
	}
	return decode[phoneStatus](c.t, r.body)
}

func (c *client) phoneStatus() phoneStatus {
	c.t.Helper()
	r := c.do(http.MethodGet, "/scan/session", nil)
	if r.code != http.StatusOK {
		c.t.Fatalf("phone session = %d %s", r.code, r.body)
	}
	return decode[phoneStatus](c.t, r.body)
}

func (c *client) scan(key, code string) response {
	c.t.Helper()
	return c.doWith(http.MethodPost, "/scan/scans", map[string]string{"code": code}, http.Header{"Idempotency-Key": {key}})
}

func (c *client) resolve(id, scanID uuid.UUID, body any) response {
	c.t.Helper()
	return c.do(http.MethodPost, "/scanner-sessions/"+id.String()+"/scans/"+scanID.String()+"/result", body)
}

// pair opens a session on desktop, claims it with phone and approves it.
func pair(t *testing.T, desktop, phone *client) uuid.UUID {
	t.Helper()
	cr := desktop.create()
	phone.claim(cr.ClaimCode)
	expect(t, desktop.command(cr.ID, "approve"), http.StatusOK, "claimed")
	return cr.ID
}

type sseEvent struct {
	name string
	data json.RawMessage
}

// stream reads one event stream in the background.
type stream struct {
	code   int
	body   []byte
	events chan sseEvent
	pings  atomic.Int32
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func (c *client) open(path string) *stream {
	c.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := c.request(ctx, http.MethodGet, path, nil, http.Header{"Accept": {"text/event-stream"}})
	res, err := c.s.http.Do(req)
	if err != nil {
		cancel()
		c.t.Fatalf("open %s: %v", path, err)
	}
	st := &stream{code: res.StatusCode, events: make(chan sseEvent, 64), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	c.t.Cleanup(st.close)
	if res.StatusCode != http.StatusOK {
		st.body, _ = io.ReadAll(res.Body)
		_ = res.Body.Close()
		close(st.events)
		close(st.done)
		return st
	}
	go st.read(res.Body)
	return st
}

func (st *stream) read(body io.ReadCloser) {
	defer close(st.done)
	defer close(st.events)
	defer func() { _ = body.Close() }()
	r := bufio.NewReader(body)
	var ev sseEvent
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if ev.name != "" {
				select {
				case st.events <- ev:
				case <-st.ctx.Done():
					return
				}
			}
			ev = sseEvent{}
		case strings.HasPrefix(line, ":"):
			st.pings.Add(1)
		case strings.HasPrefix(line, "event: "):
			ev.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.data = json.RawMessage(strings.TrimPrefix(line, "data: "))
		}
	}
}

func (st *stream) close() {
	st.cancel()
	<-st.done
}

// next returns the next event and fails when the stream ends or stays silent.
func (st *stream) next(t *testing.T, name string) json.RawMessage {
	t.Helper()
	select {
	case ev, ok := <-st.events:
		if !ok {
			t.Fatalf("stream ended, want %s", name)
		}
		if ev.name != name {
			t.Fatalf("event %s %s, want %s", ev.name, ev.data, name)
		}
		return ev.data
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s event within 5s", name)
		return nil
	}
}

// ends fails unless the server closes the stream without another event.
func (st *stream) ends(t *testing.T) {
	t.Helper()
	select {
	case ev, ok := <-st.events:
		if ok {
			t.Fatalf("event %s %s, want the end of the stream", ev.name, ev.data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream still open after 5s")
	}
}

// quiet fails when an event arrives within d.
func (st *stream) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case ev, ok := <-st.events:
		if ok {
			t.Fatalf("unexpected event %s %s", ev.name, ev.data)
		}
		t.Fatal("stream ended")
	case <-time.After(d):
	}
}

func (st *stream) state(t *testing.T) status {
	t.Helper()
	return decode[status](t, st.next(t, "state"))
}

// setSeen marks the desktop of a session alive now, as a stream tick would.
func (s *stack) setSeen(id uuid.UUID) {
	s.t.Helper()
	if _, err := testkit.SuperDB(s.t).Exec(`UPDATE tally.scanner_sessions SET desktop_seen_at = $1 WHERE id = $2`,
		s.clk.Now(), id); err != nil {
		s.t.Fatal(err)
	}
}

func (s *stack) seenAt(id uuid.UUID) time.Time {
	s.t.Helper()
	var at time.Time
	if err := testkit.SuperDB(s.t).Get(&at, `SELECT desktop_seen_at FROM tally.scanner_sessions WHERE id = $1`, id); err != nil {
		s.t.Fatal(err)
	}
	return at
}

func (s *stack) count(query string, args ...any) int {
	s.t.Helper()
	var n int
	if err := testkit.SuperDB(s.t).Get(&n, query, args...); err != nil {
		s.t.Fatal(err)
	}
	return n
}

func TestPairingLifecycle(t *testing.T) {
	// No background checks: the pool assertion must only see the scans.
	s := newStack(t, stackConfig{tick: time.Hour})
	st := s.newStore()
	skuID, unitID := s.newSku(st, "Trà xanh 500ml", "TRA-500")
	desktop, phone := s.desktop(st), s.phone(st, iPhoneUA)

	r := desktop.do(http.MethodPost, "/scanner-sessions", nil)
	expect(t, r, http.StatusCreated, "")
	cr := decode[created](t, r.body)
	if cr.ID == uuid.Nil || len(cr.ClaimCode) < 20 || !cr.ClaimExpiresAt.Equal(s.clk.Now().Add(2*time.Minute)) ||
		r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("created = %+v, cache %q", cr, r.header.Get("Cache-Control"))
	}
	if got := desktop.status(cr.ID); got != (status{State: "waiting"}) {
		t.Fatalf("status before the claim = %+v", got)
	}
	desk := desktop.open("/scanner-sessions/" + cr.ID.String() + "/events")
	if got := desk.state(t); got.State != "waiting" {
		t.Fatalf("first desktop event = %+v", got)
	}

	claimed := phone.claim(cr.ClaimCode)
	if claimed.State != "pending" || len(claimed.VerifyCode) != 4 || phone.jar["tally_scan"] == "" {
		t.Fatalf("claim = %+v, cookie %v", claimed, phone.jar)
	}
	req := decode[struct {
		DeviceLabel string `json:"device_label"`
		VerifyCode  string `json:"verify_code"`
	}](t, desk.next(t, "claim_requested"))
	if req.VerifyCode != claimed.VerifyCode || req.DeviceLabel != "iPhone · Safari" {
		t.Fatalf("claim_requested = %+v, phone verify code %s", req, claimed.VerifyCode)
	}
	if got := desktop.status(cr.ID); got.State != "pending" || got.VerifyCode != claimed.VerifyCode ||
		got.DeviceLabel != "iPhone · Safari" || got.ClaimedAt == nil || got.ApprovedAt != nil {
		t.Fatalf("pending desktop status = %+v", got)
	}
	if got := phone.phoneStatus(); got != (phoneStatus{State: "pending", VerifyCode: claimed.VerifyCode}) {
		t.Fatalf("pending phone status = %+v", got)
	}
	ph := phone.open("/scan/events")
	if got := decode[phoneStatus](t, ph.next(t, "state")); got != (phoneStatus{State: "pending", VerifyCode: claimed.VerifyCode}) {
		t.Fatalf("first phone event = %+v", got)
	}

	t.Run("a pending phone cannot scan", func(t *testing.T) {
		expect(t, phone.scan("pending-1", "8935001234567"), http.StatusConflict, "scan_pending_approval")
	})

	r = desktop.command(cr.ID, "approve")
	expect(t, r, http.StatusOK, "")
	if got := decode[status](t, r.body); got.State != "claimed" || got.ApprovedAt == nil || got.VerifyCode != claimed.VerifyCode {
		t.Fatalf("approve = %+v", got)
	}
	ph.next(t, "approved")
	if got := phone.phoneStatus(); got.State != "claimed" {
		t.Fatalf("approved phone status = %+v", got)
	}
	expect(t, desktop.command(cr.ID, "approve"), http.StatusConflict, "not_pending")

	t.Run("ten waiting scans hold no connection and get their answers", func(t *testing.T) {
		const n = 10
		results := make(chan response, n)
		for i := range n {
			go func() { results <- phone.scan(fmt.Sprintf("scan-%d", i), fmt.Sprintf("89350012345%02d", i)) }()
		}
		scans := map[string]uuid.UUID{}
		for range n {
			ev := decode[struct {
				ScanID uuid.UUID `json:"scan_id"`
				Code   string    `json:"code"`
				TTLMs  int64     `json:"ttl_ms"`
			}](t, desk.next(t, "scan"))
			if ev.TTLMs != 10000 {
				t.Errorf("scan event %+v, want ttl_ms 10000", ev)
			}
			scans[ev.Code] = ev.ScanID
		}
		if len(scans) != n {
			t.Fatalf("scan events %v", scans)
		}
		if inUse := s.db.Stats().InUse; inUse != 0 {
			t.Fatalf("%d connections in use while the scans wait for the desktop", inUse)
		}
		for code, scanID := range scans {
			body := map[string]any{"outcome": "not_found"}
			if code == "8935001234500" {
				body = map[string]any{"outcome": "added", "sku_id": skuID, "unit_id": unitID}
			}
			expect(t, desktop.resolve(cr.ID, scanID, body), http.StatusOK, "")
		}
		added := 0
		for range n {
			r := <-results
			if r.code != http.StatusOK || r.header.Get("Cache-Control") != "no-store" {
				t.Fatalf("scan = %d %s", r.code, r.body)
			}
			keys := decode[map[string]any](t, r.body)
			for k := range keys {
				if k != "outcome" && k != "product_name" && k != "unit_name" {
					t.Errorf("the phone got key %q in %s", k, r.body)
				}
			}
			if bytes.Contains(r.body, []byte(samplePrice)) || bytes.Contains(r.body, []byte(sampleStock)) {
				t.Errorf("the phone got a price or stock: %s", r.body)
			}
			res := decode[scanResult](t, r.body)
			switch res.Outcome {
			case "added":
				added++
				if res.ProductName != "Trà xanh 500ml" || res.UnitName != "chai" {
					t.Errorf("added = %+v", res)
				}
			case "not_found":
				if res.ProductName != "" || res.UnitName != "" {
					t.Errorf("not_found carries names: %+v", res)
				}
			default:
				t.Errorf("scan result %+v", res)
			}
		}
		if added != 1 {
			t.Errorf("%d added results, want 1", added)
		}
		// A second answer to a resolved scan gets the first one back.
		r := desktop.resolve(cr.ID, scans["8935001234500"], map[string]any{"outcome": "not_found"})
		if got := decode[scanResult](t, r.body); r.code != http.StatusOK || got.Outcome != "added" || got.UnitName != "chai" {
			t.Errorf("late second answer = %d %s", r.code, r.body)
		}
	})

	t.Run("the desktop answer is validated", func(t *testing.T) {
		unknown := uuid.New()
		expect(t, desktop.resolve(cr.ID, unknown, map[string]any{"outcome": "timeout"}), http.StatusUnprocessableEntity, "oneof")
		expect(t, desktop.resolve(cr.ID, unknown, map[string]any{"outcome": "added"}), http.StatusUnprocessableEntity, "required")
		// An unknown scan is 404 before the ids of the answer are looked up.
		expect(t, desktop.resolve(cr.ID, unknown, map[string]any{"outcome": "added", "sku_id": uuid.New(), "unit_id": unitID}),
			http.StatusNotFound, "not_found")
		expect(t, desktop.resolve(cr.ID, unknown, map[string]any{"outcome": "not_found"}), http.StatusNotFound, "not_found")
		var known uuid.UUID
		if err := testkit.SuperDB(t).Get(&known, `SELECT id FROM tally.scanner_scans WHERE session_id = $1 LIMIT 1`, cr.ID); err != nil {
			t.Fatal(err)
		}
		r := desktop.resolve(cr.ID, known, map[string]any{"outcome": "added", "sku_id": uuid.New(), "unit_id": unitID})
		if r.code != http.StatusUnprocessableEntity || !strings.Contains(string(r.body), `"sku_id":"not_found"`) {
			t.Errorf("answer with an unknown SKU = %d %s", r.code, r.body)
		}
	})
}

func TestRejectedPhoneScansANewCode(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop, first := s.desktop(st), s.phone(st, androidUA)
	cr := desktop.create()
	expect(t, desktop.command(cr.ID, "reject"), http.StatusConflict, "not_pending")
	expect(t, desktop.command(cr.ID, "approve"), http.StatusConflict, "not_pending")
	first.claim(cr.ClaimCode)
	ph := first.open("/scan/events")
	ph.next(t, "state")

	r := desktop.command(cr.ID, "reject")
	expect(t, r, http.StatusOK, "")
	again := decode[created](t, r.body)
	if again.ClaimCode == "" || again.ClaimCode == cr.ClaimCode || r.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("reject = %s", r.body)
	}
	ph.next(t, "rejected")
	ph.ends(t)
	expect(t, first.do(http.MethodGet, "/scan/session", nil), http.StatusUnauthorized, "scan_session_ended")
	expect(t, first.scan("k", "8935001234567"), http.StatusUnauthorized, "scan_session_ended")
	if got := desktop.status(cr.ID); got != (status{State: "waiting"}) {
		t.Fatalf("status after reject = %+v", got)
	}

	second := s.phone(st, iPhoneUA)
	expect(t, second.do(http.MethodPost, "/scan/claim", map[string]string{"code": cr.ClaimCode}), http.StatusGone, "claim_invalid")
	if got := second.claim(again.ClaimCode); got.State != "pending" {
		t.Fatalf("claim of the new code = %+v", got)
	}
	if got := desktop.status(cr.ID); got.State != "pending" || got.DeviceLabel != "iPhone · Safari" {
		t.Fatalf("status after the new claim = %+v", got)
	}
}

func TestRevokeFromEitherSide(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop := s.desktop(st)

	t.Run("desktop", func(t *testing.T) {
		phone := s.phone(st, iPhoneUA)
		id := pair(t, desktop, phone)
		desk, ph := desktop.open("/scanner-sessions/"+id.String()+"/events"), phone.open("/scan/events")
		desk.state(t)
		ph.next(t, "state")
		expect(t, desktop.do(http.MethodDelete, "/scanner-sessions/"+id.String(), nil), http.StatusNoContent, "")
		if by := decode[map[string]string](t, desk.next(t, "revoked"))["by"]; by != "desktop" {
			t.Errorf("desktop revoked by %q", by)
		}
		if by := decode[map[string]string](t, ph.next(t, "revoked"))["by"]; by != "desktop" {
			t.Errorf("phone revoked by %q", by)
		}
		desk.ends(t)
		ph.ends(t)
		expect(t, phone.do(http.MethodGet, "/scan/session", nil), http.StatusUnauthorized, "scan_session_ended")
		expect(t, phone.scan("k", "8935001234567"), http.StatusUnauthorized, "scan_session_ended")
		if got := desktop.status(id); got != (status{State: "ended"}) {
			t.Errorf("status after revoke = %+v", got)
		}
		if again := desktop.open("/scanner-sessions/" + id.String() + "/events"); again.code != http.StatusNoContent {
			t.Errorf("stream of an ended session = %d %s, want 204", again.code, again.body)
		}
		if again := phone.open("/scan/events"); again.code != http.StatusUnauthorized {
			t.Errorf("phone stream of a revoked session = %d %s, want 401", again.code, again.body)
		}
		expect(t, desktop.do(http.MethodDelete, "/scanner-sessions/"+id.String(), nil), http.StatusNoContent, "")
	})

	t.Run("phone", func(t *testing.T) {
		phone := s.phone(st, iPhoneUA)
		id := pair(t, desktop, phone)
		desk, ph := desktop.open("/scanner-sessions/"+id.String()+"/events"), phone.open("/scan/events")
		desk.state(t)
		ph.next(t, "state")
		cookie := phone.jar["tally_scan"]
		expect(t, phone.do(http.MethodDelete, "/scan/session", nil), http.StatusNoContent, "")
		if _, kept := phone.jar["tally_scan"]; kept {
			t.Error("the scanner cookie was not cleared")
		}
		if by := decode[map[string]string](t, desk.next(t, "revoked"))["by"]; by != "phone" {
			t.Errorf("desktop revoked by %q", by)
		}
		if by := decode[map[string]string](t, ph.next(t, "revoked"))["by"]; by != "phone" {
			t.Errorf("phone revoked by %q", by)
		}
		if got := desktop.status(id); got != (status{State: "ended"}) {
			t.Errorf("status after the phone left = %+v", got)
		}
		phone.jar["tally_scan"] = cookie
		expect(t, phone.do(http.MethodGet, "/scan/session", nil), http.StatusUnauthorized, "scan_session_ended")
		expect(t, phone.do(http.MethodDelete, "/scan/session", nil), http.StatusUnauthorized, "scan_session_ended")
	})
}

func TestSessionTimeouts(t *testing.T) {
	s := newStack(t, stackConfig{tick: time.Hour})
	st := s.newStore()
	desktop := s.desktop(st)

	t.Run("a phone pending for more than two minutes", func(t *testing.T) {
		phone := s.phone(st, iPhoneUA)
		cr := desktop.create()
		phone.claim(cr.ClaimCode)
		s.clk.Advance(119 * time.Second)
		s.setSeen(cr.ID)
		if got := desktop.status(cr.ID); got.State != "pending" {
			t.Fatalf("status before the approval timeout = %+v", got)
		}
		s.clk.Advance(2 * time.Second)
		s.setSeen(cr.ID)
		if got := desktop.status(cr.ID); got != (status{State: "ended"}) {
			t.Errorf("status after the approval timeout = %+v", got)
		}
		if got := phone.phoneStatus(); got != (phoneStatus{State: "ended"}) {
			t.Errorf("phone status after the approval timeout = %+v", got)
		}
		if ph := phone.open("/scan/events"); ph.code != http.StatusNoContent {
			t.Errorf("phone stream of an ended session = %d %s", ph.code, ph.body)
		}
		if desk := desktop.open("/scanner-sessions/" + cr.ID.String() + "/events"); desk.code != http.StatusNoContent {
			t.Errorf("desktop stream of an ended session = %d %s", desk.code, desk.body)
		}
		expect(t, desktop.command(cr.ID, "approve"), http.StatusConflict, "not_pending")
	})

	t.Run("a desktop silent for more than sixty seconds", func(t *testing.T) {
		phone := s.phone(st, iPhoneUA)
		id := pair(t, desktop, phone)
		s.clk.Advance(59 * time.Second)
		if got := phone.phoneStatus(); got.State != "claimed" {
			t.Fatalf("phone status within the grace = %+v", got)
		}
		s.clk.Advance(2 * time.Second)
		if got := desktop.status(id); got != (status{State: "ended"}) {
			t.Errorf("status after the desktop went silent = %+v", got)
		}
		expect(t, phone.scan("k", "8935001234567"), http.StatusUnauthorized, "scan_session_ended")
	})

	t.Run("twelve hours after the approval", func(t *testing.T) {
		phone := s.phone(st, iPhoneUA)
		id := pair(t, desktop, phone)
		s.clk.Advance(12*time.Hour - time.Second)
		s.setSeen(id)
		if got := phone.phoneStatus(); got.State != "claimed" {
			t.Fatalf("phone status just before twelve hours = %+v", got)
		}
		s.clk.Advance(2 * time.Second)
		s.setSeen(id)
		if got := desktop.status(id); got != (status{State: "ended"}) {
			t.Errorf("status after twelve hours = %+v", got)
		}
		if got := phone.phoneStatus(); got != (phoneStatus{State: "ended"}) {
			t.Errorf("phone status after twelve hours = %+v", got)
		}
	})

	t.Run("an expired claim code", func(t *testing.T) {
		cr := desktop.create()
		s.clk.Advance(2*time.Minute + time.Second)
		s.setSeen(cr.ID)
		phone := s.phone(st, iPhoneUA)
		expect(t, phone.do(http.MethodPost, "/scan/claim", map[string]string{"code": cr.ClaimCode}), http.StatusGone, "claim_invalid")
		// A fresh code of the same session works.
		r := desktop.command(cr.ID, "refresh-code")
		expect(t, r, http.StatusOK, "")
		phone.claim(decode[created](t, r.body).ClaimCode)
	})
}

func TestClaimCodeIsSingleUse(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop := s.desktop(st)

	t.Run("a used code", func(t *testing.T) {
		cr := desktop.create()
		s.phone(st, iPhoneUA).claim(cr.ClaimCode)
		other := s.phone(st, androidUA)
		expect(t, other.do(http.MethodPost, "/scan/claim", map[string]string{"code": cr.ClaimCode}), http.StatusGone, "claim_invalid")
		if _, ok := other.jar["tally_scan"]; ok {
			t.Error("a refused claim set the scanner cookie")
		}
		expect(t, desktop.command(cr.ID, "refresh-code"), http.StatusConflict, "already_claimed")
	})

	t.Run("an old code after a refresh", func(t *testing.T) {
		cr := desktop.create()
		r := desktop.command(cr.ID, "refresh-code")
		expect(t, r, http.StatusOK, "")
		fresh := decode[created](t, r.body)
		if fresh.ClaimCode == cr.ClaimCode || r.header.Get("Cache-Control") != "no-store" {
			t.Fatalf("refresh = %s", r.body)
		}
		phone := s.phone(st, iPhoneUA)
		expect(t, phone.do(http.MethodPost, "/scan/claim", map[string]string{"code": cr.ClaimCode}), http.StatusGone, "claim_invalid")
		phone.claim(fresh.ClaimCode)
	})

	t.Run("junk", func(t *testing.T) {
		phone := s.phone(st, iPhoneUA)
		for _, code := range []string{"", "   ", "not-a-code", strings.Repeat("A", 65)} {
			expect(t, phone.do(http.MethodPost, "/scan/claim", map[string]string{"code": code}), http.StatusGone, "claim_invalid")
		}
	})
}

func TestTenantIsolation(t *testing.T) {
	s := newStack(t, stackConfig{})
	a, b := s.newStore(), s.newStore()
	deskA, deskB := s.desktop(a), s.desktop(b)
	cr := deskA.create()

	onB := s.phone(b, iPhoneUA)
	expect(t, onB.do(http.MethodPost, "/scan/claim", map[string]string{"code": cr.ClaimCode}), http.StatusGone, "claim_invalid")

	onA := s.phone(a, iPhoneUA)
	onA.claim(cr.ClaimCode)
	expect(t, deskA.command(cr.ID, "approve"), http.StatusOK, "")

	onB.jar["tally_scan"] = onA.jar["tally_scan"]
	expect(t, onB.do(http.MethodGet, "/scan/session", nil), http.StatusUnauthorized, "scan_session_ended")
	expect(t, onB.scan("k", "8935001234567"), http.StatusUnauthorized, "scan_session_ended")
	if ph := onB.open("/scan/events"); ph.code != http.StatusUnauthorized {
		t.Errorf("stream with another store's cookie = %d %s", ph.code, ph.body)
	}
	expect(t, onB.do(http.MethodDelete, "/scan/session", nil), http.StatusUnauthorized, "scan_session_ended")
	expect(t, deskB.do(http.MethodGet, "/scanner-sessions/"+cr.ID.String(), nil), http.StatusNotFound, "not_found")
	expect(t, deskB.do(http.MethodDelete, "/scanner-sessions/"+cr.ID.String(), nil), http.StatusNotFound, "not_found")

	// The session of store A is untouched.
	if got := onA.phoneStatus(); got.State != "claimed" {
		t.Errorf("phone of store A = %+v", got)
	}
	// Store A's desktop cookie is no good on store B's host.
	stray := &client{t: t, s: s, host: b.host, jar: map[string]string{"tally_session": deskA.token}}
	expect(t, stray.do(http.MethodGet, "/scanner-sessions/"+cr.ID.String(), nil), http.StatusUnauthorized, "")
}

func TestOtherUsersSeeNoSession(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop := s.desktop(st)
	phone := s.phone(st, iPhoneUA)
	cr := desktop.create()
	phone.claim(cr.ClaimCode)
	id := cr.ID.String()

	for name, other := range map[string]*client{
		"another user":                    s.desktop(st),
		"the same user in another window": s.desktopOf(st, desktop.p.UserID),
	} {
		t.Run(name, func(t *testing.T) {
			expect(t, other.do(http.MethodGet, "/scanner-sessions/"+id, nil), http.StatusNotFound, "not_found")
			if ev := other.open("/scanner-sessions/" + id + "/events"); ev.code != http.StatusNotFound {
				t.Errorf("stream = %d %s, want 404", ev.code, ev.body)
			}
			expect(t, other.command(cr.ID, "approve"), http.StatusNotFound, "not_found")
			expect(t, other.command(cr.ID, "reject"), http.StatusNotFound, "not_found")
			expect(t, other.command(cr.ID, "refresh-code"), http.StatusNotFound, "not_found")
			expect(t, other.resolve(cr.ID, uuid.New(), map[string]any{"outcome": "not_found"}), http.StatusNotFound, "not_found")
			expect(t, other.do(http.MethodDelete, "/scanner-sessions/"+id, nil), http.StatusNotFound, "not_found")
		})
	}
	if got := desktop.status(cr.ID); got.State != "pending" {
		t.Errorf("the owner's session = %+v", got)
	}
}

func TestFourthSessionRevokesTheOldest(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop := s.desktop(st)
	first := desktop.create()
	desk := desktop.open("/scanner-sessions/" + first.ID.String() + "/events")
	desk.state(t)
	rest := []created{desktop.create(), desktop.create()}
	desk.quiet(t, 100*time.Millisecond)

	rest = append(rest, desktop.create())
	if by := decode[map[string]string](t, desk.next(t, "revoked"))["by"]; by != "system" {
		t.Errorf("oldest session revoked by %q", by)
	}
	if got := desktop.status(first.ID); got.State != "ended" {
		t.Errorf("oldest session = %+v", got)
	}
	for _, cr := range rest {
		if got := desktop.status(cr.ID); got.State != "waiting" {
			t.Errorf("session %s = %+v", cr.ID, got)
		}
	}
	// Another user of the store keeps their own three.
	if got := s.desktop(st).create(); got.ID == uuid.Nil {
		t.Error("another user could not open a session")
	}
	for _, cr := range rest {
		if got := desktop.status(cr.ID); got.State != "waiting" {
			t.Errorf("session %s after another user opened one = %+v", cr.ID, got)
		}
	}
}

func TestFourthSessionRevokesTheOldestWaitingOne(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop := s.desktop(st)
	paired := pair(t, desktop, s.phone(st, iPhoneUA))
	waiting := desktop.create()
	newer := desktop.create()
	desk := desktop.open("/scanner-sessions/" + waiting.ID.String() + "/events")
	desk.state(t)

	// The paired session is the oldest, yet the oldest one still showing its QR code makes room.
	desktop.create()
	if by := decode[map[string]string](t, desk.next(t, "revoked"))["by"]; by != "system" {
		t.Errorf("waiting session revoked by %q", by)
	}
	if got := desktop.status(waiting.ID); got.State != "ended" {
		t.Errorf("oldest waiting session = %+v", got)
	}
	if got := desktop.status(paired); got.State != "claimed" {
		t.Errorf("paired session = %+v", got)
	}
	if got := desktop.status(newer.ID); got.State != "waiting" {
		t.Errorf("newer waiting session = %+v", got)
	}
}

func TestFourthSessionRevokesTheOldestWhenPhonesClaimedThemAll(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop := s.desktop(st)
	ids := []uuid.UUID{pair(t, desktop, s.phone(st, iPhoneUA)), pair(t, desktop, s.phone(st, androidUA))}
	pending := desktop.create()
	s.phone(st, iPhoneUA).claim(pending.ClaimCode)

	desktop.create()
	if got := desktop.status(ids[0]); got.State != "ended" {
		t.Errorf("oldest session = %+v", got)
	}
	if got := desktop.status(ids[1]); got.State != "claimed" {
		t.Errorf("second session = %+v", got)
	}
	if got := desktop.status(pending.ID); got.State != "pending" {
		t.Errorf("pending session = %+v", got)
	}
}

func TestSignedOutDesktopEndsTheSession(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop, phone := s.desktop(st), s.phone(st, iPhoneUA)
	id := pair(t, desktop, phone)
	desk, ph := desktop.open("/scanner-sessions/"+id.String()+"/events"), phone.open("/scan/events")
	desk.state(t)
	ph.next(t, "state")

	s.sessions.revoke(desktop.token)
	if by := decode[map[string]string](t, desk.next(t, "revoked"))["by"]; by != "system" {
		t.Errorf("desktop revoked by %q", by)
	}
	desk.ends(t)
	ph.next(t, "ended")
	ph.ends(t)
	expect(t, phone.do(http.MethodGet, "/scan/session", nil), http.StatusUnauthorized, "scan_session_ended")
	var revokedBy string
	if err := testkit.SuperDB(t).Get(&revokedBy, `SELECT revoked_by FROM tally.scanner_sessions WHERE id = $1`, id); err != nil ||
		revokedBy != "system" {
		t.Errorf("revoked_by = %q %v", revokedBy, err)
	}
}

func TestSignOutAfterTheDesktopStreamClosedEndsThePhoneStream(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop, phone := s.desktop(st), s.phone(st, iPhoneUA)
	id := pair(t, desktop, phone)
	desk, ph := desktop.open("/scanner-sessions/"+id.String()+"/events"), phone.open("/scan/events")
	desk.state(t)
	ph.next(t, "state")

	// Signing out closes the page, and with it the desktop stream, before that stream's next check.
	desk.close()
	ph.quiet(t, 200*time.Millisecond)
	s.sessions.revoke(desktop.token)
	start := time.Now()
	ph.next(t, "ended")
	ph.ends(t)
	// The clock is fixed, so the desktop never goes silent for desktopGrace: the phone's own check
	// ended the session, within a few of its ticks.
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("phone told after %s", waited)
	}
	expect(t, phone.do(http.MethodGet, "/scan/session", nil), http.StatusUnauthorized, "scan_session_ended")
	var revokedBy string
	if err := testkit.SuperDB(t).Get(&revokedBy, `SELECT revoked_by FROM tally.scanner_sessions WHERE id = $1`, id); err != nil ||
		revokedBy != "system" {
		t.Errorf("revoked_by = %q %v", revokedBy, err)
	}
}

// The desktop and the phone streams can both notice a sign-out at once. The one that revoked the
// session tells the streams and closes them; the other must leave a stream alone, or it could cut
// it before the end event reaches it.
func TestLosingSignOutRevokeLeavesTheStreamsOpen(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop, phone := s.desktop(st), s.phone(st, iPhoneUA)
	key := scanner.Key{TenantID: st.id, SessionID: pair(t, desktop, phone)}
	ctx := context.Background()

	if err := s.svc.RevokeForSignOut(ctx, key); err != nil {
		t.Fatal(err)
	}
	// A stream the winner has not reached yet stands in for one still waiting for its end event.
	events, cancel, err := s.svc.Hub().Subscribe(key, scanner.RolePhone)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := s.svc.RevokeForSignOut(ctx, key); err != nil {
		t.Fatal(err)
	}
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("the losing revoke closed the stream")
		}
		t.Fatalf("the losing revoke sent %q", ev.Name)
	case <-time.After(100 * time.Millisecond):
	}
	if !s.svc.Hub().Publish(key, scanner.RolePhone, scanner.Event{Name: scanner.EventEnded}) {
		t.Fatal("the stream is gone after the losing revoke")
	}
	var revokedBy string
	if err := testkit.SuperDB(t).Get(&revokedBy, `SELECT revoked_by FROM tally.scanner_sessions WHERE id = $1`,
		key.SessionID); err != nil || revokedBy != "system" {
		t.Errorf("revoked_by = %q %v", revokedBy, err)
	}
}

func TestPhoneStreamOutlivesAReauthenticatedDesktop(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop, phone := s.desktop(st), s.phone(st, iPhoneUA)
	id := pair(t, desktop, phone)
	ph := phone.open("/scan/events")
	ph.next(t, "state")

	// A rotated cookie keeps the login session, so the phone stays paired.
	desktop.token = s.sessions.rotate(desktop.token)
	desktop.jar["tally_session"] = desktop.token
	ph.quiet(t, 300*time.Millisecond)
	if got := desktop.status(id); got.State != "claimed" {
		t.Fatalf("session after a re-authentication = %+v", got)
	}
}

func TestReauthenticatedDesktopKeepsTheSession(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop, phone := s.desktop(st), s.phone(st, iPhoneUA)
	id := pair(t, desktop, phone)
	desk := desktop.open("/scanner-sessions/" + id.String() + "/events")
	desk.state(t)

	// A re-authentication replaces the cookie the stream was opened with, not the login session.
	desktop.token = s.sessions.rotate(desktop.token)
	desktop.jar["tally_session"] = desktop.token
	desk.quiet(t, 300*time.Millisecond)
	if got := desktop.status(id); got.State != "claimed" {
		t.Fatalf("session after a re-authentication = %+v", got)
	}

	s.sessions.revoke(desktop.token)
	if by := decode[map[string]string](t, desk.next(t, "revoked"))["by"]; by != "system" {
		t.Errorf("desktop revoked by %q", by)
	}
	desk.ends(t)
}

func TestMaintenance(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop, phone := s.desktop(st), s.phone(st, iPhoneUA)
	id := pair(t, desktop, phone)
	pending := desktop.create()
	desk := desktop.open("/scanner-sessions/" + id.String() + "/events")
	desk.state(t)
	before := s.seenAt(id)

	s.held.hold(st.id, true)
	stranger := s.phone(st, androidUA)
	// Only a holder of a live code or of a phone cookie learns that the store is held.
	expect(t, stranger.do(http.MethodPost, "/scan/claim", map[string]string{"code": "not-a-code"}), http.StatusGone, "claim_invalid")
	expect(t, stranger.scan("k", "8935001234567"), http.StatusUnauthorized, "scan_session_ended")
	expect(t, stranger.do(http.MethodPost, "/scan/claim", map[string]string{"code": pending.ClaimCode}),
		http.StatusLocked, "tenant_maintenance")
	expect(t, phone.scan("k", "8935001234567"), http.StatusLocked, "tenant_maintenance")
	if got := phone.phoneStatus(); got.State != "claimed" {
		t.Errorf("phone status in maintenance = %+v", got)
	}

	// Past the refresh interval, the ticks of a held tenant still do not write.
	s.clk.Advance(40 * time.Second)
	time.Sleep(300 * time.Millisecond)
	if got := s.seenAt(id); !got.Equal(before) {
		t.Fatalf("desktop_seen_at moved to %v in maintenance, was %v", got, before)
	}
	desk.quiet(t, 0)

	s.held.hold(st.id, false)
	deadline := time.Now().Add(3 * time.Second)
	for !s.seenAt(id).Equal(s.clk.Now()) {
		if time.Now().After(deadline) {
			t.Fatalf("desktop_seen_at = %v after maintenance, want %v", s.seenAt(id), s.clk.Now())
		}
		time.Sleep(20 * time.Millisecond)
	}
	expect(t, phone.scan("k", "8935001234567"), http.StatusOK, "")
}

func TestPhoneLeavesAHeldOrReadOnlyStore(t *testing.T) {
	for name, block := range map[string]func(s *stack, st store, on bool){
		"maintenance": func(s *stack, st store, on bool) { s.held.hold(st.id, on) },
		"read-only":   func(s *stack, st store, on bool) { s.readOnly.set(st.id, on) },
	} {
		t.Run(name, func(t *testing.T) {
			s := newStack(t, stackConfig{})
			st := s.newStore()
			desktop, phone := s.desktop(st), s.phone(st, iPhoneUA)
			id := pair(t, desktop, phone)
			pending := desktop.create()
			ph := phone.open("/scan/events")
			ph.next(t, "state")

			block(s, st, true)
			stranger := s.phone(st, androidUA)
			expect(t, stranger.do(http.MethodPost, "/scan/claim", map[string]string{"code": "not-a-code"}), http.StatusGone, "claim_invalid")
			expect(t, stranger.scan("k", "8935001234567"), http.StatusUnauthorized, "scan_session_ended")
			if name == "read-only" {
				expect(t, stranger.do(http.MethodPost, "/scan/claim", map[string]string{"code": pending.ClaimCode}),
					http.StatusForbidden, "subscription_read_only")
				expect(t, phone.scan("k", "8935001234567"), http.StatusForbidden, "subscription_read_only")
			}

			r := phone.do(http.MethodDelete, "/scan/session", nil)
			expect(t, r, http.StatusNoContent, "")
			if _, kept := phone.jar["tally_scan"]; kept {
				t.Error("scanner cookie kept after leaving")
			}
			ph.next(t, "revoked")
			block(s, st, false)
			if got := desktop.status(id); got.State != "ended" {
				t.Errorf("session after the phone left = %+v", got)
			}
		})
	}
}

func TestConcurrentOpensKeepThreeSessions(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	userID := ids.New()
	const n = 8
	desktops := make([]*client, n)
	for i := range desktops {
		desktops[i] = s.desktopOf(st, userID)
	}
	codes := make(chan int, n)
	var wg sync.WaitGroup
	for _, d := range desktops {
		wg.Go(func() {
			res, err := s.http.Do(d.request(context.Background(), http.MethodPost, "/scanner-sessions", nil, nil))
			if err != nil {
				codes <- 0
				return
			}
			_ = res.Body.Close()
			codes <- res.StatusCode
		})
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusCreated {
			t.Errorf("concurrent open = %d, want 201", code)
		}
	}
	open := s.count(`SELECT count(*) FROM tally.scanner_sessions WHERE tenant_id = $1 AND created_by = $2 AND revoked_at IS NULL`,
		st.id, userID)
	if open != 3 {
		t.Fatalf("%d open sessions after %d concurrent opens, want 3", open, n)
	}
}

func TestAnswerAfterTheSessionEndedIsRefused(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop, phone := s.desktop(st), s.phone(st, iPhoneUA)
	id := pair(t, desktop, phone)
	desk := desktop.open("/scanner-sessions/" + id.String() + "/events")
	desk.state(t)
	done := make(chan response, 1)
	go func() { done <- phone.scan("k1", "8935001234567") }()
	ev := decode[struct {
		ScanID uuid.UUID `json:"scan_id"`
	}](t, desk.next(t, "scan"))

	expect(t, desktop.do(http.MethodDelete, "/scanner-sessions/"+id.String(), nil), http.StatusNoContent, "")
	if r := <-done; r.code != http.StatusOK || decode[scanResult](t, r.body).Outcome == "not_found" {
		t.Fatalf("scan of a revoked session = %d %s", r.code, r.body)
	}
	expect(t, desktop.resolve(id, ev.ScanID, map[string]any{"outcome": "not_found"}), http.StatusConflict, "scan_expired")
	if n := s.count(`SELECT count(*) FROM tally.scanner_scans WHERE id = $1 AND result IS NOT NULL`, ev.ScanID); n != 0 {
		t.Errorf("an answer was stored for a scan of an ended session")
	}
}

func TestScanIdempotency(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	skuID, unitID := s.newSku(st, "Trà xanh 500ml", "TRA-500")
	desktop, phone := s.desktop(st), s.phone(st, iPhoneUA)
	id := pair(t, desktop, phone)
	scanOf := func(key string) (uuid.UUID, int) {
		t.Helper()
		var ids []uuid.UUID
		if err := testkit.SuperDB(t).Select(&ids, `SELECT id FROM tally.scanner_scans WHERE session_id = $1 AND idempotency_key = $2`,
			id, key); err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			return uuid.Nil, 0
		}
		return ids[0], len(ids)
	}
	unresolved := func(key string) int {
		return s.count(`SELECT count(*) FROM tally.scanner_scans WHERE session_id = $1 AND idempotency_key = $2 AND result IS NULL`, id, key)
	}

	// Without a desktop stream the scan answers desktop_unavailable and stores no result.
	for range 2 {
		r := phone.scan("k1", "8935001234567")
		if got := decode[scanResult](t, r.body); r.code != http.StatusOK || got.Outcome != "desktop_unavailable" {
			t.Fatalf("scan without a desktop = %d %s", r.code, r.body)
		}
	}
	first, rows := scanOf("k1")
	if rows != 1 || unresolved("k1") != 1 {
		t.Fatalf("%d rows for one key, unresolved %d", rows, unresolved("k1"))
	}
	expect(t, phone.scan("k-late", "8935001234574"), http.StatusOK, "desktop_unavailable")
	late, _ := scanOf("k-late")

	desk := desktop.open("/scanner-sessions/" + id.String() + "/events")
	desk.state(t)
	done := make(chan response, 1)
	go func() { done <- phone.scan("k1", "8935001234567") }()
	ev := decode[struct {
		ScanID uuid.UUID `json:"scan_id"`
	}](t, desk.next(t, "scan"))
	if ev.ScanID != first {
		t.Fatalf("retry got scan %s, want the stored %s", ev.ScanID, first)
	}
	expect(t, desktop.resolve(id, first, map[string]any{"outcome": "added", "sku_id": skuID, "unit_id": unitID}), http.StatusOK, "")
	want := scanResult{Outcome: "added", ProductName: "Trà xanh 500ml", UnitName: "chai"}
	if r := <-done; r.code != http.StatusOK || decode[scanResult](t, r.body) != want {
		t.Fatalf("answered scan = %d %s", r.code, r.body)
	}

	// The same key replays the stored answer without asking the desktop again.
	r := phone.scan("k1", "8935001234567")
	if r.code != http.StatusOK || decode[scanResult](t, r.body) != want {
		t.Fatalf("replay = %d %s", r.code, r.body)
	}
	desk.quiet(t, 200*time.Millisecond)
	if _, rows := scanOf("k1"); rows != 1 {
		t.Fatalf("%d rows after the replay", rows)
	}
	expect(t, phone.scan("k1", "8935001234581"), http.StatusConflict, "idempotency_key_reused")

	// An answer after the scan's ten seconds is refused.
	s.clk.Advance(11 * time.Second)
	expect(t, desktop.resolve(id, late, map[string]any{"outcome": "not_found"}), http.StatusConflict, "scan_expired")
	expect(t, phone.doWith(http.MethodPost, "/scan/scans", map[string]string{"code": "8935001234567"}, nil),
		http.StatusUnprocessableEntity, "idempotency_key_required")
	expect(t, phone.scan("k-bad", "bánh"), http.StatusUnprocessableEntity, "invalid_format")
}

func TestScanCookieBehindStrayCookies(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop, phone := s.desktop(st), s.phone(st, iPhoneUA)
	pair(t, desktop, phone)
	genuine := phone.jar["tally_scan"]
	delete(phone.jar, "tally_scan")

	junk := func(n int) []*http.Cookie {
		var out []*http.Cookie
		for i := range n {
			out = append(out, &http.Cookie{Name: "tally_scan", Value: fmt.Sprintf("junk%d", i)})
		}
		return append(out, &http.Cookie{Name: "tally_scan", Value: genuine})
	}
	phone.extra = junk(1)
	if got := phone.phoneStatus(); got.State != "claimed" {
		t.Errorf("status behind a stray cookie = %+v", got)
	}
	phone.extra = junk(3)
	if got := phone.phoneStatus(); got.State != "claimed" {
		t.Errorf("status behind three stray cookies = %+v", got)
	}
	// At most four cookies are looked up.
	phone.extra = junk(4)
	expect(t, phone.do(http.MethodGet, "/scan/session", nil), http.StatusUnauthorized, "scan_session_ended")
}

func TestRateLimits(t *testing.T) {
	s := newStack(t, stackConfig{limits: &scanner.DefaultRateLimits})
	st := s.newStore()
	phone := s.phone(st, iPhoneUA)
	desktop := s.desktop(st)
	// limited sends burst requests that pass, then one more that must be refused with a Retry-After.
	limited := func(t *testing.T, burst int, send func() response) {
		t.Helper()
		for i := range burst {
			if r := send(); r.code == http.StatusTooManyRequests {
				t.Fatalf("request %d of %d limited: %s", i+1, burst, r.body)
			}
		}
		r := send()
		if r.code != http.StatusTooManyRequests || r.header.Get("Retry-After") == "" {
			t.Errorf("request beyond the burst = %d %s (Retry-After %q), want 429 with Retry-After",
				r.code, r.body, r.header.Get("Retry-After"))
		}
	}

	t.Run("claim", func(t *testing.T) {
		limited(t, 5, func() response { return phone.do(http.MethodPost, "/scan/claim", map[string]string{"code": "nope"}) })
	})
	t.Run("phone sign-outs have their own bucket", func(t *testing.T) {
		limited(t, 5, func() response { return phone.do(http.MethodDelete, "/scan/session", nil) })
	})
	t.Run("scans", func(t *testing.T) {
		limited(t, 10, func() response { return phone.scan("k", "8935001234567") })
	})
	id := desktop.create().ID.String()
	t.Run("phone stream opens", func(t *testing.T) {
		limited(t, 5, func() response { return s.rawOpen(phone, "/scan/events") })
	})
	t.Run("desktop stream opens have their own bucket", func(t *testing.T) {
		limited(t, 5, func() response { return s.rawOpen(desktop, "/scanner-sessions/"+id+"/events") })
	})
	t.Run("opening, refreshing, approving and rejecting share one bucket", func(t *testing.T) {
		// The create above took one of the six.
		cr := desktop.create()
		for range 3 {
			expect(t, desktop.command(cr.ID, "refresh-code"), http.StatusOK, "")
		}
		expect(t, desktop.command(cr.ID, "reject"), http.StatusConflict, "not_pending")
		r := desktop.command(cr.ID, "approve")
		if r.code != http.StatusTooManyRequests || r.header.Get("Retry-After") == "" {
			t.Errorf("seventh command = %d %s, want 429 with Retry-After", r.code, r.body)
		}
	})
}

// rawOpen sends one stream request and returns its status and headers without reading events.
func (s *stack) rawOpen(c *client, path string) response {
	s.t.Helper()
	res, err := s.http.Do(c.request(context.Background(), http.MethodGet, path, nil, nil))
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode == http.StatusOK {
		return response{code: res.StatusCode, header: res.Header}
	}
	raw, _ := io.ReadAll(res.Body)
	return response{code: res.StatusCode, body: raw, header: res.Header}
}

func TestEventStreamOutlivesTheServerTimeouts(t *testing.T) {
	s := newStack(t, stackConfig{writeTimeout: time.Second, ping: 200 * time.Millisecond})
	st := s.newStore()
	desktop := s.desktop(st)
	cr := desktop.create()
	desk := desktop.open("/scanner-sessions/" + cr.ID.String() + "/events")
	desk.state(t)
	desk.quiet(t, 3200*time.Millisecond)
	if n := desk.pings.Load(); n < 10 {
		t.Errorf("%d pings in 3.2s, want at least 10", n)
	}
	phone := s.phone(st, iPhoneUA)
	phone.claim(cr.ClaimCode)
	desk.next(t, "claim_requested")

	// A reopened stream starts with the state, here the pending phone.
	desk.close()
	again := desktop.open("/scanner-sessions/" + cr.ID.String() + "/events")
	if got := again.state(t); got.State != "pending" || got.VerifyCode == "" || got.DeviceLabel != "iPhone · Safari" {
		t.Errorf("first event of the reopened stream = %+v", got)
	}
	// So does the phone's, after the approval.
	expect(t, desktop.command(cr.ID, "approve"), http.StatusOK, "")
	ph := phone.open("/scan/events")
	if got := decode[phoneStatus](t, ph.next(t, "state")); got.State != "claimed" {
		t.Errorf("first phone event = %+v", got)
	}
	ph.quiet(t, 1500*time.Millisecond)
}

func TestServerRestartEndsTheStreams(t *testing.T) {
	s := newStack(t, stackConfig{})
	st := s.newStore()
	desktop, phone := s.desktop(st), s.phone(st, iPhoneUA)
	id := pair(t, desktop, phone)
	desk, ph := desktop.open("/scanner-sessions/"+id.String()+"/events"), phone.open("/scan/events")
	desk.state(t)
	ph.next(t, "state")

	s.svc.Hub().CloseAll()
	desk.next(t, "restarting")
	desk.ends(t)
	ph.next(t, "restarting")
	ph.ends(t)
	expect(t, s.rawOpen(desktop, "/scanner-sessions/"+id.String()+"/events"), http.StatusServiceUnavailable, "server_restarting")
	expect(t, phone.scan("k", "8935001234567"), http.StatusOK, "desktop_unavailable")
}

func TestPurgeDeletesOnlyLongEndedSessions(t *testing.T) {
	s := newStack(t, stackConfig{tick: time.Hour})
	st := s.newStore()
	desktop := s.desktop(st)

	revoked := desktop.create()
	expect(t, desktop.do(http.MethodDelete, "/scanner-sessions/"+revoked.ID.String(), nil), http.StatusNoContent, "")
	phone := s.phone(st, iPhoneUA)
	dead := pair(t, desktop, phone)
	expect(t, phone.scan("k", "8935001234567"), http.StatusOK, "desktop_unavailable")
	if n := s.count(`SELECT count(*) FROM tally.scanner_scans WHERE session_id = $1`, dead); n != 1 {
		t.Fatalf("%d scans of the dead session", n)
	}

	s.clk.Advance(25 * time.Hour)
	live := desktop.create()
	recent := desktop.create()
	expect(t, desktop.do(http.MethodDelete, "/scanner-sessions/"+recent.ID.String(), nil), http.StatusNoContent, "")

	n, err := s.svc.Purge(context.Background(), st.id)
	if err != nil || n != 2 {
		t.Fatalf("purge = %d %v, want 2", n, err)
	}
	var left []uuid.UUID
	if err := testkit.SuperDB(t).Select(&left, `SELECT id FROM tally.scanner_sessions WHERE tenant_id = $1 ORDER BY created_at, id`,
		st.id); err != nil {
		t.Fatal(err)
	}
	want := map[uuid.UUID]bool{live.ID: true, recent.ID: true}
	if len(left) != 2 || !want[left[0]] || !want[left[1]] {
		t.Errorf("sessions left %v, want %s and %s", left, live.ID, recent.ID)
	}
	if n := s.count(`SELECT count(*) FROM tally.scanner_scans WHERE tenant_id = $1`, st.id); n != 0 {
		t.Errorf("%d scans left", n)
	}
	if again, err := s.svc.Purge(context.Background(), st.id); err != nil || again != 0 {
		t.Errorf("second purge = %d %v", again, err)
	}
}
