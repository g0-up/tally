---
phase: 2
title: "API: N units and per-unit prices"
status: done
priority: P1
effort: 1.5d
dependencies: [1]
---

# Phase 2: API: N units and per-unit prices

## Goal

Let one request create a SKU with any number of units and a retail/wholesale price per unit, let one request save the whole unit table of an existing SKU, return per-unit prices in SKU reads, and allow removing a per-unit price.

## What already exists (reuse, do not rebuild)

- `sku_units` (factor, active, `is_default_sale`, `is_default_purchase`) and `price_list_items.unit_id` (`migrations/000007`, `000009`). No migration is needed.
- `PriceWriter.SetSalePrice` writes a non-base unit row (`pricing/writer.go:356`, `rowUnit`); `catalogPrices.SetSalePrice` passes `UnitID` (`app/domains.go:364`).
- Unit CRUD with the in-use check: `AddSkuUnit`/`UpdateSkuUnit`/`DeleteSkuUnit` (`catalog/skus.go:651–810`, `s.usage.SkuUnitInUse`).
- The resolver already prefers a unit row over base × factor (`pricing/resolver.go:154–177`).

## Requirements

1. **Create with units.** `POST /products` (`sku`) and `POST /products:quick-create` accept `units: [{ unit_id, factor, retail_price?, wholesale_price? }]`. Rules:
   - Each `unit_id` must be active, differ from `base_unit_id` and be unique in the list. `factor > 0` goes through the existing `parseFactor`.
   - Field errors use the index path: `sku.units[2].factor` = `invalid`, `sku.units[1].unit_id` = `duplicate` / `is_base` / `unknown_unit`.
   - `units` and `purchase_unit_id` + `factor` are mutually exclusive (`sku.units` = `with_purchase_unit`). The legacy pair keeps working unchanged.
   - With `units`, the default purchase unit is the unit with the largest factor. This matches today's single "đơn vị lớn" behaviour and the prototype's `big` pick (prototype 2744).
   - Each unit price is written through `SetSalePrice` with that `UnitID`, in the same transaction as the SKU.
2. **Procurement new product.** `procurement.NewProduct` gains `Units`. The purchase line is counted in `PurchaseUnitID`, which must then be one of `Units` (or the base unit). Map it through `app/procurement_ports.go` to the catalog quick-create.
3. **Save the unit table.** Add `PUT /skus/{id}/units` with `If-Match` on the SKU version. The body is the full table: the base row's prices plus `units: [{ unit_id, factor, active, retail_price, wholesale_price }]`. In one transaction it:
   - adds new units,
   - updates factor/active of existing ones (history is safe: lines snapshot `unit_factor_snap`),
   - deletes units missing from the body only when they have no lock (rule 5); a locked one fails with `unit_in_use` on `units[i]` and its reason, so the client sends `active: false` instead,
   - sets each price, and removes it when it is null,
   - when the default purchase unit becomes inactive, moves `is_default_purchase` to the remaining active unit with the largest factor (base unit if none) instead of failing with `default_unit_inactive` (`skus.go:763`). The default sale unit stays the base unit.
   The delete check matters because today's `SkuUnitInUse` counts `price_list_items` and `price_changes` as use (`usage_repo.go:33`), and `DeleteSkuUnit` also refuses a unit with barcodes (`skus.go:806`). Without the split below, a unit created with its own price would read as "used on documents" at once.
   This is the only new endpoint. It exists because a client-side sequence of add/update/delete/sale-price calls could fail half-way and leave a half-saved table.
4. **Remove a per-unit price.** `SalePriceCommand` with `Price == nil` currently needs a `ColorID`. Also allow a non-base `UnitID`, which removes that unit row so the resolver falls back to base × factor. Record a `price_changes` row as removals already do.
5. **Read per-unit prices and locks.** `SkuUnit` gains `RetailPrice`/`WholesalePrice` (general color, `min_qty = 0`, the store's base retail/wholesale lists, the same filter as `sku_list.go:66`) and `LockedBy`. Fill them wherever units load for `GET /skus` and SKU/product detail. `skuUnitDTO` exposes `retail_price` and `wholesale_price` (null = auto) and `locked_by: "documents" | "price_history" | "barcodes" | null`, checked in that order:
   - `documents`: a row in the document tables of `skuUnitReferences` (every table except `price_list_items` and `price_changes`);
   - `price_history`: a `price_changes` row for the SKU and unit. It stays forever; the history is an audit log (user decision 2026-10-21);
   - `barcodes`: a `sku_barcodes` row for the unit.
   A unit that has only a current price row and no `price_changes` row is not locked; deleting it deletes that price row first. Compute `locked_by` for a whole page in one `ANY($1)` query per source.
6. **Contract.** Update `api/openapi.yaml` (create bodies, `PUT /skus/{id}/units`, the unit DTO, the sale-price null rule) and regenerate the web types (`apps/web/src/shared/api/schema.d.ts`) with the repo's existing generator command.

## Files

- Modify: `apps/api/internal/catalog/dto.go`, `products.go`, `quick_create.go`, `skus.go`, `sku_list.go`, `http.go`, `ports.go` (`UnitLocks`; `SalePrice` remove form if needed), `usage_repo.go`, `errors.go`
- Modify: `apps/api/internal/pricing/writer.go` (validate + remove path for unit rows)
- Modify: `apps/api/internal/procurement/new_products.go`, `ports.go`, `dto.go`; `apps/api/internal/app/procurement_ports.go`
- Modify: `apps/api/api/openapi.yaml`, `apps/api/internal/app/openapi_contract_test.go`
- Tests: `apps/api/internal/catalog/catalog_integration_test.go`, `apps/api/internal/app/catalog_partner_integration_test.go`, `apps/api/internal/procurement/new_products_test.go`, `apps/api/internal/pricing/pricing_integration_test.go`

## Tasks & Steps

- [x] Add a `UnitPriceInput` to `SkuExtras` and validate it in `CreateProduct` and quick-create, before the transaction.
- [x] Generalise `setBasePrices(…, baseUnitID, …)` to `setUnitPrices(…, unitID, …)` and call it per unit.
- [x] Replace `addPurchaseUnit` with `addUnits(units, defaultPurchase)` and keep a one-element path for the legacy pair.
- [x] Add `UnitLocks(ctx, q, skuIDs)` to `UsageReader` (`ports.go`, `usage_repo.go`) returning the `locked_by` reason per SKU unit; keep `SkuUnitInUse` for `DeleteSkuUnit` unchanged.
- [x] Implement `ReplaceSkuUnits` in `skus.go` under `lockSku` + version check, reusing `insertSkuUnit`, the update SQL and `UnitLocks`; move the default purchase unit as in rule 3; record the `sku.units_replace` audit event (the repo checks audit actions are `entity.verb`) with before/after.
- [x] Allow unit-row removal in `SalePriceCommand.validate` and the writer.
- [x] Load unit prices in the unit query used by the SKU list and SKU detail (one query per page, not per SKU).
- [x] Extend procurement `NewProduct` and the port mapping.
- [x] Update OpenAPI and the contract test; regenerate the web schema types.

## Verification

- `cd apps/api && go test ./internal/catalog/... ./internal/pricing/... ./internal/procurement/... ./internal/app/...`
- Integration cases:
  - Creating with cái 500, dây×50 @24.000, thùng×500 without a price gives 3 `sku_units` and price rows for cái and dây only. `POST /pricing/quote` returns 24.000 for 1 dây (`source` = list row) and 250.000 for 1 thùng (base × factor).
  - Duplicate unit, base unit repeated and factor 0 each return 422 with the indexed field.
  - `PUT /skus/{id}/units` that deletes a unit used on a receipt returns `unit_in_use`, and nothing in the table changed (atomic).
  - `locked_by` reads `documents` for a unit on a receipt, `price_history` for a unit that had its own price, `barcodes` for a unit with a barcode, and null for a fresh unit without a price.
  - Setting the default purchase unit (thùng) inactive succeeds and makes dây the default purchase unit.
  - After thùng is inactive, `GET /inventory/stock-card` with `unit_mode=auto` no longer uses thùng, and a lookup of thùng's barcode returns not found.
  - Clearing the dây price makes the next quote return 25.000.
  - A stale `If-Match` returns 412.
  - A purchase order with a new product with units receives into the base unit with the right `base_qty`.

## Risks

- The SKU list query is hot (`ix_skus__status`). Load unit prices in one `ANY($1)` query per page, as `loadSkuInfo` does, and check the existing list benchmark/read-index tests still pass.
- Tenant isolation: the new endpoint goes through the same tenant transaction (RLS). Add it to `tenant_isolation_integration_test.go`.

## Review follow-ups (2026-10-21)

From `plans/reports/code-reviewer-261021-1408-phase-02-api-review.md`, fixed and tested:

- A purchase-order new product with units and no `purchase_unit_id` now buys in the line's unit, so the line price becomes its reference cost (was divided by the largest factor, or dropped).
- `locked_by` stays on `GET /skus` (rule 5): migration `000061_catalog_line_sku_unit_indexes` adds `(tenant_id, sku_id, unit_id)` on the 10 line tables, and `UnitLocks` is one `unnest` + `EXISTS` query per page.
- `PUT /skus/{id}/units` requires `units` (an empty body no longer deletes the table); an omitted row price means no own price, as documented in OpenAPI.
- At most 20 unit rows per request (`units` = `too_many`) on create, quick-create, purchasing and PUT.
- Adding, changing or deleting a single unit moves the SKU version, so a stale table gets 412. Prices set from other screens do not move it; OpenAPI states that the table's prices win.
