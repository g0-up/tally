package scanner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
)

func init() { gin.SetMode(gin.TestMode) }

type noSessions struct{}

func (noSessions) SessionActive(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error) {
	return false, nil
}

type held bool

func (h held) InMaintenance(context.Context, uuid.UUID) (bool, error) { return bool(h), nil }

type writes bool

func (w writes) CanWrite(context.Context, uuid.UUID) (bool, error) { return bool(w), nil }

// testEngine registers h on a bare engine: both groups under /api/v1, no host or session chain.
func testEngine(h *Handler, secure bool) *gin.Engine {
	e := gin.New()
	api := e.Group("/api/v1")
	h.Register(Routes{
		Tenant: api, Public: api,
		Commands:    func(g *gin.RouterGroup, r string) *httpx.CommandRouter { return httpx.NewCommandRouter(g, r, nil) },
		Sessions:    noSessions{},
		Maintenance: held(false), Entitlements: writes(true), Cookies: middleware.CookieNames(secure),
	})
	return e
}

func TestRegisterWithoutAServiceServesEveryRoute(t *testing.T) {
	e := testEngine(NewHandler(nil), true)
	var got []string
	for _, r := range e.Routes() {
		got = append(got, r.Method+" "+r.Path)
	}
	for _, want := range []string{
		"POST /api/v1/scanner-sessions", "GET /api/v1/scanner-sessions/:id", "DELETE /api/v1/scanner-sessions/:id",
		"POST /api/v1/scanner-sessions/:id", "GET /api/v1/scanner-sessions/:id/events",
		"POST /api/v1/scanner-sessions/:id/scans/:scanId/result",
		"POST /api/v1/scan/claim", "GET /api/v1/scan/session", "DELETE /api/v1/scan/session",
		"GET /api/v1/scan/events", "POST /api/v1/scan/scans",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("route %s not registered (got %v)", want, got)
		}
	}
}

func TestRegisterNeedsEveryCollaborator(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Register without a scanner cookie name did not panic")
		}
	}()
	e := gin.New()
	NewHandler(nil).Register(Routes{
		Tenant: e.Group("/a"), Public: e.Group("/b"),
		Commands:    func(g *gin.RouterGroup, r string) *httpx.CommandRouter { return httpx.NewCommandRouter(g, r, nil) },
		Sessions:    noSessions{},
		Maintenance: held(false), Entitlements: writes(true),
	})
}

func serve(e http.Handler, method, path string, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:1234"
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body httpx.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func TestPhoneRoutesOutsideATenantHost(t *testing.T) {
	e := testEngine(NewHandler(nil), false)
	if rec := serve(e, http.MethodPost, "/api/v1/scan/claim", `{"code":"X"}`); rec.Code != http.StatusGone ||
		errorCode(t, rec) != CodeClaimInvalid {
		t.Errorf("claim: %d %s", rec.Code, rec.Body)
	}
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/scan/session"}, {http.MethodGet, "/api/v1/scan/events"},
		{http.MethodPost, "/api/v1/scan/scans"}, {http.MethodDelete, "/api/v1/scan/session"},
	} {
		if rec := serve(e, r.method, r.path, `{"code":"X"}`); rec.Code != http.StatusUnauthorized || errorCode(t, rec) != CodeSessionEnded {
			t.Errorf("%s %s: %d %s", r.method, r.path, rec.Code, rec.Body)
		}
	}
	// Desktop routes need a signed-in user.
	if rec := serve(e, http.MethodPost, "/api/v1/scanner-sessions", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("create without a session: %d", rec.Code)
	}
}

func TestRateLimitsAnswer429WithRetryAfter(t *testing.T) {
	tight := RateLimit{PerSecond: 0.001, Burst: 2}
	e := testEngine(NewHandler(nil, WithRateLimits(RateLimits{Claim: tight, Scans: tight, Events: tight, Commands: tight})), false)
	for _, r := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/scan/claim"}, {http.MethodDelete, "/api/v1/scan/session"}, {http.MethodPost, "/api/v1/scan/scans"},
		{http.MethodGet, "/api/v1/scan/events"}, {http.MethodGet, "/api/v1/scanner-sessions/" + uuid.NewString() + "/events"},
		{http.MethodPost, "/api/v1/scanner-sessions"},
	} {
		for i := range 3 {
			rec := serve(e, r.method, r.path, `{"code":"X"}`)
			if i < 2 && rec.Code == http.StatusTooManyRequests {
				t.Errorf("%s %s: limited after %d requests", r.method, r.path, i)
			}
			if i == 2 && (rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "") {
				t.Errorf("%s %s: third request %d (Retry-After %q), want 429", r.method, r.path, rec.Code, rec.Header().Get("Retry-After"))
			}
		}
	}
	// Opening, refreshing, approving and rejecting share one bucket, which opening used up.
	for _, verb := range []string{"refresh-code", "approve", "reject"} {
		if rec := serve(e, http.MethodPost, "/api/v1/scanner-sessions/"+uuid.NewString()+":"+verb, ""); rec.Code != http.StatusTooManyRequests {
			t.Errorf("%s after the bucket ran out: %d, want 429", verb, rec.Code)
		}
	}
}

func TestScanCookieAttributes(t *testing.T) {
	for _, secure := range []bool{true, false} {
		h := NewHandler(nil)
		h.cookies = middleware.CookieNames(secure)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		h.setScanCookie(c, "secret", int(sessionTTL/time.Second))
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("cookies %v", cookies)
		}
		ck := cookies[0]
		wantName := map[bool]string{true: "__Host-tally_scan", false: "tally_scan"}[secure]
		if ck.Name != wantName || ck.Value != "secret" || ck.Path != "/" || ck.Domain != "" || ck.MaxAge != 43200 ||
			!ck.HttpOnly || ck.Secure != secure || ck.SameSite != http.SameSiteStrictMode {
			t.Errorf("secure=%v cookie %+v", secure, ck)
		}
	}
}

func TestSecretsReadsEveryScanCookie(t *testing.T) {
	h := NewHandler(nil)
	h.cookies = middleware.CookieNames(false)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Cookie", "tally_scan=junk; tally_session=s; tally_scan=real")
	if got := h.secrets(c); !slices.Equal(got, []string{"junk", "real"}) {
		t.Fatalf("secrets %v", got)
	}
}

func TestWritableGatesPhoneWrites(t *testing.T) {
	for name, tc := range map[string]struct {
		h      *Handler
		status int
		code   string
	}{
		"maintenance": {&Handler{maintenance: held(true), entitlements: writes(true)}, http.StatusLocked, "tenant_maintenance"},
		"read-only":   {&Handler{maintenance: held(false), entitlements: writes(false)}, http.StatusForbidden, "subscription_read_only"},
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		if tc.h.writable(c, uuid.New()) || rec.Code != tc.status || errorCode(t, rec) != tc.code {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	if !(&Handler{maintenance: held(false), entitlements: writes(true)}).writable(c, uuid.New()) {
		t.Error("an open tenant may not write")
	}
}

func TestHubErrorsAnswer503(t *testing.T) {
	for err, code := range map[error]string{ErrStreamLimit: CodeStreamLimit, ErrHubClosed: CodeServerRestarting} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		renderHubError(c, err)
		if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != code {
			t.Errorf("%v: %d %s", err, rec.Code, rec.Body)
		}
	}
}

// sseEvent is one parsed server-sent event; comment lines are reported as name ":".
type sseEvent struct{ name, data string }

// readEvents parses the stream until it ends and sends every event and comment.
func readEvents(r *bufio.Reader, out chan<- sseEvent) {
	defer close(out)
	var ev sseEvent
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(line, ":"):
			out <- sseEvent{name: ":"}
		case strings.HasPrefix(line, "event: "):
			ev.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.data = strings.TrimPrefix(line, "data: ")
		case line == "" && ev.name != "":
			out <- ev
			ev = sseEvent{}
		}
	}
}

// pumpServer serves one stream driven by events and check, on a server whose WriteTimeout is
// one second, and returns the parsed events.
func pumpServer(t *testing.T, events <-chan Event, timing streamTiming, check func(context.Context) tickResult) (<-chan sseEvent, *http.Response) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := startSSE(w)
		if err != nil {
			return
		}
		pump(r.Context(), s, Event{Name: EventState, Data: PhoneStatus{State: StatePending}}, events, phoneTerminal, timing, check)
	}))
	srv.Config.WriteTimeout = time.Second
	srv.Config.ReadTimeout = time.Second
	srv.Start()
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL) //nolint:noctx // closed by the test
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	out := make(chan sseEvent, 64)
	go readEvents(bufio.NewReader(res.Body), out)
	return out, res
}

func next(t *testing.T, ch <-chan sseEvent, skipPings bool) (sseEvent, bool) {
	t.Helper()
	for {
		select {
		case ev, ok := <-ch:
			if ok && skipPings && ev.name == ":" {
				continue
			}
			return ev, ok
		case <-time.After(5 * time.Second):
			t.Fatal("no event within 5 seconds")
			return sseEvent{}, false
		}
	}
}

func TestStreamOutlivesTheServerWriteTimeout(t *testing.T) {
	events := make(chan Event, 1)
	out, res := pumpServer(t, events, streamTiming{ping: 200 * time.Millisecond, tick: time.Hour}, nil)
	if res.Header.Get("Content-Type") != "text/event-stream" || !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("headers %v", res.Header)
	}
	if ev, _ := next(t, out, false); ev.name != EventState || ev.data != `{"state":"pending"}` {
		t.Fatalf("first event %+v, want state", ev)
	}
	deadline := time.Now().Add(3200 * time.Millisecond)
	pings := 0
	for time.Now().Before(deadline) {
		ev, ok := next(t, out, false)
		if !ok {
			t.Fatalf("stream ended after %d pings", pings)
		}
		if ev.name == ":" {
			pings++
		}
	}
	events <- Event{Name: EventApproved, Data: struct{}{}}
	if ev, _ := next(t, out, true); ev.name != EventApproved || ev.data != "{}" {
		t.Fatalf("event after 3 s %+v", ev)
	}
}

func TestStreamEndsOnATerminalEvent(t *testing.T) {
	events := make(chan Event, 2)
	out, _ := pumpServer(t, events, streamTiming{ping: time.Hour, tick: time.Hour}, nil)
	next(t, out, true)
	events <- Event{Name: EventRejected, Data: struct{}{}}
	if ev, _ := next(t, out, true); ev.name != EventRejected {
		t.Fatalf("got %+v", ev)
	}
	if _, ok := next(t, out, true); ok {
		t.Fatal("stream open after a terminal event")
	}
}

func TestStreamWritesWhatTheCheckDecides(t *testing.T) {
	var calls atomic.Int32
	out, _ := pumpServer(t, make(chan Event), streamTiming{ping: time.Hour, tick: 20 * time.Millisecond},
		func(context.Context) tickResult {
			if calls.Add(1) < 3 {
				return tickResult{}
			}
			return tickResult{events: []Event{{Name: EventEnded, Data: struct{}{}}}, end: true}
		})
	next(t, out, true)
	if ev, _ := next(t, out, true); ev.name != EventEnded {
		t.Fatalf("got %+v", ev)
	}
	if _, ok := next(t, out, true); ok {
		t.Fatal("stream open after the check ended it")
	}
}

func TestStreamClosesAfterRepeatedCheckFailures(t *testing.T) {
	var calls atomic.Int32
	out, _ := pumpServer(t, make(chan Event), streamTiming{ping: time.Hour, tick: 20 * time.Millisecond},
		func(context.Context) tickResult {
			calls.Add(1)
			return tickResult{err: errors.New("database down")}
		})
	next(t, out, true)
	if _, ok := next(t, out, true); ok {
		t.Fatal("stream wrote an event after failed checks")
	}
	if n := calls.Load(); n != maxTickFailures {
		t.Fatalf("%d checks before closing, want %d", n, maxTickFailures)
	}
}

// A slow check must not stall the stream: pings and events keep flowing and ticks are skipped.
func TestSlowCheckDoesNotBlockTheStream(t *testing.T) {
	var running, overlapped atomic.Int32
	events := make(chan Event, 1)
	out, _ := pumpServer(t, events, streamTiming{ping: 50 * time.Millisecond, tick: 10 * time.Millisecond},
		func(ctx context.Context) tickResult {
			if running.Add(1) > 1 {
				overlapped.Store(1)
			}
			defer running.Add(-1)
			<-ctx.Done() // runs until its timeout
			return tickResult{}
		})
	next(t, out, true)
	if ev, _ := next(t, out, false); ev.name != ":" {
		t.Fatalf("got %+v, want a ping while the check runs", ev)
	}
	events <- Event{Name: EventApproved, Data: struct{}{}}
	if ev, _ := next(t, out, true); ev.name != EventApproved {
		t.Fatalf("got %+v", ev)
	}
	if overlapped.Load() != 0 {
		t.Fatal("two checks ran at once")
	}
}
