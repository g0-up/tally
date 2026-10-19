---
title: "Phase 06: Pricing & Promotions"
status: done
priority: P1
effort: 5d
dependsOn: [phase-05-catalog-partners]
---

# Phase 06 — Pricing & Promotions

## Context links

- [database-schema-catalog-pricing.md](design/database-schema-catalog-pricing.md) §5 price lists, §5.1 resolver order, §6 promotions
- [database-schema-sales-kits.md](design/database-schema-sales-kits.md) header — `<sales pricing columns>`, `price_source` snapshot, `DocumentCalculator`
- [database-schema-org-partners.md](design/database-schema-org-partners.md) `tenant_settings.prices_include_tax`, `rounding_mode`, `customer_groups.base_price_kind`
- [architecture.md](design/architecture.md) patterns table (Chain + Strategy `PriceResolver`)
- PRD §6.3 (PRC-01..04, PRC-06..08), §7.3 money/tax/rounding, §7.9 colors

## Overview

Build the `pricing` domain: price lists (retail, wholesale, group, customer, season), price list items with color/unit/tier dimensions, the append-only price history, the `PriceResolver` chain, the `PromotionEngine`, the `DocumentCalculator` used by every sales document (quote, invoice, POS cart, exchange), the minimum-price guard (PRC-04), and the `PriceWriter` used by catalog quick-create, procurement lines and the inventory screen (PRC-08). Quotes (PRC-05) are implemented in phase 09 with the sales documents but consume this calculator.

## Key insights

- One resolver for all callers: POS, cart, invoice, quote, exchange call `PriceResolver.Resolve(ctx, PriceQuery{PartyID, CustomerGroupID, SkuID, ColorID, UnitID, Qty, BusinessDate})`, returning `ResolvedPrice{Amount, Source PriceSource}`. Changing color on a line re-resolves (PRC-07).
- Quantity converts to base quantity **before** tier matching (PRC-02): `baseQty = qty × unit_factor`. A row with a matching `unit_id` wins over `base price × factor`.
- Price history is written by Go in the same transaction as the price change (no triggers); `source` records where the edit came from: `catalog`, `purchase_order`, `product_list`, `inventory`, `price_list`.
- Money uses `money.Money` (shopspring/decimal value object, 4 dp storage); VND display rounds to 0 dp at the boundary set by `rounding_mode`; remainder goes to the largest line so totals reconcile exactly.
- Promotion usage limits use `SELECT … FOR UPDATE` on the promotion row inside the posting transaction; `promotion_usages` unique key makes reposting idempotent.

## Requirements

| ID | Behaviour |
|---|---|
| PRC-01 | Price list CRUD by kind with validity range and priority; resolver returns one price and a visible `PriceSource` (list name, kind, color/unit/tier hit) |
| PRC-02 | Tier prices per unit/qty, customer-specific list; base-qty conversion before tiering; price snapshot stored on the line at confirmation |
| PRC-03 | Promotions: line %/amount, order %/amount, code, gift, combo price; `stackable` rule; `max_discount` cap; non-stackable promotions never combine; gifts become zero-price `gift` lines that still post stock and COGS |
| PRC-04 | `MinPriceGuard`: line net unit price < `skus.min_price` → `exception_required{kind: below_min_price}`; save only with a valid exception confirmation; `min_price_snap` stored |
| PRC-06 | Tax rate snapshot per line (`tax_rate_snap`), `prices_include_tax` setting, rounding by line or document; changing a tax rate never touches posted lines |
| PRC-07 | Color price rows; "Bỏ giá riêng" removes a color row; "Áp dụng cho tất cả màu" removes all color rows of the SKU in that list; history names the color or "cả sản phẩm" |
| PRC-08 | `PriceWriter.SetSalePrice(ctx, cmd{SkuID, ColorID?, UnitID?, Kind retail/wholesale, Price, Source, SourceDocID?})` used from procurement lines, product list and inventory; only new transactions affected |

## Architecture

```
pricing/
  model.go        PriceList, PriceListItem, PriceChange, Promotion, PromotionItem, PromotionUsage, PriceSource (value object, JSON)
  resolver.go     PriceResolver = chain of PriceListSource strategies:
                    CustomerListSource → GroupListSource → SeasonListSource → BaseListSource
                  ListRowMatcher (color exact > any; unit exact > base×factor; max min_qty ≤ baseQty)
  promotion.go    PromotionEngine + PromotionRule strategies: LinePercent, LineAmount, OrderPercent, OrderAmount, Gift, ComboPrice
                  StackingPolicy (non-stackable best-first, stackable cumulative, cap by max_discount)
  calculator.go   DocumentCalculator.Calculate(DocumentDraft) → CalculatedDocument
                  steps (Template Method): price lines → line discounts → allocate order discount by eligible value
                  → tax base (include/exclude) → rounding (LineRounding | DocumentRounding strategy) → totals
  guard.go        MinPriceGuard (returns exception requirement), used by sales posting commands
  writer.go       PriceWriter implements catalog.PriceWriter; writes item + price_changes in one tx
  seed.go         implements org.PriceListSeeder (retail + wholesale base lists)
  ports.go, repo.go, service.go, http.go, dto.go, errors.go
```

Routes (`/api/v1`): `GET/POST /price-lists`, `GET/PATCH/DELETE /price-lists/{id}` (If-Match), `GET/PUT /price-lists/{id}/items` (bulk upsert, max 500 rows), `DELETE /price-lists/{id}/items/{itemId}` (Bỏ giá riêng), `POST /skus/{id}/sale-price` (PRC-08, body `{kind, color_id?, apply_all_colors?, price, source}`), `GET /skus/{id}/price-history`, `POST /pricing/quote` (resolve many lines at once for UI; no persistence), `POST /pricing/calculate` (full document preview), `GET/POST /promotions`, `GET/PATCH /promotions/{id}`, `POST /promotions/{id}:activate|deactivate`, `GET /promotions/validate-code?code=`.

`/pricing/calculate` is the single preview API the web uses for quote, invoice, cart and exchange drafts; the posting commands call the same `DocumentCalculator` server-side and ignore client totals.

## Related files

| Path | Action |
|---|---|
| `apps/api/internal/pricing/{model.go,resolver.go,promotion.go,calculator.go,guard.go,writer.go,seed.go,ports.go,repo.go,service.go,http.go,dto.go,errors.go}` + tests | create |
| `apps/api/internal/platform/money/money.go` | modify — `Allocate(total, weights)` largest-remainder, `RoundVND` |
| `apps/api/internal/catalog/service.go` | modify — wire `PriceWriter` into quick-create and sale price fields |
| `apps/api/internal/org/provisioner.go` | modify — wire price list seeder |
| `apps/api/internal/catalog/search.go` | modify — `price_hint` via resolver (base list, no party) |
| `apps/api/api/openapi.yaml`, `apps/api/internal/app/routes.go` | modify |

## Implementation steps

1. `money` additions with property tests: `Allocate` sum equals total for random weights; rounding never loses a đồng.
2. Price lists + items repo; validity and kind invariants checked in Go before DB CHECKs (friendly errors); base list uniqueness surfaces as `409 base_price_list_exists`.
3. `PriceWriter` with history rows: add (old NULL), change, remove (new NULL); `apply_all_colors` deletes color rows and records each removal.
4. Resolver chain + `ListRowMatcher`; batch variant `ResolveMany` loading candidate items for all SKUs in one query (`ix_price_list_items__sku`) to keep POS fast.
5. Promotions CRUD + engine; code validation endpoint; usage recording function `RecordUsage(q, promotionID, docType, docID, amount)` called by sales posting (phase 09).
6. `DocumentCalculator` Template Method with Strategy for rounding; golden tests from PRD §7.3 examples and prototype sample invoice totals.
7. `MinPriceGuard` returning `apperr.ExceptionRequired{Kind: "below_min_price", Lines: [...]}` and verifying exception confirmations via `org.ExceptionService.Consume`.
8. Seeder: retail + wholesale lists on tenant creation.
9. Tests: resolver precedence table-driven (customer > group > season > base; priority tie; validity boundaries in tenant timezone), color fallback, unit factor vs unit row, tier boundaries (min_qty equal), stacking + cap, gift lines, rounding modes, exception path, isolation.

## Todo

- [x] money Allocate/RoundVND + property tests. Evidence: `money/allocate_test.go` (`TestPropertyAllocateKeepsTotal`, `TestPropertyRoundVNDKeepTotal`).
- [x] Price lists/items repo + service + routes. Evidence: `TestPricingOverHTTP` (If-Match 428/412, items upsert/delete, `409 base_price_list_exists`).
- [x] PriceWriter + price history (PRC-07/08). Evidence: `TestTiersUnitsAndColors` (Bỏ giá riêng old → NULL with the color, apply_all_colors), `TestSalePriceNeedsTheBaseList`; catalog quick-create writes through the `catalogPrices` adapter in `app/domains.go`.
- [x] PriceResolver chain + ResolveMany. Evidence: `TestResolverChainPrecedence`, `TestSameLevelListsByPriorityThenNewest`, `TestSeasonEndsAtLocalMidnight`, matcher unit tests; `BenchmarkResolveManyFiftyLines` ≈ 10–11 ms/op, `TestResolveManyFiftyLinesIsFast` (median < 20 ms outside `-race`).
- [x] Promotions + engine + code validation + usage recording. Evidence: `promotion_test.go`, `TestPromotionCodesAndUsageLimit` (usage limit, idempotent `RecordUsage`, code reasons).
- [x] DocumentCalculator + rounding strategies + golden tests. Evidence: `calculator_test.go` goldens, `TestPropertyRandomDocumentsReconcile` (1 000 documents, both modes).
- [x] MinPriceGuard + exception confirmation consumption. Evidence: `TestMinPriceNeedsTheOwnersConfirmation` (the error renders `409 exception_required`, the platform's status for that kind, not 422).
- [x] Seeder wired into provisioner; search price_hint. Evidence: `TestSeederAndSandboxReset`, `TestPriceHintsUseTheRetailGeneralPrice`, `TestPricingOverHTTP` (base lists of a new store, `price_hint` in search).
- [x] OpenAPI + isolation tests. Evidence: `TestOpenAPIContract`, `TestPricingTenantIsolation`, `TestPricingOverHTTP` (every documented pricing id route answers 404 to another store).

## Success criteria & verification

- `go test ./internal/pricing/... ./internal/platform/money/...` and integration suite green.
- Table test: customer list price 95 000 beats group 98 000 beats season 99 000 beats wholesale 100 000; expired season list is skipped on `valid_to + 1` in Asia/Ho_Chi_Minh.
- Selling 2 thùng (factor 24) hits the `min_qty = 48` tier; selling 47 lẻ does not.
- Color "đỏ" without its own row resolves the general price and `price_source.color_id` is null; after "Bỏ giá riêng" the history shows old → NULL with the color.
- Sum of rounded line amounts equals document total for 1 000 random documents in both rounding modes.
- Line under `min_price` without confirmation → `409 exception_required` (the platform status for an owner-confirmable exception); with a matching confirmation → saved and confirmation marked used.

## Risks

| Risk | Mitigation |
|---|---|
| Resolver slow on POS with many lines | `ResolveMany` single query + per-request cache; benchmark 50 lines < 20 ms |
| Ambiguous stacking expectations | Rules documented in API error text and settings help; golden tests from prototype |
| Client/server total drift | Server recalculates; web shows server preview only |

## Security

- Price edits require an authenticated owner session; price history is append-only (`REVOKE UPDATE, DELETE`); promotion code lookup rate-limited to stop enumeration.

## Rollback

Revert code; price data only in dev. A mispricing defect in production is rolled back by release; posted lines keep their price snapshots, so rollback never reprices history.

## Next

Phase 08 calls `PriceWriter` from purchase lines; phase 09 uses `DocumentCalculator`, `MinPriceGuard` and promotion usage in posting; phase 15/16 build price list and line editors.
