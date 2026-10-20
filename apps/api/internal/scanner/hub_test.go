package scanner

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// counts returns how many streams and scan waiters the hub holds.
func (h *MemoryHub) counts() (streams, waiters int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.streams), len(h.waiters)
}

func newKey() Key { return Key{TenantID: uuid.New(), SessionID: uuid.New()} }

// recv reads one event or fails after a second.
func recv(t *testing.T, ch <-chan Event) (Event, bool) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		return ev, ok
	case <-time.After(time.Second):
		t.Fatal("no event within a second")
		return Event{}, false
	}
}

func mustSubscribe(t *testing.T, h Hub, key Key, role Role) (<-chan Event, func()) {
	t.Helper()
	ch, cancel, err := h.Subscribe(key, role)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return ch, cancel
}

func TestHubReplacesTheStreamOfASide(t *testing.T) {
	h, key := NewMemoryHub(), newKey()
	a, _ := mustSubscribe(t, h, key, RoleDesktop)
	b, _ := mustSubscribe(t, h, key, RoleDesktop)
	if ev, ok := recv(t, a); !ok || ev.Name != EventReplaced {
		t.Fatalf("old stream got %+v (open %v), want replaced", ev, ok)
	}
	if _, ok := recv(t, a); ok {
		t.Fatal("old stream still open after replaced")
	}
	if !h.Publish(key, RoleDesktop, Event{Name: EventScan}) {
		t.Fatal("publish to the new stream failed")
	}
	if ev, _ := recv(t, b); ev.Name != EventScan {
		t.Fatalf("new stream got %+v", ev)
	}
	// The phone side is a stream of its own.
	if h.Publish(key, RolePhone, Event{Name: EventApproved}) {
		t.Fatal("publish to a side without a stream succeeded")
	}
}

// A handler that exits late after a reload must not remove the stream that replaced it.
func TestHubLateCancelOfReplacedStreamKeepsTheNewOne(t *testing.T) {
	h, key := NewMemoryHub(), newKey()
	_, cancelA := mustSubscribe(t, h, key, RoleDesktop)
	b, cancelB := mustSubscribe(t, h, key, RoleDesktop)
	cancelA()
	if !h.Publish(key, RoleDesktop, Event{Name: EventScan}) {
		t.Fatal("the late cancel removed the new stream")
	}
	if ev, _ := recv(t, b); ev.Name != EventScan {
		t.Fatalf("new stream got %+v", ev)
	}
	cancelB()
	if streams, _ := h.counts(); streams != 0 {
		t.Fatalf("%d streams left after cancel", streams)
	}
	cancelB() // idempotent
}

func TestHubPublishNeverBlocksOnASlowReader(t *testing.T) {
	h, key := NewMemoryHub(), newKey()
	ch, _ := mustSubscribe(t, h, key, RoleDesktop)
	done := make(chan int)
	go func() {
		sent := 0
		for range 100 {
			if h.Publish(key, RoleDesktop, Event{Name: EventScan}) {
				sent++
			}
		}
		done <- sent
	}()
	select {
	case sent := <-done:
		if sent != streamBuffer {
			t.Fatalf("%d events queued, want the buffer of %d", sent, streamBuffer)
		}
	case <-time.After(time.Second):
		t.Fatal("publish blocked")
	}
	// The terminal event still fits after a full buffer.
	_, _ = mustSubscribe(t, h, key, RoleDesktop)
	var last Event
	for ev := range ch {
		last = ev
	}
	if last.Name != EventReplaced {
		t.Fatalf("last event %q, want replaced", last.Name)
	}
}

func TestHubAwaitDeliversAndForgetsWaiters(t *testing.T) {
	h, key, scanID := NewMemoryHub(), newKey(), uuid.New()
	res, cancel := h.Await(key, scanID)
	h.Resolve(scanID, ScanResult{Outcome: OutcomeAdded, ProductName: "Tea"})
	select {
	case r := <-res:
		if r.Outcome != OutcomeAdded || r.ProductName != "Tea" {
			t.Fatalf("result %+v", r)
		}
	case <-time.After(time.Second):
		t.Fatal("no result")
	}
	cancel()
	if _, waiters := h.counts(); waiters != 0 {
		t.Fatalf("%d waiters left", waiters)
	}
	// A late answer, after the waiter gave up, returns at once.
	done := make(chan struct{})
	go func() { h.Resolve(scanID, ScanResult{Outcome: OutcomeNotFound}); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("late resolve hung")
	}
}

func TestHubWaiterMapEmptiesAfterTimeoutAndCancel(t *testing.T) {
	h, key := NewMemoryHub(), newKey()
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, cancel := h.Await(key, uuid.New())
			defer cancel()
			timer := time.NewTimer(time.Duration(i%3) * time.Millisecond)
			defer timer.Stop()
			select {
			case <-res:
			case <-timer.C: // the wait timed out, or the request was cancelled
			}
		}()
	}
	wg.Wait()
	if _, waiters := h.counts(); waiters != 0 {
		t.Fatalf("%d waiters left after every wait ended", waiters)
	}
}

func TestHubCloseEndsStreamsAndWaitersOfOneSession(t *testing.T) {
	h, key, other := NewMemoryHub(), newKey(), newKey()
	desk, _ := mustSubscribe(t, h, key, RoleDesktop)
	phone, _ := mustSubscribe(t, h, key, RolePhone)
	keep, _ := mustSubscribe(t, h, other, RoleDesktop)
	res, cancel := h.Await(key, uuid.New())
	defer cancel()
	kept, cancelKept := h.Await(other, uuid.New())
	defer cancelKept()
	h.Close(key)
	for _, ch := range []<-chan Event{desk, phone} {
		if _, ok := recv(t, ch); ok {
			t.Fatal("stream of the closed session still open")
		}
	}
	if _, ok := <-res; ok {
		t.Fatal("waiter of the closed session got a result")
	}
	select {
	case <-kept:
		t.Fatal("waiter of another session closed")
	default:
	}
	if !h.Publish(other, RoleDesktop, Event{Name: EventScan}) {
		t.Fatal("stream of another session closed")
	}
	_ = keep
}

func TestHubCloseAllSendsRestartingAndRefusesNewStreams(t *testing.T) {
	h, key := NewMemoryHub(), newKey()
	ch, cancel := mustSubscribe(t, h, key, RolePhone)
	res, cancelWait := h.Await(key, uuid.New())
	h.CloseAll()
	if ev, ok := recv(t, ch); !ok || ev.Name != EventRestarting {
		t.Fatalf("got %+v (open %v), want restarting", ev, ok)
	}
	if _, ok := recv(t, ch); ok {
		t.Fatal("stream open after restarting")
	}
	if _, ok := <-res; ok {
		t.Fatal("waiter got a result after CloseAll")
	}
	cancel()
	cancelWait()
	if _, _, err := h.Subscribe(key, RoleDesktop); !errors.Is(err, ErrHubClosed) {
		t.Fatalf("subscribe after CloseAll: %v, want ErrHubClosed", err)
	}
	late, cancelLate := h.Await(key, uuid.New())
	defer cancelLate()
	if _, ok := <-late; ok {
		t.Fatal("await after CloseAll did not return a closed channel")
	}
}

func TestHubStreamLimits(t *testing.T) {
	h := newMemoryHub(2, 3)
	tenant := uuid.New()
	k1, k2, k3 := Key{tenant, uuid.New()}, Key{tenant, uuid.New()}, Key{tenant, uuid.New()}
	_, cancel1 := mustSubscribe(t, h, k1, RoleDesktop)
	mustSubscribe(t, h, k2, RoleDesktop)
	if _, _, err := h.Subscribe(k3, RoleDesktop); !errors.Is(err, ErrStreamLimit) {
		t.Fatalf("third stream of a tenant: %v, want ErrStreamLimit", err)
	}
	// Replacing a stream does not count twice.
	mustSubscribe(t, h, k1, RoleDesktop)
	mustSubscribe(t, h, newKey(), RoleDesktop)
	if _, _, err := h.Subscribe(newKey(), RoleDesktop); !errors.Is(err, ErrStreamLimit) {
		t.Fatalf("stream beyond the total: %v, want ErrStreamLimit", err)
	}
	cancel1() // the replaced stream's cancel frees nothing
	if _, _, err := h.Subscribe(k3, RoleDesktop); !errors.Is(err, ErrStreamLimit) {
		t.Fatalf("after a stale cancel: %v, want ErrStreamLimit", err)
	}
	h.Close(k1)
	mustSubscribe(t, h, k3, RoleDesktop)
}

func TestHubIsSafeForConcurrentUse(t *testing.T) {
	h := NewMemoryHub()
	keys := []Key{newKey(), newKey(), newKey()}
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := keys[i%len(keys)]
			role := []Role{RoleDesktop, RolePhone}[i%2]
			ch, cancel, err := h.Subscribe(key, role)
			if err != nil {
				return
			}
			defer cancel()
			scanID := uuid.New()
			res, cancelWait := h.Await(key, scanID)
			defer cancelWait()
			h.Publish(key, role, Event{Name: EventScan})
			h.Resolve(scanID, ScanResult{Outcome: OutcomeAdded})
			<-res
			if i%7 == 0 {
				h.Close(key)
			}
			for range len(ch) {
				<-ch
			}
		}()
	}
	wg.Wait()
	h.CloseAll()
	if streams, waiters := h.counts(); streams != 0 || waiters != 0 {
		t.Fatalf("%d streams and %d waiters left", streams, waiters)
	}
}
