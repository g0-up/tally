import { test } from "@playwright/test";

// AT-15: a backup is restored to a point in time and the projections are rebuilt from the ledger, so the
// balances after the restore match the ledger. The drill runs outside the browser against a restored copy
// of the database, so this spec records the skip rather than running it. Run the drill with
// infra/scripts/restore-drill.sh (its usage lists --target-time, --backup, --tenant and --report-dir);
// its report is the evidence for this journey.

test("restores a backup and rebuilds the projections to the balances of the ledger", async () => {
  test.skip(true, "run infra/scripts/restore-drill.sh against a restored copy; the drill writes its own report");
});
