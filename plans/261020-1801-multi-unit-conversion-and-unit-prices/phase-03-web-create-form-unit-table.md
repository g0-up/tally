---
phase: 3
title: "Web: unit table on create screens, units on Tồn kho"
status: done
priority: P1
effort: 1.5d
dependencies: [2]
---

# Phase 3: Web: unit table on create screens, units on Tồn kho

## Goal

Port the approved v6.15 unit table into the React app and use it on every product create screen, so one submit creates the SKU with all its units and per-unit prices.

## Context

- The draft has one big unit today: `ProductDraft.bigUnitId` + `factor` (`catalog/components/product-draft.ts:13–60`). `purchaseUnit(d)` maps it to `purchase_unit_id` + `factor` (84). `quickCreateRequest` converts the cost to the purchase unit (118–120).
- `ProductForm` (`catalog/components/product-form.tsx`) is shared by `containers/new-product-dialog.tsx` (Danh mục), `containers/quick-product-dialog.tsx` (Nhập hàng › Danh sách hàng hóa, `procurement/pages/products-page.tsx`), and the onboarding steps `onboarding/steps/catalog-setup-step.tsx` and `opening-stock-step.tsx`.
- The purchase-order new-product line has its own `factor` / `purchase_unit_id` (`procurement/buy-lines.ts:45, 108, 304–358`).
- `useUnits()` reads the unit directory (`catalog/api/queries.ts:36`). The `Combobox` takes `onCreate` (`shared/ui/combobox.tsx`). `POST /units` exists; there is no web hook for it.
- The list column is built by `unitConversionLines(s)` (`catalog/api/adapters.ts`, used at `catalog/pages/products-page.tsx:62`).

## Requirements

1. **Draft.** Replace `bigUnitId` + `factor` with `units: UnitRow[]`, where `UnitRow = { key, unitId, unitName, factor, retail, wholesale }`. Keep `retail` and `wholesale` on the draft for the base row. A null price means "auto".
2. **Validation** in `validateProductDraft`, with keys `units.<i>.factor` and `units.<i>.unit`. The messages come from v6.15: the factor must be positive, a duplicate unit, the base unit repeated. `unitPriceBelowBase(d)` returns the warning rows; it is not an error.
3. **Requests.**
   - `productRequest` sends `units` (Phase 2 shape) and no longer sends `purchase_unit_id`/`factor`.
   - `quickCreateRequest` sends `units`. The purchase price stays per the default purchase unit (the largest factor), the same rule as the API.
4. **`UnitPriceTable`** (new `catalog/components/unit-price-table.tsx`), controlled: `value`, `onChange`, `errors`, `baseUnitName`. It renders the v6.15 markup with the app's UI primitives and tokens:
   - the base row, then the extra rows with a remove button that has an `aria-label`;
   - "+ Thêm đơn vị" as a secondary small button;
   - the placeholder "<base × factor> tự tính";
   - the warning callout;
   - stacked rows below 600px.
   The unit field is a `Combobox` over `useUnits()` with `onCreate` → new `useCreateUnit()` in `catalog/api/mutations.ts` (`POST /units`, then invalidate `catalogKeys.units`). The hint "Đơn vị mới, sẽ thêm vào danh mục đơn vị" shows before creation.
5. **Form.** `ProductForm` replaces the "Đơn vị lớn / Hệ số" fields with `UnitPriceTable`. The base retail/wholesale inputs move into the table's base row, so they appear once. `productFormErrors` maps the API's indexed paths (`sku.units[1].factor`) to the draft keys (`units.1.factor`).
6. **Purchase-order new line.** The `BuyLine` new-product state gains `units`. The inline row keeps base unit + count unit. The "Đơn vị & giá…" link opens `UnitPriceTable` in a small dialog. The line's unit select lists every unit in the table. `buyLineRequest` sends `units` + `purchase_unit_id` (Phase 2 rule 2).
7. **Lists.** `unitConversionLines` renders `1 dây = 50 cái · 24.000`, or `· tự tính` (muted) when the price is null. Both lists (Danh mục and Nhập hàng) pick it up.
8. **Kho › Tồn kho** (`inventory/components/stock-table.tsx`), as approved in v6.15. The table already receives the listed SKUs (`skus` prop, `SkuListItem` with `units`), so no new request:
   - the name cell's sub line becomes "<code> · ĐV cơ sở: cái · 1 dây = 50 cái · 1 thùng = 500 cái", built from the active units without prices (a `unitConversionLines` variant, not the priced list form);
   - "Tồn thực" and "Có thể bán" on the main row add a `CellSub` "= 2 thùng 5 dây" from a new `autoConvertQty(sku, qty)` in `catalog/api/adapters.ts`. It follows the API's `AutoConvert.Format` (`inventory/queries.go:507`): active units with factor > 1, largest first, whole counts, remainder in the base unit, without the "(… cái)" tail. Nothing when the SKU has one unit, the SKU is not loaded yet, or the quantity is below the smallest factor;
   - color sub rows, the "Giá bán" column and the footer stay as they are.

## Files

- Create: `apps/web/src/features/catalog/components/unit-price-table.tsx`
- Modify: `catalog/components/product-draft.ts`, `product-form.tsx`, `catalog/containers/new-product-dialog.tsx`, `quick-product-dialog.tsx`, `catalog/api/mutations.ts`, `catalog/api/adapters.ts`, `catalog/schemas.ts`, `catalog/index.ts` (exports)
- Modify: `procurement/buy-lines.ts`, `procurement/containers/new-po-dialog.tsx` (or the line component that renders the new-product row), `procurement/schemas.ts`
- Modify: `inventory/components/stock-table.tsx` (unit line, converted quantity)
- Check only: `onboarding/steps/catalog-setup-step.tsx`, `opening-stock-step.tsx`. They inherit through `ProductForm`; change them only if they read `bigUnitId`/`factor`.

## Tasks & Steps

- [x] Change the draft type, `initialProductDraft`, validation and both request builders; update `product-draft` unit tests first.
- [x] Add `useCreateUnit`.
- [x] Build `UnitPriceTable` to match the v6.15 markup and classes 1:1.
- [x] Wire it into `ProductForm` and map server field errors.
- [x] Extend the PO new-product line and its request.
- [x] Update `unitConversionLines`.
- [x] Add `autoConvertQty` with unit tests copied from the API's cases (180 cái, bó 10, thùng 100 → "1 thùng 8 bó"; below the smallest factor → nothing; inactive unit skipped; decimal base quantity), then wire the Tồn kho cells.
- [x] `grep -rn 'bigUnitId' apps/web/src` returns nothing.

## Verification

- `cd apps/web && pnpm test -- product-draft buy-lines unit-price-table adapters stock-table` (new tests: indexed errors, auto price, warning, request shape for 0/1/N units, largest factor = purchase unit, `autoConvertQty`, the Tồn kho unit line and converted quantity).
- `pnpm typecheck` after `pnpm gen:api`.
- Manual: create "Kẹo X" (AC2) from Danh mục, from Nhập hàng and inside a PO; the list shows the three units. Receive 2 thùng 5 dây; Kho › Tồn kho shows 1.250 with "= 2 thùng 5 dây" (AC6).

## Risks

- `quickCreateRequest` cost conversion: a wrong purchase unit changes the reference cost. Cover it with a unit test (cái 500 cost, thùng ×500 → purchase price 250.000).
- The onboarding steps render `ProductForm` in a narrow column; check them at 390px.

## Implementation notes (2026-10-21)

Report: `plans/reports/fullstack-developer-261021-1408-phase-03-web-create-form-unit-table.md`.

- The PO line's unit table opens as an inline row under the line, as in the v6.15 prototype, not as a dialog.
- Stacked rows start below 860px, the prototype's breakpoint for `.ut`.
- A unit missing from the directory is created when it is picked in the combobox; the row shows the hint until `POST /units` returns.
- Quick-create names the base unit as `purchase_unit_id` when the table has units but none above the base unit.
- Not checked by hand in a browser, including the onboarding column at 390px.
- `e2e/procurement.spec.ts` still fills the removed "Hệ số" field; Phase 5 owns it.
