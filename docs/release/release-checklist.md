# Release checklist

The gates of PRD §17 for releasing Tally 1.1, each with the evidence or command that proves it. Tick a
box only when the linked evidence exists for the release build. Copy this list into the release
ticket or report for each release rather than ticking it here; this page stays the template.

## Definition of Done (every module)

- [ ] **Requirements, UX and API exist for each module in scope.** Evidence: the PRD, the phase plans
      in `plans/`, `apps/api/api/openapi.yaml` and the screens in `apps/web/src/features/`.
- [ ] **Unit, integration and end-to-end tests cover success, error, retry, concurrency and
      permission paths.** Evidence: a green CI run on the release commit (`make test`,
      `make test-integration`, `make e2e`) and the [acceptance matrix](acceptance-matrix.md).
- [ ] **Stock, debt and fund balances reconcile with their ledgers; migrations and recovery are
      tested.** Evidence: `tally ledger verify` with 0 mismatches after the acceptance and load runs
      (see the [support checklist](../support/support-checklist.md#3-ledger-drift) for the command);
      the migration reversibility tests in CI; a restore drill report from `make restore-drill`
      (written to `test-results/restore-drill/`).
- [ ] **CSV export, audit, pagination and clear errors are tested.** Evidence: acceptance matrix rows
      NFR-05, NFR-13 and AT-14.
- [ ] **No open high-severity defect on tenant isolation, data loss or financial posting.** Evidence:
      the issue tracker filtered to those labels, empty; acceptance matrix rows AT-09, NFR-01 and
      NFR-02.
- [ ] **User guide, support checklist, and the metrics and alerts needed exist.** Evidence:
      [user guide](../user-guide/README.md), [support checklist](../support/support-checklist.md),
      metrics in `apps/api/internal/platform/metrics/metrics.go`, alert rules in
      [`infra/monitoring/alerts.yml`](../../infra/monitoring/alerts.yml). No Prometheus or
      Alertmanager runs in the stack yet, so no alert can page anyone (runbook "Alerts and
      observability"; acceptance matrix NFR-09).
- [ ] **The pilot owner confirmed the data and the real flows** (sign-off below). A paid subscription
      is commercial evidence, not a substitute for the technical tests.

## Before commercial release

1. - [ ] **Discovery confirmed the segment, the critical flows and the value worth paying for.**
         Evidence: the owner's discovery notes; the PRD's open questions (§18) answered or deferred.
2. - [ ] **Every AC and AT in the release scope passes with stored evidence.** Evidence: every row
         of the [acceptance matrix](acceptance-matrix.md) at "Đạt" with an evidence link. Command:
         `make acceptance`, with the stack, seed and variables of
         [Running the acceptance specs](acceptance-matrix.md#running-the-acceptance-specs). The
         specs are written but have not run on a real stack; a run counts only when its ledger
         verify step ran.
3. - [ ] **Load test on the reference data, peak-season soak, backup/restore and security review.**
         Evidence and commands:
         - `make load-seed`, then `make load-test` and `make soak-test`, on the production-like
           stack and never on production, against the budgets in
           [`load/k6/thresholds.json`](../../load/k6/thresholds.json); both end with `tally ledger
           verify`. Settings and order: [`load/README.md`](../../load/README.md);
         - `make restore-drill` ([`infra/scripts/restore-drill.sh`](../../infra/scripts/restore-drill.sh)):
           a PASS report with measured RPO ≤ 15 min and RTO ≤ 4 h and a clean ledger verify. A
           `--db-only` run reports PARTIAL and does not satisfy this gate;
         - `make security-scan` with no open high or critical finding, plus a signed manual
           security review. CI runs it without images; the release workflow scans each image with
           trivy before pushing it.
4. - [ ] **Terms, retention, privacy, billing, SLA and support are reviewed and published.**
         Evidence: published documents. Open today: plan prices ("Chưa chốt"), support hours (PRD
         §14.3), retention of a purged store's object-store files (OPEN-11; expired exports are
         already deleted), and an SLA (the 99.9 % availability is a target only).
5. - [ ] **Rollback, ledger-drift, incident and customer-communication plans exist.** Evidence:
         rollback (redeploy the previous image tags and restore the pre-migration backup that
         `make prod-migrate` takes; runbook "4. Production on the homelab"); ledger drift, incidents
         and customer messages in the [support checklist](../support/support-checklist.md); tenant
         restore in [tenant-restore.md](../operations/tenant-restore.md).
6. - [ ] **No module is advertised before it works, and the limits are stated.** Evidence: sales and
         landing copy reviewed against the [user guide](../user-guide/README.md), which states the 1.1
         limits: one account, one warehouse, no delivery.

## Deployment

- [ ] The production checklist of the runbook ("4. Production on the homelab", "First deploy
      checklist") is done for this release.
- [ ] Browser, device and peripheral matrix recorded for the release build
      ([device-browser-matrix.md](../operations/device-browser-matrix.md)), including the
      [certified peripherals](../operations/device-browser-matrix.md#6-certified-peripherals).
- [ ] The API runs as a single replica (the tenant host cache is purged in-process only).

## Pilot sign-off

Filled in by the pilot store owner after using the release on their real data.

| Field | Value |
|---|---|
| Store | |
| Owner | |
| Release version | |
| Pilot period (from – to) | |
| Opening balances (stock, debts, funds) confirmed | |
| Flows used for real (sales, POS, receipts, debts, counts, kits…) | |
| Issues raised and their status | |
| The owner confirms the data and flows match the store's reality | |
| Signature and date | |
