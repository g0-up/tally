//go:build integration

package app_test

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/identity"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/middleware"
	"github.com/g0-up/tally/apps/api/internal/scanner"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// scannerStack is the identity and scanner modules behind a real listener, so the desktop's
// event stream runs its periodic checks against real login sessions.
type scannerStack struct {
	stack
	clk *clock.Fixed
	url string
}

func newScannerStack(t *testing.T) scannerStack {
	t.Helper()
	clk := testkit.Clock()
	capture := &captureMailer{}
	deps := identityDeps(t, capture, clk)
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
	domains := app.NewDomains(deps.DB, clk, deps.Audit)
	a, err := app.New(app.Options{
		PublicBaseURL: testBaseURL,
		Logger:        slog.New(slog.DiscardHandler),
		Ports:         app.Ports{Sessions: idSvc, TenantHosts: idSvc, LoginSessions: idSvc},
		CSRFKey:       []byte("0123456789abcdef0123456789abcdef"),
		Now:           clk.Now,
		APIRateLimit:  testAPIRateLimit,
		Modules: app.Modules{
			Identity: identity.NewHandler(idSvc),
			Scanner: scanner.NewHandler(domains.Scanner, scanner.WithRateLimits(testScannerRateLimits),
				scanner.WithStreamTiming(200*time.Millisecond, 50*time.Millisecond)),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts = a.TenantHostCache()
	srv := httptest.NewServer(a.Handler())
	// Cleanups run last-in first-out: the hub ends every stream before the server waits for them.
	t.Cleanup(srv.Close)
	t.Cleanup(domains.Scanner.Hub().CloseAll)
	return scannerStack{stack: stack{handler: a.Handler(), identity: idSvc, mail: capture}, clk: clk, url: srv.URL}
}

type streamEvent struct {
	name string
	data json.RawMessage
}

// openStream opens an event stream of b over the listener and parses its events until it ends.
func (s scannerStack) openStream(t *testing.T, b *browser, path string) <-chan streamEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url+path, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.Host = b.host
	req.Header.Set("Accept", "text/event-stream")
	for _, c := range b.cookies {
		req.AddCookie(c)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		_ = res.Body.Close()
		cancel()
		t.Fatalf("open %s = %d", path, res.StatusCode)
	}
	out := make(chan streamEvent, 64)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(out)
		defer func() { _ = res.Body.Close() }()
		r := bufio.NewReader(res.Body)
		var ev streamEvent
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "" && ev.name != "":
				out <- ev
				ev = streamEvent{}
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = json.RawMessage(strings.TrimPrefix(line, "data: "))
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return out
}

func nextEvent(t *testing.T, events <-chan streamEvent, name string) json.RawMessage {
	t.Helper()
	select {
	case ev, ok := <-events:
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

func quietStream(t *testing.T, events <-chan streamEvent, d time.Duration) {
	t.Helper()
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("stream ended")
		}
		t.Fatalf("unexpected event %s %s", ev.name, ev.data)
	case <-time.After(d):
	}
}

// The desktop's stream follows its login session by id: a re-authentication, which replaces the
// session cookie, keeps the scanner session, the stream's checks never count as use of the login
// session, and signing out ends the scanner session.
func TestScannerStreamFollowsTheLoginSession(t *testing.T) {
	s := newScannerStack(t)
	o := s.signUp(t)
	desktop := s.signIn(t, o)
	created := testkit.DecodeJSON[struct {
		ID        uuid.UUID `json:"id"`
		ClaimCode string    `json:"claim_code"`
	}](t, desktop.do(http.MethodPost, v1+"/scanner-sessions", nil))
	sessionPath := v1 + "/scanner-sessions/" + created.ID.String()
	phone := s.browser(t, o.host)
	phone.hostContext()
	expect(t, phone.do(http.MethodPost, v1+"/scan/claim", map[string]string{"code": created.ClaimCode}), http.StatusOK, "pending")
	expect(t, desktop.do(http.MethodPost, sessionPath+":approve", nil), http.StatusOK, "claimed")
	events := s.openStream(t, desktop, sessionPath+"/events")
	nextEvent(t, events, "state")

	oldCookie := desktop.cookies["tally_session"].Value
	expect(t, desktop.do(http.MethodPost, v1+"/auth/reauth", map[string]string{"password": password}), http.StatusOK, "")
	if desktop.cookies["tally_session"].Value == oldCookie {
		t.Fatal("the re-authentication kept the session cookie; the test needs a rotated one")
	}
	quietStream(t, events, 400*time.Millisecond)
	expect(t, desktop.do(http.MethodGet, sessionPath, nil), http.StatusOK, "claimed")
	expect(t, phone.do(http.MethodGet, v1+"/scan/session", nil), http.StatusOK, "claimed")

	// Past the minute after which a request moves last_seen_at, the stream's checks still leave it
	// alone while they keep the desktop of the scanner session alive.
	super := testkit.SuperDB(t)
	seen := func() (login, desk time.Time) {
		t.Helper()
		if err := super.QueryRow(`
			SELECT s.last_seen_at, sc.desktop_seen_at
			FROM tally.scanner_sessions sc JOIN tally.sessions s ON s.id = sc.login_session_id
			WHERE sc.id = $1`, created.ID).Scan(&login, &desk); err != nil {
			t.Fatal(err)
		}
		return login, desk
	}
	loginBefore, deskBefore := seen()
	for range 3 {
		s.clk.Advance(25 * time.Second)
		time.Sleep(300 * time.Millisecond)
	}
	loginAfter, deskAfter := seen()
	if !loginAfter.Equal(loginBefore) {
		t.Errorf("last_seen_at moved from %v to %v without a request of the desktop", loginBefore, loginAfter)
	}
	if !deskAfter.After(deskBefore) {
		t.Errorf("desktop_seen_at stayed at %v while the stream was open", deskAfter)
	}
	quietStream(t, events, 0)

	expect(t, desktop.do(http.MethodPost, v1+"/auth/logout", nil), http.StatusOK, "")
	var revoked struct {
		By string `json:"by"`
	}
	if err := json.Unmarshal(nextEvent(t, events, "revoked"), &revoked); err != nil || revoked.By != "system" {
		t.Errorf("scanner session revoked by %q after sign-out (%v)", revoked.By, err)
	}
	expect(t, phone.do(http.MethodGet, v1+"/scan/session", nil), http.StatusUnauthorized, "scan_session_ended")
}
