//go:build integration

package idempotency_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func key(t *testing.T, value, body string) idempotency.Key {
	t.Helper()
	h, err := idempotency.RequestHash("POST", "/api/v1/things", []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return idempotency.Key{Value: value, RequestHash: h}
}

func TestBeginCompleteReplay(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	g := idempotency.NewGuard(testkit.Clock())
	k := key(t, "k-1", `{"a":1}`)

	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		stored, err := g.Begin(ctx, q, tn.ID, "thing.create", k)
		if err != nil || stored != nil {
			t.Fatalf("first Begin = %v, %v; want nil, nil", stored, err)
		}
		return g.Complete(ctx, q, tn.ID, "thing.create", k, 201, json.RawMessage(`{"id":"x"}`))
	})
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		stored, err := g.Begin(ctx, q, tn.ID, "thing.create", k)
		if err != nil || stored == nil {
			t.Fatalf("replay Begin = %v, %v", stored, err)
		}
		if stored.Status != 201 || string(stored.Body) != `{"id": "x"}` {
			t.Errorf("stored = %d %s", stored.Status, stored.Body)
		}
		return nil
	})
	// Same key, different request.
	err := d.WithTenantTx(ctx, tn.ID, func(q db.Querier) error {
		_, err := g.Begin(ctx, q, tn.ID, "thing.create", key(t, "k-1", `{"a":2}`))
		return err
	})
	if !apperr.Is(err, idempotency.CodeKeyReused) {
		t.Fatalf("reused key: err = %v", err)
	}
	// Same key value in another scope is independent.
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		stored, err := g.Begin(ctx, q, tn.ID, "other.scope", k)
		if err != nil || stored != nil {
			t.Fatalf("other scope Begin = %v, %v", stored, err)
		}
		return nil
	})
}

func TestFailedCommandReleasesKey(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	g := idempotency.NewGuard(testkit.Clock())
	k := key(t, "k-fail", `{}`)
	boom := errors.New("command failed")

	err := d.WithTenantTx(ctx, tn.ID, func(q db.Querier) error {
		if _, err := g.Begin(ctx, q, tn.ID, "s", k); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		stored, err := g.Begin(ctx, q, tn.ID, "s", k)
		if err != nil || stored != nil {
			t.Fatalf("retry after failure: %v, %v", stored, err)
		}
		return nil
	})
}

func TestCommittedWithoutResponseIsInProgress(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	g := idempotency.NewGuard(testkit.Clock())
	k := key(t, "k-open", `{}`)
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		_, err := g.Begin(ctx, q, tn.ID, "s", k)
		return err
	})
	err := d.WithTenantTx(ctx, tn.ID, func(q db.Querier) error {
		_, err := g.Begin(ctx, q, tn.ID, "s", k)
		return err
	})
	if !apperr.Is(err, idempotency.CodeInProgress) {
		t.Fatalf("err = %v, want in progress", err)
	}
}

func TestExpiredKeyIsReclaimedAndPurged(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	clk := testkit.Clock()
	g := idempotency.NewGuard(clk)
	old := key(t, "k-old", `{"v":1}`)
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		if _, err := g.Begin(ctx, q, tn.ID, "s", old); err != nil {
			return err
		}
		if _, err := g.Begin(ctx, q, tn.ID, "s", key(t, "k-purge", `{}`)); err != nil {
			return err
		}
		return g.Complete(ctx, q, tn.ID, "s", old, 200, nil)
	})
	clk.Advance(idempotency.TTL + 1)

	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		// A different request may reuse an expired key.
		reuse := key(t, "k-old", `{"v":2}`)
		stored, err := g.Begin(ctx, q, tn.ID, "s", reuse)
		if err != nil || stored != nil {
			t.Fatalf("expired key: %v, %v", stored, err)
		}
		if err := g.Complete(ctx, q, tn.ID, "s", reuse, 201, json.RawMessage(`{"v":2}`)); err != nil {
			return err
		}
		n, err := g.Purge(ctx, q)
		if err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("purged %d keys, want 1 (k-purge)", n)
		}
		return nil
	})
}

// Replay only answers for a completed, unexpired key sent with the same request.
func TestReplayReturnsOnlyCompletedMatchingKeys(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	clk := testkit.Clock()
	g := idempotency.NewGuard(clk)
	done := key(t, "k-done", `{"a":1}`)
	open := key(t, "k-open", `{}`)
	replay := func(k idempotency.Key) *idempotency.Stored {
		t.Helper()
		var stored *idempotency.Stored
		testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
			var err error
			stored, err = g.Replay(ctx, q, tn.ID, "s", k)
			return err
		})
		return stored
	}

	if replay(done) != nil {
		t.Fatal("unknown key replayed")
	}
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		if _, err := g.Begin(ctx, q, tn.ID, "s", done); err != nil {
			return err
		}
		if _, err := g.Begin(ctx, q, tn.ID, "s", open); err != nil {
			return err
		}
		return g.Complete(ctx, q, tn.ID, "s", done, 201, json.RawMessage(`{"id":"x"}`))
	})
	if stored := replay(done); stored == nil || stored.Status != 201 || string(stored.Body) != `{"id": "x"}` {
		t.Fatalf("completed key: %+v", stored)
	}
	if replay(open) != nil {
		t.Error("key without a response replayed")
	}
	if replay(key(t, "k-done", `{"a":2}`)) != nil {
		t.Error("key replayed for a different request")
	}
	clk.Advance(idempotency.TTL)
	if replay(done) != nil {
		t.Error("expired key replayed")
	}
	err := d.WithTenantTx(ctx, tn.ID, func(q db.Querier) error {
		_, err := g.Replay(ctx, q, tn.ID, "", done)
		return err
	})
	if err == nil {
		t.Error("empty scope accepted")
	}
}

func TestCompleteValidation(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	g := idempotency.NewGuard(testkit.Clock())
	k := key(t, "k-v", `{}`)
	err := d.WithTenantTx(ctx, tn.ID, func(q db.Querier) error {
		if err := g.Complete(ctx, q, tn.ID, "s", k, 422, nil); err == nil {
			t.Error("non-2xx stored")
		}
		if err := g.Complete(ctx, q, tn.ID, "s", k, 200, json.RawMessage(`{bad`)); err == nil {
			t.Error("invalid JSON stored")
		}
		if err := g.Complete(ctx, q, tn.ID, "s", k, 200, nil); err == nil {
			t.Error("unclaimed key completed")
		}
		if _, err := g.Begin(ctx, q, tn.ID, "", k); err == nil {
			t.Error("empty scope accepted")
		}
		if _, err := g.Begin(ctx, q, tn.ID, "s", idempotency.Key{Value: "x"}); err == nil {
			t.Error("key without hash accepted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Under parallel load the same key applies exactly once; every other request replays it.
func TestParallelDuplicateKeyAppliesOnce(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	g := idempotency.NewGuard(testkit.Clock())
	k := key(t, "k-par", `{"x":1}`)

	const workers = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	applied, replayed := 0, 0
	errs := make(chan error, workers)
	for range workers {
		wg.Go(func() {
			err := d.WithTenantTx(ctx, tn.ID, func(q db.Querier) error {
				stored, err := g.Begin(ctx, q, tn.ID, "probe.create", k)
				if err != nil {
					return err
				}
				if stored != nil {
					mu.Lock()
					replayed++
					mu.Unlock()
					return nil
				}
				if _, err := q.ExecContext(ctx, `INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at) VALUES ($1, $2, 'applied', 'applied', now(), now())`, ids.New(), tn.ID); err != nil {
					return err
				}
				if err := g.Complete(ctx, q, tn.ID, "probe.create", k, 201, json.RawMessage(`{}`)); err != nil {
					return err
				}
				mu.Lock()
				applied++
				mu.Unlock()
				return nil
			})
			if err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if applied != 1 || replayed != workers-1 {
		t.Fatalf("applied=%d replayed=%d, want 1 and %d", applied, replayed, workers-1)
	}
	assertProbeRows(t, d, tn.ID, 1)
}

func assertProbeRows(t *testing.T, d *db.DB, tenantID uuid.UUID, want int) {
	t.Helper()
	testkit.InTenant(t, d, tenantID, func(q db.Querier) error {
		var n int
		if err := sqlx.GetContext(context.Background(), q, &n, `SELECT count(*) FROM parties`); err != nil {
			return err
		}
		if n != want {
			t.Errorf("parties rows = %d, want %d", n, want)
		}
		return nil
	})
}

func TestWritesFailInsideReadOnlyTransaction(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	g := idempotency.NewGuard(testkit.Clock())
	k := key(t, "k-ro", `{}`)
	steps := map[string]func(q db.Querier) error{
		"begin": func(q db.Querier) error {
			_, err := g.Begin(ctx, q, tn.ID, "s", k)
			return err
		},
		"complete": func(q db.Querier) error {
			return g.Complete(ctx, q, tn.ID, "s", k, 200, nil)
		},
		"purge": func(q db.Querier) error {
			_, err := g.Purge(ctx, q)
			return err
		},
	}
	for name, step := range steps {
		t.Run(name, func(t *testing.T) {
			err := d.WithReadOnlyTenantTx(ctx, tn.ID, step)
			if err == nil || apperr.KindOf(err) != apperr.Internal {
				t.Fatalf("err = %v, want an internal database error", err)
			}
		})
	}
}

type thing struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

func TestOnceRunsACreateOncePerKey(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	g := idempotency.NewGuard(testkit.Clock())
	runs := 0
	once := func(k *idempotency.Key, fail error) (thing, bool, error) {
		var out thing
		var replayed bool
		err := d.WithTenantTx(ctx, tn.ID, func(q db.Querier) (err error) {
			replayed, err = idempotency.Once(ctx, q, g, tn.ID, "thing.create", k, 201, &out, func() error {
				runs++
				out = thing{ID: ids.New().String(), Count: runs}
				return fail
			})
			return err
		})
		return out, replayed, err
	}

	t.Run("without a key every call runs", func(t *testing.T) {
		runs = 0
		a, ra, errA := once(nil, nil)
		b, rb, errB := once(nil, nil)
		if errA != nil || errB != nil || ra || rb || runs != 2 || a.ID == b.ID {
			t.Fatalf("got %v %v %v / %v %v %v, runs %d", a, ra, errA, b, rb, errB, runs)
		}
	})

	t.Run("the same request replays the first result", func(t *testing.T) {
		runs = 0
		k := key(t, "once-1", `{"a":1}`)
		first, r1, err := once(&k, nil)
		if err != nil || r1 {
			t.Fatalf("first = %v %v", r1, err)
		}
		again, r2, err := once(&k, nil)
		if err != nil || !r2 || again != first || runs != 1 {
			t.Fatalf("replay = %v %v %v, first %v, runs %d", again, r2, err, first, runs)
		}
		other := key(t, "once-1", `{"a":2}`)
		if _, _, err := once(&other, nil); !apperr.Is(err, idempotency.CodeKeyReused) || runs != 1 {
			t.Fatalf("reused key: %v, runs %d", err, runs)
		}
	})

	t.Run("a failed create releases the key", func(t *testing.T) {
		runs = 0
		k := key(t, "once-fail", `{}`)
		boom := errors.New("boom")
		if _, _, err := once(&k, boom); !errors.Is(err, boom) {
			t.Fatalf("failed create: %v", err)
		}
		out, replayed, err := once(&k, nil)
		if err != nil || replayed || out.Count != 2 || runs != 2 {
			t.Fatalf("retry after failure = %v %v %v, runs %d", out, replayed, err, runs)
		}
	})
}
