package cli

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/g0-up/tally/apps/api/internal/posting"
)

func TestDifferenceKey(t *testing.T) {
	const id = "01a151ca-f69a-7c5c-a888-02547aa855c9"
	cases := map[string]string{
		"inventory: sku on hand " + id + " (SP00001): stored 290.0000, ledger 285.0000":         "inventory: sku on hand " + id + " (SP00001)",
		"inventory: sku on hand by lots " + id + " (SP00001): stored 290.0000, ledger 285.0000": "inventory: sku on hand by lots " + id + " (SP00001)",
		"kits: assembly order component consumed " + id + " (LR-1 SP 2): stored 3, expected 4":  "kits: assembly order component consumed " + id + " (LR-1 SP 2)",
		"debt: settlement " + id + " reverses a missing settlement":                             "debt: settlement " + id,
		"cash: no ids here": "cash: no ids here",
	}
	for line, want := range cases {
		if got := differenceKey(line); got != want {
			t.Errorf("differenceKey(%q) = %q, want %q", line, got, want)
		}
	}
	// The values after the id change with a rebuild; the key does not.
	if differenceKey("kits: assembly order completed "+id+" (LD-1): stored 11, expected 10") !=
		differenceKey("kits: assembly order completed "+id+" (LD-1): stored 12, expected 10") {
		t.Error("the key depends on the stored value")
	}
	// Rows of one document differ by their label.
	if differenceKey("kits: assembly order component consumed "+id+" (LR-1 SP1): stored 3, expected 4") ==
		differenceKey("kits: assembly order component consumed "+id+" (LR-1 SP2): stored 3, expected 4") {
		t.Error("two components of one order share a key")
	}
}

func TestLedgerRebuildersPairWithVerifiers(t *testing.T) {
	for _, v := range ledgerVerifiers {
		if (v.rebuild == nil) != (v.rebuilds == nil) {
			t.Errorf("verifier %s sets only one of rebuild and rebuilds", v.name)
		}
		if v.rebuilds != nil && len(v.rebuilds()) == 0 {
			t.Errorf("verifier %s rebuilds no check", v.name)
		}
	}
}

func TestPrintRebuild(t *testing.T) {
	const tenant = "01a151ca-f5ca-7bbc-bd9f-e1fd56182e81"
	const fund = "01a151ca-f69a-7c5c-a888-02547aa855c9"
	const ret = "01a151ca-f69a-7c5c-a888-02547aa855ca"
	const lot = "01a151ca-f69a-7c5c-a888-02547aa855cb"
	res := rebuildResult{
		before: []string{
			"cash: fund balance " + fund + " (Cash): stored 10, ledger 9",
			"sales: sales return total " + ret + " (TH-1): stored 11, expected 10",
		},
		after: []string{
			"sales: sales return total " + ret + " (TH-1): stored 11, expected 10",
			"inventory: lot balance " + lot + " (L-1): stored 1, ledger 2",
		},
		rebuilt: map[string][]posting.Rebuilt{"cash": {{Check: "fund balance", Rows: 1}}},
	}
	tid := uuid.MustParse(tenant)
	var b strings.Builder
	cleared, err := printRebuild(&b, tid, res, false)
	if err != nil || cleared != 1 {
		t.Fatalf("printRebuild = %d, %v; want 1 cleared", cleared, err)
	}
	for _, want := range []string{
		"rebuild tenant " + tenant + " cash: fund balance " + fund,
		"manual tenant " + tenant + " sales: sales return total " + ret,
		"manual tenant " + tenant + " inventory: lot balance " + lot + " (L-1): stored 1, ledger 2 (after the rebuild)",
		"would rebuild tenant " + tenant + " cash: fund balance: 1 rows",
	} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, b.String())
		}
	}
	// A written run that left differences was rolled back: nothing was rebuilt.
	b.Reset()
	if _, err := printRebuild(&b, tid, res, true); err != nil || !strings.Contains(b.String(), "would rebuild tenant") {
		t.Errorf("written run with differences left = %q, %v", b.String(), err)
	}
	b.Reset()
	res.after = nil
	if _, err := printRebuild(&b, tid, res, true); err != nil || !strings.Contains(b.String(), "rebuilt tenant "+tenant+" cash: fund balance: 1 rows") {
		t.Errorf("clean written run = %q, %v", b.String(), err)
	}
}

func TestLedgerRebuildFlags(t *testing.T) {
	const tenant = "01a151ca-f5ca-7bbc-bd9f-e1fd56182e81"
	env := map[string]string{}
	if _, err := run(t, env, "ledger", "rebuild", "--tenant", tenant, "--dry-run", "--confirm"); err == nil || !strings.Contains(err.Error(), "dry-run") {
		t.Errorf("rebuild with --dry-run and --confirm = %v", err)
	}
	if _, err := run(t, env, "ledger", "rebuild", "--tenant", "nope", "--confirm"); err == nil || !strings.Contains(err.Error(), "--tenant must be a tenant id") {
		t.Errorf("rebuild of a bad tenant id = %v", err)
	}
	if _, err := run(t, env, "ledger", "rebuild"); err == nil || !strings.Contains(err.Error(), "tenant") {
		t.Errorf("rebuild without --tenant = %v", err)
	}
	out, err := run(t, env, "ledger", "rebuild", "--help")
	if err != nil || !strings.Contains(out, "TAKE A BACKUP FIRST") || !strings.Contains(out, "By default (or with --dry-run) the command is a dry run") ||
		!strings.Contains(out, "--confirm") {
		t.Errorf("help = %q, %v; want the backup warning, the default dry run and --confirm", out, err)
	}
}

func TestLedgerRebuildRefusesANegativeStatementTimeout(t *testing.T) {
	out, err := run(t, map[string]string{}, "ledger", "rebuild", "--tenant", "0198f5c4-8f3a-7c2e-9a51-3c1d2b4e6f70", "--statement-timeout", "-1s")
	if err == nil || !strings.Contains(err.Error(), "--statement-timeout must not be negative") {
		t.Fatalf("rebuild with a negative timeout = %q, %v", out, err)
	}
}
