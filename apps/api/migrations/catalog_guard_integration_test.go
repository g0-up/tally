//go:build integration

package migrations_test

import (
	"context"
	"slices"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// tenantPolicy is how PostgreSQL renders the tenant_isolation policy expression every tenant table uses.
const tenantPolicy = `(tenant_id = (NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid)`

// tenantTables lists every table of schema tally that has a tenant_id column and is not a
// platform table, i.e. every table that must be isolated by RLS.
func tenantTables(t *testing.T, x *sqlx.DB) []string {
	t.Helper()
	var all []string
	if err := x.SelectContext(context.Background(), &all, `
		SELECT c.table_name FROM information_schema.columns c
		JOIN information_schema.tables t USING (table_schema, table_name)
		WHERE c.table_schema = 'tally' AND c.column_name = 'tenant_id' AND t.table_type = 'BASE TABLE'
		ORDER BY c.table_name`); err != nil {
		t.Fatalf("list tenant tables: %v", err)
	}
	tables := slices.DeleteFunc(all, func(name string) bool { return slices.Contains(platformTables, name) })
	if len(tables) == 0 {
		t.Fatal("no tenant tables found")
	}
	return tables
}

func count(t *testing.T, x *sqlx.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := x.GetContext(context.Background(), &n, query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func names(t *testing.T, x *sqlx.DB, query string, args ...any) []string {
	t.Helper()
	var out []string
	if err := x.SelectContext(context.Background(), &out, query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out
}

func TestCatalogHasNoProceduralObjects(t *testing.T) {
	x := testkit.SuperDB(t)
	checks := map[string]string{
		"functions or procedures in tally": `SELECT p.proname::text FROM pg_proc p WHERE p.pronamespace = 'tally'::regnamespace`,
		"user triggers":                    `SELECT tgname::text FROM pg_trigger WHERE NOT tgisinternal`,
		"event triggers":                   `SELECT evtname::text FROM pg_event_trigger`,
		"extensions beyond plpgsql":        `SELECT extname::text FROM pg_extension WHERE extname <> 'plpgsql'`,
		"views, materialized views or sequences": `
			SELECT relname::text FROM pg_class
			WHERE relnamespace = 'tally'::regnamespace AND relkind IN ('v', 'm', 'S')`,
		"rules": `SELECT rulename::text FROM pg_rules WHERE schemaname = 'tally'`,
		"enum, domain, range or composite types": `
			SELECT t.typname::text FROM pg_type t
			LEFT JOIN pg_class c ON c.oid = t.typrelid
			WHERE t.typnamespace = 'tally'::regnamespace
			  AND (t.typtype IN ('e', 'd', 'r', 'm') OR (t.typtype = 'c' AND c.relkind = 'c'))`,
		"generated or identity columns": `
			SELECT table_name || '.' || column_name FROM information_schema.columns
			WHERE table_schema = 'tally' AND (is_generated <> 'NEVER' OR is_identity = 'YES')`,
		"expression indexes": `
			SELECT c.relname::text FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
			WHERE c.relnamespace = 'tally'::regnamespace AND (i.indexprs IS NOT NULL OR 0 = ANY (i.indkey::int2[]))`,
		"function defaults": `
			SELECT table_name || '.' || column_name FROM information_schema.columns
			WHERE table_schema = 'tally' AND column_default ~ '\('`,
	}
	for what, query := range checks {
		if found := names(t, x, query); len(found) > 0 {
			t.Errorf("%s: %v", what, found)
		}
	}
}

func TestEveryTenantTableForcesTenantIsolation(t *testing.T) {
	x := testkit.SuperDB(t)
	tables := tenantTables(t, x)
	for _, table := range tables {
		var rls, force bool
		if err := x.QueryRowxContext(context.Background(), `
			SELECT relrowsecurity, relforcerowsecurity FROM pg_class
			WHERE relnamespace = 'tally'::regnamespace AND relname = $1`, table).Scan(&rls, &force); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		if !rls || !force {
			t.Errorf("%s: row security enabled=%v forced=%v, want both", table, rls, force)
		}
		var policies []struct {
			Name       string  `db:"policyname"`
			Permissive string  `db:"permissive"`
			Roles      string  `db:"roles"`
			Cmd        string  `db:"cmd"`
			Qual       *string `db:"qual"`
			WithCheck  *string `db:"with_check"`
		}
		if err := x.SelectContext(context.Background(), &policies, `
			SELECT policyname::text, permissive, roles::text, cmd, qual, with_check
			FROM pg_policies WHERE schemaname = 'tally' AND tablename = $1`, table); err != nil {
			t.Fatalf("%s policies: %v", table, err)
		}
		if len(policies) != 1 {
			t.Errorf("%s: %d policies, want exactly tenant_isolation", table, len(policies))
			continue
		}
		p := policies[0]
		if p.Name != "tenant_isolation" || p.Permissive != "PERMISSIVE" || p.Roles != "{public}" || p.Cmd != "ALL" ||
			p.Qual == nil || *p.Qual != tenantPolicy || p.WithCheck == nil || *p.WithCheck != tenantPolicy {
			t.Errorf("%s: policy %+v does not match the tenant isolation policy", table, p)
		}
	}
	t.Logf("row security verified on %d tenant tables", len(tables))

	if n := count(t, x, `SELECT count(*) FROM pg_policies WHERE schemaname = 'tally'`); n != len(tables) {
		t.Errorf("%d policies in schema tally, want one per tenant table (%d)", n, len(tables))
	}
	for _, table := range platformTables {
		var rls bool
		if err := x.QueryRowxContext(context.Background(), `
			SELECT relrowsecurity FROM pg_class
			WHERE relnamespace = 'tally'::regnamespace AND relname = $1 AND relkind = 'r'`, table).Scan(&rls); err != nil {
			t.Errorf("platform table %s: %v", table, err)
			continue
		}
		if rls {
			t.Errorf("platform table %s has row security; authentication reads it before a tenant is known", table)
		}
	}
}

func TestTenantForeignKeysAreComposite(t *testing.T) {
	x := testkit.SuperDB(t)
	tables := tenantTables(t, x)
	// A foreign key from a tenant table to another tenant table must carry tenant_id, so a row can
	// never point at another tenant's row.
	var fks []struct {
		Name      string `db:"name"`
		Composite bool   `db:"composite"`
	}
	if err := x.SelectContext(context.Background(), &fks, `
		SELECT src.relname || ' -> ' || dst.relname || ' ' || c.conname AS name,
		       EXISTS (
		         SELECT 1 FROM unnest(c.conkey, c.confkey) AS k(s, d)
		         JOIN pg_attribute sa ON sa.attrelid = c.conrelid AND sa.attnum = k.s
		         JOIN pg_attribute da ON da.attrelid = c.confrelid AND da.attnum = k.d
		         WHERE sa.attname = 'tenant_id' AND da.attname = 'tenant_id') AS composite
		FROM pg_constraint c
		JOIN pg_class src ON src.oid = c.conrelid
		JOIN pg_class dst ON dst.oid = c.confrelid
		WHERE c.contype = 'f' AND c.connamespace = 'tally'::regnamespace
		  AND src.relname = ANY ($1) AND dst.relname = ANY ($1)
		ORDER BY 1`, tables); err != nil {
		t.Fatalf("list foreign keys: %v", err)
	}
	if len(fks) == 0 {
		t.Fatal("no foreign keys between tenant tables found")
	}
	for _, fk := range fks {
		if !fk.Composite {
			t.Errorf("foreign key without tenant_id: %s", fk.Name)
		}
	}
	t.Logf("%d foreign keys between tenant tables carry tenant_id", len(fks))
}

func TestConstraintAndIndexNamesFollowConvention(t *testing.T) {
	x := testkit.SuperDB(t)
	// Go matches violations by constraint name (db.IsUniqueViolation and friends), so no name may
	// be one PostgreSQL invented. golang-migrate's own schema_migrations table is not ours.
	bad := names(t, x, `
		SELECT r.relname || '.' || c.conname FROM pg_constraint c
		JOIN pg_class r ON r.oid = c.conrelid
		WHERE c.connamespace = 'tally'::regnamespace AND r.relname <> 'schema_migrations'
		  AND NOT ((c.contype = 'p' AND c.conname = 'pk_' || r.relname)
		        OR (c.contype = 'u' AND starts_with(c.conname, 'ux_' || r.relname || '__'))
		        OR (c.contype = 'f' AND starts_with(c.conname, 'fk_' || r.relname || '__'))
		        OR (c.contype = 'c' AND starts_with(c.conname, 'ck_' || r.relname || '__')))
		ORDER BY 1`)
	for _, name := range bad {
		t.Errorf("constraint does not follow pk_<table> / ux|fk|ck_<table>__<rule>: %s", name)
	}
	bad = names(t, x, `
		SELECT r.relname || '.' || ix.relname FROM pg_index i
		JOIN pg_class r ON r.oid = i.indrelid
		JOIN pg_class ix ON ix.oid = i.indexrelid
		WHERE r.relnamespace = 'tally'::regnamespace AND r.relname <> 'schema_migrations'
		  AND NOT ((i.indisprimary AND ix.relname = 'pk_' || r.relname)
		        OR (i.indisunique AND starts_with(ix.relname, 'ux_' || r.relname || '__'))
		        OR (NOT i.indisunique AND starts_with(ix.relname, 'ix_' || r.relname || '__')))
		ORDER BY 1`)
	for _, name := range bad {
		t.Errorf("index does not follow pk_<table> / ux|ix_<table>__<cols>: %s", name)
	}
	if n := count(t, x, `SELECT count(*) FROM pg_constraint WHERE connamespace = 'tally'::regnamespace`); n < 500 {
		t.Fatalf("only %d constraints found; is the schema migrated?", n)
	}
}

func TestTenantIndexesLeadWithTenantID(t *testing.T) {
	x := testkit.SuperDB(t)
	// Every secondary index of a tenant table starts with tenant_id, so a tenant's rows are one
	// contiguous range of the index. The primary key on the global UUIDv7 id is exempt by design.
	unled := names(t, x, `
		SELECT ix.relname FROM pg_index i
		JOIN pg_class r ON r.oid = i.indrelid
		JOIN pg_class ix ON ix.oid = i.indexrelid
		LEFT JOIN pg_attribute a ON a.attrelid = r.oid AND a.attnum = i.indkey[0]
		WHERE r.relname = ANY ($1) AND NOT i.indisprimary AND a.attname IS DISTINCT FROM 'tenant_id'
		ORDER BY 1`, tenantTables(t, x))
	for _, name := range unled {
		if _, ok := unledTenantIndexes[name]; !ok {
			t.Errorf("tenant index does not lead with tenant_id: %s", name)
		}
	}
	for name := range unledTenantIndexes {
		if !slices.Contains(unled, name) {
			t.Errorf("exception %s no longer exists or now leads with tenant_id; drop it from the list", name)
		}
	}
}

func TestDocumentsReferenceTheirWarehouseCampaignAndParty(t *testing.T) {
	x := testkit.SuperDB(t)
	want := []string{
		"fk_stock_moves__warehouse_id", "fk_reservations__warehouse_id", "fk_stock_counts__warehouse_id",
		"fk_stock_adjustments__warehouse_id", "fk_receipts__warehouse_id", "fk_purchase_returns__warehouse_id",
		"fk_stock_outs__warehouse_id", "fk_sales_returns__warehouse_id", "fk_assembly_orders__warehouse_id",
		"fk_disassemblies__warehouse_id", "fk_debt_offsets__party_id", "fk_credit_notes__reason_id",
		"fk_quotes__campaign_id", "fk_invoices__campaign_id", "fk_sales_documents__campaign_id",
		"fk_sales_document_lines__campaign_id", "fk_obligations__campaign_id", "fk_assembly_orders__campaign_id",
		"ux_price_list_items__id", "ux_price_changes__id", "ux_promotion_items__id", "ux_obligation_schedules__id",
		"ux_statement_versions__id",
	}
	have := names(t, x, `SELECT conname FROM pg_constraint WHERE connamespace = 'tally'::regnamespace AND conname = ANY ($1)`, want)
	for _, name := range want {
		if !slices.Contains(have, name) {
			t.Errorf("constraint %s is missing", name)
		}
	}
}

func TestRolePrivileges(t *testing.T) {
	x := testkit.SuperDB(t)
	has := func(role, table, priv string) bool {
		t.Helper()
		var ok bool
		if err := x.GetContext(context.Background(), &ok, `SELECT has_table_privilege($1, 'tally.' || $2, $3)`, role, table, priv); err != nil {
			t.Fatalf("has_table_privilege(%s, %s, %s): %v", role, table, priv, err)
		}
		return ok
	}
	for _, table := range appendOnlyTables {
		if has("tally_app", table, "UPDATE") || has("tally_app", table, "DELETE") {
			t.Errorf("append-only %s: tally_app may UPDATE or DELETE", table)
		}
		if !has("tally_app", table, "SELECT") || !has("tally_app", table, "INSERT") {
			t.Errorf("append-only %s: tally_app must SELECT and INSERT", table)
		}
	}
	for _, table := range platformTables {
		if has("tally_readonly", table, "SELECT") {
			t.Errorf("platform table %s is readable by tally_readonly", table)
		}
	}
	for _, table := range tenantTables(t, x) {
		if !has("tally_readonly", table, "SELECT") {
			t.Errorf("tenant table %s is not readable by tally_readonly", table)
		}
		for _, priv := range []string{"SELECT", "INSERT"} {
			if !has("tally_app", table, priv) {
				t.Errorf("tenant table %s: tally_app lacks %s", table, priv)
			}
		}
		if !slices.Contains(appendOnlyTables, table) && (!has("tally_app", table, "UPDATE") || !has("tally_app", table, "DELETE")) {
			t.Errorf("tenant table %s: tally_app lacks UPDATE or DELETE", table)
		}
	}
	for _, role := range []string{"tally_app", "tally_readonly"} {
		var create bool
		if err := x.GetContext(context.Background(), &create, `SELECT has_schema_privilege($1, 'tally', 'CREATE')`, role); err != nil {
			t.Fatal(err)
		}
		if create {
			t.Errorf("%s may create objects in schema tally", role)
		}
		if has(role, "schema_migrations", "SELECT") || has(role, "schema_migrations", "UPDATE") {
			t.Errorf("%s may touch schema_migrations", role)
		}
		for _, priv := range []string{"TRUNCATE", "REFERENCES", "TRIGGER"} {
			if bad := names(t, x, `
				SELECT relname::text FROM pg_class
				WHERE relnamespace = 'tally'::regnamespace AND relkind = 'r' AND has_table_privilege($1, oid, $2)`,
				role, priv); len(bad) > 0 {
				t.Errorf("%s holds %s on %v", role, priv, bad)
			}
		}
	}
	var bypass bool
	if err := x.GetContext(context.Background(), &bypass, `SELECT rolbypassrls OR rolsuper FROM pg_roles WHERE rolname = 'tally_app'`); err != nil {
		t.Fatal(err)
	}
	if bypass {
		t.Error("tally_app bypasses row security")
	}
}

func TestNullsNotDistinctRejectsDuplicateGlobalFlag(t *testing.T) {
	ctx := context.Background()
	d := testkit.DB(t)
	key := "guard.nulls_not_distinct"
	insert := func(q db.Querier) error {
		_, err := q.ExecContext(ctx, `INSERT INTO feature_flags (key, tenant_id, enabled, updated_at) VALUES ($1, NULL, true, now())`, key)
		return err
	}
	if err := d.WithPlatformTx(ctx, insert); err != nil {
		t.Fatalf("first global flag: %v", err)
	}
	err := d.WithPlatformTx(ctx, insert)
	if !db.IsUniqueViolation(err, "ux_feature_flags__key_tenant_id") {
		t.Fatalf("second global flag with the same key: err = %v, want unique violation", err)
	}
}
