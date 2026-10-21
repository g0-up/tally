---
title: Review the multi-unit plan for its Kho impact
date: 2026-10-21
summary: "Added Kho › Tồn kho unit display and split the unit delete locks (documents, price history, barcodes) in the multi-unit plan"
---

# Review the multi-unit plan for its Kho impact

## What happened
The user asked whether `plans/261020-1801-multi-unit-conversion-and-unit-prices/` affects Kho and whether Kho should show the units. The plan named no Kho screen. Scouting found:
- Tồn kho shows only the base unit (`inventory/components/stock-table.tsx:88`), and its "Sửa giá" button opens `EditPriceDialog`, which Phase 4 changes.
- Thẻ kho already reads the SKU's active units for "Quy đổi tự động" (`inventory/queries.go:703`), so it follows the new units without code.
- Kiểm kê scans add the unit factor; an inactive unit's barcode stops resolving (`catalog/code_lookup.go`).
- `SkuUnitInUse` counts `price_list_items` and `price_changes` as use (`catalog/usage_repo.go:33`). A unit created with its own price would read as "used on documents", and a cleared price still leaves history, so the plan's in-use flag would be wrong.
- Units with barcodes cannot be deleted, and deactivating the default purchase unit fails with `default_unit_inactive`.

## Decision
- User choices: Tồn kho shows the unit/conversion line and the converted on-hand/sellable quantity ("= 2 thùng 5 dây"). It does not show per-unit prices or an edit button.
- User choice: a unit with price history is "Ngừng dùng" only; the history stays.
- The API exposes `locked_by: documents | price_history | barcodes | null` instead of a boolean.
- Deactivating the default purchase unit moves the default to the next largest active unit.
- The web `autoConvertQty` follows the API's `AutoConvert.Format`. The prototype reuses `qMix`.
- Phase 5 adds `e2e/inventory.spec.ts`, a Tồn kho parity state and the Kho user guide page. Effort is now 5.5d.

## Next steps
Phase 1 (prototype v6.15) still gates everything; it now includes `#kho.ton`.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
