//go:build integration

package scanner_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/scanner"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// anyCode accepts every code and knows no SKU; the tests below only answer not_found.
type anyCode struct{}

func (anyCode) ValidateCode(string) error { return nil }

func (anyCode) ItemNames(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (string, string, error) {
	return "", "", scanner.ErrUnknownSKU
}

// hookedHub is a MemoryHub whose Await can run a hook first and whose Resolve can drop delivery,
// to place the desktop's answer where the phone has no waiter registered.
type hookedHub struct {
	*scanner.MemoryHub
	mu           sync.Mutex
	beforeAwait  func(scanID uuid.UUID)
	dropResolves bool
}

func (h *hookedHub) Await(key scanner.Key, scanID uuid.UUID) (<-chan scanner.ScanResult, func()) {
	h.mu.Lock()
	hook := h.beforeAwait
	h.beforeAwait = nil
	h.mu.Unlock()
	if hook != nil {
		hook(scanID)
	}
	return h.MemoryHub.Await(key, scanID)
}

func (h *hookedHub) Resolve(scanID uuid.UUID, r scanner.ScanResult) {
	h.mu.Lock()
	drop := h.dropResolves
	h.mu.Unlock()
	if !drop {
		h.MemoryHub.Resolve(scanID, r)
	}
}

// pairedService is an approved session of a fresh tenant on a service built around hub.
type pairedService struct {
	svc     *scanner.Service
	owner   scanner.Owner
	id      uuid.UUID
	secrets []string
}

func newPairedService(t *testing.T, hub scanner.Hub) pairedService {
	t.Helper()
	ctx := context.Background()
	d := testkit.DB(t)
	svc := scanner.NewService(scanner.Deps{DB: d, Clock: testkit.Clock(), Catalog: anyCode{}, Hub: hub})
	t.Cleanup(hub.CloseAll)
	tn := testkit.NewTenant(t, d)
	o := scanner.Owner{TenantID: tn.ID, UserID: ids.New(), LoginSessionID: ids.New()}
	cr, err := svc.Create(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := svc.Claim(ctx, tn.ID, cr.Code, iPhoneUA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Approve(ctx, o, cr.ID); err != nil {
		t.Fatal(err)
	}
	return pairedService{svc: svc, owner: o, id: cr.ID, secrets: []string{cl.Secret}}
}

func scanKey(t *testing.T, value, code string) idempotency.Key {
	t.Helper()
	h, err := idempotency.RequestHash("POST", "/api/v1/scan/scans", []byte(`{"code":"`+code+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	return idempotency.Key{Value: value, RequestHash: h}
}

// openDesktop subscribes the desktop side of the session and returns its events.
func (p pairedService) openDesktop(t *testing.T) <-chan scanner.Event {
	t.Helper()
	events, cancel, err := p.svc.Hub().Subscribe(scanner.Key{TenantID: p.owner.TenantID, SessionID: p.id}, scanner.RoleDesktop)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	return events
}

func TestRetryGetsAnAnswerStoredBeforeItsWaiter(t *testing.T) {
	ctx := context.Background()
	hub := &hookedHub{MemoryHub: scanner.NewMemoryHub()}
	p := newPairedService(t, hub)
	key := scanKey(t, "k1", "8935001234567")

	// Without a desktop stream the first attempt stores an unanswered scan.
	first, err := p.svc.Scan(ctx, p.owner.TenantID, p.secrets, key, "8935001234567")
	if err != nil || first.Outcome != scanner.OutcomeDesktopUnavailable {
		t.Fatalf("first attempt = %+v %v", first, err)
	}
	// The desktop answers the stored scan after the retry committed but before it waits.
	var resolveErr error
	hub.beforeAwait = func(scanID uuid.UUID) {
		_, resolveErr = p.svc.Resolve(ctx, p.owner, p.id, scanID, scanner.ResultInput{Outcome: scanner.OutcomeNotFound})
	}
	p.openDesktop(t)
	start := time.Now()
	got, err := p.svc.Scan(ctx, p.owner.TenantID, p.secrets, key, "8935001234567")
	if resolveErr != nil {
		t.Fatalf("resolve: %v", resolveErr)
	}
	if err != nil || got.Outcome != scanner.OutcomeNotFound {
		t.Fatalf("retry = %+v %v, want the stored not_found", got, err)
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Errorf("retry waited %v for an answer already stored", waited)
	}
}

func TestTimedOutScanReturnsAnAnswerStoredMeanwhile(t *testing.T) {
	ctx := context.Background()
	hub := &hookedHub{MemoryHub: scanner.NewMemoryHub(), dropResolves: true}
	p := newPairedService(t, hub)
	desk := p.openDesktop(t)

	done := make(chan scanner.ScanResult, 1)
	errs := make(chan error, 1)
	key := scanKey(t, "k1", "8935001234567")
	go func() {
		r, err := p.svc.Scan(ctx, p.owner.TenantID, p.secrets, key, "8935001234567")
		done <- r
		errs <- err
	}()
	var ev scanner.Event
	select {
	case ev = <-desk:
	case <-time.After(5 * time.Second):
		t.Fatal("no scan event within 5s")
	}
	raw, err := json.Marshal(ev.Data)
	if err != nil {
		t.Fatal(err)
	}
	scanID := decode[struct {
		ScanID uuid.UUID `json:"scan_id"`
	}](t, raw).ScanID
	// The answer is stored but never reaches the waiter, as when it lands while no waiter is
	// registered; the phone reads it back once its wait runs out.
	if _, err := p.svc.Resolve(ctx, p.owner, p.id, scanID, scanner.ResultInput{Outcome: scanner.OutcomeNotFound}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	select {
	case r := <-done:
		if err := <-errs; err != nil || r.Outcome != scanner.OutcomeNotFound {
			t.Fatalf("scan = %+v %v, want the stored not_found", r, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("scan still waiting after 10s")
	}
}
