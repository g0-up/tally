# Test report: 1440 targets and the axe, keyboard and focus fixes

Plan: [261020-0021-desktop-targets-axe-keyboard](../261020-0021-desktop-targets-axe-keyboard/plan.md)
Status: the whole `a11y` project passes on the demo sandbox store. Only the two print states skip.
No parity state starts failing at 1440 or at 390.

## Commands and store

Every command ran from `apps/web` against the dev stack (`make dev-api` on :8080, `make dev-web` on
:5173). The store was the demo sandbox `e2e-a11y-18733-demo`. The owner password stayed in the shell.

```
E2E_TENANT_URL=http://e2e-a11y-18733-demo.localhost:5173 E2E_OWNER_EMAIL=<owner> \
  E2E_OWNER_PASSWORD=<shell only> corepack pnpm exec playwright test --project=a11y --reporter=line
corepack pnpm exec playwright test --project=visual --grep "parity at 1440|shell at 1440"
corepack pnpm exec playwright test --project=visual --grep "parity at 390"
corepack pnpm lint && corepack pnpm typecheck && corepack pnpm exec vitest run
```

## Results

| Run | Before | After |
|-----|--------|-------|
| Full `a11y` project | 137 of 194 run before the time limit cut it off. Failures in axe (3 states), focus (3), keyboard (3) and touch at 1440 (13 states) | **188 passed, 6 skipped**. The skips are `print.sales-document` and `print.statement` in axe, touch 390 and touch 1440 |
| Focus and keyboard specs alone | 6 failed | 8 passed |
| Axe on `tong-quan.dashboard`, `danh-muc.hang-hoa`, `thiet-lap.thue-bao` | 3 failed | 3 passed |
| Touch at 1440 and 390 | 13 failed at 1440 | 121 passed, 4 skipped (print) |
| `corepack pnpm lint`, `corepack pnpm typecheck` | clean | clean |
| `vitest run` | | 158 files, 1134 tests passed |

## Repaired checks still fail when they should

Each repaired check ran against a negative case in a scratch spec, outside the repo:

| Check | Negative case | Result |
|-------|---------------|--------|
| `tabTo` (keyboard spec) | Target that never takes focus (`#view h1`) | Throws "focus never reached" |
| Focus trap count | Dialog open, focus moved out with `blur()` | Count 0, so `toBe(1)` fails |
| Focus ring | `outline: none; box-shadow: none` injected | Returns `false` |
| Removed opener | Focus on `<body>` | Returns `false` |
| Sidebar chevron | Tab to the chevron, no Enter | "Thu gọn Kho" assertion fails |

## What the repaired specs revealed

- **No app bug in focus or keyboard handling.** Radix keeps focus in the dialog, restores it, and
  moves it into `#view` when the opener is gone. Programmatic focus shows the global
  `:focus-visible` ring.
- **One more spec bug.** The keyboard spec found the sidebar chevron by its name "Mở rộng Kho", but
  the name changes to "Thu gọn Kho" on expand, so the locator stopped matching. The check now
  follows the focused button and also asserts `aria-expanded="true"`.

## Review fixes

A code review of the change found three checkbox defects. All three were fixed before the final runs:

1. **Frame around checkboxes in form fields.** The prototype `.f input` border and padding applied
   to the now `appearance: none` checkbox. The checkbox rule now sets `border: 0`, `padding: 0` and
   `border-radius: 0`.
2. **Hit areas covered stacked options.** The negative vertical margin let a checkbox's box cover
   the option above it. The margin is now horizontal only. At ≤860px, `.check` centres its items.
3. **Forced colours.** The native control drew at the full box size. It now draws at the square
   size, and the wrapping label stays the large target.

A scratch probe on `/settings/account` measured the checkbox: 24x24 at 1440 and 44x44 at 390, with
border 0, and the box inside its label at both widths.

## Parity

**At 1440**, the before run used the same store and came before any source change:

| | Failed | Passed | Skipped |
|-|--------|--------|---------|
| Before | 56 | 6 | 2 |
| After | 43 | 19 | 2 |

- No state started failing.
- The 13 extra passes are addition states. They compare against baselines that the before run wrote
  into the untracked `e2e/visual/parity.spec.ts-snapshots/`.
- Two ratios changed by more than 500 pixels:
  - `thiet-lap.tai-khoan` went from 0.07 to 0.09. The checkbox is now a 24px box where the native
    one was 13px, so the content below it moves down 4px. This state already failed.
  - `thiet-lap.nhat-ky` gained 614 pixels and stayed at 0.07.

**At 390**, the run was compared on the same store. The before run used this plan's two visual
changes (`globals.css`, `cash-band.tsx`) temporarily reverted to the 390 plan's state, and both
files were restored afterwards (byte comparison checked).

- Both runs had 50 failed, 11 passed and 2 skipped, with the same failing states.
- No ratio moved by 500 pixels or more.
- The 390 report's numbers (47 failed, 14 passed) came from the fresh store, so they are not
  comparable. The extra demo-store failures are addition states whose 390 baselines were captured
  on the fresh store: `onboarding.wizard`, `settings.account-closure`, `sales.pos.offline` and
  `debts.receivables.payment-dialog`. They fail the same way with or without this change.

## Not covered

- The seeded `cua-hang-a` store, because its owner password is not available locally.
- The msedge project.
