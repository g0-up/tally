---
title: "Fix the 390px touch targets across the app"
description: "Make every control on every app state at least 44x44 CSS px at 390px wide, so e2e/a11y/touch-targets.spec.ts passes at 390 without weakening it."
status: in-progress
priority: P1
effort: 1d
branch: master
tags: [frontend, accessibility, css]
blockedBy: []
blocks: []
created: 2026-10-19
---

# Fix the 390px touch targets across the app

## Outcome

`touch targets at 390 › <state> keeps every control at least 44px` passes for every state in
`e2e/visual/parity-states.ts`, both on a fresh store and on the seeded `cua-hang-a` store that CI
uses. `touch targets at 1440` (24px) keeps passing.

## Evidence (run 2026-10-19, fresh throwaway store, 63 tests)

59 failed, 2 passed, 2 skipped (the print states need document ids). Only `onboarding.wizard` passes
at 390. Full per-state list: run the command in phase 2 step 1. Six causes explain every failure:

| # | Control | Size at 390 | States | Cause |
|---|---------|-------------|--------|-------|
| 1 | Skip link "Bỏ qua tới nội dung" | 156x42 | 43 (every shell state) | Moved off screen with `top: -200px` but still measured. When it has focus it is 42px tall (`globals.css:510`). |
| 2 | `.btn` (e.g. "Thu tiền khách", "‹ Kỳ trước", dialog buttons) | Wx37 | most route and dialog states | Ported prototype rule `@media (max-width: 520px) { .btn { padding: 12px 18px; } }` (`globals.css:292`, prototype line 289). The 44px block covers `.btn-sm` but not `.btn`. |
| 3 | `.auth-links a` ("Quên mật khẩu?", "Về trang đăng nhập", …) | Wx20 | the 7 auth states | Inline links in a flex row with no minimum height (`globals.css:381`). |
| 4 | Lone links "Về trang đầu" (`app/route-fallbacks.tsx:57`), "Về Tài khoản & bảo mật" (`settings/pages/account-closure-page.tsx:63`), and the anchor `.lnk` "Thẻ kho" (`inventory/components/stock-table.tsx:175,190`) | Wx17 | error.not-found, settings.account-closure, kho.ton | The 44px block covers `button.lnk` only. Anchors and plain `<Link>`s have no minimum. |
| 5 | Color chip `.cp` "Đỏ" | 42x44 | kho.ton | The 44px block gives `.cp` a 44px height but no minimum width. |
| 6 | Checkbox `INPUT` | 13x13 | thiet-lap.tai-khoan, sales.returns.exchange-dialog | Native checkbox at its default size. The spec measures the input, not the wrapping `.check` label. |

The fresh store has almost no rows. Controls inside table rows, pagination and row actions only
show up with data, so the seeded store may surface more failures. Phase 2 runs that inventory.

## Constraints

- Do not weaken the spec. Keep the selector list, the width-or-height rule, and the exclusions as they are.
- The app's CSS is the prototype stylesheet ported verbatim, with additions after the end marker.
  Make every change in the additions block, inside the existing
  `@media (max-width: 860px), (pointer: coarse)` block. Do not edit the ported rules. Desktop stays as
  it is; the app already differs from the prototype at 390 for `.btn-sm`, chips and fields in the
  same way.
- Keep visible sizes close to the prototype. Grow the hit box (min-height, min-width, inline-flex,
  an invisible padding ring) rather than the visible glyph where possible.
- Keep the skip link working: the first Tab focuses it, it is visible while it has focus, and Enter
  moves focus to `main`. The `app-shell.test.tsx` and `console-shell.test.tsx` assertions keep passing.
- WCAG 2.2 AA stays intact. Checkboxes keep their checked and disabled states, a visible focus ring,
  and a usable rendering in forced colors.

## Non-goals

- Desktop (24px) changes beyond what the 390 fixes imply. (Correction: the 1440 run did not already pass. It has 13 failures that predate this plan; see the report.)
- Changing the prototype file or the parity spec.
- Committing the untracked `e2e/visual/parity.spec.ts-snapshots/` folder.

## Phases

| Phase | File | Status |
|-------|------|--------|
| 1. Shared CSS and markup fixes for the six causes | [phase-01-shared-target-fixes.md](phase-01-shared-target-fixes.md) | completed |
| 2. Seeded-store inventory, remaining fixes and verification | [phase-02-seeded-inventory-and-verify.md](phase-02-seeded-inventory-and-verify.md) | in progress: verified on `thegioidoan` instead of the seeded store; criterion 2 met by [the follow-up plan](../261020-0021-desktop-targets-axe-keyboard/plan.md) |

Phase 2 depends on phase 1.

## Acceptance criteria

1. `playwright test --project a11y --grep "touch targets at 390"` passes on a fresh store and on the
   seeded `cua-hang-a` store. The print states may skip when their document ids are not set.
2. `touch targets at 1440`, `axe`, and the keyboard and focus specs in `e2e/a11y/` pass.
3. Parity at 390 shows no new failure caused by this change: compare the diff ratios before and after.
   Parity at 1440 does not change.
4. `corepack pnpm lint`, `corepack pnpm typecheck` and `vitest run` are clean.
5. Onboarding's own 44px overrides that the global rules now cover are removed, and onboarding still
   passes its a11y run.

## Result

See [the tester report](../reports/tester-261019-2225-touch-targets-390.md).
- 390 passes on a fresh store and on a demo sandbox store with 22 SKUs.
- The seeded `cua-hang-a` run was not done, because its owner password is not available locally.
  The user's production store `thegioidoan` was tested in its place, after the web deploy of
  2026-10-20. Every state passes there except three, and none of the three is a touch-target issue.
- Criterion 2 is met by [the follow-up plan](../261020-0021-desktop-targets-axe-keyboard/plan.md):
  the whole `a11y` project passes on the demo sandbox store
  ([report](../reports/tester-261020-0021-desktop-targets-axe-keyboard.md)). Only the seeded-store
  part of criterion 1 stays open.
