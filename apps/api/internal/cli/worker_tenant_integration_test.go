//go:build integration

package cli

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/app"
	"github.com/g0-up/tally/apps/api/internal/inventory"
	"github.com/g0-up/tally/apps/api/internal/notification"
	"github.com/g0-up/tally/apps/api/internal/org"
	"github.com/g0-up/tally/apps/api/internal/platform/audit"
	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/money"
	"github.com/g0-up/tally/apps/api/internal/platform/outbox"
	"github.com/g0-up/tally/apps/api/internal/sales"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// tenantProbe records, for every outbox message or notification sweep it is handed, the tenant
// its transaction is scoped to and the tenants whose rows that transaction can read.
type tenantProbe struct {
	mu   sync.Mutex
	seen map[uuid.UUID]int
	bad  []string
}

func newTenantProbe() *tenantProbe { return &tenantProbe{seen: map[uuid.UUID]int{}} }

// check reads app.tenant_id and the tenants of the rows of table visible to q; want is the tenant
// the work is for, uuid.Nil when only the scope itself tells it.
func (p *tenantProbe) check(ctx context.Context, q db.Querier, want uuid.UUID, table string) error {
	var scoped string
	if err := sqlx.GetContext(ctx, q, &scoped, `SELECT current_setting('app.tenant_id', true)`); err != nil {
		return err
	}
	var visible []uuid.UUID
	if err := sqlx.SelectContext(ctx, q, &visible, `SELECT DISTINCT tenant_id FROM `+table+` ORDER BY 1`); err != nil {
		return err
	}
	tenant, err := uuid.Parse(scoped)
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case err != nil:
		p.bad = append(p.bad, fmt.Sprintf("work for %s runs outside a tenant transaction (app.tenant_id %q)", want, scoped))
	case want != uuid.Nil && tenant != want:
		p.bad = append(p.bad, fmt.Sprintf("work for %s runs in a transaction of %s", want, tenant))
	case slices.ContainsFunc(visible, func(id uuid.UUID) bool { return id != tenant }):
		p.bad = append(p.bad, fmt.Sprintf("a transaction of %s reads the %s of %v", tenant, table, visible))
	default:
		p.seen[tenant]++
	}
	return nil
}

// sweepProbe is a notification producer that raises nothing and checks the scope of every sweep.
type sweepProbe struct{ p *tenantProbe }

func (sweepProbe) Kinds() []string { return []string{"tenant_probe"} }

func (s sweepProbe) Sweep(ctx context.Context, q db.Querier, _ notification.SweepTime) ([]notification.Notice, error) {
	return nil, s.p.check(ctx, q, uuid.Nil, "skus")
}

// jobErr fails on a failure a job reports for one of tenants and on one that names no tenant
// (listing the tenants, a lost connection). Other tests of the package leave minimal tenants in
// the shared database; a failure scoped to one of theirs is logged, not judged here.
func jobErr(t *testing.T, job string, err error, tenants []uuid.UUID) {
	t.Helper()
	if err == nil {
		return
	}
	failures := []error{err}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		failures = joined.Unwrap()
	}
	for _, f := range failures {
		id, scoped := failedTenant(f)
		switch {
		case !scoped:
			t.Errorf("%s failed: %v", job, f)
		case slices.Contains(tenants, id):
			t.Errorf("%s failed for %s: %v", job, id, f)
		default:
			t.Logf("%s failed for %s, a tenant of another test: %v", job, id, f)
		}
	}
}

// failedTenant reads the tenant a per-tenant job failure starts with ("tenant <id>: ...").
func failedTenant(err error) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(err.Error(), "tenant ")
	if !ok || len(rest) < 36 {
		return uuid.Nil, false
	}
	id, parseErr := uuid.Parse(rest[:36])
	return id, parseErr == nil && strings.HasPrefix(rest[36:], ": ")
}

// The worker's outbox dispatcher and every scheduled job act on each store in a transaction
// scoped to that store: a handler and a sweep see app.tenant_id of their own store and no row of
// another, and the effects of each job land in both of two stores seeded by the load generator.
func TestWorkerJobsRunInTheTransactionOfEachStore(t *testing.T) {
	out, err := run(t, loadEnv(t), "seed", "load", "--tenants", "2", "--skus", "40", "--moves", "200", "--seed", "11",
		"--slug-prefix", "worker")
	if err != nil {
		t.Fatalf("seed load = %q, %v", out, err)
	}
	super := testkit.SuperDB(t)
	database := testkit.DB(t)
	ctx := context.Background()
	var tenants []uuid.UUID
	for _, slug := range []string{"worker-11-1", "worker-11-2"} {
		tenants = append(tenants, uuid.MustParse(loadTenantID(t, super, slug)))
	}
	count := func(query string, id uuid.UUID) int {
		t.Helper()
		return loadCount(t, super, query, id.String())
	}
	logger := slog.New(slog.DiscardHandler)
	now := time.Now()
	rec := audit.NewRecorder(clock.NewFixed(now))
	domains := app.NewDomains(database, clock.NewFixed(now), rec)

	// A manual hold that expires within the hour and an SKU below its minimum, in each store.
	held := map[uuid.UUID]uuid.UUID{}
	short := map[uuid.UUID]uuid.UUID{}
	for _, id := range tenants {
		var sku uuid.UUID
		if err := super.Get(&sku, `SELECT sku_id FROM tally.sku_stock WHERE tenant_id = $1 AND sellable - reserved >= 1 ORDER BY sku_id LIMIT 1`, id); err != nil {
			t.Fatalf("a stocked SKU of %s: %v", id, err)
		}
		expires := now.Add(time.Hour)
		r, err := domains.Inventory.CreateReservation(ctx, audit.Actor{TenantID: id, Type: audit.ActorSystem, Source: audit.SourceWorker},
			inventory.ManualReservation{SkuID: sku, Qty: money.NewQtyFromInt(1), ExpiresAt: &expires})
		if err != nil {
			t.Fatalf("hold in %s: %v", id, err)
		}
		held[id] = r.ID
		if _, err := super.Exec(`UPDATE tally.skus SET min_stock = 1000000 WHERE tenant_id = $1 AND id = $2`, id, sku); err != nil {
			t.Fatal(err)
		}
		short[id] = sku
	}

	// Outbox: the handlers the worker subscribes, then a probe on every topic the two stores hold
	// and on an event each store gets now.
	dispatcher := outbox.NewDispatcher(database, clock.System{}, nil, logger)
	stock := inventory.NewService(inventory.Deps{DB: database, Clock: clock.System{}, Audit: rec})
	dispatcher.Subscribe(inventory.TopicRecostSuggested, stock.HandleRecostSuggested)
	for topic, h := range domains.CRM.Handlers() {
		dispatcher.Subscribe(topic, h)
	}
	for topic, h := range domains.Notification.Handlers() {
		dispatcher.Subscribe(topic, h)
	}
	for topic, h := range org.OnboardingHandlers() {
		dispatcher.Subscribe(topic, h)
	}
	const probeTopic = "tenant_probe.checked"
	for _, id := range tenants {
		if err := database.WithTenantTx(ctx, id, func(q db.Querier) error {
			_, err := outbox.NewPublisher(clock.System{}).Publish(ctx, q, id, outbox.Event{Topic: probeTopic, Payload: map[string]string{}})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	var topics []string
	if err := super.Select(&topics, `SELECT DISTINCT topic FROM tally.outbox_events WHERE tenant_id IN ($1, $2) ORDER BY 1`, tenants[0], tenants[1]); err != nil {
		t.Fatal(err)
	}
	handled := newTenantProbe()
	for _, topic := range topics {
		dispatcher.Subscribe(topic, func(ctx context.Context, q db.Querier, msg outbox.Message) error {
			return handled.check(ctx, q, msg.TenantID, "outbox_events")
		})
	}
	for range 100 {
		n, err := dispatcher.DispatchOnce(ctx)
		jobErr(t, "outbox dispatch", err, tenants)
		if n == 0 {
			break
		}
	}
	for _, id := range tenants {
		if n := count(`SELECT count(*) FROM tally.outbox_events WHERE tenant_id = $1 AND (processed_at IS NULL OR last_error IS NOT NULL)`, id); n != 0 {
			t.Errorf("%s has %d events left undelivered or failed", id, n)
		}
		if want := count(`SELECT count(*) FROM tally.outbox_events WHERE tenant_id = $1`, id); handled.seen[id] != want {
			t.Errorf("handlers ran %d times in a transaction of %s, want once per event (%d)", handled.seen[id], id, want)
		}
	}

	// Expired idempotency keys: everything the load generator recorded is past its TTL in 8 days.
	for _, id := range tenants {
		if count(`SELECT count(*) FROM tally.idempotency_keys WHERE tenant_id = $1`, id) == 0 {
			t.Fatalf("%s recorded no idempotency keys", id)
		}
	}
	_, err = purgeIdempotency(ctx, database, clock.NewFixed(now.Add(8*24*time.Hour)))
	jobErr(t, "idempotency purge", err, tenants)

	// Stock jobs two hours on release both holds; shift close two days on closes both shifts.
	later := clock.NewFixed(now.Add(2 * time.Hour))
	jobErr(t, "stock jobs", runStockJobs(ctx, database, inventory.NewService(inventory.Deps{DB: database, Clock: later, Audit: rec}), logger), tenants)
	for _, id := range tenants {
		if count(`SELECT count(*) FROM tally.shifts WHERE tenant_id = $1 AND status = 'open'`, id) == 0 {
			t.Fatalf("%s has no open shift", id)
		}
	}
	shifts := sales.NewService(sales.Deps{DB: database, Clock: clock.NewFixed(now.Add(48 * time.Hour)), Audit: rec})
	jobErr(t, "shift close", closeStaleShifts(ctx, database, shifts, logger), tenants)

	// The sweep runs every producer of the worker, and the probe.
	swept := newTenantProbe()
	producers := notification.NewProducerRegistry(append(notification.DefaultProducers().Producers(), sweepProbe{swept})...)
	notices := notification.NewService(notification.Deps{DB: database, Clock: clock.System{}, Producers: producers})
	jobErr(t, "notification sweep", sweepNotifications(ctx, database, notices, logger), tenants)

	if _, err := domains.SaaS.RecordUsage(ctx); err != nil {
		jobErr(t, "usage", err, tenants)
	}

	for _, id := range tenants {
		if n := count(`SELECT count(*) FROM tally.idempotency_keys WHERE tenant_id = $1`, id); n != 0 {
			t.Errorf("%s keeps %d expired idempotency keys", id, n)
		}
		var status string
		if err := super.Get(&status, `SELECT status FROM tally.reservations WHERE tenant_id = $1 AND id = $2`, id, held[id]); err != nil {
			t.Fatal(err)
		}
		if status != inventory.ReservationReleased {
			t.Errorf("the expired hold of %s is %s, want %s", id, status, inventory.ReservationReleased)
		}
		if n := count(`SELECT count(*) FROM tally.shifts WHERE tenant_id = $1 AND status = 'open'`, id); n != 0 {
			t.Errorf("%s keeps %d stale shifts open", id, n)
		}
		var raised int
		if err := super.Get(&raised, `SELECT count(*) FROM tally.notifications
			WHERE tenant_id = $1 AND kind = 'below_min_stock' AND entity_id = $2 AND resolved_at IS NULL`, id, short[id]); err != nil {
			t.Fatal(err)
		}
		if raised != 1 {
			t.Errorf("%s has %d open below-minimum notices of its short SKU, want 1", id, raised)
		}
		if swept.seen[id] != 1 {
			t.Errorf("the sweep ran %d times in a transaction of %s, want 1", swept.seen[id], id)
		}
		var usage struct {
			ActiveSKUs int `db:"active_skus"`
			Posted     int `db:"documents_posted"`
		}
		if err := super.Get(&usage, `SELECT active_skus, documents_posted FROM tally.tenant_usage_daily
			WHERE tenant_id = $1 ORDER BY usage_date DESC LIMIT 1`, id); err != nil {
			t.Fatalf("usage of %s: %v", id, err)
		}
		if want := count(`SELECT count(*) FROM tally.skus WHERE tenant_id = $1 AND status = 'active'`, id); usage.ActiveSKUs != want || want == 0 {
			t.Errorf("usage of %s counts %d active SKUs, want %d", id, usage.ActiveSKUs, want)
		}
		if usage.Posted == 0 {
			t.Errorf("usage of %s counts no posted documents", id)
		}
	}
	for _, p := range []*tenantProbe{handled, swept} {
		for _, b := range p.bad {
			t.Error(b)
		}
	}
}

// The roles the services connect as cannot step around row-level security, and the pool the
// worker and the API use connects as the application role.
func TestServiceRolesCannotBypassRowLevelSecurity(t *testing.T) {
	var roles []struct {
		Name   string `db:"rolname"`
		Super  bool   `db:"rolsuper"`
		Bypass bool   `db:"rolbypassrls"`
	}
	if err := testkit.SuperDB(t).Select(&roles, `SELECT rolname, rolsuper, rolbypassrls FROM pg_roles
		WHERE rolname IN ('tally_owner', 'tally_app', 'tally_readonly') ORDER BY rolname`); err != nil {
		t.Fatal(err)
	}
	if len(roles) != 3 {
		t.Fatalf("roles = %+v, want tally_app, tally_owner and tally_readonly", roles)
	}
	for _, r := range roles {
		if r.Super || r.Bypass {
			t.Errorf("%s: superuser %t, bypasses RLS %t; want neither", r.Name, r.Super, r.Bypass)
		}
	}
	var user string
	testkit.InTenant(t, testkit.DB(t), testkit.NewTenant(t, testkit.DB(t)).ID, func(q db.Querier) error {
		return sqlx.GetContext(context.Background(), q, &user, `SELECT current_user`)
	})
	if user != "tally_app" {
		t.Errorf("the application pool connects as %s, want tally_app", user)
	}
}
