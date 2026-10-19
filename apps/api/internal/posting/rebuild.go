package posting

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/db"
)

// InTenant is the tenant in scope of a tenant transaction. Rebuild statements filter on it
// explicitly on top of row level security, so a session that bypasses RLS still touches one
// tenant only; it fails when no tenant is in scope.
const InTenant = "current_setting('app.tenant_id')::uuid"

// lockTables names the table and key column of each lock kind.
var lockTables = map[string]struct{ table, key string }{
	LockSkuStock:     {"sku_stock", "sku_id"},
	LockLots:         {"lots", "id"},
	LockLotBalances:  {"lot_balances", "lot_id"},
	LockReservations: {"reservations", "id"},
	LockObligations:  {"obligations", "id"},
	LockPayments:     {"payments", "id"},
	LockFundAccounts: {"fund_accounts", "id"},
}

// LockTenant locks every row of every lock kind of the tenant in scope, kind by kind in the
// global order and each kind in key order, as a posting touching all of them would. A rebuild
// takes it before rewriting projections, so a posting through the coordinator waits for it. A
// writer that takes none of these locks (supplier document matching, a sku_stock row inserted for
// a new SKU) can still commit under it; a repair may then write a stale value, which the verify
// that ends the rebuild finds, rolling the rebuild back.
func LockTenant(ctx context.Context, q db.Querier) error {
	for _, kind := range lockOrder {
		t, ok := lockTables[kind]
		if !ok {
			return fmt.Errorf("posting: no table for lock kind %q", kind)
		}
		rows, err := q.QueryxContext(ctx, fmt.Sprintf(`SELECT 1 FROM %s WHERE tenant_id = %s ORDER BY %s FOR UPDATE`, t.table, InTenant, t.key))
		if err != nil {
			return fmt.Errorf("posting: lock %s: %w", kind, err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("posting: lock %s: %w", kind, err)
		}
	}
	return nil
}

// Repair rewrites one projection from its ledger. Stmt runs with the rows of the verify check
// named Check as the CTE bad (id, label, stored, expected), so it rewrites exactly the rows that
// check finds wrong, to the value the check expects; $1, when Stmt uses it, is the time of the
// rebuild. Stmt rewrites rows of the tenant in scope only (InTenant).
//
// Parent names the versioned document table of the rows Stmt rewrites, when those rows carry no
// version of their own (the lines of an order): Stmt then returns the document id of each row it
// rewrote, and the rebuild bumps the version and updated_at of those documents as a posting does,
// so a client holding the old version is refused.
type Repair struct {
	Check, Stmt, Parent string
}

// Rebuilt is how many rows a rebuild rewrote for one verify check.
type Rebuilt struct {
	Check string
	Rows  int64
}

// Checks names the verify checks repairs rewrite.
func Checks(repairs []Repair) []string {
	out := make([]string, len(repairs))
	for i, r := range repairs {
		out[i] = r.Check
	}
	return out
}

// RunRepairs runs repairs in order, each seeing what the ones before it rewrote, on the check
// queries query returns by name, and returns the repairs that rewrote rows.
func RunRepairs(ctx context.Context, q db.Querier, query func(check string) (string, error), repairs []Repair, now time.Time) ([]Rebuilt, error) {
	var out []Rebuilt
	for _, r := range repairs {
		n, err := runRepair(ctx, q, query, r, now)
		if err != nil {
			return nil, fmt.Errorf("rebuild %s: %w", r.Check, err)
		}
		if n > 0 {
			out = append(out, Rebuilt{Check: r.Check, Rows: n})
		}
	}
	return out, nil
}

// runRepair runs one repair and returns how many rows it rewrote.
func runRepair(ctx context.Context, q db.Querier, query func(check string) (string, error), r Repair, now time.Time) (int64, error) {
	check, err := query(r.Check)
	if err != nil {
		return 0, err
	}
	stmt := "WITH bad AS (" + check + ")\n" + r.Stmt
	var args []any
	if strings.Contains(stmt, "$1") {
		args = append(args, now)
	}
	if r.Parent == "" {
		res, err := q.ExecContext(ctx, stmt, args...)
		if err != nil {
			return 0, err
		}
		return res.RowsAffected()
	}
	var parents []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &parents, stmt, args...); err != nil {
		return 0, err
	}
	if len(parents) == 0 {
		return 0, nil
	}
	if _, err := q.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET updated_at = $1, version = version + 1
		WHERE id = ANY($2) AND tenant_id = %s`, r.Parent, InTenant), now, parents); err != nil {
		return 0, fmt.Errorf("bump %s: %w", r.Parent, err)
	}
	return int64(len(parents)), nil
}

// StatusCheck compares the stored status of the documents of Table with the one their stored
// quantities justify, derived by the same function as the postings that move those quantities.
// Find returns the documents whose status differs. Verify reports them; a rebuild runs the check
// after its quantity repairs and moves each document to the expected status.
type StatusCheck struct {
	Name, Table string
	Find        func(ctx context.Context, q db.Querier) ([]StatusMismatch, error)
}

// StatusMismatch is a document whose stored status is not the one its quantities justify.
type StatusMismatch struct {
	ID                      uuid.UUID
	Version                 int
	Label, Stored, Expected string
}

// StatusCheckNames names checks.
func StatusCheckNames(checks []StatusCheck) []string {
	out := make([]string, len(checks))
	for i, c := range checks {
		out[i] = c.Name
	}
	return out
}

// VerifyStatuses runs checks and returns one line per mismatch, in the format of the verify
// checks: "<check> <id> (<label>): stored <status>, expected <status>".
func VerifyStatuses(ctx context.Context, q db.Querier, checks []StatusCheck) ([]string, error) {
	var out []string
	for _, c := range checks {
		found, err := c.Find(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("verify %s: %w", c.Name, err)
		}
		for _, m := range found {
			out = append(out, fmt.Sprintf("%s %s (%s): stored %s, expected %s", c.Name, m.ID, m.Label, m.Stored, m.Expected))
		}
	}
	return out, nil
}

// FixStatuses moves every document checks find to its expected status, bumping its version and
// updated_at as a posting does, and returns the checks that rewrote rows.
func FixStatuses(ctx context.Context, q db.Querier, checks []StatusCheck, now time.Time) ([]Rebuilt, error) {
	var out []Rebuilt
	for _, c := range checks {
		found, err := c.Find(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("rebuild %s: %w", c.Name, err)
		}
		for _, m := range found {
			if _, err := db.UpdateWithVersion(ctx, q, c.Table, m.ID, m.Version, now, db.Set{Column: "status", Value: m.Expected}); err != nil {
				return nil, fmt.Errorf("rebuild %s: %w", c.Name, err)
			}
		}
		if len(found) > 0 {
			out = append(out, Rebuilt{Check: c.Name, Rows: int64(len(found))})
		}
	}
	return out, nil
}
