---
title: "Phase 05: Catalog & Partners"
status: done
priority: P1
effort: 7d
dependsOn: [phase-04-identity-organization]
---

# Phase 05 — Catalog & Partners

## Context links

- [database-schema-catalog-pricing.md](design/database-schema-catalog-pricing.md) §1 lookups, §2 dynamic attributes, §3 products/SKUs, §3.1 search fallback, §4 mix groups, §7 edit rules
- [database-schema-org-partners.md](design/database-schema-org-partners.md) §3 `reasons`, §4 partners, §5 invariants
- Brainstorm §2 (JSONB + attribute sets chosen), §4.1–§4.4: `plans/reports/brainstorm-261016-1528-catalog-database-design.md`
- [ubiquitous-language.md](design/ubiquitous-language.md) §2.2
- PRD §6.2 (CAT-01..11), §7.9 colors, §7.10 mix packs, §7.3 units

## Overview

Implement the dynamic catalog (colors, units, tax rates, attributes/options, attribute sets = "mẫu ngành", categories, products, variant axes, SKU matrix generation, SKU units/barcodes, images), mix groups, the shared search used by the "ô tìm & chọn" (CAT-08), edit-rule Specifications, and partners (customers, suppliers, groups, contacts, addresses), plus reasons. Provide the seeders the tenant provisioner calls.

## Key insights

- Attribute values live in `products.attributes` JSONB validated by `catalog.AttributeValidator` with a registry per data type; variant axes produce SKUs, so stock never lives in attributes (CAT-04). Parent-child industry template inheritance is out (brainstorm decision).
- Colors are not an attribute: the 7 fixed colors sit on lots and document lines (CAT-09, §7.9). A SKU has `has_color_stock` implied by any lot with a color; "Chưa gán màu" is `color_id IS NULL`.
- Names are unique by `name_key = textnorm.Key(name)` (no expression indexes). Search uses `search_norm LIKE '%term%'` behind `catalog.SearchRepository` so the token fallback can replace it without touching callers.
- Edit rules are Specifications fed by `catalog.UsageReader`, a port implemented by inventory/sales/procurement repositories later (until then, an implementation that queries document line tables directly through read-only SQL in `catalog/usage_repo.go`).

## Requirements

| ID | Behaviour |
|---|---|
| CAT-01 | Product + SKU CRUD: code unique per tenant, barcode unique, name, images, category, brand (attribute), variants, status `active`/`inactive` (Ngừng bán keeps history) |
| CAT-02 | Base unit + conversions (thùng/hộp/dây/lốc/chai/kg); factor > 0, ≤ 6 decimals; default sale/purchase unit; lines snapshot factor |
| CAT-03 | Reference purchase price, sale price (writes base list in phase 06), min/max stock, `track_lot`/`track_expiry`, spec text; tracking toggle blocked while on-hand ≠ 0 |
| CAT-04 | Attribute sets with ordered items, required flags; filter products by attribute (`attributes @> $json` on GIN) |
| CAT-05 | Suppliers under Danh mục → Nhà cung cấp: add/edit/stop trading/delete (delete only when unused); search by name/phone/supplied item/tax code; stopped suppliers hidden from pickers |
| CAT-06 | Customers: contacts, group, addresses, payment terms, credit limit; a party can be both customer and supplier with separate ledgers |
| CAT-07 | Reasons by kind (return, damage, loss, gift, internal use, adjustment, write-off …); used reasons only deactivated |
| CAT-08 | One search endpoint for SKUs, customers, suppliers: accent-insensitive by code/name/barcode/phone/flavor; returns on-hand by color for SKUs (from `lot_balances` once phase 07 lands; field present, zero until then) |
| CAT-09 | 7 seeded colors (xanh, đỏ, tím, vàng, nâu, đen, trắng) with hex tokens from the prototype; not user-editable in 1.1 (OPEN-21) |
| CAT-10 | Mix groups: name, pack unit, pieces per pack, part unit, pieces per part, flavor list (SKU + short label); validation messages verbatim from prototype |
| CAT-11 | Quick-create product from a purchase order line: name, base unit, conversion, purchase price, sale price; duplicate name blocked (`name_key`) — endpoint here, used by phase 08 UI |

## Architecture

```
catalog/
  model.go          Color, Unit, TaxRate, Attribute, AttributeOption, AttributeSet, Category, Product, VariantAxis, Sku, SkuUnit, Barcode, MixGroup, MixGroupItem
  attributes.go     AttributeTypeRegistry: map[DataType]ValueValidator (text, number(+unit_label, min/max), boolean, date, select, multiselect)
  variants.go       VariantMatrixFactory.Build(product, axes, options) → []SkuDraft (cartesian product, variant_key = sorted option ids, variant_label "Hộp 500g · Dâu")
  edit_rules.go     AttributeChangeSpec, OptionDeleteSpec, AttributeSetChangeSpec, VariantAxisChangeSpec, SKUChangeSpec, UnitConversionSpec, MixGroupChangeSpec
  mix.go            MixGroupPolicy
  search.go         SearchIndexer (build search_norm from code, name, variant label, barcodes, flavor labels), SearchRepository port
  ports.go          ProductRepo, SkuRepo, LookupRepo, MixGroupRepo, UsageReader, StockByColorReader (implemented by inventory), PriceWriter (implemented by pricing, for CAT-11/sale price)
  service.go        CatalogService (products, skus, variants), LookupService, MixGroupService, QuickCreateService
  seed.go           implements org.ColorSeeder, UnitSeeder, TaxSeeder, AttributeSetSeeder
partner/
  model.go          Party, CustomerProfile, SupplierProfile, CustomerGroup, Contact, Address
  dedupe.go         PartyDeduplicator (phone_key, tax_code, name_key) → warning, not block
  service.go, repo.go, http.go, seed.go (customer groups Khách lẻ/Khách sỉ)
org/reasons_*     ReasonService (reasons table owned by org)
```

Search API: `GET /api/v1/search?type=sku|customer|supplier&q=&limit=20&in_stock=` → items with `kind`, `id`, `code`, `label`, `sublabel`, `unit`, `price_hint` (phase 06), `on_hand_by_color[]` (phase 07). Query: normalise term with `textnorm.SearchNorm`; exact code/barcode first (b-tree), then `search_norm LIKE` ordered by `position(term)`-free ranking computed in Go (prefix match first, then length).

Routes: `/colors`, `/units`, `/tax-rates`, `/attributes`(+`/options`), `/attribute-sets`(+`/items`), `/categories`, `/products`, `/products/{id}/variant-axes`, `/products/{id}/variants:preview` (matrix preview), `/products/{id}/variants` (generate), `/skus`, `/skus/{id}/units`, `/skus/{id}/barcodes`, `/products/{id}/images`, `/mix-groups`, `/products:quick-create`, `/customers`, `/suppliers` (+`:stop|reopen`, delete only when unused), `/mix-groups/{id}:deactivate|reactivate`, `/customer-groups`, `/parties/{id}/contacts`, `/parties/{id}/addresses`, `/reasons`, `/search`.

## Related files

| Path | Action |
|---|---|
| `apps/api/internal/catalog/{model.go,attributes.go,variants.go,edit_rules.go,mix.go,search.go,ports.go,repo.go,usage_repo.go,service.go,http.go,dto.go,errors.go,seed.go}` + tests | create |
| `apps/api/internal/partner/{model.go,dedupe.go,ports.go,repo.go,service.go,http.go,dto.go,errors.go,seed.go}` + tests | create |
| `apps/api/internal/org/{reasons.go,reasons_repo.go,reasons_http.go}` | create; `seed.go` default reasons |
| `apps/api/internal/org/provisioner.go` | modify — wire catalog/partner/reason seeders |
| `apps/api/internal/platform/textnorm/textnorm.go` | modify — `PhoneKey` (digits only, `+84`→`0`) |
| `apps/api/api/openapi.yaml`, `apps/api/internal/app/routes.go` | modify |

## Implementation steps

1. Lookups: colors (read-only list, seeded with code + hex from prototype tokens), units (name_key unique), tax rates; seeders.
2. Attributes + options + sets + items; `AttributeTypeRegistry` with validators; `AttributeChangeSpec` and `OptionDeleteSpec` with `UsageReader` (JSONB containment query on products, `sku_variant_values` lookup).
3. Categories tree (sibling uniqueness via `UNIQUE NULLS NOT DISTINCT (tenant_id, parent_id, name_key)`, cycle check in Go by walking parents, max depth 4).
4. Products: create/update with attribute validation against the set; status; images via attachments.
5. Variant axes (≤ 3, select attributes only) + `VariantMatrixFactory` + preview endpoint returning the grid; generate SKUs in one transaction with codes from pattern `<product code>-<n>` or user-provided; base `sku_units` row; `search_norm` built by `SearchIndexer`.
6. SKU units/barcodes with default sale/purchase partial uniques; `UnitConversionSpec`; `SKUChangeSpec` (tracking toggle needs on-hand via `StockByColorReader`, nil-safe until phase 07).
7. Mix groups + `MixGroupPolicy`; copy validation messages verbatim from prototype (Nhóm mix section, see UI spec report) into `catalog/errors.go` codes with Vietnamese messages.
8. Search endpoint + benchmark test with 20 000 generated SKUs (integration, `-run BenchmarkSearch`) recording p95 to feed the phase 21 gate.
9. Partners: customers/suppliers with profiles, dedupe warnings, stop trading, delete-if-unused, groups (base price kind), contacts, addresses; search by phone/tax code/name/supplied item (join on `purchase_order_lines` once phase 08 exists; until then name/phone/tax code).
10. Reasons CRUD with deactivate-if-used.
11. Quick-create product (CAT-11) endpoint: product + single SKU + units + optional sale price via `PriceWriter` port (no-op until phase 06 wires it).
12. Tests: validators per data type, matrix factory (2×3×2 axes → 12 SKUs, stable keys), edit specs, mix policy, name_key collisions (`Kẹo Dừa` vs `keo dua`), isolation for every endpoint.

## Todo

- [x] Lookups + seeders (colors, units, tax rates). Evidence: `catalog/lookups.go`, `catalog/seed.go`, `TestSeedersAreIdempotentAndTheSandboxResets`.
- [x] Attributes/options/sets + registry validators + specs. Evidence: `catalog/attributes.go`, `attribute_service.go`, `edit_rules.go`, `TestAttributeEditRulesOnceUsed` (409 `attribute_in_use`).
- [x] Categories tree. Evidence: `catalog/categories.go`, `TestCategoriesFormATreeOfAtMostFourLevels`.
- [x] Products + images. Evidence: `catalog/products.go`; the image path (pending scan → 422, clean → linked) is covered over HTTP in `app/catalog_partner_integration_test.go`.
- [x] Variant axes + matrix preview/generate. Evidence: `catalog/variants.go`, `variant_service.go`, `TestVariantPreviewEqualsGenerationAndRegenerationIsIdempotent` (2×3×2 → 12) and the HTTP test.
- [x] SKU units/barcodes + specs. Evidence: `catalog/skus.go`, `TestSkuUnitsBarcodesAndDeleteRules`.
- [x] Mix groups + policy + verbatim messages. Evidence: `catalog/mix.go`, `mix_groups.go`, `errors.go`, `TestMixGroupsAndQuickCreate`.
- [x] Unified search + 20k SKU benchmark. Evidence: `catalog/search.go`, `TestSearchFindsAccentInsensitiveAndExactBarcodeFirst`, `BenchmarkSearch` p95 66–94 ms (gate 1 s) in `plans/reports/fullstack-developer-261017-0148-search-benchmark-p95.md`.
- [x] Partners (customers, suppliers, groups, contacts, addresses, dedupe). Evidence: `internal/partner`, its integration tests; a stopped supplier leaves `GET /search?type=supplier` (HTTP test).
- [x] Reasons. Evidence: `org/reasons*.go`, `org/reasons_integration_test.go`.
- [x] Quick-create product. Evidence: `catalog/quick_create.go`, `TestMixGroupsAndQuickCreate`; sale price goes through the no-op `PriceWriter` until pricing wires it.
- [x] Provisioner wiring + OpenAPI + isolation tests. Evidence: `app/domains.go`, `api/openapi.yaml` (`TestOpenAPIContract`), `TestCatalogAndPartnersOverHTTP` (every id-bearing operation of the spec answers 404 across tenants).

## Success criteria & verification

- `make -C apps/api test test-integration` green.
- Matrix preview equals generated SKUs; regenerating is idempotent (`UNIQUE (tenant_id, product_id, variant_key)`).
- Changing an attribute's `data_type` after use returns `409 attribute_in_use`; deactivating instead works.
- Search for `banh dau xanh` finds `Bánh đậu xanh`; exact barcode returns a single SKU first; benchmark p95 recorded in `plans/reports/` by the implementer.
- Supplier with status stopped is excluded from `GET /search?type=supplier` but its documents remain readable.

## Risks

| Risk | Mitigation |
|---|---|
| `LIKE '%term%'` too slow at 20 000 SKU | Benchmark now; fallback token table pre-designed (catalog-pricing §3.1) |
| JSONB attribute drift | Validator on every write; attribute codes immutable after use |
| Duplicate partners from typos | Dedupe warnings on phone/tax code/name key; merge is out of scope |

## Security

- All reads/writes inside `WithTenantTx`; images via presigned URLs; attribute JSON size limit 16 KB; text fields length-limited.

## Rollback

Revert code; data created in dev only. Tables already exist from phase 03.

## Next

Phase 06 adds price lists and the resolver; phase 07 implements `StockByColorReader` for search and edit rules; phase 15 builds the catalog screens.
