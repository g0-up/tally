---
title: Fixed the 390px touch targets across the app
date: 2026-10-19
summary: "Shared 44px rules in the 860px/coarse block fix every 390 touch-target state on two local stores; the seeded store, the 1440 run and the axe/keyboard failures remain open"
---

# Fixed the 390px touch targets across the app

## What happened
- The touch-target spec at 390 failed on 59 of 63 states. Six shared causes explained them:
  - the skip link was measured off screen
  - `.btn` was 37px tall
  - auth links and anchor `.lnk`s had no minimum height
  - `.cp` and `.chip` were 42px wide
  - native checkboxes were 13x13
- Every fix went into the `@media (max-width: 860px), (pointer: coarse)` block of `globals.css`.
  The ported prototype rules were not touched.
- The checkbox is now a 44x44 transparent box with a 20px square drawn as an SVG background, and
  falls back to the native control in forced colors.
- Two lone links moved onto shared classes:
  - the not-found link uses `.auth-links`
  - the account-closure back link is a `TextLink`
- After the fix:
  - fresh store: every state passes except the reset dialog, which exists only in sandbox stores
  - demo sandbox store (22 SKUs): 61 passed, 2 print states skipped
  - parity at 390: no state started failing
  - lint, typecheck and vitest (1131 tests) are clean

## Surprises
- The plan said the 1440 run already passed. It has 13 failures at 24px from before this change:
  auth links, checkboxes and inline links at desktop width.
- The full a11y suite has 9 failures from before this change: axe contrast, label mismatch and
  scrollable region, plus 6 keyboard and focus tests. Running the same tests at HEAD, with the
  change stashed, gave the same 9 failures.
- The first demo-store run was invalid: its onboarding was not finished, so every route redirected
  to onboarding.

## Open
- The seeded `cua-hang-a` run is not done, because its owner password is not available locally.
- The 1440 failures, and the axe and keyboard failures, need their own follow-up.
- NFR-11 evidence is unchanged.
- Report: plans/reports/tester-261019-2225-touch-targets-390.md

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
