//go:build integration

package outbox_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/metrics"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

type eventRow struct {
	Attempts    int        `db:"attempts"`
	LastError   *string    `db:"last_error"`
	ProcessedAt *time.Time `db:"processed_at"`
	AvailableAt time.Time  `db:"available_at"`
}

func readEvent(t *testing.T, d *db.DB, tenantID, id uuid.UUID) eventRow {
	t.Helper()
	var r eventRow
	testkit.InTenant(t, d, tenantID, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(),
			`SELECT attempts, last_error, processed_at, available_at FROM outbox_events WHERE id = $1`, id).StructScan(&r)
	})
	return r
}

func publish(t *testing.T, d *db.DB, p *outbox.Publisher, tenantID uuid.UUID, evt outbox.Event) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	testkit.InTenant(t, d, tenantID, func(q db.Querier) error {
		var err error
		id, err = p.Publish(context.Background(), q, tenantID, evt)
		return err
	})
	return id
}

func quietLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// Each test uses its own topic namespace; other tenants' events from parallel tests in the same
// database are delivered to no handler and simply marked processed.
func TestDispatchDeliversAndMarksProcessed(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	clk := testkit.Clock()
	m := metrics.New()
	pub := outbox.NewPublisher(clk)
	disp := outbox.NewDispatcher(d, clk, m, quietLogger())

	var got []outbox.Message
	disp.Subscribe("deliver.ok", func(ctx context.Context, q db.Querier, msg outbox.Message) error {
		got = append(got, msg)
		_, err := q.ExecContext(ctx, `INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at) VALUES ($1, $2, 'handled', 'handled', now(), now())`, ids.New(), msg.TenantID)
		return err
	})
	id := publish(t, d, pub, tn.ID, outbox.Event{Topic: "deliver.ok", Payload: map[string]string{"sku": "A"}})
	delayed := publish(t, d, pub, tn.ID, outbox.Event{Topic: "deliver.ok", Delay: time.Minute})

	clk.Advance(30 * time.Second)
	if _, err := disp.DispatchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != id || got[0].TenantID != tn.ID {
		t.Fatalf("delivered %+v", got)
	}
	var payload map[string]string
	if err := json.Unmarshal(got[0].Payload, &payload); err != nil || payload["sku"] != "A" {
		t.Fatalf("payload %s", got[0].Payload)
	}
	if r := readEvent(t, d, tn.ID, id); r.ProcessedAt == nil || r.Attempts != 1 {
		t.Fatalf("event after delivery: %+v", r)
	}
	if r := readEvent(t, d, tn.ID, delayed); r.ProcessedAt != nil {
		t.Fatal("delayed event delivered early")
	}
	// Only the delayed event (available 30 s from now) is pending; it is not late, so lag is 0.
	if body := scrape(t, m); !strings.Contains(body, "tally_outbox_lag_seconds 0") || !strings.Contains(body, `tally_outbox_events_total{outcome="processed",topic="deliver.ok"} 1`) {
		t.Fatalf("metrics:\n%s", body)
	}

	clk.Advance(time.Minute)
	if _, err := disp.DispatchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if r := readEvent(t, d, tn.ID, delayed); r.ProcessedAt == nil {
		t.Fatal("delayed event not delivered when due")
	}
	if _, err := disp.DispatchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("processed events redelivered: %d deliveries", len(got))
	}
}

func TestFailingHandlerRetriesWithBackoffThenDies(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	clk := testkit.Clock()
	pub := outbox.NewPublisher(clk)
	disp := outbox.NewDispatcher(d, clk, nil, quietLogger())

	var calls atomic.Int32
	disp.Subscribe("deliver.fail", func(ctx context.Context, q db.Querier, msg outbox.Message) error {
		calls.Add(1)
		// This write must be rolled back with the failed attempt.
		if _, err := q.ExecContext(ctx, `INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at) VALUES ($1, $2, 'partial', 'partial', now(), now())`, ids.New(), msg.TenantID); err != nil {
			return err
		}
		return errors.New(strings.Repeat("x", 2000))
	})
	id := publish(t, d, pub, tn.ID, outbox.Event{Topic: "deliver.fail"})

	if _, err := disp.DispatchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	r := readEvent(t, d, tn.ID, id)
	if r.Attempts != 1 || r.ProcessedAt != nil || r.LastError == nil || len(*r.LastError) != 1000 {
		t.Fatalf("after first failure: %+v", r)
	}
	if want := clk.Now().Add(outbox.Backoff(1)); !r.AvailableAt.Equal(want) {
		t.Fatalf("available_at = %v, want %v", r.AvailableAt, want)
	}
	// Not due yet: no second attempt.
	if _, err := disp.DispatchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("retried before backoff: %d calls", calls.Load())
	}
	for range outbox.MaxAttempts + 2 {
		clk.Advance(2 * time.Hour)
		if _, err := disp.DispatchOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != outbox.MaxAttempts {
		t.Fatalf("handler ran %d times, want %d", calls.Load(), outbox.MaxAttempts)
	}
	if r := readEvent(t, d, tn.ID, id); r.Attempts != outbox.MaxAttempts || r.ProcessedAt != nil {
		t.Fatalf("dead event: %+v", r)
	}
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		var n int
		if err := q.QueryRowxContext(ctx, `SELECT count(*) FROM parties`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("failed handler writes committed: %d rows", n)
		}
		return nil
	})
}

// Each event commits on its own: when the transaction of one event fails after its handler ran,
// only that event is delivered again, never the ones before it.
func TestFailedTransactionRepeatsOnlyItsEvent(t *testing.T) {
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	clk := testkit.Clock()
	pub := outbox.NewPublisher(clk)
	disp := outbox.NewDispatcher(d, clk, nil, quietLogger())

	events := make([]uuid.UUID, 3)
	for i := range events {
		events[i] = publish(t, d, pub, tn.ID, outbox.Event{Topic: "deliver.once"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	sent := map[uuid.UUID]int{}
	disp.Subscribe("deliver.once", func(_ context.Context, _ db.Querier, msg outbox.Message) error {
		sent[msg.ID]++ // the external side effect, e.g. an email
		if msg.ID == events[1] && sent[msg.ID] == 1 {
			cancel() // the transaction of the second event cannot commit
		}
		return nil
	})
	if _, err := disp.DispatchOnce(ctx); err == nil {
		t.Fatal("failed transaction not reported")
	}
	if r := readEvent(t, d, tn.ID, events[0]); r.ProcessedAt == nil {
		t.Fatal("first event not committed before the failure")
	}
	if r := readEvent(t, d, tn.ID, events[1]); r.ProcessedAt != nil || r.Attempts != 0 {
		t.Fatalf("failed event: %+v", r)
	}
	if _, err := disp.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, id := range events {
		want := 1
		if i == 1 {
			want = 2
		}
		if sent[id] != want {
			t.Errorf("event %d sent %d times, want %d", i, sent[id], want)
		}
		if r := readEvent(t, d, tn.ID, id); r.ProcessedAt == nil {
			t.Errorf("event %d not processed", i)
		}
	}
}

// One round delivers at most BatchSize events of a tenant; the rest is reported as lag.
func TestBatchLimitLeavesTheRestAsLag(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	clk := testkit.Clock()
	m := metrics.New()
	pub := outbox.NewPublisher(clk)
	disp := outbox.NewDispatcher(d, clk, m, quietLogger())
	var calls atomic.Int32
	disp.Subscribe("deliver.bulk", func(context.Context, db.Querier, outbox.Message) error {
		calls.Add(1)
		return nil
	})
	for range outbox.BatchSize + 3 {
		publish(t, d, pub, tn.ID, outbox.Event{Topic: "deliver.bulk"})
	}
	clk.Advance(time.Minute)
	if _, err := disp.DispatchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != outbox.BatchSize {
		t.Fatalf("first round delivered %d, want %d", calls.Load(), outbox.BatchSize)
	}
	if body := scrape(t, m); !strings.Contains(body, "tally_outbox_lag_seconds 60") {
		t.Fatalf("lag not reported:\n%s", body)
	}
	if _, err := disp.DispatchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != outbox.BatchSize+3 {
		t.Fatalf("delivered %d, want %d", calls.Load(), outbox.BatchSize+3)
	}
	if body := scrape(t, m); !strings.Contains(body, "tally_outbox_lag_seconds 0") {
		t.Fatalf("lag after draining:\n%s", body)
	}
}

func TestPanickingHandlerIsRecorded(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	clk := testkit.Clock()
	disp := outbox.NewDispatcher(d, clk, nil, quietLogger())
	disp.Subscribe("deliver.panic", func(context.Context, db.Querier, outbox.Message) error { panic("bug") })
	id := publish(t, d, outbox.NewPublisher(clk), tn.ID, outbox.Event{Topic: "deliver.panic"})
	if _, err := disp.DispatchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if r := readEvent(t, d, tn.ID, id); r.Attempts != 1 || r.LastError == nil || !strings.Contains(*r.LastError, "panic") {
		t.Fatalf("event: %+v", r)
	}
}

func TestPublishValidation(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	p := outbox.NewPublisher(testkit.Clock())
	err := d.WithTenantTx(ctx, tn.ID, func(q db.Querier) error {
		if _, err := p.Publish(ctx, q, tn.ID, outbox.Event{Topic: "nope"}); err == nil {
			t.Error("invalid topic accepted")
		}
		if _, err := p.Publish(ctx, q, tn.ID, outbox.Event{Topic: "a.b", Delay: -time.Second}); err == nil {
			t.Error("negative delay accepted")
		}
		if _, err := p.Publish(ctx, q, tn.ID, outbox.Event{Topic: "a.b", Payload: make(chan int)}); err == nil {
			t.Error("unmarshalable payload accepted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	d := testkit.DB(t)
	disp := outbox.NewDispatcher(d, testkit.Clock(), nil, quietLogger())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- disp.Run(ctx, 10*time.Millisecond) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func scrape(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	return rec.Body.String()
}

func TestDispatchSkipsReplacedSandboxes(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	live := testkit.NewTenant(t, d)
	replaced := testkit.NewTenant(t, d)
	clk := testkit.Clock()
	pub := outbox.NewPublisher(clk)
	disp := outbox.NewDispatcher(d, clk, metrics.New(), quietLogger())
	var got []uuid.UUID
	disp.Subscribe("replaced.skip", func(_ context.Context, _ db.Querier, msg outbox.Message) error {
		got = append(got, msg.TenantID)
		return nil
	})
	liveEvt := publish(t, d, pub, live.ID, outbox.Event{Topic: "replaced.skip"})
	staleEvt := publish(t, d, pub, replaced.ID, outbox.Event{Topic: "replaced.skip"})
	if _, err := testkit.SuperDB(t).Exec(`UPDATE tally.tenants SET is_sandbox = true, status = 'replaced' WHERE id = $1`, replaced.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := disp.DispatchOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != live.ID {
		t.Fatalf("handled tenants %v, want only %s", got, live.ID)
	}
	if r := readEvent(t, d, live.ID, liveEvt); r.ProcessedAt == nil {
		t.Fatal("event of the active tenant not delivered")
	}
	// An event written to a replaced sandbox stays pending: nothing handles it.
	if r := readEvent(t, d, replaced.ID, staleEvt); r.ProcessedAt != nil || r.Attempts != 0 {
		t.Fatalf("event of the replaced sandbox: %+v", r)
	}
}
