package posting

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/metrics"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// fakeDB runs fn without a database; failures queues errors returned instead of running fn.
type fakeDB struct {
	calls    int
	failures []error
}

func (f *fakeDB) WithTenantTx(_ context.Context, _ uuid.UUID, fn func(q db.Querier) error) error {
	f.calls++
	if len(f.failures) > 0 {
		err := f.failures[0]
		f.failures = f.failures[1:]
		return err
	}
	return fn(nil)
}

type fakeIdem struct {
	replayed    *idempotency.Stored
	replayErr   error
	stored      *idempotency.Stored
	beginErr    error
	completeErr error
	completed   []int
	bodies      []string
}

func (f *fakeIdem) Replay(context.Context, db.Querier, uuid.UUID, string, idempotency.Key) (*idempotency.Stored, error) {
	return f.replayed, f.replayErr
}

func (f *fakeIdem) Begin(context.Context, db.Querier, uuid.UUID, string, idempotency.Key) (*idempotency.Stored, error) {
	return f.stored, f.beginErr
}

func (f *fakeIdem) Complete(_ context.Context, _ db.Querier, _ uuid.UUID, _ string, _ idempotency.Key, status int, body json.RawMessage) error {
	f.completed = append(f.completed, status)
	f.bodies = append(f.bodies, string(body))
	return f.completeErr
}

type fakeDocs struct {
	err      error
	prefixes []string
}

func (f *fakeDocs) Next(_ context.Context, _ db.Querier, _ uuid.UUID, prefix string) (string, error) {
	f.prefixes = append(f.prefixes, prefix)
	return prefix + "-000001", f.err
}

type fakePeriods struct{ err error }

func (f fakePeriods) Check(context.Context, db.Querier, uuid.UUID, time.Time) error { return f.err }

type fakeAudit struct {
	err    error
	events []audit.Event
}

func (f *fakeAudit) Record(_ context.Context, _ db.Querier, _ audit.Actor, evt audit.Event) error {
	f.events = append(f.events, evt)
	return f.err
}

type fakeOutbox struct {
	err    error
	topics []string
}

func (f *fakeOutbox) Publish(_ context.Context, _ db.Querier, _ uuid.UUID, evt outbox.Event) (uuid.UUID, error) {
	f.topics = append(f.topics, evt.Topic)
	return uuid.Nil, f.err
}

type fakeEntitlements struct{ err error }

func (f fakeEntitlements) CanPost(context.Context, db.Querier, uuid.UUID) error { return f.err }

// testCommand is a configurable PostingCommand.
type testCommand struct {
	scope  string
	prefix string
	locks  LockSet
	apply  func(ctx context.Context, tx Tx) (Result, error)
}

func (c testCommand) Scope() string           { return c.scope }
func (c testCommand) BusinessDate() time.Time { return clock.Date(2026, 3, 15) }
func (c testCommand) DocPrefix() string       { return c.prefix }
func (c testCommand) LockKeys() LockSet       { return c.locks }
func (c testCommand) Apply(ctx context.Context, tx Tx) (Result, error) {
	if c.apply == nil {
		return Result{Body: map[string]string{"doc_no": tx.DocNo()}}, nil
	}
	return c.apply(ctx, tx)
}

type harness struct {
	db      *fakeDB
	idem    *fakeIdem
	docs    *fakeDocs
	audit   *fakeAudit
	outbox  *fakeOutbox
	metrics *metrics.Metrics
	deps    Deps
}

func newHarness() *harness {
	h := &harness{db: &fakeDB{}, idem: &fakeIdem{}, docs: &fakeDocs{}, audit: &fakeAudit{}, outbox: &fakeOutbox{}, metrics: metrics.New()}
	h.deps = Deps{
		DB: h.db, Idempotency: h.idem, Docs: h.docs, Periods: fakePeriods{}, Audit: h.audit, Outbox: h.outbox,
		Clock: clock.NewFixed(time.Date(2026, 3, 15, 3, 0, 0, 0, time.UTC)), Metrics: h.metrics,
	}
	return h
}

func (h *harness) coordinator(t *testing.T) *Coordinator {
	t.Helper()
	c, err := NewCoordinator(h.deps)
	if err != nil {
		t.Fatal(err)
	}
	c.retryDelay = func() time.Duration { return 0 }
	return c
}

func (h *harness) scrape(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	h.metrics.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	return string(body)
}

var (
	tenantID = uuid.MustParse("01920000-0000-7000-8000-000000000001")
	owner    = Actor{TenantID: tenantID, Type: audit.ActorOwner, ID: uuid.MustParse("01920000-0000-7000-8000-000000000002"), Source: audit.SourceWeb}
	key      = idempotency.Key{Value: "k1", RequestHash: make([]byte, sha256.Size)}
)

func TestNewCoordinatorRequiresDeps(t *testing.T) {
	full := newHarness().deps
	strip := []func(*Deps){
		func(d *Deps) { d.DB = nil }, func(d *Deps) { d.Idempotency = nil }, func(d *Deps) { d.Docs = nil },
		func(d *Deps) { d.Periods = nil }, func(d *Deps) { d.Audit = nil }, func(d *Deps) { d.Outbox = nil },
		func(d *Deps) { d.Clock = nil },
	}
	for i, fn := range strip {
		d := full
		fn(&d)
		if _, err := NewCoordinator(d); err == nil {
			t.Errorf("case %d: missing dependency accepted", i)
		}
	}
	optional := full
	optional.Entitlements, optional.Metrics = nil, nil
	if _, err := NewCoordinator(optional); err != nil {
		t.Fatalf("optional deps: %v", err)
	}
}

func TestPostAppliesInOrder(t *testing.T) {
	h := newHarness()
	c := h.coordinator(t)
	var locked []uuid.UUID
	c.RegisterLocker(LockSkuStock, func(_ context.Context, _ db.Querier, ids []uuid.UUID) error {
		locked = append(locked, ids...)
		return nil
	})
	var locks LockSet
	locks.Add(LockSkuStock, idB, idA)
	cmd := testCommand{scope: "receipt.post", prefix: "PN", locks: locks, apply: func(_ context.Context, tx Tx) (Result, error) {
		if tx.TenantID() != tenantID || tx.Actor() != owner || !tx.Now().Equal(time.Date(2026, 3, 15, 3, 0, 0, 0, time.UTC)) ||
			!tx.BusinessDate().Equal(clock.Date(2026, 3, 15)) || tx.DocNo() != "PN-000001" {
			t.Errorf("tx context: %v %v %v %v %q", tx.TenantID(), tx.Actor(), tx.Now(), tx.BusinessDate(), tx.DocNo())
		}
		if len(locked) != 2 || locked[0] != idA {
			t.Errorf("rows not locked before Apply: %v", locked)
		}
		tx.Audit(audit.Event{Action: "receipt.posted", EntityType: "receipt"})
		tx.Audit(audit.Event{Action: "x", EntityType: "receipt", DocNo: "OTHER"})
		tx.Publish(outbox.Event{Topic: "receipt.posted"})
		return Result{Body: map[string]string{"doc_no": tx.DocNo()}}, nil
	}}
	resp, err := c.Post(context.Background(), owner, key, cmd)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 201 || string(resp.Body) != `{"doc_no":"PN-000001"}` || resp.Replayed || resp.DocNo != "PN-000001" {
		t.Fatalf("resp = %+v body %s", resp, resp.Body)
	}
	if len(h.audit.events) != 2 || h.audit.events[0].DocNo != "PN-000001" || h.audit.events[1].DocNo != "OTHER" {
		t.Errorf("audits %+v", h.audit.events)
	}
	if len(h.outbox.topics) != 1 || len(h.idem.completed) != 1 || h.idem.completed[0] != 201 {
		t.Errorf("outbox %v completed %v", h.outbox.topics, h.idem.completed)
	}
	if !strings.Contains(h.scrape(t), `tally_posting_duration_seconds_count{outcome="applied",scope="receipt.post"} 1`) {
		t.Error("applied outcome not observed")
	}
}

func TestPostReplaysStoredResponse(t *testing.T) {
	h := newHarness()
	h.idem.stored = &idempotency.Stored{Status: 201, Body: json.RawMessage(`{"doc_no":"PN-000001"}`)}
	applied := false
	resp, err := h.coordinator(t).Post(context.Background(), owner, key, testCommand{scope: "receipt.post", prefix: "PN",
		apply: func(context.Context, Tx) (Result, error) { applied = true; return Result{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Replayed || resp.Status != 201 || string(resp.Body) != `{"doc_no":"PN-000001"}` || resp.DocNo != "" {
		t.Fatalf("resp = %+v", resp)
	}
	if applied || len(h.docs.prefixes) != 0 || len(h.idem.completed) != 0 {
		t.Fatal("replay re-ran the command")
	}
	if !strings.Contains(h.scrape(t), `outcome="replayed"`) {
		t.Error("replayed outcome not observed")
	}
}

// A completed request replays even when the guards would now reject a new posting.
func TestPostReplaysCompletedRequestBeforeGuards(t *testing.T) {
	h := newHarness()
	h.idem.replayed = &idempotency.Stored{Status: 201, Body: json.RawMessage(`{"doc_no":"PN-000001"}`)}
	h.idem.beginErr = errors.New("Begin must not run for a completed request")
	h.deps.Entitlements = fakeEntitlements{err: apperr.NewReadOnlyTenant()}
	h.deps.Periods = fakePeriods{err: apperr.NewPeriodLocked("locked")}
	applied := false
	resp, err := h.coordinator(t).Post(context.Background(), owner, key, testCommand{scope: "receipt.post", prefix: "PN",
		apply: func(context.Context, Tx) (Result, error) { applied = true; return Result{}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Replayed || resp.Status != 201 || string(resp.Body) != `{"doc_no":"PN-000001"}` {
		t.Fatalf("resp = %+v", resp)
	}
	if applied || len(h.docs.prefixes) != 0 || len(h.idem.completed) != 0 {
		t.Fatal("replay re-ran the command")
	}
}

func TestPostWithoutPrefixAndCustomStatus(t *testing.T) {
	h := newHarness()
	resp, err := h.coordinator(t).Post(context.Background(), owner, key, testCommand{scope: "price.update",
		apply: func(context.Context, Tx) (Result, error) { return Result{Status: 200}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Status != 200 || resp.Body != nil || resp.DocNo != "" || len(h.docs.prefixes) != 0 || h.idem.bodies[0] != "" {
		t.Fatalf("resp = %+v, issued %v", resp, h.docs.prefixes)
	}
}

func TestPostRejections(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name    string
		setup   func(h *harness)
		cmd     testCommand
		actor   Actor
		key     idempotency.Key
		outcome string
		check   func(t *testing.T, err error)
	}{
		{name: "read-only tenant", setup: func(h *harness) { h.deps.Entitlements = fakeEntitlements{err: apperr.NewReadOnlyTenant()} },
			outcome: "rejected", check: func(t *testing.T, err error) {
				if !apperr.Is(err, "subscription_read_only") {
					t.Error(err)
				}
			}},
		{name: "period locked", setup: func(h *harness) { h.deps.Periods = fakePeriods{err: apperr.NewPeriodLocked("locked")} },
			outcome: "rejected", check: func(t *testing.T, err error) {
				if apperr.KindOf(err) != apperr.PeriodLocked {
					t.Error(err)
				}
			}},
		{name: "replay lookup", setup: func(h *harness) { h.idem.replayErr = boom }, outcome: "error"},
		{name: "key reused", setup: func(h *harness) { h.idem.beginErr = apperr.NewConflict(idempotency.CodeKeyReused, "x") }, outcome: "rejected"},
		{name: "doc number", setup: func(h *harness) { h.docs.err = boom }, cmd: testCommand{prefix: "PN"}, outcome: "error"},
		{name: "unregistered lock", cmd: testCommand{locks: func() LockSet { var s LockSet; s.Add(LockLots, idA); return s }()}, outcome: "error"},
		{name: "apply error", cmd: testCommand{apply: func(context.Context, Tx) (Result, error) {
			return Result{}, apperr.NewValidation("bad", "bad", nil)
		}}, outcome: "rejected"},
		{name: "audit error", setup: func(h *harness) { h.audit.err = boom }, cmd: testCommand{apply: func(_ context.Context, tx Tx) (Result, error) {
			tx.Audit(audit.Event{Action: "a", EntityType: "e"})
			return Result{}, nil
		}}, outcome: "error"},
		{name: "outbox error", setup: func(h *harness) { h.outbox.err = boom }, cmd: testCommand{apply: func(_ context.Context, tx Tx) (Result, error) {
			tx.Publish(outbox.Event{Topic: "t.x"})
			return Result{}, nil
		}}, outcome: "error"},
		{name: "non-2xx without error", cmd: testCommand{apply: func(context.Context, Tx) (Result, error) { return Result{Status: 409}, nil }},
			outcome: "error", check: func(t *testing.T, err error) {
				if !strings.Contains(err.Error(), "non-2xx") {
					t.Error(err)
				}
			}},
		{name: "unmarshalable body", cmd: testCommand{apply: func(context.Context, Tx) (Result, error) { return Result{Body: make(chan int)}, nil }}, outcome: "error"},
		{name: "complete error", setup: func(h *harness) { h.idem.completeErr = boom }, outcome: "error"},
		{name: "invalid actor", actor: Actor{TenantID: tenantID}, outcome: "error"},
		{name: "missing key", key: idempotency.Key{Value: "x"}, outcome: "error"},
		{name: "empty scope", cmd: testCommand{scope: "-"}, outcome: "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness()
			if tc.setup != nil {
				tc.setup(h)
			}
			cmd := tc.cmd
			switch cmd.scope {
			case "":
				cmd.scope = "test.cmd"
			case "-":
				cmd.scope = ""
			}
			actor, k := owner, key
			if tc.actor != (Actor{}) {
				actor = tc.actor
			}
			if tc.key.Value != "" {
				k = tc.key
			}
			resp, err := h.coordinator(t).Post(context.Background(), actor, k, cmd)
			if err == nil {
				t.Fatalf("accepted: %+v", resp)
			}
			if resp.Status != 0 || resp.Body != nil || resp.DocNo != "" {
				t.Errorf("response returned with error: %+v", resp)
			}
			if tc.check != nil {
				tc.check(t, err)
			}
			if cmd.scope != "" && !strings.Contains(h.scrape(t), `outcome="`+tc.outcome+`"`) {
				t.Errorf("outcome %q not observed", tc.outcome)
			}
		})
	}
}

func TestPostRetriesDeadlockOnce(t *testing.T) {
	deadlock := &pgconn.PgError{Code: "40P01"}
	h := newHarness()
	h.db.failures = []error{deadlock}
	if _, err := h.coordinator(t).Post(context.Background(), owner, key, testCommand{scope: "s.x"}); err != nil {
		t.Fatalf("retry did not recover: %v", err)
	}
	if h.db.calls != 2 {
		t.Fatalf("calls = %d, want 2", h.db.calls)
	}

	h = newHarness()
	h.db.failures = []error{deadlock, &pgconn.PgError{Code: "40001"}}
	if _, err := h.coordinator(t).Post(context.Background(), owner, key, testCommand{scope: "s.x"}); !db.IsRetryable(err) {
		t.Fatalf("second failure: %v", err)
	}
	if h.db.calls != 2 {
		t.Fatalf("calls = %d, want exactly one retry", h.db.calls)
	}

	h = newHarness()
	h.db.failures = []error{deadlock}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := h.coordinator(t)
	c.retryDelay = func() time.Duration { return time.Hour }
	if _, err := c.Post(ctx, owner, key, testCommand{scope: "s.x"}); !db.IsRetryable(err) || h.db.calls != 1 {
		t.Fatalf("cancelled context retried: %v calls %d", err, h.db.calls)
	}
}

func TestDefaultRetryDelayIsJittered(t *testing.T) {
	c, err := NewCoordinator(newHarness().deps)
	if err != nil {
		t.Fatal(err)
	}
	for range 50 {
		if d := c.retryDelay(); d < 10*time.Millisecond || d >= 50*time.Millisecond {
			t.Fatalf("delay %v outside [10ms, 50ms)", d)
		}
	}
}
