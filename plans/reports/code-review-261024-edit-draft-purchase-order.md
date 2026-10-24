# Code review: edit and delete a draft purchase order (2026-10-24)

Scope: uncommitted diff (15 files) and the untracked po-delete-dialog files. Plan: plans/261024-1051-edit-draft-purchase-order/.
Checks run: vitest on src/features/procurement and src/features/sales/api (15 files, 112 tests pass), `pnpm typecheck` clean,
eslint on the touched files clean. Go integration test and e2e were not run.

## High

1. **Line colors are silently removed when the color directory has not loaded.** `new-po-dialog.tsx:54` waits for the order
   and its SKUs, not for `/colors`. `colors.codeOf` returns "none" until the directory has loaded (`catalog/api/queries.ts:92`),
   so `draftOrderBuyLine` gets `color = null`. `PoForm` keeps that in `useState(initial)` (`new-po-dialog.tsx:90`).
   `orderLinesRequest` then leaves out `color_id`, the lines JSON differs from `storedOrderLineRequest`, and any save sends a
   PUT that strips every line's color. If `/colors` fails, this happens every time. In the normal case it is a race:
   `/colors` must be slower than the order request followed by the SKU request. This also breaks "nothing changed sends no
   PUT". Tests stub `/colors` as `[]`, so they never cover it. Fix: have the loader wait until the color lookup has
   resolved, and show the error if it failed.
2. **The form is keyed by version, which breaks "a retry continues from the last step that finished".**
   `useSaveOrder` lives inside `PoForm`, which is keyed by `${id}:${version}` (`new-po-dialog.tsx:67,93`). A
   `PartialCommitError` invalidates `POSTING_KEYS` (`use-commit.ts:156`) and `useOrder` refetches. Every edit-mode partial
   failure comes after a PATCH/PUT, so the version has changed and the form remounts. The remount drops the progress
   (`done`, `writes`) and the idempotency key. Two concrete results:
   - If confirm succeeds and a Giá bán lẻ write fails, the toast says "Bấm lại để làm tiếp phần còn thiếu". After the
     remount, `sell` is back at the SKU's current price, so `sellChanges` is empty. The next click toasts success and
     never writes the price.
   - If confirm lost its answer but was stored, the dialog now shows a confirmed order. "Lưu nháp" toasts a false
     "Đã lưu nháp PO-…". "Lưu & chốt" sends a new confirm, which fails with 409 and shows "…chưa chốt đơn: Đơn nhập đã chốt."
   This is worse than accepted limitation (b): it loses intent progress, not only unsaved edits. Fix: move `useSaveOrder`
   up into `NewPoDialog`, and do not re-key for a version that this intent's own write produced. At minimum, carry
   `sells` across the remount.

## Medium

3. **The loader shows no status guard.** `new-po-dialog.tsx:53-74` renders the edit form for any `d.status`. A draft
   confirmed elsewhere (a 412 makes the list refetch) or by this intent (item 2) reopens as "Sửa đơn nhập" with save
   buttons. Show "Đơn nhập đã chốt." and no form when `d.status !== "draft"`.
4. **A failed SKU load drops lines.** `new-po-dialog.tsx:54` deliberately goes on when `skus.isError`. Every row then
   gets `sku = null`, and `orderLinesRequest` skips such rows as empty (`buy-lines.ts:478`). If the owner adds a row and
   saves, the PUT replaces the whole order with that one row. The same silent drop happens past `MAX_SKU_IDS` (200)
   distinct SKUs. Show the error instead of the form.

## Low

5. A cleared Ngày dự kiến giao is silently kept (`mutations.ts:148`). The API cannot clear it (`orders.go:348`), but the
   form shows it as empty and the save toasts success. Either block clearing the date in edit mode or show a hint.
6. `storedOrderLineRequest` keeps the stored `discount_amount` (`buy-lines.ts:502`), but `orderLinesRequest` always sends
   0. A line with a discount set through the API is therefore always PUT, and the save resets its discount.
7. The 412 retry path is last-writer-wins. `save-order.test.tsx` "stores nothing over a draft changed elsewhere" asserts
   that the second click PUTs the old form at the newer version. In the app, the 412 refetch usually remounts the form
   first, so this only bites inside the refetch window. The test name overstates the guarantee.
8. Delete: `PoDeleteDialog` holds the version as a snapshot (`po-delete-dialog.tsx:22`), so after a 412 every retry
   fails with 412 again until the owner closes and reopens the dialog. Also, `onSuccess` waits for invalidation before
   `after`, so the open drawer refetches the deleted order and can flash its 404 state before it closes.
9. Accessibility is fine. The row buttons have `aria-label="Sửa PO-…"` / `"Xóa PO-…"`, which contain the visible text.
   The drawer buttons rely on the drawer's title for context, which is acceptable.

## Verified OK

- If-Match chaining across PATCH, PUT and confirm (3→4→5).
- PATCH sends `supplier_party_id` only when the supplier changed.
- Moving `isStale` to `api-error.ts` keeps the behavior.
- The create path is unchanged, and the existing create tests pass unmodified.
- The `write` closure tracks the rebound `edit`.
- A lost answer leads to a GET and then a diff.

## Test gaps

- No dialog test where the color directory resolves late or fails, or where the SKU load fails.
- No test of a partial failure in edit mode followed by a retry through the real dialog (with the remount).
- Orders page: no check that a confirmed order's drawer has no Sửa/Xóa nháp.
- The e2e test was not run.

Status: DONE_WITH_CONCERNS

## Resolution (2026-10-24)

- **1 Fixed.** The edit loader waits for the color list when any line has a color, and shows the error if that list fails. `ColorDirectory` now exposes `pending` and `error`. Covered by the dialog test "waits for the color list…".
- **2 Fixed.** The form is no longer keyed by version. It opens once on the draft it first read, and keeps what was typed and the save progress through the refreshes its own saves cause. Covered by "retries only the price after the edits and the confirm were stored".
- **3 Fixed.** An order that is no longer a draft shows "Đơn nhập đã chốt." instead of the form. This is checked only before the form opens.
- **4 Fixed.** A failed SKU load shows its error. A line whose SKU is missing blocks the form instead of being dropped.
- **5 Kept, recorded as a limitation.** The API cannot clear an expected date.
- **6 Kept, recorded as a limitation.** A discount set only through the API is reset when its line is re-sent, because the UI has no discount field.
- **7 By design (plan).** A retry after a 412 re-reads the order, then sends what differs, as `useSaveInvoice` does.
- **8a Kept.** After a 412 the app-wide message asks the user to reload, and reopening the dialog from the refreshed list takes the new version.
- **8b Not reproduced.** A failed refetch keeps the cached `data`, and the drawer shows an error only when `!o`.
- **Out of scope, pre-existing.** The receipt dialog (`new-receipt-dialog.tsx`) and the sales invoice dialog (`useColorMaps`) prefill line colors without waiting for the color list either.
