//go:build integration

package cli

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/clock"
	"github.com/g0-up/tally/apps/api/internal/platform/config"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/idempotency"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// serviceEnv is a complete serve/worker environment against the test database. Mail and object
// storage are configured but not contacted at start-up.
func serviceEnv(t testing.TB) map[string]string {
	t.Helper()
	return map[string]string{
		"APP_ENV":                 "test",
		"PUBLIC_BASE_URL":         "http://localhost:8080",
		"DATABASE_URL":            testkit.AppURL(t),
		"SMTP_HOST":               "localhost",
		"MAIL_FROM":               "Tally <no-reply@tally.localhost>",
		"OBJECT_STORE_ENDPOINT":   "http://localhost:9000",
		"OBJECT_STORE_BUCKET":     "tally-dev",
		"OBJECT_STORE_ACCESS_KEY": "tally-dev",
		"OBJECT_STORE_SECRET_KEY": "tally-dev-secret",
		"METRICS_ADDR":            "127.0.0.1:0",
	}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func stopWithin(t *testing.T, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("stopped with %v, want nil", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("did not stop after cancel")
	}
}

// insertExpiredKey stores an idempotency key that expired long ago.
func insertExpiredKey(t *testing.T) testkit.Tenant {
	t.Helper()
	d := testkit.DB(t)
	tn := testkit.NewTenant(t, d)
	h, err := idempotency.RequestHash("POST", "/api/v1/things", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	testkit.InTenant(t, d, tn.ID, func(q db.Querier) error {
		_, err := idempotency.NewGuard(testkit.Clock()).Begin(context.Background(), q, tn.ID, "thing.create",
			idempotency.Key{Value: "old", RequestHash: h})
		return err
	})
	return tn
}

func keysOf(t *testing.T, tn testkit.Tenant) int {
	t.Helper()
	var n int
	if err := testkit.SuperDB(t).Get(&n, `SELECT count(*) FROM tally.idempotency_keys WHERE tenant_id = $1`, tn.ID); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestServeEndToEnd(t *testing.T) {
	env := serviceEnv(t)
	env["MIGRATE_ON_START"] = "true"
	env["MIGRATE_DATABASE_URL"] = testkit.OwnerURL(t)
	setMigrations(t, testkit.Schema)
	cfg, err := config.LoadServe(mapLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	lns, err := listen("127.0.0.1:0", cfg.MetricsAddr)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil)) // slog serialises writes
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, cfg, logger, lns[0], lns[1]) }()

	api := "http://" + lns[0].Addr().String()
	waitFor(t, "api health", func() bool {
		return checkHealth(context.Background(), http.DefaultClient, api+"/healthz", time.Second) == nil
	})
	if code, body := get(t, api+"/readyz"); code != http.StatusOK {
		t.Errorf("readyz = %d %s", code, body)
	}
	// Unknown routes pass the edge chain: the loopback address is not a served host.
	if code, _ := get(t, api+"/api/v1/nothing-here"); code != http.StatusMisdirectedRequest {
		t.Errorf("request on the loopback host = %d, want 421", code)
	}
	code, metricsBody := get(t, "http://"+lns[1].Addr().String()+"/metrics")
	if code != http.StatusOK || !strings.Contains(metricsBody, "tally_http_requests_total") {
		t.Errorf("metrics = %d, missing tally_http_requests_total", code)
	}
	stopWithin(t, cancel, done)
	if !strings.Contains(logs.String(), "migrations applied") {
		t.Errorf("MIGRATE_ON_START did not run: %s", logs.String())
	}
}

func TestServeFailsWhenDatabaseIsUnreachable(t *testing.T) {
	env := serviceEnv(t)
	env["DATABASE_URL"] = "postgres://tally_app:x@127.0.0.1:1/tally?sslmode=disable&connect_timeout=2"
	cfg, err := config.LoadServe(mapLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	lns, err := listen("127.0.0.1:0", cfg.MetricsAddr)
	if err != nil {
		t.Fatal(err)
	}
	if err := runServe(context.Background(), cfg, discardLogger(), lns[0], lns[1]); err == nil {
		t.Fatal("runServe with an unreachable database succeeded")
	}
	// The listeners are released.
	if ln, err := net.Listen("tcp", lns[0].Addr().String()); err != nil {
		t.Errorf("api listener still open: %v", err)
	} else {
		_ = ln.Close()
	}
}

func TestWorkerPurgesExpiredKeysAndServesMetrics(t *testing.T) {
	tn := insertExpiredKey(t)
	cfg, err := config.LoadWorker(mapLookup(serviceEnv(t)))
	if err != nil {
		t.Fatal(err)
	}
	lns, err := listen(cfg.MetricsAddr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	sched := workerSchedule{
		dispatch: 50 * time.Millisecond, purge: time.Hour, stock: time.Hour, shift: time.Hour, sweep: time.Hour, export: time.Hour,
		subscription: time.Hour, usage: time.Hour,
	}
	go func() { done <- runWorker(ctx, cfg, sched, discardLogger(), lns[0]) }()

	waitFor(t, "expired key purge", func() bool { return keysOf(t, tn) == 0 })
	code, body := get(t, "http://"+lns[0].Addr().String()+"/metrics")
	if code != http.StatusOK || !strings.Contains(body, "tally_outbox_lag_seconds") {
		t.Errorf("worker metrics = %d, missing tally_outbox_lag_seconds", code)
	}
	stopWithin(t, cancel, done)
}

func TestMaintenancePurgeIdempotencyCommand(t *testing.T) {
	tn := insertExpiredKey(t)
	out, err := run(t, map[string]string{"DATABASE_URL": testkit.AppURL(t)}, "maintenance", "purge-idempotency")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "expired idempotency keys") || keysOf(t, tn) != 0 {
		t.Errorf("output %q, remaining keys %d", out, keysOf(t, tn))
	}
}

func TestMigrateCommandsAgainstDatabase(t *testing.T) {
	setMigrations(t, testkit.Schema)
	latest := strconv.FormatUint(uint64(testkit.SchemaVersion(t)), 10)
	clean := "schema version " + latest + " (clean)"
	env := map[string]string{"MIGRATE_DATABASE_URL": testkit.OwnerURL(t)}
	out, err := run(t, env, "migrate", "version")
	if err != nil || !strings.Contains(out, clean) {
		t.Fatalf("version = %q, %v", out, err)
	}
	// Without APP_ENV a schema change is guarded like production.
	if _, err := run(t, env, "migrate", "up"); err == nil || !strings.Contains(err.Error(), "without --backup-file") {
		t.Fatalf("up without APP_ENV = %v, want backup guard error", err)
	}
	// Up to date: with APP_ENV=test up succeeds without a backup and keeps the version.
	env["APP_ENV"] = "test"
	if out, err := run(t, env, "migrate", "up"); err != nil || !strings.Contains(out, clean) {
		t.Fatalf("up = %q, %v", out, err)
	}
	// In production the guard lets a fresh backup through.
	env["APP_ENV"] = "production"
	backup := writeBackup(t, 10, time.Now())
	if out, err := run(t, env, "migrate", "up", "--backup-file", backup); err != nil || !strings.Contains(out, "schema version "+latest) {
		t.Fatalf("production up with backup = %q, %v", out, err)
	}
	if out, err := run(t, env, "migrate", "force", latest, "--backup-file", backup); err != nil || !strings.Contains(out, clean) {
		t.Fatalf("force = %q, %v", out, err)
	}
}

func TestTenantCreateCommand(t *testing.T) {
	env := serviceEnv(t)
	env["SMTP_PORT"] = "1025"
	super := testkit.SuperDB(t)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	email := "owner-" + suffix + "@example.test"

	out, err := run(t, env, "tenant", "create", "--name", "Cửa hàng A", "--owner-email", email)
	if err != nil {
		t.Fatalf("tenant create = %q, %v", out, err)
	}
	if !strings.Contains(out, "created tenant cua-hang-a (Cửa hàng A)") || !strings.Contains(out, "http://cua-hang-a.localhost:8080/") {
		t.Errorf("output = %q", out)
	}
	var tenant struct {
		ID     string `db:"id"`
		Status string `db:"status"`
	}
	if err := super.Get(&tenant, `SELECT t.id, u.status FROM tally.tenants t JOIN tally.users u ON u.tenant_id = t.id WHERE t.slug = 'cua-hang-a' AND u.email = $1`, email); err != nil {
		t.Fatal(err)
	}
	if tenant.Status != "pending_verification" {
		t.Errorf("owner status = %s", tenant.Status)
	}
	for table, want := range map[string]int{"tally.stores": 1, "tally.warehouses": 1, "tally.tenant_settings": 1} {
		var n int
		if err := super.Get(&n, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tenant.ID); err != nil || n != want {
			t.Errorf("%s rows = %d, %v; want %d", table, n, err, want)
		}
	}
	var audits int
	if err := super.Get(&audits, `SELECT count(*) FROM tally.audit_events WHERE tenant_id = $1 AND action = 'tenant.create' AND source = 'cli'`, tenant.ID); err != nil || audits != 1 {
		t.Errorf("tenant.create audit entries = %d, %v", audits, err)
	}

	// The same name again gets the next free address; the e-mail address is already taken.
	out, err = run(t, env, "tenant", "create", "--name", "Cửa hàng A", "--owner-email", "second-"+email)
	if err != nil || !strings.Contains(out, "created tenant cua-hang-a-2 ") {
		t.Errorf("second tenant create = %q, %v", out, err)
	}
	if _, err := run(t, env, "tenant", "create", "--name", "Cửa hàng B", "--owner-email", email); err == nil {
		t.Error("an owner e-mail that already has a store must be refused")
	}
	if _, err := run(t, env, "tenant", "create", "--name", "Cửa hàng C"); err == nil || !strings.Contains(err.Error(), "owner-email") {
		t.Errorf("missing --owner-email = %v", err)
	}
}

func TestPurgeIdempotencySkipsReplacedSandboxes(t *testing.T) {
	live := insertExpiredKey(t)
	replaced := insertExpiredKey(t)
	if _, err := testkit.SuperDB(t).Exec(`UPDATE tally.tenants SET is_sandbox = true, status = 'replaced' WHERE id = $1`, replaced.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := purgeIdempotency(context.Background(), testkit.DB(t), clock.System{}); err != nil {
		t.Fatal(err)
	}
	if keysOf(t, live) != 0 || keysOf(t, replaced) != 1 {
		t.Fatalf("keys left: active %d, replaced %d; want 0 and 1", keysOf(t, live), keysOf(t, replaced))
	}
}
