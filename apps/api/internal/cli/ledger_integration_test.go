//go:build integration

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/g0-up/tally/apps/api/internal/platform/ids"
	"github.com/g0-up/tally/apps/api/internal/testkit"
)

func TestLedgerVerifyCommand(t *testing.T) {
	tn := testkit.NewTenant(t, testkit.DB(t))
	env := map[string]string{"DATABASE_URL": testkit.AppURL(t)}

	out, err := run(t, env, "ledger", "verify", "--tenant", tn.ID.String())
	if err != nil || !strings.Contains(out, "verified 1 tenants: 0 mismatches") {
		t.Fatalf("verify of a consistent tenant = %q, %v", out, err)
	}
	if out, err := run(t, env, "ledger", "verify"); err != nil || !strings.Contains(out, " 0 mismatches") {
		t.Fatalf("verify of every tenant = %q, %v", out, err)
	}

	// A fund whose balance is not its opening balance plus its entries.
	fund := ids.New()
	now := time.Now().UTC()
	if _, err := testkit.SuperDB(t).Exec(`
		INSERT INTO tally.fund_accounts (id, tenant_id, kind, name, opening_balance, balance, created_at, updated_at)
		VALUES ($1, $2, 'cash', 'Két', 0, 500000, $3, $3)`, fund, tn.ID, now); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, env, "ledger", "verify", "--tenant", tn.ID.String())
	if err == nil || !strings.Contains(err.Error(), "1 projections disagree") {
		t.Fatalf("verify of a tampered fund = %v, want a mismatch failure", err)
	}
	if !strings.Contains(out, "tenant "+tn.ID.String()+" cash: fund "+fund.String()) || !strings.Contains(out, "1 mismatches") {
		t.Errorf("output = %q, want the mismatching fund", out)
	}

	// --sku narrows the run to the stock ledger of one SKU: the tampered fund is not checked.
	if out, err := run(t, env, "ledger", "verify", "--tenant", tn.ID.String(), "--sku", ids.New().String()); err != nil ||
		!strings.Contains(out, "verified 1 tenants: 0 mismatches") {
		t.Errorf("verify of one sku = %q, %v", out, err)
	}
	if _, err := run(t, env, "ledger", "verify", "--sku", "nope"); err == nil || !strings.Contains(err.Error(), "--sku must be a sku id") {
		t.Errorf("bad --sku = %v", err)
	}

	if _, err := run(t, env, "ledger", "verify", "--tenant", "nope"); err == nil || !strings.Contains(err.Error(), "--tenant must be a tenant id") {
		t.Errorf("bad --tenant = %v", err)
	}
}
