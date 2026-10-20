---
phase: 4
title: "Full a11y suite, parity and gates"
status: completed
---

# Phase 4: full verification

## Context

Prove acceptance criteria 1–5 in [plan.md](plan.md). The earlier full run stopped at 137 of 194
tests because of the 10-minute foreground limit. Run the suite in the background, or split it by
spec file, so it finishes.

## Files

- Create `plans/reports/tester-261020-0021-desktop-targets-axe-keyboard.md`
- Modify `plans/261019-2225-touch-targets-390/plan.md`, the phase-02 file, and
  `plans/reports/tester-261019-2225-touch-targets-390.md`: criterion 2 status and a link
- Modify any app or spec file needed to fix a new axe node the demo data surfaces

## Steps

1. **Dev stack.** Reuse `make dev-api` (:8080) and `make dev-web` (:5173) if they are running.
   Otherwise start them in the background and stop them at the end of the phase.
2. **Full a11y suite** on the demo sandbox store
   (`E2E_TENANT_URL=http://e2e-a11y-18733-demo.localhost:5173`), with the password in the shell
   only. Run it in the background with no 590s cap. If it must be split, run one spec file per run:
   `axe.spec.ts`, then `focus.spec.ts` and `keyboard.spec.ts`, then `touch-targets.spec.ts`.
   `test-results` is cleared on each run, so let the `owner-session` setup run each time.
3. **New failures from the demo data.** Fix any new axe node the demo data surfaces, at its
   source, then rerun that spec.
4. **Parity.**
   - Run `--project visual --grep "parity at 1440"` and compare it with the phase 3 baseline.
   - Run `--project visual --grep "parity at 390"` and compare it with the 390 report.
   - No state may start failing.
5. **Gates.** Run `corepack pnpm lint`, `corepack pnpm typecheck` and
   `corepack pnpm exec vitest run`.
6. **Report.**
   - Write the tester report: commands, store, before and after numbers, parity ratios, and any
     app bug the repaired specs revealed.
   - Update the 390 plan's criterion 2 and its report.
   - Set this plan's phases and status.

## Validation

- Every criterion in plan.md has evidence in the report.
- `ps` shows no dev server or Playwright process left from this phase.

## Risks and rollback

- If the demo store's data drifts (the reset dialog modifies the sandbox), reset it with
  "Đặt lại mẫu" before the run.
- Rollback: this phase changes only reports and plans, plus any fix from step 3. Revert those.
