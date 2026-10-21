---
title: Build multiple units per SKU with unit prices
date: 2026-10-21
summary: "Delivered the unit table, the Đơn vị & giá editor and units on Tồn kho; e2e run still pending the owner password"
---

# Build multiple units per SKU with unit prices

## What happened
Ran `plans/261020-1801-multi-unit-conversion-and-unit-prices/` with `--auto`. The user approved prototype v6.15, then the API, the web create screens, the "Đơn vị & giá" editor, the units on Tồn kho, e2e specs, parity states and three user guide pages landed. Everything is uncommitted on master.

## Review findings fixed
- The editor resent base prices it never changed, so a concurrent price edit was overwritten. It now sends a base price only when it differs from the table it opened with.
- Saving units did not refresh the sales SKU lookup; the sale price save did not refresh POS and sales either. Both invalidation lists now include them.
- Removing a unit in a purchase-order new product left the line on a unit that no longer existed. The line now moves to the base unit, converting qty and price by the old factor.
- Row errors followed row indexes, so removing an earlier row put the error on the wrong row. Errors are now remapped by the row keys captured when they were found.
- A late "+ Tạo mới" response could overwrite a row the user had already changed; it now applies only to the unchanged row.

## Evidence
`go test ./...` passes; web lint, typecheck and `pnpm test` (1393/1393) pass. The Playwright suite (AC7) was not run: it needs `E2E_OWNER_PASSWORD`, which the agent does not have.

## Open
- The two multi-unit parity states skip without `E2E_MULTI_UNIT_SKU_CODE`; `cua-hang-a` has no multi-unit SKU.
- Acceptance fixtures (`acceptance/at-01`, `addStockApi`) post receipts and stock adjustments without the Idempotency-Key those endpoints require. Pre-existing, not fixed.
- Each e2e run creates a "Kẹo e2e …" product in the dev store.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.

> Historical work record — not durable authority. Prefer docs/specs/ADRs for current decisions.
