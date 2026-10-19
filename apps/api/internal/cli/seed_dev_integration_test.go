//go:build integration

package cli

import (
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"

	"github.com/g0-up/tally/apps/api/internal/testkit"
)

// freeDevStoreSlug moves any store at the development store's address to another one. The tests
// of the package share one database and the tenant create test names its store the same way, so
// each side starts without the other's store whatever order they run in.
func freeDevStoreSlug(t *testing.T, super *sqlx.DB) {
	t.Helper()
	if _, err := super.Exec(`UPDATE tally.tenants SET slug = 'moved-' || substr(md5(id::text), 1, 12) WHERE slug = $1`, devStoreSlug); err != nil {
		t.Fatalf("free the development store address: %v", err)
	}
}

// seed dev creates the browser suites' store with its owner activated from the e2e variables,
// fills it with a little verified data, and a second run leaves it as it is.
func TestSeedDevCreatesTheDevelopmentStoreOnce(t *testing.T) {
	env := loadEnv(t)
	const (
		email    = "chu-cua-hang-a@e2e.example.test"
		password = "Mật-khẩu-e2e-2026"
	)
	env[devOwnerEmailEnv] = email
	env[devOwnerPasswordEnv] = password
	super := testkit.SuperDB(t)
	freeDevStoreSlug(t, super)
	t.Cleanup(func() { freeDevStoreSlug(t, super) })

	out, err := run(t, env, "seed", "dev")
	if err != nil {
		t.Fatalf("seed dev = %q, %v", out, err)
	}
	if !strings.Contains(out, "created tenant "+devStoreSlug+" ") || !strings.Contains(out, "owner="+email+" active") ||
		!strings.Contains(out, "verified 1 tenants: 0 mismatches") {
		t.Errorf("seed dev output:\n%s", out)
	}
	if strings.Contains(out, password) || strings.Contains(out, "token") || strings.Contains(out, "/verify") {
		t.Errorf("output leaks the password or the verification link:\n%s", out)
	}
	id := loadTenantID(t, super, devStoreSlug)
	var name string
	if err := super.Get(&name, `SELECT display_name FROM tally.tenants WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if name != devStoreName {
		t.Errorf("store name = %q, want %q", name, devStoreName)
	}
	var owner string
	if err := super.Get(&owner, `SELECT status FROM tally.users WHERE tenant_id = $1 AND email = $2`, id, email); err != nil {
		t.Fatal(err)
	}
	if owner != "active" {
		t.Errorf("owner status = %s, want active", owner)
	}
	skusQuery := `SELECT count(*) FROM tally.skus WHERE tenant_id = $1`
	movesQuery := `SELECT count(*) FROM tally.stock_moves WHERE tenant_id = $1`
	skus, moves := loadCount(t, super, skusQuery, id), loadCount(t, super, movesQuery, id)
	if skus != devSeedOptions.Skus || moves < devSeedOptions.Moves {
		t.Errorf("skus = %d, stock moves = %d; want %d and at least %d", skus, moves, devSeedOptions.Skus, devSeedOptions.Moves)
	}

	again, err := run(t, env, "seed", "dev")
	if err != nil {
		t.Fatalf("second seed dev = %q, %v", again, err)
	}
	if !strings.Contains(again, "tenant "+devStoreSlug+" already exists id="+id) {
		t.Errorf("second seed dev output:\n%s", again)
	}
	if n, m := loadCount(t, super, skusQuery, id), loadCount(t, super, movesQuery, id); n != skus || m != moves {
		t.Errorf("second run changed the store: skus %d -> %d, stock moves %d -> %d", skus, n, moves, m)
	}

	// A store at that address that seed dev did not fill is reported, not taken for the data set.
	freeDevStoreSlug(t, super)
	if out, err := run(t, env, "tenant", "create", "--name", devStoreName, "--owner-email", "bare-"+email); err != nil ||
		!strings.Contains(out, "created tenant "+devStoreSlug+" ") {
		t.Fatalf("tenant create = %q, %v", out, err)
	}
	bare := loadTenantID(t, super, devStoreSlug)
	if out, err := run(t, env, "seed", "dev"); err == nil || !strings.Contains(err.Error(), "already exists id="+bare+" with 0 SKUs and 0 stock moves") {
		t.Errorf("seed dev over a bare store = %q, %v; want it refused", out, err)
	}
	if n := loadCount(t, super, skusQuery, bare); n != 0 {
		t.Errorf("seed dev filled a store it did not create: %d SKUs", n)
	}
}
