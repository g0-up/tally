---
phase: 4
title: "Web: Sửa đơn vị & giá, per-unit price edit"
status: done
priority: P1
effort: 0.5d
dependencies: [2, 3]
---

# Phase 4: Web: Sửa đơn vị & giá, per-unit price edit

## Goal

Let a manager open an existing goods SKU, edit its whole unit table and per-unit prices, and save it in one request. Let the price list editor show and clear per-unit prices.

## Context

- Danh mục › Hàng hóa: `catalog/pages/products-page.tsx`. It already opens `BarcodeUnitsDrawer` from a row; the SKU in the list carries `units`, which have prices after Phase 2.
- `pricing/price-edit.ts` + `components/edit-price-dialog.tsx` edit base retail/wholesale through `api/mutations.ts` (`POST /pricing/sale-prices`).
- `UnitPriceTable` from Phase 3.
- POS loads units and prices from the bootstrap (`apps/api/internal/sales/pos_bootstrap.go`). The web caches it under the sales query keys.

## Requirements

1. **Row action "Đơn vị & giá"** on goods rows when the user has the settings permission (the same gate as v6.15 `can('settings')`). It opens `EditUnitsDialog` (new `catalog/containers/edit-units-dialog.tsx`).
2. **Dialog**: `UnitPriceTable` prefilled from the SKU.
   - The base unit is read-only; changing the base unit is out of scope.
   - A unit with `locked_by` set shows its reason and a "Ngừng dùng" toggle instead of remove: `documents` → "Đã dùng trên chứng từ", `price_history` → "Đã có lịch sử giá", `barcodes` → "Có mã vạch".
   - Changing the factor of a unit locked by `documents` shows "Chứng từ cũ giữ hệ số cũ."
   - A unit with a barcode (from the SKU's `barcodes`): "Ngừng dùng" shows "Mã vạch của đơn vị này sẽ không quét được ở bán hàng và kiểm kê."; a factor change shows "Kiểm kê quét mã này sẽ cộng theo hệ số mới."
   - "Ngừng dùng" on the default purchase unit is allowed; the hint names the unit that becomes the default (Phase 2 rule 3).
   - Save sends `PUT /skus/{id}/units` with `If-Match: etagOf(sku.version)`.
   - A 412 shows the existing "đã thay đổi, tải lại" message pattern.
   - `unit_in_use` marks that row and switches it to "Ngừng dùng".
3. **How "locked" is known.** The dialog reads `locked_by` from Phase 2's unit DTO, so it shows the state before save. A `unit_in_use` save error is still handled for a lock that appeared after the list loaded.
4. **Cache.** On success, invalidate `skuKeys` (list + by-ids), the price list queries, the POS bootstrap/catalog queries, and the inventory stock list and stock card queries, so POS prices the new unit at once and Tồn kho / Thẻ kho show the new units.
5. **Price list editor.** `EditPriceDialog` lists the SKU's per-unit retail/wholesale next to the base price. Clearing one sends `price: null` with that `unit_id` (Phase 2 rule 4). Per-unit tiers and per-color unit prices stay on the price list screen as today. The dialog opens from Kho › Tồn kho (`inventory/pages/stock-page.tsx`) and Nhập hàng › Hàng hóa (`procurement/pages/products-page.tsx`); both must keep working and get a test.

## Files

- Create: `apps/web/src/features/catalog/containers/edit-units-dialog.tsx`
- Modify: `catalog/pages/products-page.tsx`, `catalog/api/mutations.ts` (`useReplaceSkuUnits`), `catalog/index.ts`
- Modify: `pricing/price-edit.ts`, `pricing/components/edit-price-dialog.tsx`, `pricing/api/mutations.ts`
- Check: `inventory/pages/stock-page.tsx`, `procurement/pages/products-page.tsx` (callers of `EditPriceDialog`)

## Tasks & Steps

- [x] Add `useReplaceSkuUnits(skuId)` with the invalidations.
- [x] Build `EditUnitsDialog` on `UnitPriceTable` with an `editing` mode (read-only base, in-use rows).
- [x] Add the row action and permission gate (gate deviation: see notes).
- [x] Extend `price-edit.ts` with unit rows and null-to-remove; update `edit-price-dialog.tsx`.
- [x] Component tests: prefill, each `locked_by` reason, barcode hints, default purchase unit hint, 412, `unit_in_use` mapping, cleared price sends null, `EditPriceDialog` opened from Tồn kho.

## Verification

- `cd apps/web && pnpm test -- edit-units-dialog price-edit save-sale-price products-page stock-page`
- Manual (AC3, AC5, AC6): clear the dây price → POS sells 1 dây at 25.000; try to remove a unit used on a receipt → "Ngừng dùng" is offered and the unit disappears from POS, Tồn kho's unit line and Thẻ kho's unit chips after save.

## Risks

- A stale list: the dialog uses the SKU version from the list. After another tab's edit, save returns 412. The dialog refetches the SKU and keeps the user's draft, the same pattern as the product edit form.

## Implementation notes

- **SKU version source.** `SkuListItem` does not carry `version`, and making it required would break many fixtures. The dialog reads `GET /skus/{id}` through the new `useSkuDetail` (`skuDetailSchema`: version, units with `locked_by`, barcodes). The base unit row of that read carries the base prices.
- **Edit mode of `UnitPriceTable`.** A new optional `saved` prop (`SavedUnits`) turns it on; with `baseUnitName` the base unit is read-only. Locked rows show the unit name with the lock reason instead of the unit picker, and only the "Ngừng dùng" checkbox (also shown for a unit saved as inactive). The hints compare each row with its saved unit. The default purchase hint follows the API rule (`purchaseRowAfterSave`). Stopped rows get `.ut-dim`, and the preview skips them. The CSS `.ut-off` / `.ut-dim` from the prototype was added to `globals.css`.
- **Edit helpers** in `product-draft.ts`: `savedUnitTable`, `savedUnitsOf`, `savedUnitRow`, `purchaseRowAfterSave`, `skuUnitsRequest` (the whole table, with a blank unit price sent as null), `sameUnitTable` and `editUnitTableErrors`. The last one refuses a blank base price that is saved, because the API would keep it.
- **Errors.**
  - 412 goes through the global handler (stale toast plus invalidating all queries). The form keeps its draft, and the next save uses the refetched version.
  - `unit_in_use` (`unitsInUse`) puts the removed unit back as "Ngừng dùng" with the lock reason, marks the row, and remembers the lock until the dialog closes.
  - Any other 422 maps `units[i].*` to its row through `productFormErrors`.
- **Cache.** `useReplaceSkuUnits` invalidates `skuKeys.all` (list, by-ids and detail), `["pricing"]`, `posKeys.all`, `["inventory","stock"]` and `["inventory","stock-card"]`. The pricing and inventory keys are written as literals because those features import catalog.
- **Price editor.**
  - `PriceDraft.units` and `CurrentPrices.units` hold the units' own prices.
  - `salePriceRequests` adds one request per changed unit price, with `unit_id` and `price: null` for a cleared one.
  - `postSalePrice` slots a unit write as `<scope>:unit-<id>`, so the base-price keys are unchanged.
  - The partial-save message names the unit ("giá sỉ / thùng").
  - The dialog lists the active non-base units with the auto price as placeholder; `autoPrice` is now exported from catalog.
  - The unit checks: an own price must be above 0, and an own wholesale must not be above the own retail (the unit table rule).
- **Deviation: permission gate.** The web app has no role or permission model, and these API routes have no role check. So `can('settings')` cannot be reproduced, and the action is shown on every goods row. Add the gate when a permission model exists.
- **Tests.**
  - `edit-units-dialog.test.tsx` (9) and `catalog/pages/products-page.test.tsx`.
  - Additions to `product-draft.test.ts`, `api/mutations.test.ts` and `save-sale-price.test.tsx`.
  - `pricing/price-edit.test.ts`.
  - A unit-price test in `inventory/pages/stock-page.test.tsx`, and `procurement/pages/products-page.test.tsx`.
- **Manual verification** (AC3, AC5, AC6) was not run here; the e2e work covers it.

