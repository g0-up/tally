//go:build integration

package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func insertProbe(t *testing.T, d *db.DB, tenantID uuid.UUID, note string) {
	t.Helper()
	testkit.InTenant(t, d, tenantID, func(q db.Querier) error {
		_, err := q.ExecContext(context.Background(), `INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at) VALUES ($1, $2, $3, $3, now(), now())`, ids.New(), tenantID, note)
		return err
	})
}

func countProbe(t *testing.T, q db.Querier, tenantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := sqlx.GetContext(context.Background(), q, &n, `SELECT count(*) FROM parties WHERE tenant_id = $1`, tenantID); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestQueryWithoutTenantTxReturnsNoRows(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	a, b := testkit.NewTenant(t, d), testkit.NewTenant(t, d)
	insertProbe(t, d, a.ID, "visible to A only")

	var stored int
	if err := testkit.SuperDB(t).GetContext(ctx, &stored, `SELECT count(*) FROM tally.parties WHERE tenant_id = $1`, a.ID); err != nil || stored != 1 {
		t.Fatalf("superuser sees %d rows (err %v), want 1", stored, err)
	}

	if err := d.WithPlatformTx(ctx, func(q db.Querier) error {
		if n := countProbe(t, q, a.ID); n != 0 {
			t.Errorf("platform tx sees %d rows of an RLS table, want 0", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	testkit.InTenant(t, d, b.ID, func(q db.Querier) error {
		if n := countProbe(t, q, a.ID); n != 0 {
			t.Errorf("tenant B sees %d rows of tenant A, want 0", n)
		}
		return nil
	})
	testkit.InTenant(t, d, a.ID, func(q db.Querier) error {
		if n := countProbe(t, q, a.ID); n != 1 {
			t.Errorf("tenant A sees %d rows, want 1", n)
		}
		return nil
	})
}

func TestTenantTxCannotWriteAnotherTenant(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	a, b := testkit.NewTenant(t, d), testkit.NewTenant(t, d)
	err := d.WithTenantTx(ctx, a.ID, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at) VALUES ($1, $2, 'x', 'x', now(), now())`, ids.New(), b.ID)
		return err
	})
	if err == nil {
		t.Fatal("insert of another tenant's row succeeded")
	}
	if d.WithTenantTx(ctx, uuid.Nil, func(db.Querier) error { return nil }) == nil {
		t.Fatal("nil tenant accepted")
	}
}

func TestTenantTxRollsBack(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	a := testkit.NewTenant(t, d)
	boom := errors.New("boom")
	err := d.WithTenantTx(ctx, a.ID, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, `INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at) VALUES ($1, $2, 'x', 'x', now(), now())`, ids.New(), a.ID); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was swallowed")
			}
		}()
		_ = d.WithTenantTx(ctx, a.ID, func(q db.Querier) error {
			if _, err := q.ExecContext(ctx, `INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at) VALUES ($1, $2, 'y', 'y', now(), now())`, ids.New(), a.ID); err != nil {
				return err
			}
			panic("handler bug")
		})
	}()
	testkit.InTenant(t, d, a.ID, func(q db.Querier) error {
		if n := countProbe(t, q, a.ID); n != 0 {
			t.Errorf("rolled back rows visible: %d", n)
		}
		return nil
	})
}

func TestReadOnlyTenantTxRejectsWrites(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	a := testkit.NewTenant(t, d)
	insertProbe(t, d, a.ID, "existing")

	if err := d.WithReadOnlyTenantTx(ctx, a.ID, func(q db.Querier) error {
		if n := countProbe(t, q, a.ID); n != 1 {
			t.Errorf("read-only tx sees %d rows, want 1", n)
		}
		var role string
		if err := sqlx.GetContext(ctx, q, &role, `SELECT current_user`); err != nil {
			return err
		}
		if role != "tally_readonly" {
			t.Errorf("current_user = %q, want tally_readonly", role)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	err := d.WithReadOnlyTenantTx(ctx, a.ID, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `INSERT INTO parties (id, tenant_id, name, name_key, created_at, updated_at) VALUES ($1, $2, 'x', 'x', now(), now())`, ids.New(), a.ID)
		return err
	})
	if !db.IsReadOnlyViolation(err) {
		t.Fatalf("write in read-only tx: err = %v, want read-only violation", err)
	}
}

func TestAppRoleCannotUpdateAppendOnlyTables(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	a := testkit.NewTenant(t, d)
	err := d.WithTenantTx(ctx, a.ID, func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `DELETE FROM audit_events`)
		return err
	})
	if !db.IsReadOnlyViolation(err) {
		t.Fatalf("DELETE on audit_events: err = %v, want insufficient privilege", err)
	}
}

func TestUpdateWithVersion(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	a := testkit.NewTenant(t, d)
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

	err := d.WithPlatformTx(ctx, func(q db.Querier) error {
		v, err := db.UpdateWithVersion(ctx, q, "tenants", a.ID, 1, now, db.Set{Column: "display_name", Value: "Renamed"})
		if err != nil {
			return err
		}
		if v != 2 {
			t.Errorf("new version = %d, want 2", v)
		}
		var name string
		var updated time.Time
		if err := q.QueryRowxContext(ctx, `SELECT display_name, updated_at FROM tenants WHERE id = $1`, a.ID).Scan(&name, &updated); err != nil {
			return err
		}
		if name != "Renamed" || !updated.Equal(now) {
			t.Errorf("row = %q %v", name, updated)
		}

		_, err = db.UpdateWithVersion(ctx, q, "tenants", a.ID, 1, now, db.Set{Column: "display_name", Value: "Stale"})
		if apperr.KindOf(err) != apperr.PreconditionFailed {
			t.Errorf("stale version: err = %v, want precondition failed", err)
		}
		_, err = db.UpdateWithVersion(ctx, q, "tenants", ids.New(), 1, now, db.Set{Column: "display_name", Value: "Ghost"})
		if apperr.KindOf(err) != apperr.NotFound {
			t.Errorf("missing row: err = %v, want not found", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = d.WithPlatformTx(ctx, func(q db.Querier) error {
		_, err := db.UpdateWithVersion(ctx, q, "tenants", a.ID, 2, now, db.Set{Column: "no_such_column", Value: 1})
		return err
	})
	if err == nil || apperr.KindOf(err) != apperr.Internal {
		t.Errorf("bad column: err = %v, want internal", err)
	}
}

func TestTenantIDsListsTenants(t *testing.T) {
	d := testkit.DB(t)
	a := testkit.NewTenant(t, d)
	got, err := d.TenantIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range got {
		found = found || id == a.ID
	}
	if !found {
		t.Fatalf("TenantIDs misses %s", a.ID)
	}
}

func TestActiveTenantIDsSkipsReplacedSandboxesAndHeldTenants(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	live := testkit.NewTenant(t, d)
	replaced := testkit.NewTenant(t, d)
	held := testkit.NewTenant(t, d)
	err := d.WithPlatformTx(ctx, func(q db.Querier) error {
		if _, err := q.ExecContext(ctx, `UPDATE tenants SET maintenance = true WHERE id = $1`, held.ID); err != nil {
			return err
		}
		_, err := q.ExecContext(ctx, `UPDATE tenants SET is_sandbox = true, status = 'replaced' WHERE id = $1`, replaced.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	contains := func(ids []uuid.UUID, id uuid.UUID) bool {
		for _, x := range ids {
			if x == id {
				return true
			}
		}
		return false
	}
	active, err := d.ActiveTenantIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(active, live.ID) || contains(active, replaced.ID) || contains(active, held.ID) {
		t.Fatalf("ActiveTenantIDs = %v, want %s and neither %s nor %s", active, live.ID, replaced.ID, held.ID)
	}
	all, err := d.TenantIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(all, live.ID) || !contains(all, replaced.ID) {
		t.Fatalf("TenantIDs = %v, want both %s and %s", all, live.ID, replaced.ID)
	}
}

func TestMigratorVersionAndDown(t *testing.T) {
	m, err := db.NewMigrator(testkit.OwnerURL(t), testkit.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	want := testkit.SchemaVersion(t)
	v, dirty, err := m.Version()
	if err != nil || v != want || dirty {
		t.Fatalf("version = %d dirty=%v err=%v, want %d clean", v, dirty, err, want)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up when current: %v", err)
	}
	if _, err := db.NewMigrator(testkit.OwnerURL(t), nil); !errors.Is(err, db.ErrNoMigrations) {
		t.Fatalf("nil source: err = %v", err)
	}
}
