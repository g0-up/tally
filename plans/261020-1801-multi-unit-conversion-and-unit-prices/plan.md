---
title: "Multiple units per SKU with a sale price for each unit"
description: "Replace the single 'đơn vị lớn' with a KiotViet-style unit table (base unit + N units, each with factor, retail and wholesale price) on every product create screen and a new 'Sửa đơn vị & giá' editor, show the units and converted stock in Kho › Tồn kho, prototyped in v6.15 first."
status: in-progress
priority: P1
effort: 5.5d
branch: master
tags: [feature, frontend, backend, api, catalog, pricing, prototype-parity]
blockedBy: []
blocks: []
created: 2026-10-21
---

# Multiple units per SKU with a sale price for each unit

## Overview

A shop sells one item in several units: 1 cái = 500đ, 1 dây = 50 cái = 25.000đ, 1 thùng = 500 cái. Stock stays in the base unit; documents keep the unit and quantity as entered and snapshot the factor (already true: `receipt_lines.unit_id/qty/unit_factor_snap`, `base_qty = qty × factor`).

The backend already holds N units per SKU (`sku_units`), per-unit price rows (`price_list_items.unit_id`) and a resolver that prices a line in its unit before falling back to base price × factor (`apps/api/internal/pricing/resolver.go:154`). The gaps are: the create API takes only one extra unit and base prices, the list API does not return per-unit prices, a per-unit price cannot be removed, and the web forms show one "đơn vị lớn" and base prices only.

## Outcome

On every product create screen and in a new "Sửa đơn vị & giá" editor, the user maintains a unit table: the base unit with its retail and wholesale price, then any number of units "= [hệ số] <base unit>" with an optional retail and wholesale price. A blank price shows the computed base price × factor as a placeholder and is not stored. POS, quotes and invoices price a line in the chosen unit from that table.

Kho › Tồn kho shows the units too: under the product name, "ĐV cơ sở: cái · 1 dây = 50 cái · 1 thùng = 500 cái", and under the on-hand and sellable figures, the quantity converted to the larger units ("1.250" with "= 2 thùng 5 dây" below), the same "Quy đổi tự động" rule Thẻ kho already uses.

## Kho impact

| Screen | Effect | Work |
|---|---|---|
| Tồn kho | Shows only the base unit today (`inventory/components/stock-table.tsx:88`). Its "Sửa giá" button opens `EditPriceDialog`, which Phase 4 changes. | Unit line + converted quantity (Phases 1, 3); price dialog test from Tồn kho (Phases 4, 5). |
| Thẻ kho | Its unit chips come from the SKU's active units (`stock-card-filters.tsx:44`, `inventory/queries.go:703`). New units appear on their own; a unit set to "Ngừng dùng" disappears from the chips. | Test only (Phase 5). |
| Kiểm kê | A scan adds the scanned unit's factor (`count-drawer.tsx:103`). A "Ngừng dùng" unit's barcode no longer scans (`catalog/code_lookup.go` requires `su.active`); a factor change applies to the next scan. | Warnings in the editor (Phases 1, 4). |
| Điều chỉnh, Lô & hạn, Giữ hàng | Base unit only, as in the prototype. | None. |

## Constraints

- **Prototype first.** Phase 1 builds `docs/design/prototype-v6.15.html` (a copy of v6.14 plus the unit table). Phases 2–5 start only after the user approves it.
- **Design system = the prototype's own system.** Use only the `:root` tokens and the existing component classes (`.btn` variants, `.fg`/`.f`, `.in`, `.check`, `.callout`, tables, `.hint`, `.lnk`). No new colors, radii, shadows or fonts. One gradient CTA per screen. Visible labels; errors under the field with `aria-invalid` + `aria-describedby`; 44px targets under 600px wide, 24px on desktop.
- Reuse `sku_units`, `price_list_items`, `PriceWriter.SetSalePrice`, `addSkuUnit`/`updateSkuUnit`/`deleteSkuUnit`. No new tables.
- Keep `purchase_unit_id` + `factor` on create requests working (the procurement new-product port and existing clients use them).
- Changing a factor never rewrites history: lines snapshot it (PRD CAT-02).

## Non-goals

- A "Bán trực tiếp" (sell-only/purchase-only) flag per unit. The user dropped it on 2026-10-21.
- Selling non-base units in offline POS. The offline pack only carries base-unit tiles today; that stays.
- Per-unit price tiers (`min_qty > 0`) and per-color unit prices in the form. The price list screen keeps owning those.
- Per-unit prices in the Tồn kho "Giá bán" column and a "Đơn vị & giá" button on Tồn kho. The column keeps the base retail/wholesale price (user decision 2026-10-21).
- Entering Kho documents (Điều chỉnh, Kiểm kê, Lô & hạn) in a non-base unit. They stay in the base unit.

## User decisions (2026-10-21)

1. Add "Sửa đơn vị & giá" for existing products, not only the create form.
2. Each unit has a retail price and a wholesale price.
3. The unit table appears on every product create screen: Danh mục › Thêm hàng hóa, Nhập hàng › Thêm hàng hóa mới (list and inside a purchase order), and the onboarding catalog/stock item dialog.
4. The prototype becomes a new file `prototype-v6.15.html`; v6.14 stays untouched.
5. Kho › Tồn kho shows the unit/conversion line under the name and the converted on-hand/sellable quantity. Not per-unit prices, not an edit button.
6. A unit that has a price history (`price_changes`) but is on no document can only be set to "Ngừng dùng", never deleted: the price history is an audit log and stays. The editor names the reason ("Đã có lịch sử giá"), separate from "Đã dùng trên chứng từ" and "Có mã vạch".

## Phases

| # | Phase | Status |
|---|-------|--------|
| 1 | [Prototype v6.15: unit table (approval gate)](./phase-01-prototype-v6-15-unit-table.md) | Done (approved 2026-10-21) |
| 2 | [API: N units and per-unit prices](./phase-02-api-units-and-unit-prices.md) | Done |
| 3 | [Web: unit table on create screens, units on Tồn kho](./phase-03-web-create-form-unit-table.md) | Done |
| 4 | [Web: Sửa đơn vị & giá, per-unit price edit](./phase-04-web-edit-units-and-prices.md) | Done |
| 5 | [Tests, parity and docs](./phase-05-tests-parity-docs.md) | In progress (e2e run pending) |

Phase 1 blocks everything. Phase 2 blocks 3 and 4. Phases 3 and 4 share `product-form.tsx`/`product-draft.ts`, so they run in order, not in parallel.

## Acceptance criteria

1. The user approves `prototype-v6.15.html` before any app code changes.
2. Creating "Kẹo X": cái 500/–, dây = 50 cái 24.000/–, thùng = 500 cái blank → one request creates the SKU with 3 units; POS sells 1 dây at 24.000 and 1 thùng at 250.000 (500 × 500).
3. Clearing a unit's price in "Sửa đơn vị & giá" removes the row; the next sale falls back to base × factor.
4. Validation: factor > 0 for non-base units, no duplicate unit, the base unit cannot repeat; a unit price lower than the base unit's own price (the "Thùng 500đ" trap in the KiotViet screenshot) shows a warning, not an error.
5. A unit used on a document, with a price history or with a barcode cannot be deleted; the editor names which of the three applies and offers "Ngừng dùng" instead. Setting the default purchase unit to "Ngừng dùng" moves the default to the remaining active unit with the largest factor (base unit if none), with no error.
6. Kho › Tồn kho for "Kẹo X" with 1.250 cái on hand shows "ĐV cơ sở: cái · 1 dây = 50 cái · 1 thùng = 500 cái" and "= 2 thùng 5 dây" under Tồn thực 1.250; Thẻ kho offers "Theo dây" and "Theo thùng"; after "Ngừng dùng" on thùng, both screens drop it.
7. `go test ./...`, `pnpm lint`, `pnpm typecheck`, `pnpm test`, `e2e/catalog.spec.ts`, `e2e/procurement.spec.ts`, `e2e/inventory.spec.ts`, `e2e/a11y` and `e2e/visual` pass.
