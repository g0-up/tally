//go:build integration

package periodlock_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/platform/periodlock"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// appendEvent appends a period lock event; through is a date, or nil for a reopen of everything.
func appendEvent(t *testing.T, d *db.DB, tenantID uuid.UUID, action string, through any, at time.Time) {
	t.Helper()
	testkit.InTenant(t, d, tenantID, func(q db.Querier) error {
		var reason any
		if action == "reopen" {
			reason = "correction"
		}
		_, err := q.ExecContext(context.Background(), `
			INSERT INTO period_locks (id, tenant_id, action, locked_through, reason, actor_id, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, ids.New(), tenantID, action, through, reason, ids.New(), at)
		return err
	})
}

func check(t *testing.T, d *db.DB, tenantID uuid.UUID, date time.Time) error {
	t.Helper()
	var out error
	err := d.WithTenantTx(context.Background(), tenantID, func(q db.Querier) error {
		out = periodlock.NewGuard().Check(context.Background(), q, tenantID, date)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCheckAgainstLatestEvent(t *testing.T) {
	d := testkit.DB(t)
	a := testkit.NewTenant(t, d)
	if err := check(t, d, a.ID, clock.Date(2020, 1, 1)); err != nil {
		t.Fatalf("no lock: %v", err)
	}

	appendEvent(t, d, a.ID, "lock", clock.Date(2026, 2, 28), testkit.Epoch)
	if err := check(t, d, a.ID, clock.Date(2026, 2, 28)); apperr.KindOf(err) != apperr.PeriodLocked {
		t.Fatalf("lock date itself: err = %v, want period locked", err)
	}
	if err := check(t, d, a.ID, time.Date(2026, 2, 28, 23, 59, 0, 0, time.UTC)); apperr.KindOf(err) != apperr.PeriodLocked {
		t.Fatalf("time of day on lock date: err = %v", err)
	}
	if err := check(t, d, a.ID, clock.Date(2026, 3, 1)); err != nil {
		t.Fatalf("day after lock: %v", err)
	}

	appendEvent(t, d, a.ID, "reopen", clock.Date(2026, 1, 31), testkit.Epoch.Add(time.Hour))
	if err := check(t, d, a.ID, clock.Date(2026, 2, 15)); err != nil {
		t.Fatalf("after reopen: %v", err)
	}
	if err := check(t, d, a.ID, clock.Date(2026, 1, 31)); apperr.KindOf(err) != apperr.PeriodLocked {
		t.Fatalf("still locked through Jan: %v", err)
	}

	appendEvent(t, d, a.ID, "reopen", nil, testkit.Epoch.Add(2*time.Hour))
	if through, err := lockedThrough(t, d, a.ID); err != nil || through != nil {
		t.Fatalf("after a full reopen: locked through %v, %v; want nothing locked", through, err)
	}
	if err := check(t, d, a.ID, clock.Date(2020, 1, 1)); err != nil {
		t.Fatalf("after a full reopen: %v", err)
	}
	appendEvent(t, d, a.ID, "lock", clock.Date(2026, 1, 15), testkit.Epoch.Add(3*time.Hour))
	if err := check(t, d, a.ID, clock.Date(2026, 1, 15)); apperr.KindOf(err) != apperr.PeriodLocked {
		t.Fatalf("locked again after a full reopen: %v", err)
	}
}

func lockedThrough(t *testing.T, d *db.DB, tenantID uuid.UUID) (*time.Time, error) {
	t.Helper()
	var through *time.Time
	var out error
	err := d.WithTenantTx(context.Background(), tenantID, func(q db.Querier) error {
		through, out = periodlock.NewGuard().LockedThrough(context.Background(), q, tenantID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return through, out
}

func TestLockEventNeedsADate(t *testing.T) {
	d := testkit.DB(t)
	a := testkit.NewTenant(t, d)
	err := d.WithTenantTx(context.Background(), a.ID, func(q db.Querier) error {
		_, err := q.ExecContext(context.Background(), `
			INSERT INTO period_locks (id, tenant_id, action, locked_through, actor_id, created_at)
			VALUES ($1, $2, 'lock', NULL, $3, $4)`, ids.New(), a.ID, ids.New(), testkit.Epoch)
		return err
	})
	if !db.IsCheckViolation(err, "ck_period_locks__lock_date") {
		t.Fatalf("lock without a date: err = %v, want ck_period_locks__lock_date", err)
	}
}

func TestUnknownTenant(t *testing.T) {
	d := testkit.DB(t)
	ghost := ids.New()
	err := d.WithTenantTx(context.Background(), ghost, func(q db.Querier) error {
		_, err := periodlock.NewGuard().LockedThrough(context.Background(), q, ghost)
		return err
	})
	if apperr.KindOf(err) != apperr.NotFound {
		t.Fatalf("err = %v", err)
	}
}
