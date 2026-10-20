---
phase: 2
title: "Seeded-store inventory, remaining fixes and verification"
status: in-progress
dependsOn: [1]
---

# Phase 2: seeded-store inventory, remaining fixes and verification

## Context

The phase 1 inventory came from a throwaway store with almost no rows. CI runs the a11y suite
against `cua-hang-a`, which `make seed` creates with 40 SKUs and at least 400 stock moves. Tables,
pagination and row actions only render with that data, so new failures can appear there.

## Files

- Modify `apps/web/src/shared/styles/globals.css`, in the 44px block, for any new shared control.
- Modify the feature components that the seeded run names, only where a shared class cannot fix the control.
- Create `plans/reports/tester-261019-2225-touch-targets-390.md`.

## Steps

1. Run the 390 spec from `apps/web`, with the dev stack up. Use the default ports :5173 and :8080,
   and reuse servers that are already running.
   ```
   E2E_TENANT_URL=<store url> E2E_OWNER_EMAIL=<owner> E2E_OWNER_PASSWORD=<in shell only> \
     timeout 590 corepack pnpm exec playwright test --project a11y \
     --grep "touch targets at 390|seeded store's owner" --reporter=line
   ```
   - Run it once on a fresh store, then once on the seeded store. For the seeded store, run
     `make seed` with `E2E_OWNER_PASSWORD` set in the shell only (never in a file), per
     `docs/runbook.md` and `docs/operations/device-browser-matrix.md`.
2. Group the remaining failures by shared class and fix each group in the 44px block. Change a
   component only when the control has no shared class.
3. Run the full a11y suite: touch targets at 390 and 1440, axe, keyboard and focus. All must pass.
4. Run parity at 390 and 1440. Compare the ratios with the phase 1 step 1 record. List each state
   whose ratio rose, and why.
5. Run `corepack pnpm lint`, `corepack pnpm typecheck` and `timeout 590 corepack pnpm exec vitest run`.
6. Write the tester report: commands, pass/fail counts per run, the parity comparison, and the
   remaining limits (print states skipped without document ids).
7. In `docs/release/acceptance-matrix.md`, change the NFR-11 evidence cell to link this report, but
   only if the full a11y suite passes on the seeded store.
8. Stop the dev servers this work started.

## Validation

- Plan acceptance criteria 1–5 are met, with run output in the report.

## Risks and rollback

- **The seeded store is unavailable locally.** Seed one with `make seed`. If seeding fails, record
  the blocker in the report and report the seeded run as not done. Do not claim it passed.
- **Rollback:** revert the extra CSS and component edits from this phase. Phase 1 stays.

## Status note (2026-10-20)

Step 3 is met on the demo sandbox store by
[the follow-up plan](../261020-0021-desktop-targets-axe-keyboard/plan.md), which fixed the 1440
targets and the axe, keyboard and focus failures
([report](../reports/tester-261020-0021-desktop-targets-axe-keyboard.md)). The seeded-store run
and step 7 are still open.
