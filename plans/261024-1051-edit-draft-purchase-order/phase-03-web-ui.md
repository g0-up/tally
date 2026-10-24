---
phase: 3
title: "Web UI: Sửa/Xóa buttons and edit dialog"
status: completed
effort: 0.5d
---

# Phase 3: Web UI: Sửa/Xóa buttons and edit dialog

## Context

- `NewPoDialog` (`apps/web/src/features/procurement/containers/new-po-dialog.tsx`) holds the supplier, the expected date and the `BuyEditor` lines.
- `PoTable` (`components/po-table.tsx:74`) and `PoDrawer` (`components/po-drawer.tsx`) show "Chốt đơn" on a draft.
- `OrdersPage` (`pages/orders-page.tsx`) owns the `Opened` union of dialogs.
- Sales invoices follow the same pattern: `InvoiceDialog` takes `invoice: Invoice | null` and keys the form by `id:version`, and `invoice-delete-dialog.tsx` confirms a deletion.

## Requirements

1. `NewPoDialog` takes an optional `order?: Order` (from the list, so it has no lines).
   - With an order, load `useOrder(order.id)` for the lines and `useSkusByIds` for their SKUs. Show the loading text until both arrive.
   - Prefill the supplier `{ id, name }`, `expected_date` and `lines.map(draftOrderBuyLine)`. Key the form by `id:version`.
   - Title: "Sửa đơn nhập PO-…". The buttons are the same as when creating.
   - Pass `order` to `useSaveOrder`.
2. Add `containers/po-delete-dialog.tsx`, a `CommitDialog` worded like `invoice-delete-dialog.tsx`: "Xóa đơn nhập nháp PO-…?", with a hint that a draft has no stock or debt effect, and a "Xóa nháp" submit button using `useDeleteOrder`.
3. `PoTable` gets `onEdit` and `onDelete`. Draft rows show "Chốt đơn", "Sửa" and "Xóa" (danger), in the button order the invoice table uses.
4. `PoDrawer` gets `onEdit` and `onDelete`. A draft shows "Chốt đơn", "Sửa" and "Xóa nháp".
5. `OrdersPage` adds `{ kind: "edit"; order }` and `{ kind: "delete"; order }` to `Opened`. After a delete, close the drawer if it showed that order.

## Files

- Modify: `containers/new-po-dialog.tsx`, `containers/new-po-dialog.test.tsx`
- Create: `containers/po-delete-dialog.tsx`, `containers/po-delete-dialog.test.tsx`
- Modify: `components/po-table.tsx`, `components/po-drawer.tsx`, `pages/orders-page.tsx`, `pages/orders-page.test.tsx`

All files are under `apps/web/src/features/procurement/`.

## Tests

- `new-po-dialog.test.tsx`: the edit mode prefills supplier, date and lines. Saving after changing a quantity PUTs the lines.
- `po-delete-dialog.test.tsx`: confirming sends DELETE with If-Match.
- `orders-page.test.tsx`: a draft row shows Sửa/Xóa and a confirmed row does not. Sửa opens the edit dialog.

## Validation

`make test-web`, `make lint`. Check by hand at 390px and desktop: the row's three buttons stay on one line (`flexWrap: nowrap`) without the table scrolling sideways beyond what it does today. If they do not fit, keep only "Sửa" on the row and leave "Xóa" in the drawer, and note it in the report.

## Result (2026-10-24)

Measured with a mocked list in Chromium. At 1280px the table stays 960px wide with Chốt đơn, Sửa and Xóa on one line. At 390px the page does not scroll sideways. The table already scrolled inside its card (775px in a 358px frame) and now measures 872px. Keeping only Sửa would still measure 819px, so the fallback would not meet the bar either. The row keeps both buttons, as the owner decided.
