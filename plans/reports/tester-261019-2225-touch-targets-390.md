# Test report: 390px touch targets

Plan: [261019-2225-touch-targets-390](../261019-2225-touch-targets-390/plan.md)
Status: 390 passes on both local stores. The seeded `cua-hang-a` run was not done. The 1440 run and
part of the axe and keyboard suite failed here, but those failures existed before this change.
Update 2026-10-20: criterion 2 is met. [The follow-up plan](../261020-0021-desktop-targets-axe-keyboard/plan.md)
fixed them, and the whole `a11y` project now passes on the demo store
([report](tester-261020-0021-desktop-targets-axe-keyboard.md)).

## Commands

Every command ran from `apps/web` against the dev stack on :5173 and :8080. The owner password
stayed in the shell.

```
E2E_TENANT_URL=<store> E2E_OWNER_EMAIL=<owner> E2E_OWNER_PASSWORD=<shell only> \
  timeout 590 corepack pnpm exec playwright test --project a11y \
  --grep "touch targets at 390|seeded store's owner" --reporter=line
```

The 1440 run used `touch targets at 1440`, and the full suite used `--project a11y` with no grep.
Parity used `--project visual --grep "parity at 390"`.

## Stores

| Store | Data |
|-------|------|
| Fresh store `e2e-a11y-18733` | Onboarding done, almost no rows |
| Demo sandbox `e2e-a11y-18733-demo` | Onboarding done, 22 SKUs, 22 stock rows. The sales, purchasing and debt document tables are empty. |
| Seeded `cua-hang-a` | Not run. Its owner password is not available locally. Resetting it would change an account the user may be using. |

## Results

| Run | Before | After |
|-----|--------|-------|
| Touch 390, fresh store | 59 failed, 2 passed, 2 skipped | 59 passed, 2 skipped, 2 failed: `kho.ton` "Đỏ" 42x44 (fixed afterwards with the `.chip` rule; passes on the demo store) and `tong-quan.dashboard.reset-dialog` (see below) |
| Touch 390, demo store | not run | **61 passed**, 2 skipped |
| Touch 1440, fresh store | 13 failed (same list) | 13 failed, 48 passed, 2 skipped |
| Full a11y, fresh store (axe, keyboard, focus) | 9 failed at HEAD | 9 failed, same tests and same axe messages |
| `corepack pnpm lint`, `corepack pnpm typecheck` | | clean |
| `vitest run` | | 158 files, 1131 tests passed |

- **Skipped tests.** The 2 skipped states are the print states. They need document ids, and the
  document tables are empty on both stores.
- **Reset dialog.** "Đặt lại mẫu" exists only in sandbox stores, so this dialog state times out on
  the fresh store. It passes on the demo store.
- **How "before" was measured.** The before numbers for the full suite came from the same tests at
  HEAD, with this change set aside by `git stash push -- apps/web/src` and then restored.

### The "before" failures fixed

| Cause | Fix |
|-------|-----|
| Skip link measured off screen, 42px tall when focused | Visually hidden (1x1, `clip-path`) until focused; 44px tall when focused |
| `.btn` 37px tall | `.btn` added to the 44px min-height list |
| `.auth-links a` 20px tall | min-height and `inline-flex` |
| Anchor `.lnk`, "Về trang đầu", "Về Tài khoản & bảo mật" | `a.lnk` in the min-height and min-width lists. The not-found paragraph uses `auth-links`, and the account-closure back link is a `TextLink`. |
| `.cp` and `.chip` ("Đỏ") 42px wide | min-width 44px, centered |
| Native checkbox 13x13 | 44x44 transparent box with a 20px square drawn on it. Checked, disabled and focus-visible states are kept. Forced colors fall back to the native control. |

The onboarding overrides `.onb .btn` and `.onb .btn-row > .lnk` were removed, because the global
rules now cover them. Onboarding still passes at 390.

## Failures that existed before this change

These failures are the same at HEAD, so this change did not cause them.

**Touch 1440 (24px)**
- Auth links Wx20
- Checkbox 13x13 on `thiet-lap.tai-khoan` and the exchange dialog
- "Thẻ kho" 52x17: `a.lnk` is inline at desktop width
- "Về Tài khoản & bảo mật" 153x17
- "Về trang đầu" 77x20
- Reset dialog: environmental

The plan assumed the 1440 run already passed. It did not. The plan made desktop out of scope, so
these failures were left as they were.

**axe**
- color-contrast on `tong-quan.dashboard`
- label-content-name-mismatch (4 nodes) and scrollable-region-focusable on `danh-muc.hang-hoa` and
  `thiet-lap.thue-bao`

**Keyboard and focus**
- Focus stays inside a dialog
- Focus when the opener is removed
- Visible focus indicator
- Sidebar group
- Stock adjustment dialog
- Stock count dialog

## Parity at 390

- **Before:** 59 failed. Every prototype state already differed from its prototype capture by 4–40%,
  because the store data differs.
- **After:** 47 failed, 14 passed.
  - The 12 extra passes are addition states. They now compare against baselines that the before run
    wrote into the untracked `e2e/visual/parity.spec.ts-snapshots/`.
  - No state started failing.
  - `chien-dich.khach-hang` and `kho.kiem-ke.new-count-dialog` rose by 0.01, because `.btn` is
    taller.
  - Five states fell by 0.01.

Parity at 1440 was not run. No rendered rule changes at desktop width for prototype states: the skip
link is hidden in both versions, and the two TSX edits touch addition screens only.

## Production store `thegioidoan` (real data)

- **Run:** the 390 spec against `https://thegioidoan.sidecup.app` as deployed. The deployed web build
  is from 18:11 on 2026-10-19 and does not contain this change.
- **Result:** 60 failed, 2 skipped (print states). This is the "before" picture on real data.
- **Every undersized control falls into a group the local code already fixes:**
  - skip link
  - `.btn` 37px
  - auth links
  - lone links
  - "Đỏ" 42x44
  - checkbox 13x13
  - the date-picker button "Chọn ngày trên lịch" 19x44, on 21 states. Its `.dp-btn` min-width came
    with the onboarding rebuild, which was committed after that deploy.
- **No new kind of control** appeared with real data.

### After the web deploy (2026-10-20 00:06)

- **Deploy:** only `web` was redeployed, with image `tally-web:55480f3-dirty`, following the runbook's
  web-only redeploy. api, worker and postgres were not touched.
  - Rollback image: `tally-web:rollback-202610200006`.
  - The container reports healthy, and the edge serves `assets/index-DtyvgHZQ.css`.
- **The same 390 run:** 58 passed, 3 failed, 2 skipped. None of the 3 failures is an undersized
  control:
  - `tong-quan.dashboard.reset-dialog`: `thegioidoan` is not a sandbox store, so it has no
    "Đặt lại mẫu" button.
  - `auth.signup` and `auth.find-store`: `net::ERR_NAME_NOT_RESOLVED` for `https://sidecup.app`.
    This machine's resolver does not resolve the apex domain (1.1.1.1 does). The "before" run hit
    the same error on these two states.

## Limits

- The seeded `cua-hang-a` run was not done, so acceptance criterion 1 is met only on the two local
  stores. Rows that appear only with seeded documents are untested: sales, purchasing and debt
  tables, and pagination.
- Acceptance criterion 2 is not met. The failures above existed before this change and are outside
  the 390 scope.
- The NFR-11 evidence cell in `docs/release/acceptance-matrix.md` was left unchanged, because the
  full a11y suite does not pass on the seeded store.
