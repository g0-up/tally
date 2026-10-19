package db

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"slices"
	"sort"

	"github.com/jmoiron/sqlx"
)

// JSONList scans a JSON array column, such as to_jsonb(text_array), into a Go slice; database/sql
// cannot scan PostgreSQL arrays directly. NULL scans as an empty list.
type JSONList[T any] []T

// Scan implements sql.Scanner.
func (l *JSONList[T]) Scan(src any) error {
	var raw []byte
	switch v := src.(type) {
	case nil:
		*l = JSONList[T]{}
		return nil
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("scan json list: unsupported type %T", src)
	}
	var out []T
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("scan json list: %w", err)
	}
	if out == nil {
		out = []T{}
	}
	*l = out
	return nil
}

// Value implements driver.Valuer as a JSON array.
func (l JSONList[T]) Value() (driver.Value, error) {
	if l == nil {
		return []byte("[]"), nil
	}
	return json.Marshal([]T(l))
}

// TenantTable is a table of tenant rows: it has a tenant_id column and row security.
type TenantTable struct {
	Name    string
	Columns []string
	// Deferred are references of this table left out of the order to break a reference cycle.
	Deferred []DeferredRef
}

// DeferredRef is a foreign key left out of the parents-first order because it closes a cycle
// (a quote and the invoice it became reference each other). Its columns are nullable: a copy
// writes them once every table is in, a purge clears them before deleting.
type DeferredRef struct {
	Constraint string
	// Columns are the referencing columns other than tenant_id.
	Columns []string
}

// TenantTables lists the tenant tables of schema tally with their columns in declaration order,
// every table before the tables that reference it, apart from the Deferred references. It reads
// the live catalog, so the list follows the migrations without a hand-kept copy. Full data
// exports, tenant copies and restore checks walk it.
func TenantTables(ctx context.Context, q Querier) ([]TenantTable, error) {
	var rows []struct {
		Name    string           `db:"name"`
		Columns JSONList[string] `db:"columns"`
	}
	if err := sqlx.SelectContext(ctx, q, &rows, `
		SELECT c.relname AS name, to_jsonb(array_agg(a.attname::text ORDER BY a.attnum)) AS columns
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
		WHERE n.nspname = 'tally' AND c.relkind = 'r' AND c.relrowsecurity
		  AND EXISTS (SELECT 1 FROM pg_attribute t
		              WHERE t.attrelid = c.oid AND t.attname = 'tenant_id' AND NOT t.attisdropped)
		GROUP BY c.relname
		ORDER BY c.relname`); err != nil {
		return nil, fmt.Errorf("list tenant tables: %w", err)
	}
	tables := make(map[string]TenantTable, len(rows))
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		tables[r.Name] = TenantTable{Name: r.Name, Columns: r.Columns}
		names = append(names, r.Name)
	}
	order, deferred, err := orderTables(ctx, q, names)
	if err != nil {
		return nil, err
	}
	out := make([]TenantTable, len(order))
	for i, n := range order {
		t := tables[n]
		t.Deferred = deferred[n]
		out[i] = t
	}
	return out, nil
}

// TenantOwnedTable is a table that holds rows of a tenant: by its own tenant_id column, or, when
// Via is set, through a reference to a table that has one (such as the messages of a ticket).
type TenantOwnedTable struct {
	Name     string
	Via      *TenantOwnedVia
	Deferred []DeferredRef
}

// TenantOwnedVia is the single-column reference that ties a row without tenant_id to its tenant:
// Column of the table references ParentColumn of Parent.
type TenantOwnedVia struct {
	Column, Parent, ParentColumn string
}

// TenantOwnedTables lists every table of schema tally holding tenant rows, with or without row
// security, every table before the tables that reference it apart from the Deferred references.
// The tenants table itself and retired_tenant_slugs (which outlives the tenant on purpose) are
// not listed. Tenant purges walk it in reverse.
func TenantOwnedTables(ctx context.Context, q Querier) ([]TenantOwnedTable, error) {
	var direct []string
	if err := sqlx.SelectContext(ctx, q, &direct, `
		SELECT c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_id' AND NOT a.attisdropped
		WHERE n.nspname = 'tally' AND c.relkind = 'r' AND c.relname NOT IN ('tenants', 'retired_tenant_slugs')
		ORDER BY c.relname`); err != nil {
		return nil, fmt.Errorf("list tenant-owned tables: %w", err)
	}
	var vias []struct {
		Child        string `db:"child"`
		Column       string `db:"col"`
		Parent       string `db:"parent"`
		ParentColumn string `db:"parent_col"`
	}
	if err := sqlx.SelectContext(ctx, q, &vias, `
		SELECT DISTINCT ON (c.relname) c.relname AS child, ca.attname AS col, p.relname AS parent, pa.attname AS parent_col
		FROM pg_constraint k
		JOIN pg_class c ON c.oid = k.conrelid
		JOIN pg_class p ON p.oid = k.confrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute ca ON ca.attrelid = c.oid AND ca.attnum = k.conkey[1]
		JOIN pg_attribute pa ON pa.attrelid = p.oid AND pa.attnum = k.confkey[1]
		WHERE k.contype = 'f' AND n.nspname = 'tally' AND cardinality(k.conkey) = 1 AND c.oid <> p.oid
		  AND p.relname NOT IN ('tenants', 'retired_tenant_slugs')
		  AND EXISTS (SELECT 1 FROM pg_attribute t WHERE t.attrelid = p.oid AND t.attname = 'tenant_id' AND NOT t.attisdropped)
		  AND NOT EXISTS (SELECT 1 FROM pg_attribute t WHERE t.attrelid = c.oid AND t.attname = 'tenant_id' AND NOT t.attisdropped)
		ORDER BY c.relname, k.conname`); err != nil {
		return nil, fmt.Errorf("list tenant-owned dependent tables: %w", err)
	}
	tables := make(map[string]TenantOwnedTable, len(direct)+len(vias))
	names := make([]string, 0, len(direct)+len(vias))
	for _, n := range direct {
		tables[n] = TenantOwnedTable{Name: n}
		names = append(names, n)
	}
	for _, v := range vias {
		tables[v.Child] = TenantOwnedTable{Name: v.Child, Via: &TenantOwnedVia{Column: v.Column, Parent: v.Parent, ParentColumn: v.ParentColumn}}
		names = append(names, v.Child)
	}
	order, deferred, err := orderTables(ctx, q, names)
	if err != nil {
		return nil, err
	}
	out := make([]TenantOwnedTable, len(order))
	for i, n := range order {
		t := tables[n]
		t.Deferred = deferred[n]
		out[i] = t
	}
	return out, nil
}

// tableReference is a foreign key of schema tally between two distinct tables.
type tableReference struct {
	Child      string           `db:"child"`
	Parent     string           `db:"parent"`
	Constraint string           `db:"constraint_name"`
	Columns    JSONList[string] `db:"columns"`  // referencing columns other than tenant_id
	Nullable   bool             `db:"nullable"` // every column of Columns accepts NULL
}

// orderTables orders names parents first along the foreign keys among them.
func orderTables(ctx context.Context, q Querier, names []string) ([]string, map[string][]DeferredRef, error) {
	var refs []tableReference
	if err := sqlx.SelectContext(ctx, q, &refs, `
		SELECT c.relname AS child, p.relname AS parent, k.conname AS constraint_name,
		  to_jsonb(ARRAY(SELECT a.attname::text FROM unnest(k.conkey) WITH ORDINALITY u(num, ord)
		                 JOIN pg_attribute a ON a.attrelid = k.conrelid AND a.attnum = u.num
		                 WHERE a.attname <> 'tenant_id' ORDER BY u.ord)) AS columns,
		  NOT EXISTS (SELECT 1 FROM unnest(k.conkey) u(num)
		              JOIN pg_attribute a ON a.attrelid = k.conrelid AND a.attnum = u.num
		              WHERE a.attname <> 'tenant_id' AND a.attnotnull) AS nullable
		FROM pg_constraint k
		JOIN pg_class c ON c.oid = k.conrelid
		JOIN pg_class p ON p.oid = k.confrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE k.contype = 'f' AND n.nspname = 'tally' AND c.oid <> p.oid
		ORDER BY c.relname, k.conname`); err != nil {
		return nil, nil, fmt.Errorf("list tenant table references: %w", err)
	}
	return parentsFirst(names, refs)
}

// parentsFirst orders names so every name follows its parents, alphabetically among equals.
// When the remaining references form a cycle it defers one nullable reference on the cycle (the
// first by table and constraint name) and goes on; a cycle of required references is an error.
func parentsFirst(names []string, refs []tableReference) ([]string, map[string][]DeferredRef, error) {
	names = slices.Clone(names)
	sort.Strings(names)
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
	}
	var active []tableReference
	for _, r := range refs {
		if known[r.Child] && known[r.Parent] {
			active = append(active, r)
		}
	}
	placed := make(map[string]bool, len(names))
	deferred := map[string][]DeferredRef{}
	out := make([]string, 0, len(names))
	for len(out) < len(names) {
		progressed := false
		for _, n := range names {
			if placed[n] {
				continue
			}
			ready := true
			for _, r := range active {
				if r.Child == n && !placed[r.Parent] {
					ready = false
					break
				}
			}
			if ready {
				placed[n] = true
				out = append(out, n)
				progressed = true
			}
		}
		if progressed {
			continue
		}
		i := cycleBreak(active, placed)
		if i < 0 {
			return nil, nil, fmt.Errorf("list tenant tables: required foreign keys form a cycle among %d tables", len(names)-len(out))
		}
		r := active[i]
		deferred[r.Child] = append(deferred[r.Child], DeferredRef{Constraint: r.Constraint, Columns: r.Columns})
		active = slices.Delete(active, i, i+1)
	}
	return out, deferred, nil
}

// cycleBreak returns the index of the first nullable reference between unplaced tables whose
// parent leads back to its child, or -1.
func cycleBreak(active []tableReference, placed map[string]bool) int {
	reaches := func(from, to string) bool {
		seen := map[string]bool{from: true}
		next := []string{from}
		for len(next) > 0 {
			n := next[len(next)-1]
			next = next[:len(next)-1]
			if n == to {
				return true
			}
			for _, r := range active {
				if r.Child == n && !placed[r.Parent] && !seen[r.Parent] {
					seen[r.Parent] = true
					next = append(next, r.Parent)
				}
			}
		}
		return false
	}
	for i, r := range active {
		if r.Nullable && len(r.Columns) > 0 && !placed[r.Child] && !placed[r.Parent] && reaches(r.Parent, r.Child) {
			return i
		}
	}
	return -1
}
