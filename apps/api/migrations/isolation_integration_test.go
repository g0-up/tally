//go:build integration

package migrations_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// sampleOverrides pins columns whose generic sample value would break a multi-column CHECK, or
// fills a nullable column a CHECK requires for the chosen kind.
var sampleOverrides = map[string]map[string]string{
	"attributes":                   {"code": "ab"},
	"disassembly_lines":            {"return_base_qty": "1"},
	"landed_cost_allocation_lines": {"amount": "2"},
	"mix_groups":                   {"units_per_pack": "2"},
	"period_locks":                 {"locked_through": "2026-01-01"},
	"price_changes":                {"new_price": "1"},
	"sales_returns":                {"total_amount": "0"},
	"sales_documents":              {"source": "pos"},
	// Item lines carry a SKU; only a mix pack header line has none.
	"quote_lines":          {"sku_id": sampleSKU},
	"invoice_lines":        {"sku_id": sampleSKU},
	"sales_document_lines": {"sku_id": sampleSKU},
	"pos_cart_lines":       {"sku_id": sampleSKU},
	// Every receipt line except a wrong item is received into a lot.
	"receipt_lines": {"lot_id": sampleLot},
}

const sampleLot = "00000000-0000-7000-8000-0000000000bb"

const sampleSKU = "00000000-0000-7000-8000-0000000000aa"

type column struct {
	Name     string `db:"name"`
	Type     string `db:"type"`
	Category string `db:"category"`
	NotNull  bool   `db:"not_null"`
	Default  bool   `db:"has_default"`
}

var inListRe = regexp.MustCompile(`\(?([a-z_][a-z0-9_]*) = ANY \(ARRAY\['([^']*)'`)

// firstAllowed maps each column constrained by "col IN (...)" to the first allowed literal.
func firstAllowed(t *testing.T, x *sqlx.DB, table string) map[string]string {
	t.Helper()
	var defs []string
	if err := x.SelectContext(context.Background(), &defs, `
		SELECT pg_get_constraintdef(oid) FROM pg_constraint
		WHERE contype = 'c' AND conrelid = ('tally.' || $1)::regclass ORDER BY conname`, table); err != nil {
		t.Fatalf("%s checks: %v", table, err)
	}
	out := map[string]string{}
	for _, def := range defs {
		for _, m := range inListRe.FindAllStringSubmatch(def, -1) {
			if _, seen := out[m[1]]; !seen {
				out[m[1]] = m[2]
			}
		}
	}
	return out
}

// sampleValue is a text literal of the column type that satisfies the common CHECK shapes
// (positive quantities, factors and amounts; 2-3 letter codes; empty JSON documents).
func sampleValue(c column) (string, error) {
	switch {
	case c.Type == "uuid":
		return ids.New().String(), nil
	case strings.HasSuffix(c.Type, "[]"):
		return "{}", nil
	case c.Type == "text" || strings.HasPrefix(c.Type, "character"):
		return "AB", nil
	case c.Category == "N":
		return "1", nil
	case c.Type == "boolean":
		return "false", nil
	case c.Type == "date":
		return "2026-01-01", nil
	case strings.HasPrefix(c.Type, "timestamp"):
		return "2026-01-01 00:00:00+00", nil
	case c.Type == "jsonb" || c.Type == "json":
		return "{}", nil
	case c.Type == "inet":
		return "127.0.0.1", nil
	case c.Type == "bytea":
		return `\x00`, nil
	}
	return "", fmt.Errorf("no sample value for type %s", c.Type)
}

// sampleRow builds an INSERT for one row of table owned by tenantID: tenant_id, every NOT NULL
// column without a default, and the overrides. Values travel as text and are cast to the column
// type by the statement.
func sampleRow(t *testing.T, x *sqlx.DB, table string, tenantID uuid.UUID) (string, []any) {
	t.Helper()
	var cols []column
	if err := x.SelectContext(context.Background(), &cols, `
		SELECT a.attname::text AS name, format_type(a.atttypid, a.atttypmod) AS type,
		       ty.typcategory::text AS category, a.attnotnull AS not_null, a.atthasdef AS has_default
		FROM pg_attribute a JOIN pg_type ty ON ty.oid = a.atttypid
		WHERE a.attrelid = ('tally.' || $1)::regclass AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, table); err != nil {
		t.Fatalf("%s columns: %v", table, err)
	}
	allowed := firstAllowed(t, x, table)
	overrides := sampleOverrides[table]
	var names, casts []string
	var args []any
	for _, c := range cols {
		value, pinned := overrides[c.Name]
		switch {
		case c.Name == "tenant_id":
			value = tenantID.String()
		case pinned:
		case !c.NotNull || c.Default:
			continue
		case allowed[c.Name] != "":
			value = allowed[c.Name]
		default:
			v, err := sampleValue(c)
			if err != nil {
				t.Fatalf("%s.%s: %v", table, c.Name, err)
			}
			value = v
		}
		args = append(args, value)
		names = append(names, pgx.Identifier{c.Name}.Sanitize())
		casts = append(casts, fmt.Sprintf("$%d::text::%s", len(args), c.Type))
	}
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		pgx.Identifier{"tally", table}.Sanitize(), strings.Join(names, ", "), strings.Join(casts, ", "))
	return query, args
}

// TestEveryTenantTableIsIsolated stores one row per tenant table for tenant A, then reads and
// writes through the application roles. Foreign keys are not under test here: the row is stored
// by the superuser with session_replication_role = replica, which skips the referential triggers
// so each table is exercised without building its parent graph. CHECK, NOT NULL and UNIQUE still
// apply.
func TestEveryTenantTableIsIsolated(t *testing.T) {
	ctx := context.Background()
	super := testkit.SuperDB(t)
	app := testkit.DB(t)
	a, b := testkit.NewTenant(t, app), testkit.NewTenant(t, app)
	tables := tenantTables(t, super)

	covered := 0
	for _, table := range tables {
		t.Run(table, func(t *testing.T) {
			insert, args := sampleRow(t, super, table, a.ID)
			tx, err := super.BeginTxx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, insert, args...); err != nil {
				_ = tx.Rollback()
				t.Fatalf("store tenant A row: %v\n%s %v", err, insert, args)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}

			countIn := func(run func(context.Context, func(db.Querier) error) error) int {
				t.Helper()
				var n int
				if err := run(ctx, func(q db.Querier) error {
					return sqlx.GetContext(ctx, q, &n, `SELECT count(*) FROM `+pgx.Identifier{table}.Sanitize())
				}); err != nil {
					t.Fatalf("count: %v", err)
				}
				return n
			}
			inTenant := func(id uuid.UUID) func(context.Context, func(db.Querier) error) error {
				return func(ctx context.Context, fn func(db.Querier) error) error { return app.WithTenantTx(ctx, id, fn) }
			}
			readOnlyIn := func(id uuid.UUID) func(context.Context, func(db.Querier) error) error {
				return func(ctx context.Context, fn func(db.Querier) error) error {
					return app.WithReadOnlyTenantTx(ctx, id, fn)
				}
			}
			if n := countIn(inTenant(a.ID)); n != 1 {
				t.Errorf("tenant A sees %d rows, want 1", n)
			}
			if n := countIn(inTenant(b.ID)); n != 0 {
				t.Errorf("tenant B sees %d rows of tenant A, want 0", n)
			}
			if n := countIn(readOnlyIn(b.ID)); n != 0 {
				t.Errorf("read-only support session of tenant B sees %d rows of tenant A, want 0", n)
			}
			if n := countIn(app.WithPlatformTx); n != 0 {
				t.Errorf("a transaction without tenant sees %d rows, want 0", n)
			}

			// The same row shape under tenant B's context, still claiming tenant A.
			forged, forgedArgs := sampleRow(t, super, table, a.ID)
			err = app.WithTenantTx(ctx, b.ID, func(q db.Querier) error {
				_, err := q.ExecContext(ctx, forged, forgedArgs...)
				return err
			})
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
				t.Errorf("insert of tenant A row under tenant B: err = %v, want row-level security violation 42501", err)
			}
			if !t.Failed() {
				covered++
			}
		})
	}
	t.Logf("tenant isolation verified on %d of %d tenant tables", covered, len(tables))
	if covered != len(tables) {
		t.Errorf("isolation covered %d of %d tenant tables", covered, len(tables))
	}
}
