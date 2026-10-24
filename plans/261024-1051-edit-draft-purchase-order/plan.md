---
title: "Edit and delete a draft purchase order"
description: "Let the owner reopen a draft Đơn nhập in the create dialog, change its supplier, expected date and lines, save it again or save and confirm it, and delete a draft they no longer need."
status: in-progress
priority: P2
effort: 1.5d
branch: master
tags: [feature, frontend, procurement]
blockedBy: []
blocks: []
created: 2026-10-24
---

# Edit and delete a draft purchase order

## Overview

A draft Đơn nhập (`status = draft`) has no effect on stock or payables, but the web app offers only "Chốt đơn" on it. A wrong line, quantity or supplier cannot be fixed before confirming.

The API already supports everything needed, and each call refuses anything but a draft at the given version (`lockDraft`, `apps/api/internal/procurement/orders.go:304`):

| Endpoint | Service | Changes |
|---|---|---|
| `PATCH /purchase-orders/:id` | `UpdateOrder` (`orders.go:320`) | supplier, order date, expected date, note |
| `PUT /purchase-orders/:id/lines` | `ReplaceLines` (`orders.go:378`) | every line, including "+ Hàng hóa mới" rows |
| `DELETE /purchase-orders/:id` | `DeleteOrder` (`orders.go:418`) | removes the draft |

None of these three endpoints has a test, and the web never calls them. So this is mainly web work, plus a test that locks the API contract the web will rely on.

## Outcome

- On a draft row in the Đơn nhập table, next to "Chốt đơn", there are a **Sửa** and a **Xóa** button. The PO drawer of a draft has **Sửa** and **Xóa nháp**.
- **Sửa** opens the "Tạo đơn nhập" dialog titled "Sửa đơn nhập PO-…", prefilled with the supplier, the expected date and the lines (product, unit, quantity, Quy cách, Đơn giá, color, Giá bán lẻ). It offers **Lưu nháp** and **Lưu & chốt đơn**, like the create dialog.
- Saving sends only what changed: the header through PATCH, the lines through PUT, then `:confirm` if the owner chose it, then any Giá bán lẻ edits. A retry continues from the last step that finished.
- **Xóa** asks for confirmation, then deletes the draft. The toast reads "Đã xóa đơn nhập nháp PO-…".
- Confirmed, partially received, completed and closed-short orders show neither button.

## User decisions (2026-10-24)

1. The buttons are on the table row and in the drawer, as on draft sales invoices.
2. No prototype update. The edit dialog reuses the prototype's "Tạo đơn nhập" dialog with another title.
3. Deleting a draft is in scope.
4. Later the same day the owner asked to sync the prototype after all: `docs/design/prototype-v6.15.min.html` now has Sửa/Xóa on draft rows (as icons, with the row redesign), "Sửa đơn nhập PO-…" in the create dialog, Sửa/Xóa nháp in the drawer and the delete confirmation. This replaces decision 2 and the "prototype screen" non-goal.

## Constraints

- Reuse `NewPoDialog`, `BuyEditor`, `orderLinesRequest`, `sellChanges`, `useCommit`/`runFollowUps` and the draft-edit pattern of `useSaveInvoice` (`apps/web/src/features/sales/api/invoice-mutations.ts:150`). Add no new shared abstraction.
- Every write sends `If-Match` with the version the previous step returned. A 412 or a lost answer re-reads the order before going on (the `stale` handling of `useSaveInvoice`).
- The supplier of a draft may since have been stopped. Keep it in the field. PATCH sends `supplier_party_id` only when the owner picked another supplier, so the server checks only a new choice.
- No API change. If the contract test in Phase 1 fails, stop and report it before any web work.

## Non-goals

- Editing the order date or the note. The create dialog does not show them, and this work does not add them.
- Editing a confirmed order. A confirmed order is closed short instead (`DeleteOrder` comment, PRD PUR-02).
- A prototype screen for editing.

## Phases

| # | Phase | Effort | Depends on |
|---|---|---|---|
| 1 | [API contract test for draft edit and delete](phase-01-api-contract-test.md) | 0.25d | — |
| 2 | [Web data layer: prefill, save edits, delete](phase-02-web-data-layer.md) | 0.5d | 1 |
| 3 | [Web UI: Sửa/Xóa buttons and edit dialog](phase-03-web-ui.md) | 0.5d | 2 |
| 4 | [E2E and user guide](phase-04-e2e-and-docs.md) | 0.25d | 3 |

## Acceptance criteria

- [x] A draft can be opened with **Sửa**, changed (supplier, expected date, add/remove/change lines, a new product), then saved as a draft. Opening it again shows the saved values.
- [x] **Lưu & chốt đơn** from the edit dialog saves the edits and confirms the order. Its status becomes "Đã chốt".
- [x] Saving with nothing changed sends no PATCH and no PUT.
- [x] An order changed elsewhere since the dialog opened (412) shows the existing "reload" message and stores nothing over the newer version.
- [x] **Xóa** / **Xóa nháp** deletes the draft after confirmation, and the list refreshes.
- [x] Non-draft orders show no Sửa/Xóa, and the API refuses them with `order_not_draft` (409).
- [ ] `make test` and `make lint` pass. The procurement integration test and the e2e procurement spec pass.
  - 2026-10-24: `make test`, the integration test, and web lint/typecheck pass. Open: `make lint` stops on a pre-existing errcheck in `apps/api/internal/catalog/catalog_test.go:541` (unchanged by this plan), and the e2e scenario "edits a draft order, confirms it, and deletes another draft" is written but not run (no `E2E_OWNER_PASSWORD` here). Review: [code review](../reports/code-review-261024-edit-draft-purchase-order.md).
