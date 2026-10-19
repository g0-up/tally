// Package reporting holds the read models of the store's reports: the dashboard, the stock,
// sales, margin, debt, cash, kit and season reports, buying and clearance suggestions, saved
// report layouts and asynchronous CSV exports. Every figure is plain SQL over the ledgers of the
// tenant, inside its row-level-security transaction.
package reporting

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/httpx"
	"github.com/g0-up/tally/apps/api/internal/platform/objectstore"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
)

// Deps are the collaborators of Service.
type Deps struct {
	DB    *db.DB
	Clock clock.Clock
	Audit *audit.Recorder
	// Debt reads the aging and the collection schedule of the debt ledger.
	Debt DebtReader
	// Objects stores finished exports. Without it exports stay queued and downloads fail.
	Objects objectstore.Store
	// Registry is the report catalogue; nil uses DefaultRegistry.
	Registry *Registry
}

// Service runs reports, keeps saved layouts and exports report tables to CSV.
type Service struct {
	db        *db.DB
	clock     clock.Clock
	audit     *audit.Recorder
	debt      DebtReader
	objects   objectstore.Store
	registry  *Registry
	publisher *outbox.Publisher
}

// NewService returns the service.
func NewService(d Deps) *Service {
	reg := d.Registry
	if reg == nil {
		reg = DefaultRegistry()
	}
	return &Service{
		db: d.DB, clock: d.Clock, audit: d.Audit, debt: d.Debt, objects: d.Objects, registry: reg,
		publisher: outbox.NewPublisher(d.Clock),
	}
}

// Registry is the report catalogue the service runs.
func (s *Service) Registry() *Registry { return s.registry }

// Scope is the tenant a request acts on. ReadOnly is set for consented support sessions: their
// reads run in a READ ONLY transaction under the tally_readonly role.
type Scope struct {
	TenantID uuid.UUID
	ReadOnly bool
	// q is the snapshot transaction of an export: every read of the run joins it instead of
	// opening its own.
	q db.Querier
}

func (s *Service) read(ctx context.Context, scope Scope, fn func(q db.Querier) error) error {
	if scope.q != nil {
		return fn(scope.q)
	}
	if scope.ReadOnly {
		return s.db.WithReadOnlyTenantTx(ctx, scope.TenantID, fn)
	}
	return s.db.WithTenantTx(ctx, scope.TenantID, fn)
}

// readOnlyZone dates reads under the read-only role (consented support sessions), which cannot
// read the tenants table, when the transaction carries no tenant time zone (db.ReadOnlyTimezone).
const readOnlyZone = "Asia/Ho_Chi_Minh"

// tenantLocation reads the time zone of the tenant of q.
func tenantLocation(ctx context.Context, q db.Querier) (*time.Location, error) {
	var readsTenant bool
	if err := q.QueryRowxContext(ctx, `SELECT has_table_privilege('tenants', 'SELECT')`).Scan(&readsTenant); err != nil {
		return nil, fmt.Errorf("reporting: read tenant privileges: %w", err)
	}
	name, err := db.ReadOnlyTimezone(ctx, q, readOnlyZone)
	if err != nil {
		return nil, err
	}
	if readsTenant {
		if err := q.QueryRowxContext(ctx, `SELECT timezone FROM tenants WHERE id = current_setting('app.tenant_id')::uuid`).Scan(&name); err != nil {
			return nil, fmt.Errorf("reporting: read tenant time zone: %w", err)
		}
	}
	return clock.LoadLocation(name)
}

// Today is the tenant's business date now.
func (s *Service) Today(ctx context.Context, scope Scope) (time.Time, error) {
	var today time.Time
	err := s.read(ctx, scope, func(q db.Querier) error {
		loc, err := tenantLocation(ctx, q)
		if err != nil {
			return err
		}
		today = clock.BusinessDate(s.clock.Now(), loc)
		return nil
	})
	return today, err
}

// Page is the page of a paged report a request asks for.
type Page struct {
	After *httpx.Cursor
	Limit int
}

// Run runs the report named by key or slug with raw query parameters.
func (s *Service) Run(ctx context.Context, scope Scope, name string, raw map[string]string, page Page) (Table, error) {
	rep, ok := s.registry.Lookup(name)
	if !ok {
		return Table{}, apperr.NewNotFound(CodeUnknownReport, "report not found")
	}
	today, err := s.Today(ctx, scope)
	if err != nil {
		return Table{}, err
	}
	p, err := rep.resolve(raw, today)
	if err != nil {
		return Table{}, err
	}
	p.After, p.Limit = page.After, page.Limit
	return s.runResolved(ctx, scope, rep, p)
}

func (s *Service) runResolved(ctx context.Context, scope Scope, rep Report, p Params) (Table, error) {
	if p.Limit <= 0 {
		p.Limit = 50
	}
	t, err := rep.run(ctx, s, scope, p)
	if err != nil {
		return Table{}, err
	}
	t.ReportKey, t.Title, t.GroupBy = rep.Key, rep.Title, p.GroupBy
	if t.Columns == nil {
		t.Columns = rep.Columns
	}
	switch rep.Range {
	case rangePeriod, rangeFuture:
		from, to := p.From, p.To
		t.From, t.To, t.Days = &from, &to, daysBetween(from, to)
	case rangeAsOf:
		asOf := p.AsOf
		t.AsOf = &asOf
	}
	if t.Rows == nil {
		t.Rows = []Row{}
	}
	if !rep.Paged {
		t.Next = nil
	}
	return project(t, p.Columns), nil
}

// eachRow runs query and scans its rows into T one at a time, in order, so a long result streams
// to fn instead of being held in memory.
func eachRow[T any](ctx context.Context, q db.Querier, query string, args []any, fn func(T) error) (err error) {
	rows, err := q.QueryxContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	for rows.Next() {
		var r T
		if err := rows.StructScan(&r); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}

// pageOf trims rows fetched with limit+1 to the page and returns the cursor of the next page.
func pageOf[T any](rows []T, limit int, cursorOf func(T) httpx.Cursor) ([]T, *httpx.Cursor) {
	if len(rows) <= limit {
		return rows, nil
	}
	rows = rows[:limit]
	c := cursorOf(rows[len(rows)-1])
	return rows, &c
}
