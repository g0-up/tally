//go:build integration

package cli

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/platform/apperr"
	"github.com/g0-up/tally/apps/api/internal/platform/db"
	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// createStore provisions a store with its owner through the CLI and returns its id and slug.
func createStore(t *testing.T, name string) (uuid.UUID, string) {
	t.Helper()
	env := serviceEnv(t)
	env["SMTP_PORT"] = "1025"
	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	slug := "restore-" + suffix
	if out, err := run(t, env, "tenant", "create", "--name", name, "--slug", slug, "--owner-email", "owner-"+suffix+"@example.test"); err != nil {
		t.Fatalf("tenant create = %q, %v", out, err)
	}
	var id uuid.UUID
	if err := testkit.SuperDB(t).Get(&id, `SELECT id FROM tally.tenants WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	return id, slug
}

// addCustomer inserts a customer of the tenant and returns its id.
func addCustomer(t *testing.T, super *sqlx.DB, tenantID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := ids.New()
	now := time.Now().UTC()
	if _, err := super.Exec(`
		INSERT INTO tally.parties (id, tenant_id, kind, name, name_key, phone, phone_key, created_at, updated_at)
		VALUES ($1, $2, 'person', $3, lower($3), '0901234567', '0901234567', $4, $4)`, id, tenantID, name, now); err != nil {
		t.Fatal(err)
	}
	return id
}

func countWhere(t *testing.T, super *sqlx.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := super.Get(&n, query, args...); err != nil {
		t.Fatal(err)
	}
	return n
}

// tenantFingerprint is every row of the tenant in every tenant table, as text, so a test can
// prove a command left the tenant exactly as it was.
func tenantFingerprint(t *testing.T, super *sqlx.DB, tenantID uuid.UUID) map[string]string {
	t.Helper()
	tx, err := super.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	tables, err := db.TenantTables(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(tables))
	for _, tb := range tables {
		var rows string
		if err := tx.Get(&rows, `SELECT COALESCE(string_agg(t::text, E'\n' ORDER BY t::text), '') FROM tally.`+tb.Name+` t WHERE tenant_id = $1`, tenantID); err != nil {
			t.Fatalf("%s: %v", tb.Name, err)
		}
		out[tb.Name] = rows
	}
	return out
}

func TestTenantRestoreCheckAndCopy(t *testing.T) {
	super := testkit.SuperDB(t)
	store, _ := createStore(t, "Cửa hàng khôi phục")
	other := testkit.NewTenant(t, testkit.DB(t))
	customer := addCustomer(t, super, store, "Chị Lan")
	otherCustomer := addCustomer(t, super, other.ID, "Anh Minh")

	scratch := testkit.RestoreScratch(t)
	live := map[string]string{"DATABASE_URL": testkit.AppURL(t)}

	out, err := run(t, live, "tenant", "restore-check", "--tenant", store.String(), "--scratch-url", scratch.AppURL)
	if err != nil || !strings.Contains(out, "verified 1 tenants: 0 mismatches") {
		t.Fatalf("restore-check = %q, %v", out, err)
	}
	if !strings.Contains(out, "parties") || !strings.Contains(out, "fund_accounts") {
		t.Errorf("restore-check does not report the restored rows: %q", out)
	}
	if _, err := run(t, live, "tenant", "restore-check", "--tenant", ids.New().String(), "--scratch-url", scratch.AppURL); err == nil ||
		!strings.Contains(err.Error(), "no tenant has the id") {
		t.Errorf("restore-check of an unknown tenant = %v", err)
	}

	// After the backup the store loses its customer, and the other store loses one too.
	if _, err := super.Exec(`DELETE FROM tally.parties WHERE id = ANY($1)`, []uuid.UUID{customer, otherCustomer}); err != nil {
		t.Fatal(err)
	}
	otherBefore := tenantFingerprint(t, super, other.ID)

	if _, err := run(t, live, "tenant", "copy", "--tenant", store.String(), "--from", scratch.AppURL); err == nil ||
		!strings.Contains(err.Error(), "not in maintenance") {
		t.Fatalf("copy outside maintenance = %v", err)
	}
	if out, err := run(t, live, "tenant", "maintenance", "on", "--tenant", store.String()); err != nil || !strings.Contains(out, "maintenance=true") {
		t.Fatalf("maintenance on = %q, %v", out, err)
	}
	out, err = run(t, live, "tenant", "copy", "--tenant", store.String(), "--from", scratch.AppURL)
	if err != nil || !strings.Contains(out, "copied 1 rows") || !strings.Contains(out, "ledgers verified") {
		t.Fatalf("copy = %q, %v", out, err)
	}
	if n := countWhere(t, super, `SELECT count(*) FROM tally.parties WHERE id = $1 AND tenant_id = $2 AND name = 'Chị Lan'`, customer, store); n != 1 {
		t.Errorf("restored customer rows = %d, want 1", n)
	}
	// The copy never touches another tenant, although the scratch copy holds its lost customer.
	if n := countWhere(t, super, `SELECT count(*) FROM tally.parties WHERE id = $1`, otherCustomer); n != 0 {
		t.Errorf("the other store's customer came back: %d rows", n)
	}
	for table, rows := range tenantFingerprint(t, super, other.ID) {
		if rows != otherBefore[table] {
			t.Errorf("copy changed %s of another tenant", table)
		}
	}
	// A second copy finds nothing missing.
	if out, err := run(t, live, "tenant", "copy", "--tenant", store.String(), "--from", scratch.AppURL); err != nil || !strings.Contains(out, "copied 0 rows") {
		t.Errorf("repeated copy = %q, %v", out, err)
	}

	// A copy that would leave the ledgers inconsistent is rolled back whole.
	if _, err := super.Exec(`DELETE FROM tally.parties WHERE id = $1`, customer); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(`UPDATE tally.fund_accounts SET balance = balance + 1 WHERE tenant_id = $1 AND kind = 'cash'`, store); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, live, "tenant", "copy", "--tenant", store.String(), "--from", scratch.AppURL)
	if err == nil || !strings.Contains(err.Error(), "copy rolled back") || !strings.Contains(out, "ledger mismatch cash") {
		t.Fatalf("copy onto inconsistent ledgers = %q, %v", out, err)
	}
	if n := countWhere(t, super, `SELECT count(*) FROM tally.parties WHERE id = $1`, customer); n != 0 {
		t.Errorf("rolled back copy left %d customer rows", n)
	}
	if _, err := super.Exec(`UPDATE tally.fund_accounts SET balance = balance - 1 WHERE tenant_id = $1 AND kind = 'cash'`, store); err != nil {
		t.Fatal(err)
	}
	if out, err := run(t, live, "tenant", "maintenance", "off", "--tenant", store.String()); err != nil || !strings.Contains(out, "maintenance=false") {
		t.Errorf("maintenance off = %q, %v", out, err)
	}
	if _, err := run(t, live, "tenant", "maintenance", "pause", "--tenant", store.String()); err == nil {
		t.Error("maintenance accepts only on or off")
	}
}

// addPrimaryContact inserts the primary contact of a party and returns its id.
func addPrimaryContact(t *testing.T, super *sqlx.DB, tenantID, party uuid.UUID, name string) uuid.UUID {
	t.Helper()
	id := ids.New()
	now := time.Now().UTC()
	if _, err := super.Exec(`
		INSERT INTO tally.contacts (id, tenant_id, party_id, name, is_primary, created_at, updated_at)
		VALUES ($1, $2, $3, $4, true, $5, $5)`, id, tenantID, party, name, now); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTenantCopyRefusesAScratchRowTheLiveDatabaseCannotTake(t *testing.T) {
	super := testkit.SuperDB(t)
	store, _ := createStore(t, "Cửa hàng liên hệ")
	customer := addCustomer(t, super, store, "Chị Mai")
	backedUp := addPrimaryContact(t, super, store, customer, "Chị Mai")
	scratch := testkit.RestoreScratch(t)
	live := map[string]string{"DATABASE_URL": testkit.AppURL(t)}

	// After the backup the contact is lost and another primary contact of the customer is
	// written: the scratch row's key is free but the one-primary-contact index refuses it, while
	// the table still holds as many rows as the scratch copy.
	if _, err := super.Exec(`DELETE FROM tally.contacts WHERE id = $1`, backedUp); err != nil {
		t.Fatal(err)
	}
	addPrimaryContact(t, super, store, customer, "Anh Tâm")
	addCustomer(t, super, store, "Anh Lộc")
	before := tenantFingerprint(t, super, store)

	if out, err := run(t, live, "tenant", "maintenance", "on", "--tenant", store.String()); err != nil {
		t.Fatalf("maintenance on = %q, %v", out, err)
	}
	out, err := run(t, live, "tenant", "copy", "--tenant", store.String(), "--from", scratch.AppURL)
	if err == nil || !strings.Contains(err.Error(), "copy rolled back") || !strings.Contains(err.Error(), "contacts") {
		t.Fatalf("copy dropping a scratch row = %q, %v", out, err)
	}
	if !strings.Contains(out, backedUp.String()) {
		t.Errorf("copy does not name the scratch row it could not restore: %q", out)
	}
	for table, rows := range tenantFingerprint(t, super, store) {
		if rows != before[table] {
			t.Errorf("rolled back copy changed %s", table)
		}
	}
}

func TestTenantPurgeRetiresTheAddress(t *testing.T) {
	store, slug := createStore(t, "Cửa hàng đóng cửa")
	super := testkit.SuperDB(t)
	addCustomer(t, super, store, "Chị Hoa")
	sandbox := testkit.NewTenant(t, testkit.DB(t))
	bystander := testkit.NewTenant(t, testkit.DB(t))
	if _, err := super.Exec(`UPDATE tally.tenants SET is_sandbox = true, sandbox_of = $2, status = 'replaced' WHERE id = $1`, sandbox.ID, store); err != nil {
		t.Fatal(err)
	}
	ticket := ids.New()
	now := time.Now().UTC()
	if _, err := super.Exec(`
		INSERT INTO tally.support_tickets (id, tenant_id, number, subject, status, priority, created_at, updated_at)
		VALUES ($1, $2, $3, 'Không in được hoá đơn', 'open', 'normal', $4, $4)`, ticket, store, "T-"+ticket.String()[:8], now); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(`
		INSERT INTO tally.support_ticket_messages (id, ticket_id, author_type, author_id, body, created_at)
		VALUES ($1, $2, 'owner', $3, 'Máy in báo lỗi', $4)`, ids.New(), ticket, ids.New(), now); err != nil {
		t.Fatal(err)
	}
	bystanderBefore := tenantFingerprint(t, super, bystander.ID)

	env := map[string]string{"APP_ENV": "test", "MIGRATE_DATABASE_URL": testkit.OwnerURL(t)}
	purge := func(id uuid.UUID, flags ...string) (string, error) {
		return run(t, env, append([]string{"tenant", "purge", "--tenant", id.String()}, flags...)...)
	}
	confirmed := []string{"--confirm-slug", slug}
	if _, err := purge(store, confirmed...); err == nil || !strings.Contains(err.Error(), "only a closed store can be purged") {
		t.Fatalf("purge of an active store = %v", err)
	}
	// A store closed because its subscription lapsed did not ask for it and took no export.
	if _, err := super.Exec(`UPDATE tally.tenants SET status = 'closed' WHERE id = $1`, store); err != nil {
		t.Fatal(err)
	}
	if _, err := purge(store, confirmed...); err == nil || !strings.Contains(err.Error(), "closure request did not complete") {
		t.Fatalf("purge of a lapsed store = %v", err)
	}
	if _, err := super.Exec(`
		INSERT INTO tally.account_closure_requests (id, tenant_id, requested_by, waiting_until, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'completed', $5, $5)`, ids.New(), store, ids.New(), now.AddDate(0, 0, -1), now); err != nil {
		t.Fatal(err)
	}
	if _, err := super.Exec(`UPDATE tally.tenants SET legal_hold = true WHERE id = $1`, store); err != nil {
		t.Fatal(err)
	}
	if _, err := purge(store, confirmed...); err == nil || !strings.Contains(err.Error(), "legal hold") {
		t.Fatalf("purge under legal hold = %v", err)
	}
	if _, err := super.Exec(`UPDATE tally.tenants SET legal_hold = (id = $2) WHERE id IN ($1, $2)`, store, sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := purge(store, confirmed...); err == nil || !strings.Contains(err.Error(), "sandbox "+sandbox.ID.String()+" is under legal hold") {
		t.Fatalf("purge with a sandbox under legal hold = %v", err)
	}
	if _, err := super.Exec(`UPDATE tally.tenants SET legal_hold = false WHERE id = $1`, sandbox.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := purge(sandbox.ID, confirmed...); err == nil || !strings.Contains(err.Error(), "is a sandbox") {
		t.Fatalf("purge of a sandbox = %v", err)
	}
	// Without APP_ENV the purge is guarded like production and needs a backup.
	if _, err := run(t, map[string]string{"MIGRATE_DATABASE_URL": testkit.OwnerURL(t)}, "tenant", "purge", "--tenant", store.String(),
		"--confirm-slug", slug); err == nil || !strings.Contains(err.Error(), "without --backup-file") {
		t.Fatalf("purge without a backup = %v", err)
	}
	if _, err := purge(store); err == nil || !strings.Contains(err.Error(), "--confirm-slug") {
		t.Fatalf("purge without --confirm-slug = %v", err)
	}
	if _, err := purge(store, "--confirm-slug", slug+"-x"); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("purge with another slug = %v", err)
	}

	// A dry run counts what the purge deletes and keeps every row.
	before := tenantFingerprint(t, super, store)
	out, err := purge(store, "--dry-run")
	if err != nil || !strings.Contains(out, "dry run") || !strings.Contains(out, "support_ticket_messages") {
		t.Fatalf("dry run = %q, %v", out, err)
	}
	for table, rows := range tenantFingerprint(t, super, store) {
		if rows != before[table] {
			t.Errorf("dry run changed %s", table)
		}
	}
	if n := countWhere(t, super, `SELECT count(*) FROM tally.tenants WHERE id = ANY($1)`, []uuid.UUID{store, sandbox.ID}); n != 2 {
		t.Errorf("tenant rows after the dry run = %d, want 2", n)
	}

	out, err = purge(store, confirmed...)
	if err != nil || !strings.Contains(out, "and 1 sandboxes") || !strings.Contains(out, "support_ticket_messages") ||
		!strings.Contains(out, "tenants/"+store.String()+"/") {
		t.Fatalf("purge = %q, %v", out, err)
	}
	gone := []uuid.UUID{store, sandbox.ID}
	var tables []string
	if err := super.Select(&tables, `
		SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'tenant_id'
		WHERE n.nspname = 'tally' AND c.relkind = 'r' AND c.relname <> 'retired_tenant_slugs'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if n := countWhere(t, super, `SELECT count(*) FROM tally.`+table+` WHERE tenant_id = ANY($1)`, gone); n != 0 {
			t.Errorf("%s keeps %d rows of the purged tenants", table, n)
		}
	}
	if n := countWhere(t, super, `SELECT count(*) FROM tally.tenants WHERE id = ANY($1)`, gone); n != 0 {
		t.Errorf("tenant rows left: %d", n)
	}
	if n := countWhere(t, super, `SELECT count(*) FROM tally.support_ticket_messages WHERE ticket_id = $1`, ticket); n != 0 {
		t.Errorf("ticket messages left: %d", n)
	}
	if n := countWhere(t, super, `SELECT count(*) FROM tally.retired_tenant_slugs WHERE slug = $1 AND tenant_id = $2`, slug, store); n != 1 {
		t.Errorf("retired address rows = %d, want 1", n)
	}
	for table, rows := range tenantFingerprint(t, super, bystander.ID) {
		if rows != bystanderBefore[table] {
			t.Errorf("purge changed %s of another tenant", table)
		}
	}

	// The address is never issued again: sign-up answers taken.
	signup := serviceEnv(t)
	signup["SMTP_PORT"] = "1025"
	_, err = run(t, signup, "tenant", "create", "--name", "Cửa hàng mới", "--slug", slug, "--owner-email", "new-"+slug+"@example.test")
	if !apperr.Is(err, "slug_taken") {
		t.Errorf("sign-up on a retired address = %v, want slug_taken", err)
	}
	if _, err := purge(store, confirmed...); err == nil || !strings.Contains(err.Error(), "no tenant has the id") {
		t.Errorf("second purge = %v", err)
	}
}
