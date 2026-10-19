//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/testkit"
	"github.com/g0-up/tally/apps/api/migrations"
)

// schemaSnapshot renders every table, column, constraint, index, policy and grant of schema tally
// as sorted lines, so two migrated databases can be compared exactly.
const schemaSnapshot = `
SELECT line FROM (
  SELECT 'column ' || table_name || '.' || column_name || ' ' || data_type || ' ' || is_nullable
         || ' ' || coalesce(column_default, '') AS line
  FROM information_schema.columns WHERE table_schema = 'tally'
  UNION ALL
  SELECT 'constraint ' || conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid)
  FROM pg_constraint WHERE connamespace = 'tally'::regnamespace
  UNION ALL
  SELECT 'index ' || indexdef FROM pg_indexes WHERE schemaname = 'tally'
  UNION ALL
  SELECT 'policy ' || tablename || ' ' || policyname || ' ' || coalesce(qual, '') || ' ' || coalesce(with_check, '')
  FROM pg_policies WHERE schemaname = 'tally'
  UNION ALL
  SELECT 'rls ' || relname || ' ' || relrowsecurity || ' ' || relforcerowsecurity
  FROM pg_class WHERE relnamespace = 'tally'::regnamespace AND relkind = 'r'
  UNION ALL
  SELECT 'grant ' || table_name || ' ' || grantee || ' ' || privilege_type
  FROM information_schema.role_table_grants WHERE table_schema = 'tally'
) s ORDER BY line`

func TestMigrationsAreReversible(t *testing.T) {
	ctx := context.Background()
	super := testkit.SuperDB(t)
	const dbName = "tally_reversibility"
	if _, err := super.ExecContext(ctx, `CREATE DATABASE `+dbName+` OWNER tally_owner`); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := super.ExecContext(cctx, `DROP DATABASE `+dbName+` WITH (FORCE)`); err != nil {
			t.Errorf("drop database: %v", err)
		}
	})

	u, err := url.Parse(testkit.OwnerURL(t))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + dbName
	ownerURL := u.String()

	owner, err := sqlx.Open("pgx", ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	// The same bootstrap infra/postgres/roles.sql performs for the main database, minus the
	// default privileges: the privileges migration must grant everything on its own.
	for _, stmt := range []string{
		`CREATE SCHEMA tally AUTHORIZATION tally_owner`,
		`GRANT USAGE ON SCHEMA tally TO tally_app, tally_readonly`,
	} {
		if _, err := owner.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	snapshot := func() []string {
		t.Helper()
		var lines []string
		if err := owner.SelectContext(ctx, &lines, schemaSnapshot); err != nil {
			t.Fatalf("schema snapshot: %v", err)
		}
		return lines
	}
	m, err := db.NewMigrator(ownerURL, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	assertVersion := func(want uint) {
		t.Helper()
		v, dirty, err := m.Version()
		if err != nil || dirty || v != want {
			t.Fatalf("version = %d dirty=%v err=%v, want %d clean", v, dirty, err, want)
		}
	}
	latest := testkit.SchemaVersion(t)

	if err := m.Up(); err != nil {
		t.Fatalf("first up: %v", err)
	}
	assertVersion(latest)
	first := snapshot()

	if err := m.Down(0); err != nil {
		t.Fatalf("down all: %v", err)
	}
	assertVersion(0)
	var left []string
	if err := owner.SelectContext(ctx, &left, `
		SELECT relname::text FROM pg_class
		WHERE relnamespace = 'tally'::regnamespace AND relname NOT LIKE 'schema_migrations%'
		ORDER BY 1`); err != nil {
		t.Fatal(err)
	}
	if len(left) > 0 {
		t.Fatalf("down all left objects behind: %v", left)
	}

	if err := m.Up(); err != nil {
		t.Fatalf("second up: %v", err)
	}
	assertVersion(latest)
	if second := snapshot(); !slices.Equal(first, second) {
		for _, l := range first {
			if !slices.Contains(second, l) {
				t.Errorf("lost after down/up: %s", l)
			}
		}
		for _, l := range second {
			if !slices.Contains(first, l) {
				t.Errorf("new after down/up: %s", l)
			}
		}
	}
	t.Logf("up, down all, up: %d migrations, %d schema facts identical", latest, len(first))

	// Rolling back the widened report checks folds the rows only the wider checks allow.
	const widenedReports = 43
	tenant, job, big := uuid.New(), uuid.New(), uuid.New()
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	if _, err := owner.ExecContext(ctx, `
		INSERT INTO tally.tenants (id, slug, legal_name, display_name, created_at, updated_at)
		VALUES ($1, 'down-data', 'Down data', 'Down data', $2, $2)`, tenant, now); err != nil {
		t.Fatal(err)
	}
	tx, err := owner.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`SELECT set_config('app.tenant_id', $1::text, true)`,
		`INSERT INTO tally.saved_reports (id, tenant_id, report_key, name, config, created_at, updated_at)
		 VALUES (gen_random_uuid(), $1, 'lot_stock', 'Lô', '{}', now(), now())`,
		`INSERT INTO tally.export_jobs (id, tenant_id, kind, params, status, requested_by, created_at)
		 VALUES (gen_random_uuid(), $1, 'report_csv', '{}', 'cancelled', gen_random_uuid(), now())`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, tenant); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tally.attachments (id, tenant_id, owner_type, owner_id, storage_key, file_name, content_type,
			size_bytes, sha256, scan_status, uploaded_by, created_at)
		VALUES ($1, $2, 'export_job', $3, 'tenants/x/exports/big.csv', 'big.csv', 'text/csv', 104857600, '\x00', 'clean', $3, now())`,
		big, tenant, job); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tally.export_jobs (id, tenant_id, kind, params, status, attachment_id, requested_by, created_at)
		VALUES ($1, $2, 'report_csv', '{}', 'succeeded', $3, $1, now())`, job, tenant, big); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := m.Down(int(latest) - widenedReports + 1); err != nil {
		t.Fatalf("down past the widened report checks with data: %v", err)
	}
	assertVersion(widenedReports - 1)
	var folded struct {
		Saved     int `db:"saved"`
		Cancelled int `db:"cancelled"`
		Big       int `db:"big"`
		Expired   int `db:"expired"`
	}
	read, err := owner.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = read.Rollback() }()
	if _, err := read.ExecContext(ctx, `SELECT set_config('app.tenant_id', $1::text, true)`, tenant); err != nil {
		t.Fatal(err)
	}
	if err := read.GetContext(ctx, &folded, `
		SELECT (SELECT count(*) FROM tally.saved_reports) AS saved,
			(SELECT count(*) FROM tally.export_jobs WHERE status = 'cancelled') AS cancelled,
			(SELECT count(*) FROM tally.attachments) AS big,
			(SELECT count(*) FROM tally.export_jobs WHERE status = 'expired' AND attachment_id IS NULL) AS expired`); err != nil {
		t.Fatal(err)
	}
	if err := read.Rollback(); err != nil {
		t.Fatal(err)
	}
	if folded.Saved != 0 || folded.Cancelled != 0 || folded.Big != 0 || folded.Expired != 1 {
		t.Fatalf("rows the narrower checks refuse survived the rollback: %+v", folded)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up after the rollback with data: %v", err)
	}
	assertVersion(latest)

	// Rolling forward over the add-on source of entitlement rows names the add-on of a module row
	// only where the store's paid add-on invoices starting that day are all of one plan.
	const addonSource = 58
	if err := m.Down(int(latest) - addonSource + 1); err != nil {
		t.Fatalf("down past the add-on source with data: %v", err)
	}
	assertVersion(addonSource - 1)
	const aiQA, storePlan = "00000000-0000-7000-8000-000000000006", "00000000-0000-7000-8000-000000000002"
	other, sub := uuid.New(), uuid.New()
	single, mixed, unpaid, planRow := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO tally.plans (id, code, name, kind, billing_period, price, modules, limits, active, created_at, updated_at)
		  VALUES ($1, 'addon_down_data', 'Down data', 'addon', 'month', 0, '{ai_qa}', '{}', true, $2, $2)`, []any{other, now}},
		{`INSERT INTO tally.subscriptions (id, tenant_id, plan_id, status, current_period_start, current_period_end, created_at, updated_at)
		  VALUES ($1, $2, $3, 'active', '2026-03-01', '2027-02-28', $4, $4)`, []any{sub, tenant, storePlan, now}},
		{`INSERT INTO tally.saas_invoices (id, tenant_id, number, plan_id, purpose, period_start, period_end, amount, status, issued_at)
		  VALUES (gen_random_uuid(), $1, 'DOWN-1', $2, 'addon', '2026-03-01', '2026-03-31', 0, 'paid', $4),
		         (gen_random_uuid(), $1, 'DOWN-2', $3, 'new_period', '2026-03-01', '2027-02-28', 0, 'paid', $4),
		         (gen_random_uuid(), $1, 'DOWN-3', $2, 'addon', '2026-04-01', '2026-04-30', 0, 'paid', $4),
		         (gen_random_uuid(), $1, 'DOWN-4', $5, 'addon', '2026-04-01', '2026-04-30', 0, 'paid', $4),
		         (gen_random_uuid(), $1, 'DOWN-5', $2, 'addon', '2026-05-01', '2026-05-31', 0, 'open', $4)`,
			[]any{tenant, aiQA, storePlan, now, other}},
		{`INSERT INTO tally.entitlements (id, tenant_id, key, value, source_subscription_id, valid_from, valid_to, created_at)
		  VALUES ($1, $5, 'module:ai_qa', 'true', NULL, '2026-03-01', '2026-03-31', $6),
		         ($2, $5, 'module:ai_qa', 'true', NULL, '2026-04-01', '2026-04-30', $6),
		         ($3, $5, 'module:ai_qa', 'true', NULL, '2026-05-01', '2026-05-31', $6),
		         ($4, $5, 'module:pos', 'true', $7, '2026-03-01', NULL, $6)`,
			[]any{single, mixed, unpaid, planRow, tenant, now, sub}},
	} {
		if _, err := owner.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("%s: %v", stmt.sql, err)
		}
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up over the add-on source with data: %v", err)
	}
	assertVersion(latest)
	var sources []struct {
		ID   uuid.UUID  `db:"id"`
		Plan *uuid.UUID `db:"source_plan_id"`
	}
	if err := owner.SelectContext(ctx, &sources, `SELECT id, source_plan_id FROM tally.entitlements WHERE tenant_id = $1`,
		tenant); err != nil {
		t.Fatal(err)
	}
	want := map[uuid.UUID]string{single: aiQA, mixed: "", unpaid: "", planRow: ""}
	if len(sources) != len(want) {
		t.Fatalf("entitlement rows = %d, want %d", len(sources), len(want))
	}
	for _, r := range sources {
		got := ""
		if r.Plan != nil {
			got = r.Plan.String()
		}
		if got != want[r.ID] {
			t.Errorf("source plan of row %s = %q, want %q", r.ID, got, want[r.ID])
		}
	}
	// A row comes from a plan's subscription or from an add-on plan, never from both.
	if _, err := owner.ExecContext(ctx, `UPDATE tally.entitlements SET source_plan_id = $2 WHERE id = $1`, planRow, aiQA); !db.IsCheckViolation(err,
		"ck_entitlements__single_source") {
		t.Errorf("a plan row naming an add-on plan: %v, want the single source check to refuse it", err)
	}

	// Rows of two add-on plans starting the same day are two rows; on the way back only the one
	// ending last stays, and it folds into a plan row of its module starting that day.
	longest := uuid.New()
	if _, err := owner.ExecContext(ctx, `
		INSERT INTO tally.entitlements (id, tenant_id, key, value, source_plan_id, valid_from, valid_to, created_at)
		VALUES (gen_random_uuid(), $1, 'module:pos', 'true', $2, '2026-03-01', '2026-03-31', $4),
		       (gen_random_uuid(), $1, 'module:pos', 'true', $3, '2026-03-01', '2026-04-15', $4),
		       ($5, $1, 'module:ai_qa', 'true', $2, '2026-06-01', '2026-06-30', $4),
		       (gen_random_uuid(), $1, 'module:ai_qa', 'true', $3, '2026-06-01', '2026-06-20', $4)`,
		tenant, aiQA, other, now, longest); err != nil {
		t.Fatal(err)
	}
	if err := m.Down(int(latest) - addonSource + 1); err != nil {
		t.Fatalf("down past the add-on source with twin rows: %v", err)
	}
	type dayRow struct {
		ID      uuid.UUID `db:"id"`
		ValidTo *string   `db:"valid_to"`
	}
	rowsOf := func(key, from string) []dayRow {
		t.Helper()
		var out []dayRow
		if err := owner.SelectContext(ctx, &out, `
			SELECT id, valid_to::text AS valid_to FROM tally.entitlements
			WHERE tenant_id = $1 AND key = $2 AND valid_from = $3::date`, tenant, key, from); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if pos := rowsOf("module:pos", "2026-03-01"); len(pos) != 1 || pos[0].ID != planRow || pos[0].ValidTo == nil ||
		*pos[0].ValidTo != "2026-04-15" {
		t.Fatalf("pos rows after the rollback = %+v, want the plan row ending 2026-04-15", pos)
	}
	if ai := rowsOf("module:ai_qa", "2026-06-01"); len(ai) != 1 || ai[0].ID != longest {
		t.Fatalf("AI Q&A rows of one day after the rollback = %+v, want the one ending last", ai)
	}
	if err := m.Up(); err != nil {
		t.Fatalf("up after folding the twin rows: %v", err)
	}
	assertVersion(latest)
}
