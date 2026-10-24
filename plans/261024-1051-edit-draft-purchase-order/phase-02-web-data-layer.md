---
phase: 2
title: "Web data layer: prefill, save edits, delete"
status: completed
effort: 0.5d
---

# Phase 2: Web data layer: prefill, save edits, delete

## Context

- `orderBuyLine` (`apps/web/src/features/procurement/buy-lines.ts:154`) builds a receiving row in base units, with an `order` ref. Editing needs the ordered row as it is: its unit, quantity and price per unit, with no `order` ref.
- `useSaveOrder` (`apps/web/src/features/procurement/api/mutations.ts:106`) only creates. `useSaveInvoice` (`apps/web/src/features/sales/api/invoice-mutations.ts:150`) shows how a draft is edited: it keeps the last header and lines JSON it sent, PATCHes or PUTs only what differs, and after a 412 or a lost answer re-reads the stored invoice.

## Requirements

1. In `buy-lines.ts`, add `draftOrderBuyLine(ol: OrderLine, sku: SkuListItem | null, color: ColorCode | null): BuyLine`. It sets `sku`, `unitId = ol.unit_id`, `qty = ol.qty`, `price = ol.unit_price`, `spec = ol.spec_text ?? ""`, `color`, `sell = sku?.retail_price ?? null`, `isNew = false` and `order = null`. Keep the current Giá bán lẻ of the SKU, so `sellChanges` reports only what the owner types.
2. In `mutations.ts`, give `SaveOrderVars` an optional `order: Order | null` (the draft being edited):
   - With no order, keep today's create path unchanged.
   - With an order, the stored doc starts as that order. Record its header JSON (`supplier_party_id`, `expected_date`) and its lines JSON. If the header differs, PATCH it. If the lines differ, PUT them. Each write sends `ifMatch: etagOf(version)` and passes its returned version to the next.
   - On 412 or `outcomeUnknown`, mark the progress stale. `isStale` is private to `invoice-mutations.ts:130`, so move it to `shared/api/api-error.ts` and import it in both places rather than copy it. The next attempt GETs `/purchase-orders/:id` first, as `useSaveInvoice` does.
   - Then run the existing `confirm` step and `sellPriceSteps`, unchanged.
   - Success toast: "Đã lưu nháp **PO-…**." or "**PO-…**: Đã chốt.", as today.
3. Add `useDeleteOrder(after)`. It sends `client.delete('/purchase-orders/:id', { ifMatch })`, toasts "Đã xóa đơn nhập nháp **PO-…**." and invalidates `procurementKeys.all`. Copy `useDeleteInvoice` (`invoice-mutations.ts:342`).
4. Map the refusals through `withProcurementMessage`. `order_not_draft` already maps to "Đơn nhập đã chốt." (`adapters.ts:64`).

## Files

- Modify: `apps/web/src/features/procurement/buy-lines.ts`, `buy-lines.test.ts`
- Modify: `apps/web/src/features/procurement/api/mutations.ts`, `api/save-order.test.tsx`
- Modify: `apps/web/src/shared/api/api-error.ts`, `apps/web/src/features/sales/api/invoice-mutations.ts` (move `isStale`)

## Tests

- `buy-lines.test.ts`: `draftOrderBuyLine` keeps the ordered unit, quantity and unit price. `orderLinesRequest([draftOrderBuyLine(ol)])` gives back the stored line's request.
- `save-order.test.tsx`:
  - An unchanged edit sends no request except a confirm, when one is asked for.
  - A header-only edit sends one PATCH. A lines-only edit sends one PUT.
  - Both together chain the versions.
  - A 412 then a retry re-reads the order.
  - Delete sends If-Match.

## Validation

`cd apps/web && pnpm test -- procurement`, then `make test-web` and `make lint` (lint includes typecheck).

## Risk

The edit mutation now has two branches. Keep the create path exactly as it is: the existing `save-order.test.tsx` cases must pass unmodified.
