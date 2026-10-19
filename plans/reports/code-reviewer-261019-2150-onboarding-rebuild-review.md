# Code review: onboarding rebuild from prototype v6.14

Plan: [261019-1858-onboarding-prototype-rebuild](../261019-1858-onboarding-prototype-rebuild/plan.md)
Status: all findings fixed and verified.

## Findings and fixes

| # | Severity | Finding | Fix | Test |
|---|----------|---------|-----|------|
| 1 | Medium | Reversals of opening stock documents (same `opening` kind, positive qty) were counted as opening stock in the confirm step's "Tồn kho theo màu" table. | `useOpeningStockLines` reads only documents with `reversal_of === null`. | `onboarding-page.test.tsx` "shows the opening totals…" (fails without the filter) |
| 2 | Low–Med | After a 409 `onboarding_steps_pending`, the checklist was not re-read, and the refused steps kept the button disabled even after the checklist changed. | The confirm mutation invalidates the checklist on refusal. The refused steps apply only while the checklist is still the one that was refused. | "names the steps the API still sees pending and re-reads the checklist" |
| 3 | Low | `useCommit` reran the fund loop with the old versions after a password prompt, so funds that were already saved got a 412. (A plain 412 is already handled app-wide: `app/platform.ts` reloads every query.) | The hook remembers the funds saved by an earlier pass and skips them. | "skips the funds already saved when it retries after the password check" (fails without the fix) |
| 4 | Low | A posted line with a lot but no expiry date showed "L · —". A line with an expiry date but no lot showed "—". | Join the parts that are present. | "shows a posted line's lot or expiry date even when it has only one of them" |
| 5 | Low | An API error on `lines[n].color_id` did not reach the form. | Map `color_id` to the color select, with `aria-invalid` and the message under the select. | "puts the API's refusal of a line's color under that color" |
| 6 | Low | Posting before the debt reasons had loaded showed a false "no reason" error. | The post button is disabled while the reasons are loading. | "waits for the adjustment reasons before posting" |
| 7 | Low | After each stock or debt posting, the web sent a redundant `:complete`. The API already completes the step in the same transaction (`inventory/adjustment.go`, `debt/adjustments.go`). | Remove the call. The checklist refresh shows the completion. | The posting tests assert no `:complete` call and that the step shows "Đã xong" |
| 8 | Low | Dead and duplicate CSS: `.onb-more`, `select.in.sku` (the SKU control is a Combobox), and `.onb-tbl .in.num` declared twice. | Removed or merged. | a11y onboarding run |

Accepted as is: N+1 detail reads for the posted debt and stock lists (bounded by the list limit); the `.dp-btn` min-width applies app-wide (intentional).

## Verification

- `corepack pnpm lint`, `corepack pnpm typecheck`: clean
- `vitest run`: 158 files, 1131 tests passed
- `playwright test e2e/onboarding.spec.ts --project root-chromium`: 2 passed
- `playwright test --project a11y --grep onboarding`: 4 passed
