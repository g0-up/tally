//go:build integration

package posting_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/docseq"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/platform/periodlock"
	"github.com/g0-up/tally/apps/api/internal/posting"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// statusEntitlements lets a tenant post unless tenants.status is read_only.
type statusEntitlements struct{}

func (statusEntitlements) CanPost(ctx context.Context, q db.Querier, tenantID uuid.UUID) error {
	var status string
	if err := q.QueryRowxContext(ctx, `SELECT status FROM tenants WHERE id = $1`, tenantID).Scan(&status); err != nil {
		return err
	}
	if status == "read_only" {
		return apperr.NewReadOnlyTenant()
	}
	return nil
}

func newCoordinator(t *testing.T, d *db.DB) *posting.Coordinator {
	t.Helper()
	clk := testkit.Clock()
	c, err := posting.NewCoordinator(posting.Deps{
		DB: d, Idempotency: idempotency.NewGuard(clk), Docs: docseq.NewIssuer(), Periods: periodlock.NewGuard(),
		Audit: audit.NewRecorder(clk), Outbox: outbox.NewPublisher(clk), Entitlements: statusEntitlements{}, Clock: clk,
	})
	if err != nil {
		t.Fatal(err)
	}
	c.RegisterLocker(posting.LockSkuStock, func(ctx context.Context, q db.Querier, rows []uuid.UUID) error {
		_, err := q.ExecContext(ctx, `SELECT 1 FROM parties WHERE id = ANY($1) ORDER BY id FOR UPDATE`, rows)
		return err
	})
	return c
}

// probeCommand inserts one party row and records an audit event and an outbox event.
type probeCommand struct {
	date  time.Time
	lock  []uuid.UUID
	fail  bool
	pause time.Duration
}

func (probeCommand) Scope() string             { return "probe.create" }
func (c probeCommand) BusinessDate() time.Time { return c.date }
func (probeCommand) DocPrefix() string         { return "PN" }
func (c probeCommand) LockKeys() posting.LockSet {
	var s posting.LockSet
	s.Add(posting.LockSkuStock, c.lock...)
	return s
}

func (c probeCommand) Apply(ctx context.Context, tx posting.Tx) (posting.Result, error) {
	id := ids.New()
	if _, err := tx.ExecContext(ctx, `INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at) VALUES ($1, $2, $3, $3, now(), now())`, id, tx.TenantID(), tx.DocNo()); err != nil {
		return posting.Result{}, err
	}
	time.Sleep(c.pause)
	if c.fail {
		return posting.Result{}, apperr.NewValidation("probe_rejected", "rejected on purpose", nil)
	}
	tx.Audit(audit.Event{Action: "probe.created", EntityType: "probe", EntityID: id, After: map[string]string{"note": tx.DocNo()}})
	tx.Publish(outbox.Event{Topic: "probe.created", Payload: map[string]string{"id": id.String()}})
	return posting.Result{Body: map[string]string{"id": id.String(), "doc_no": tx.DocNo()}}, nil
}

func actorFor(tn testkit.Tenant) posting.Actor {
	return posting.Actor{TenantID: tn.ID, Type: audit.ActorOwner, ID: ids.New(), Source: audit.SourceWeb, RequestID: "req-1"}
}

func keyOf(value string) idempotency.Key {
	h := sha256.Sum256([]byte(value))
	return idempotency.Key{Value: value, RequestHash: h[:]}
}

func count(t *testing.T, d *db.DB, tenantID uuid.UUID, query string) int {
	t.Helper()
	var n int
	testkit.InTenant(t, d, tenantID, func(q db.Querier) error {
		return q.QueryRowxContext(context.Background(), query).Scan(&n)
	})
	return n
}

func TestSameKeyFromTwoGoroutinesAppliesOnce(t *testing.T) {
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	c := newCoordinator(t, d)
	cmd := probeCommand{date: clock.Date(2026, 3, 15), pause: 100 * time.Millisecond}

	var wg sync.WaitGroup
	results := make([]posting.Response, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Go(func() { results[i], errs[i] = c.Post(context.Background(), actorFor(tn), keyOf("k-dup"), cmd) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if results[0].Replayed == results[1].Replayed {
		t.Fatalf("want one applied and one replayed: %+v", results)
	}
	// jsonb normalises key order and spacing, so a replay is equal as JSON, not byte for byte.
	var first, second map[string]string
	if err := json.Unmarshal(results[0].Body, &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(results[1].Body, &second); err != nil {
		t.Fatal(err)
	}
	if results[0].Status != 201 || results[1].Status != 201 || !maps.Equal(first, second) {
		t.Fatalf("replay differs: %s vs %s", results[0].Body, results[1].Body)
	}
	var body struct {
		DocNo string `json:"doc_no"`
	}
	if err := json.Unmarshal(results[0].Body, &body); err != nil || body.DocNo != docseq.Format("PN", 1) {
		t.Fatalf("body %s: %v", results[0].Body, err)
	}
	if n := count(t, d, tn.ID, `SELECT count(*) FROM parties`); n != 1 {
		t.Fatalf("rows applied = %d", n)
	}
	if n := count(t, d, tn.ID, `SELECT count(*) FROM audit_events WHERE action = 'probe.created' AND doc_no = 'PN-0001' AND request_id = 'req-1'`); n != 1 {
		t.Fatalf("audit events = %d", n)
	}
	if n := count(t, d, tn.ID, `SELECT count(*) FROM outbox_events WHERE topic = 'probe.created'`); n != 1 {
		t.Fatalf("outbox events = %d", n)
	}
	if n := count(t, d, tn.ID, `SELECT next_value FROM doc_sequences WHERE prefix = 'PN'`); n != 2 {
		t.Fatalf("next doc number = %d, want 2", n)
	}
}

func TestFailedApplyRollsBackEverything(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	c := newCoordinator(t, d)
	date := clock.Date(2026, 3, 15)

	if _, err := c.Post(ctx, actorFor(tn), keyOf("k-fail"), probeCommand{date: date, fail: true}); !apperr.Is(err, "probe_rejected") {
		t.Fatalf("err = %v", err)
	}
	for _, query := range []string{
		`SELECT count(*) FROM parties`, `SELECT count(*) FROM idempotency_keys`, `SELECT count(*) FROM doc_sequences`,
		`SELECT count(*) FROM audit_events`, `SELECT count(*) FROM outbox_events`,
	} {
		if n := count(t, d, tn.ID, query); n != 0 {
			t.Errorf("%s = %d after rollback", query, n)
		}
	}
	resp, err := c.Post(ctx, actorFor(tn), keyOf("k-fail"), probeCommand{date: date})
	if err != nil || resp.Replayed || resp.DocNo != "PN-0001" {
		t.Fatalf("retry after failure: %+v, %v", resp, err)
	}
}

func TestPeriodLockAndReadOnlyTenantReject(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	c := newCoordinator(t, d)
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `
			INSERT INTO period_locks (id, tenant_id, action, locked_through, actor_id, created_at)
			VALUES ($1, $2, 'lock', '2026-02-28', $3, $4)`, ids.New(), tn.ID, ids.New(), testkit.Epoch)
		return err
	})
	if _, err := c.Post(ctx, actorFor(tn), keyOf("k-locked"), probeCommand{date: clock.Date(2026, 2, 28)}); apperr.KindOf(err) != apperr.PeriodLocked {
		t.Fatalf("locked period: %v", err)
	}
	if _, err := c.Post(ctx, actorFor(tn), keyOf("k-open"), probeCommand{date: clock.Date(2026, 3, 1)}); err != nil {
		t.Fatalf("open period: %v", err)
	}

	if _, err := testkit.SuperDB(t).ExecContext(ctx, `UPDATE tally.tenants SET status = 'read_only' WHERE id = $1`, tn.ID); err != nil {
		t.Fatal(err)
	}
	_, err := c.Post(ctx, actorFor(tn), keyOf("k-readonly"), probeCommand{date: clock.Date(2026, 3, 1)})
	if !apperr.Is(err, "subscription_read_only") || apperr.KindOf(err) != apperr.ReadOnlyTenant {
		t.Fatalf("read-only tenant: %v", err)
	}
	if n := count(t, d, tn.ID, `SELECT count(*) FROM parties`); n != 1 {
		t.Fatalf("rows = %d, want only the open-period posting", n)
	}
}

// A client whose response was lost retries with the same key after the period was locked or the
// tenant turned read-only: it gets the committed response back, while a new key is refused.
func TestCompletedRequestReplaysAfterGuardsChange(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	date := clock.Date(2026, 3, 15)

	cases := []struct {
		name  string
		block func(t *testing.T, tn testkit.Tenant)
		check func(err error) bool
	}{
		{name: "period locked", block: func(t *testing.T, tn testkit.Tenant) {
			testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
				_, err := q.ExecContext(ctx, `
					INSERT INTO period_locks (id, tenant_id, action, locked_through, actor_id, created_at)
					VALUES ($1, $2, 'lock', '2026-03-31', $3, $4)`, ids.New(), tn.ID, ids.New(), testkit.Epoch)
				return err
			})
		}, check: func(err error) bool { return apperr.KindOf(err) == apperr.PeriodLocked }},
		{name: "read-only tenant", block: func(t *testing.T, tn testkit.Tenant) {
			if _, err := testkit.SuperDB(t).ExecContext(ctx, `UPDATE tally.tenants SET status = 'read_only' WHERE id = $1`, tn.ID); err != nil {
				t.Fatal(err)
			}
		}, check: func(err error) bool { return apperr.KindOf(err) == apperr.ReadOnlyTenant }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tn := testkit.NewTenant(t, d)
			c := newCoordinator(t, d)
			first, err := c.Post(ctx, actorFor(tn), keyOf("k-sale"), probeCommand{date: date})
			if err != nil {
				t.Fatal(err)
			}
			tc.block(t, tn)

			again, err := c.Post(ctx, actorFor(tn), keyOf("k-sale"), probeCommand{date: date})
			if err != nil {
				t.Fatalf("retry of a committed request: %v", err)
			}
			var want, got map[string]string
			if err := json.Unmarshal(first.Body, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(again.Body, &got); err != nil {
				t.Fatal(err)
			}
			if !again.Replayed || again.Status != first.Status || !maps.Equal(want, got) {
				t.Fatalf("retry = %+v %s, want replay of %d %s", again, again.Body, first.Status, first.Body)
			}

			if _, err := c.Post(ctx, actorFor(tn), keyOf("k-new"), probeCommand{date: date}); !tc.check(err) {
				t.Fatalf("new key: err = %v, want the guard to refuse it", err)
			}
			if n := count(t, d, tn.ID, `SELECT count(*) FROM parties`); n != 1 {
				t.Fatalf("rows = %d, want only the first posting", n)
			}
			if n := count(t, d, tn.ID, `SELECT count(*) FROM idempotency_keys`); n != 1 {
				t.Fatalf("idempotency keys = %d, want the refused key rolled back", n)
			}
		})
	}
}

// Postings that lock the same rows listed in opposite orders never deadlock, because the
// coordinator always locks in one global order.
func TestOppositeLockListsDoNotDeadlock(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	c := newCoordinator(t, d)
	rows := []uuid.UUID{ids.New(), ids.New(), ids.New()}
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		for _, id := range rows {
			if _, err := q.ExecContext(ctx, `INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at) VALUES ($1, $2, 'stock', 'stock', now(), now())`, id, tn.ID); err != nil {
				return err
			}
		}
		return nil
	})
	reversed := []uuid.UUID{rows[2], rows[1], rows[0]}

	const workers = 10
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		lock := rows
		if i%2 == 1 {
			lock = reversed
		}
		wg.Go(func() {
			_, err := c.Post(ctx, actorFor(tn), keyOf(ids.New().String()), probeCommand{date: clock.Date(2026, 3, 15), lock: lock, pause: 5 * time.Millisecond})
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, d, tn.ID, `SELECT next_value FROM doc_sequences WHERE prefix = 'PN'`); n != workers+1 {
		t.Fatalf("next doc number = %d", n)
	}
	if n := count(t, d, tn.ID, `SELECT count(*) FROM parties WHERE name LIKE 'PN-%'`); n != workers {
		t.Fatalf("applied = %d", n)
	}
}

func TestReusedKeyWithDifferentRequestConflicts(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	c := newCoordinator(t, d)
	if _, err := c.Post(ctx, actorFor(tn), keyOf("same"), probeCommand{date: clock.Date(2026, 3, 15)}); err != nil {
		t.Fatal(err)
	}
	other := idempotency.Key{Value: "same", RequestHash: keyOf("different body").RequestHash}
	_, err := c.Post(ctx, actorFor(tn), other, probeCommand{date: clock.Date(2026, 3, 15)})
	if !apperr.Is(err, idempotency.CodeKeyReused) {
		t.Fatalf("err = %v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatal("unexpected cancellation")
	}
}
