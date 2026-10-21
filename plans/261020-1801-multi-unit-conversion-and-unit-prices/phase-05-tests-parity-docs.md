---
phase: 5
title: "Tests, parity and docs"
status: in-progress
priority: P1
effort: 1d
dependencies: [3, 4]
---

# Phase 5: Tests, parity and docs

## Goal

Prove the feature end to end, move the app's prototype reference from v6.14 to v6.15, and update the user guide.

## Requirements

1. **E2E**
   - `e2e/catalog.spec.ts`: create "Kẹo X" with 3 units (AC2); open "Đơn vị & giá", clear the dây price, save (AC3); a duplicate unit and factor 0 show their errors under the fields (AC4).
   - `e2e/procurement.spec.ts`: a PO with a new product with units, received in thùng, so stock rises by `qty × 500` cái.
   - `e2e/pos.spec.ts`: sell 1 dây → 24.000, 1 thùng → 250.000.
   - `e2e/inventory.spec.ts` (AC6): after receiving 2 thùng 5 dây, Tồn kho shows the unit line and "= 2 thùng 5 dây" under 1.250; Thẻ kho offers "Theo dây" and "Theo thùng"; after "Ngừng dùng" on thùng, both drop it; "Sửa giá" from Tồn kho still saves.
   - `e2e/a11y`: the create dialog and the edit dialog pass axe; the remove buttons have names.
2. **Prototype reference v6.14 → v6.15** in `apps/web/e2e/visual/prototype-harness.ts`, `apps/web/e2e/visual/parity.spec.ts`, `apps/web/src/shared/styles/tokens.test.ts`, and the source comments in `tokens.css`/`globals.css`. The tokens are unchanged, so `tokens.test.ts` stays green without value edits.
3. **Visual parity**
   - Add states in `e2e/visual/parity-states.ts` for "Thêm hàng hóa with units", "Đơn vị & giá" and "Tồn kho with a multi-unit product" at 1440px and 390px.
   - Regenerate only the affected snapshots.
   - Leave the existing untracked `parity.spec.ts-snapshots/` in the working tree alone unless its states are the ones regenerated here.
4. **Docs**
   - `docs/user-guide/danh-muc-bang-gia.md`: the unit table, auto price, "Sửa đơn vị & giá", and "Ngừng dùng".
   - `docs/user-guide/nhap-hang.md`: units on a new product inside a PO.
   - `docs/user-guide/kho-kiem-ke-gio-qua.md`: the unit line and converted quantity on Tồn kho; that "Ngừng dùng" removes a unit from Thẻ kho and stops its barcode scanning in Kiểm kê; that a factor change applies to the next scan.
   - Update the API docs only if the repo keeps endpoint docs outside `openapi.yaml`; check `docs/` navigation first.
5. **Full gates.** All AC7 commands pass.

## Files

- Modify: `apps/web/e2e/catalog.spec.ts`, `procurement.spec.ts`, `pos.spec.ts`, `inventory.spec.ts`, `e2e/a11y/axe.spec.ts`, `e2e/a11y/touch-targets.spec.ts`
- Modify: `apps/web/e2e/visual/prototype-harness.ts`, `parity.spec.ts`, `parity-states.ts`, snapshots
- Modify: `apps/web/src/shared/styles/tokens.test.ts`, `tokens.css`, `globals.css` (reference comments only)
- Modify: `docs/user-guide/danh-muc-bang-gia.md`, `docs/user-guide/nhap-hang.md`, `docs/user-guide/kho-kiem-ke-gio-qua.md`

## Tasks & Steps

- [x] Write the e2e cases (`playwright --list` lists them). Running them against the local stack needs the owner password; not run yet.
- [x] Switch the prototype references; `grep -rn 'prototype-v6.14' apps/web/src apps/web/e2e` returns nothing.
- [x] Add the parity states. They compare against the prototype captured in the same run, so no baseline is committed; the two multi-unit states skip without `E2E_MULTI_UNIT_SKU_CODE`.
- [x] Update the three user guide pages; claims checked against the code.
- [ ] Run the full gates. `go test ./...`, lint, typecheck and `pnpm test` (1393/1393) pass on 2026-10-21; the Playwright command is pending.

## Verification

- `cd apps/api && go test ./...`
- `cd apps/web && pnpm lint && pnpm typecheck && pnpm test`
- `cd apps/web && pnpm exec playwright test e2e/catalog.spec.ts e2e/procurement.spec.ts e2e/pos.spec.ts e2e/inventory.spec.ts e2e/a11y e2e/visual`

## Risks

- Visual parity drift on unrelated screens after the reference switch: v6.15 is v6.14 plus the unit table, so only the touched states should move. A diff elsewhere means the prototype copy changed more than planned; fix the prototype, do not regenerate blindly.
- Stop any dev server or Playwright web server started here when done (`.claude/rules/process-management.md`).
