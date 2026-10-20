---
title: "Fix the 1440 touch targets and the axe, keyboard and focus failures"
description: "Make the whole e2e/a11y suite pass: 24px targets at desktop width, three axe violations, and the keyboard and focus specs, without weakening any spec."
status: completed
priority: P1
effort: 1d
branch: master
tags: [frontend, accessibility, css, e2e]
blockedBy: []
blocks: []
created: 2026-10-20
---

# Fix the 1440 touch targets and the axe, keyboard and focus failures

## Outcome

`playwright test --project a11y` passes on the local demo sandbox store. That covers axe, focus,
keyboard, and touch targets at 390 and at 1440. The print states may skip when their document ids
are not set. This closes acceptance criterion 2 of
[the 390 plan](../261019-2225-touch-targets-390/plan.md), which this plan builds on. That plan's
changes are deployed but not committed yet.

## Evidence (scouted 2026-10-20)

Sources: the fresh-store runs in the [390 report](../reports/tester-261019-2225-touch-targets-390.md),
and an axe probe on the demo store that prints each violating node.

| # | Failure | Root cause | Where |
|---|---------|------------|-------|
| 1 | Focus: "keeps keyboard focus inside the open dialog", "gives keyboard focus a visible indicator". Keyboard: all three journeys report "focus never reached …" | **Spec bug.** `locator.evaluate("(el) => …")` passes the function as a string. Playwright evaluates the string as an expression and returns `undefined` without calling it. Probe with Playwright 1.64.1: the string form returns `undefined`, a real function returns `true`. So `tabTo` never sees focus, and the trap and ring checks never compare anything. | `e2e/a11y/focus.spec.ts:25,54`, `e2e/a11y/keyboard.spec.ts:13` |
| 2 | Focus: "moves focus into the page … when the opener was removed" (timeout) | **Spec bug.** While the modal is open, Radix sets `aria-hidden` on everything outside it. `getByRole` skips aria-hidden elements, so the opener cannot be found. The `el.remove()` call would also have the string bug from #1. | `e2e/a11y/focus.spec.ts:39` |
| 3 | axe `color-contrast` on `tong-quan.dashboard` | The "Chi …" mini heading is 13px text in `--series-2` (#f45d29) on white: 3.25:1, and 4.5:1 is needed. The prototype has the same colour (prototype line 2685). | `features/dashboard/components/cash-band.tsx:101` |
| 4 | axe `scrollable-region-focusable` on `danh-muc.hang-hoa` | The `.tw` table wrapper scrolls horizontally at 1440 but cannot take keyboard focus, and none of its rows has a focusable control. | `shared/ui/data-table/data-table.tsx:30`, and the same wrapper in `shared/ui/matrix-table.tsx:22` |
| 5 | axe `label-content-name-mismatch`, 4 nodes on `thiet-lap.thue-bao` | The visible text is "Đổi sang gói này", but `aria-label="Đổi sang gói {tên}"` does not contain it (WCAG 2.5.3). | `features/settings/components/plan-cards.tsx:54` |
| 6 | Touch 1440: auth links Wx20 (7 auth states), "Về trang đầu" 77x20 | `.auth-links a` is inline at desktop width, so it has no minimum height. The 44px rule applies only at ≤860px or on coarse pointers. | `shared/styles/globals.css` additions block (~381, ~521) |
| 7 | Touch 1440: "Thẻ kho" 52x17, "Về Tài khoản & bảo mật" 153x17 | `.lnk { min-height: 24px }` does nothing on an inline `<a>`. | `globals.css:521`, `inventory/components/stock-table.tsx:175,190` |
| 8 | Touch 1440: native checkbox 13x13 on `thiet-lap.tai-khoan` and `sales.returns.exchange-dialog` | The drawn 44px checkbox exists only inside the ≤860px block. | `globals.css:539–551` |
| 9 | `tong-quan.dashboard.reset-dialog` (axe and touch) on the fresh store | Environmental. The "Đặt lại mẫu" button exists only in sandbox stores. It passes on the demo store. | none |

The earlier full run stopped at 137 of 194 tests, which was partway through touch at 390. Axe,
focus and keyboard had all finished, and touch at 390 and at 1440 were run separately. So no
failure is hidden. The demo store has more rows than the fresh store, so phase 4 may surface new
axe nodes. Any it finds are in scope.

## Constraints

- **Do not weaken any spec.** Repairing the evaluate calls is a harness fix. Each repaired check
  must still be able to fail: show it returning `false` in a negative case. If a repaired check
  then fails against the app, fix the app, not the check.
- **Keep the prototype CSS as it is.** Change only the additions block after
  `/* ---------- additions for the React build ---------- */` and `tokens.css`'s companions
  section. Leave the prototype values in place.
- **Desktop changes must stay small**, because parity at 1440 compares against the prototype.
  Record the parity ratios at 1440 before and after, and accept no new failing state.
- **Keep the 390 behaviour.** Touch at 390 keeps passing, and the 44px block keeps its values.
- **Follow e2e typing conventions.** The e2e files use `lib: ["ES2023"]` with no DOM types. For
  in-page code, use real functions with a typed globals interface, as `e2e/pos-offline.spec.ts`
  (`PageGlobals`) does, or use Playwright locators such as `:focus`. Do not use string functions.
- **Keep user-facing copy and accessible names consistent.** Unit tests that query a renamed
  button are updated to the new name.

## Validated decisions (owner, 2026-10-20)

- **Desktop checkbox:** a 24px transparent box with a 16px square drawn the same way as at 390.
- **Plan button:** the visible text stays "Đổi sang gói này"; the accessible name is
  "Đổi sang gói này: {tên gói}".
- **Scrolling tables:** the wrapper becomes a focusable named region only while it overflows.
- **"Chi" heading:** `--accent-orange-strong` (#cb4d22) on the small heading only. The chart,
  legend and KPI value keep the prototype orange.

## Non-goals

- The seeded `cua-hang-a` store: its owner password is not available locally.
- The msedge project: CI runs it, and it runs locally only if Edge is installed.
- The NFR-11 cell in `docs/release/acceptance-matrix.md`, because no seeded-store run happens.
- Committing or deploying. The user decides that separately.
- The untracked `e2e/visual/parity.spec.ts-snapshots/`.

## Phases

| Phase | File | Status |
|-------|------|--------|
| 1. Repair the focus and keyboard specs, then fix any real failure they reveal | [phase-01-focus-keyboard-specs.md](phase-01-focus-keyboard-specs.md) | completed |
| 2. Fix the three axe violations | [phase-02-axe-violations.md](phase-02-axe-violations.md) | completed |
| 3. Desktop 24px targets | [phase-03-desktop-24px-targets.md](phase-03-desktop-24px-targets.md) | completed |
| 4. Full a11y suite, parity and gates | [phase-04-full-verification.md](phase-04-full-verification.md) | completed |

Phases 1–3 touch different files and can run in any order. Phase 4 depends on all three.

## Acceptance criteria

1. `playwright test --project a11y` passes on the demo sandbox store, with only the two print
   states skipped.
2. Each repaired focus and keyboard check returns `false` in a negative case.
3. Parity at 1440 shows no state that starts failing, and parity at 390 shows none either.
   Ratio changes stay limited to the touched controls.
4. `corepack pnpm lint`, `corepack pnpm typecheck` and `vitest run` are clean, including the
   updated `subscription-page.test.tsx` and a new contrast row in `tokens.test.ts`.
5. The 390 plan and its report are updated: criterion 2 met, with a link to this plan.

## Result

See [the tester report](../reports/tester-261020-0021-desktop-targets-axe-keyboard.md).
- The whole `a11y` project passes on the demo sandbox store: 188 passed, and the 6 skips are the two
  print states.
- Every repaired check fails in a negative case. The repaired specs found no app bug. They found one
  more spec bug: the sidebar chevron locator was tied to the name that changes on expand.
- No parity state starts failing at 1440 or at 390. At 1440 only `thiet-lap.tai-khoan` moved
  noticeably (0.07 to 0.09), because the 24px checkbox box shifts the content below it by 4px.
- The review found three checkbox defects: the field border, overlapping hit areas, and the size in
  forced colours. All three are fixed.

