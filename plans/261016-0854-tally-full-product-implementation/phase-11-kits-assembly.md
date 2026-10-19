---
title: "Phase 11: Kits, Combos & Assembly"
status: done
priority: P1
effort: 5d
dependsOn: [phase-09-sales-pos]
---

# Phase 11 — Kits, Combos & Assembly

## Context links

- [database-schema-sales-kits.md](design/database-schema-sales-kits.md) §6 kits/assembly, §3 combo lines, §7 invariants
- [database-schema-inventory-procurement.md](design/database-schema-inventory-procurement.md) §1 reservations (`assembly_order_item`), lots
- [architecture.md](design/architecture.md) §3.4 (`kits.KitLotFactory`)
- [ubiquitous-language.md](design/ubiquitous-language.md) §2 kit terms (Giỏ đóng sẵn, Combo, Lệnh đóng giỏ, Tháo giỏ), §5 prefixes LD, TG
- PRD §6.7 (KIT-01..10), §7.5 kit costing, §7.10 mix packs in kits, §7.9 colors

## Overview

Implement the `kits` domain: versioned recipes for pre-packed kits (giỏ đóng sẵn, own stock) and combos (assembled at sale, no own stock), with goods/packaging/basket/accessory/mix-pack components, fixed or counter-chosen colors, packing cost; draft → publish lifecycle with preview card data; assembly orders (LD) with planned vs actual, reservations, substitutions, partial completion, scrap; balanced assembly postings creating kit lots with genealogy and earliest-expiry rule; disassembly (TG) returning exact lots/colors with damaged parts to quarantine; kit label data; budget-based kit suggestion with expected margin; and the `ComboExpander` used by sales.

## Key insights

- Product `kind` distinguishes `kit` and `combo`; `products.status = 'draft'` hides them from POS/invoice pickers (KIT-10, `catalog.PickerQuery`). Publishing activates a recipe version (`ux_kit_recipes__one_active`) and the product.
- Kit cost = actual consumed component cost (moving average at posting) + packing cost; never recomputed from current purchase prices (§7.5).
- Combo sale: `ComboExpander.Expand(recipe, qty, chosenColors)` returns `combo` header + `combo_component` children; fixed-color components issue that color; components with color stock but no fixed color require a counter choice (`ColorRequirementSpec`), never over-issuing that color.
- Assembly posting is one balanced `PostingCommand`: consume component lots (`assembly_consume`), produce kit lot (`assembly_produce`) with value = Σ consumed + packing; write `lot_genealogy` and `assembly_posting_items` snapshots; consumes reservations.
- Disassembly reads `lot_genealogy` to return each component to its exact source lot/color (`disassembly_return`); damaged quantity goes to the quarantine twin lot; kit lot issued (`disassembly_consume`).
- Mix pack components store per-flavor parts in `kit_recipe_mix_items`; price and cost per flavor.

## Requirements

| ID | Behaviour |
|---|---|
| KIT-01 | Recipe with version, validity, components (goods, packaging, basket, accessory, mix pack), packing cost; component table returns sale price, amount at sale price, cost, sellable qty; color per component |
| KIT-02 | Pre-packed kit has its own stock; assembly consumes components and produces kit in one balanced posting; sale only issues kit; fixed colors consumed exactly |
| KIT-03 | Combo expanded at sale; components issue stock; color rules as above |
| KIT-04 | Assembly order planned/actual, reserve components on `reserved`; planning consumes nothing; completion posts atomically |
| KIT-05 | Substitute component on an order; snapshot actual composition; cost/price updated |
| KIT-06 | Partial completion, scrap, disassembly to exact lots/colors, damaged → quarantine, re-assembly allowed |
| KIT-07 | Progress (planned/completed/disassembled) and kit label payload (kit name, LD no., lot, expiry, component lots with color) |
| KIT-08 | Genealogy trace kit lot ↔ component lots; kit expiry ≤ earliest component expiry |
| KIT-09 | Budget suggestion: given budget and candidate components, propose a composition with expected price/margin; never changes a confirmed invoice |
| KIT-10 | Draft kit/combo: save, preview card data, edit, delete (if never sold), publish |

## Architecture

```
kits/
  model.go          KitRecipe(+Item, MixItem), AssemblyOrder(+Item), AssemblyPosting(+Item), Disassembly(+Line), LotGenealogy
  status.go         AssemblyOrderStatus transition table
  recipe_service.go RecipeService (draft, new version, publish, supersede, delete-if-unsold), RecipeCostView (sale price, cost, sellable)
  combo.go          ComboExpander (implements sales.ComboExpander)
  assembly.go       AssemblyOrderService (create, reserve, substitute, close), PostAssembly (PostingCommand)
  lot_factory.go    KitLotFactory (lot no. = LD no. + batch, expiry = min component expiry)
  disassembly.go    PostDisassembly (PostingCommand)
  suggestion.go     BudgetKitSuggester (greedy by margin within budget, respects sellable qty)
  label.go          KitLabelPayload
  ports.go          StockConsumer/Producer, Reserver (inventory), PriceResolver (pricing), MixGroupReader, ProductActivator (catalog)
  repo.go, http.go, dto.go, errors.go
```

Routes (`/api/v1`): `GET/POST /kits` (draft kit/combo product + recipe), `GET/PATCH/DELETE /kits/{productId}`, `GET /kits/{productId}/preview`, `POST /kits/{productId}:publish`, `POST /kits/{productId}/recipes` (new version), `GET /kits/{productId}/recipes/{version}`, `POST /kits:suggest` (budget), `POST /kits:preview` (economics of an unsaved recipe), `GET /kits/progress` (KIT-07 planned/completed/disassembled + open-invoice demand), `GET/POST /assembly-orders`, `POST /assembly-orders:preview` (component shortage), `GET/PATCH /assembly-orders/{id}`, `POST /assembly-orders/{id}:reserve|close`, `POST /assembly-orders/{id}/substitutions`, `POST /assembly-orders/{id}/postings` (complete batch), `POST /assembly-orders/{id}/disassemblies`, `GET /disassemblies`, `GET /lots/{id}/genealogy`, `GET /assembly-orders/{id}/label`.

## Related files

| Path | Action |
|---|---|
| `apps/api/internal/kits/*.go` + tests | create |
| `apps/api/internal/catalog/service.go` | modify — product kind kit/combo, `PickerQuery` excludes drafts |
| `apps/api/internal/sales/lines.go` | modify — wire `ComboExpander` |
| `apps/api/internal/inventory/queries.go` | modify — genealogy in lot trace |
| `apps/api/api/openapi.yaml`, `apps/api/internal/app/routes.go`, `app/wire.go` | modify |

## Implementation steps

1. Recipes: draft product + recipe v1; components with unit conversion snapshots; mix pack components with flavor parts validated by `MixGroupPolicy`; cost view using `sku_stock.avg_cost` and resolver prices.
2. Publish/new version/supersede; delete only drafts never sold (check `sales_document_lines`); preview payload.
3. `ComboExpander` + sales integration test (combo with one fixed "đỏ" component and one counter-chosen color), plus a concurrent combo sale test: combo components join the sale lock set, so concurrent combos never sell a component below its reservations.
4. Assembly orders: create from active recipe snapshot, reserve components (`assembly_order_item` reservations), substitutions (new item with `substitutes_item_id`, planned qty moved).
5. `PostAssembly`: per batch, pick lots (fixed color exact, others FEFO or explicit), consume, produce kit lot via `KitLotFactory`, write genealogy + posting items + scrap (scrap issued as `assembly_consume` with zero output share and recorded), update order progress and status.
6. `PostDisassembly`: from kit lot, proportional component quantities from genealogy, return to source lots, damaged to quarantine twin, reason required.
7. Budget suggester and label payload.
8. Tests: balanced posting (value in = value out), earliest expiry, disassembly exact color, draft hidden from POS search, combo color requirement, ledger verify.

## Todo

- [x] Recipes + versions + cost view + mix pack components
- [x] Draft/publish/preview/delete lifecycle
- [x] ComboExpander + sales integration
- [x] Assembly orders + reservations + substitutions
- [x] Assembly posting + kit lots + genealogy + scrap
- [x] Disassembly
- [x] Budget suggestion + labels
- [x] Tests + OpenAPI

Evidence (2026-10-18): `go test -race ./...` and `go test -tags integration ./...` green;
`apps/api/internal/app/kits_integration_test.go` covers draft/preview/publish/delete, balanced
assembly with earliest expiry, genealogy, label, substitution, disassembly to exact lots/colors
with quarantine, re-assembly, combo visibility and `color_required`, concurrent combo sales
against reservations and tampered-row detection by `kits.Verify`; unit tests in
`apps/api/internal/kits/*_test.go` and `apps/api/internal/sales/combo_colors_test.go`. Report:
`plans/reports/fullstack-developer-261018-kits-assembly.md`.

Review fixes (2026-10-18, after `plans/reports/code-reviewer-261018-kits-assembly-review.md`): disassembly
refuses kits reserved by other documents, kit/combo status follows the recipe (`set_by_recipe`),
draft kit prices carry no history until publish so a priced draft deletes cleanly, packing cost is
expensed as a `cogs_adjustments` row on disassembly (migration 000042), flavor SKUs and substitute
colors are validated. Re-verified on 2026-10-18: `gofmt`, `go vet` (with and without `integration`),
`golangci-lint` 0 issues, `go test -race ./...` and `go test -tags integration -count=1 ./...` green,
kits and sales concurrency tests green under `-race`.

## Success criteria & verification

- Integration suite green; `tally ledger verify` clean; for each assembly posting Σ consumed value + packing cost = produced value.
- Kit lot expiry equals the earliest component lot expiry.
- Disassembling 2 kits returns components to the same lots/colors recorded in genealogy; damaged 1 unit lands in quarantine (not sellable).
- Draft combo does not appear in `GET /search?type=sku&sellable=true` or POS bootstrap; after publish it does.
- Combo sale without a counter color for a colored component → `422 color_required`.

## Risks

| Risk | Mitigation |
|---|---|
| Complex genealogy for partial disassembly | Proportional split by genealogy quantity with remainder to last; tests on fractions |
| Substitution changing cost expectations | Cost view recomputed on the order; posting snapshots actual cost |
| Reservations are per SKU, not per color: a fixed-color assembly order reserves the SKU, so another color can still be sold down to the reserved quantity | Accepted (known limitation); `PostAssembly` picks the fixed color exactly and refuses with `insufficient_stock` when that color is short |
| Disassembly values the returned kit at the kit SKU's moving average, not the specific kit lot's produced value | Accepted (known limitation); kit lots of one SKU normally share cost, and `kits.Verify` checks the posting balances |
| Packing cost on disassembly | User decision 2026-10-18: components return to their source lots at their original consumed cost; the packing-cost share is written off as a COGS adjustment, not folded into component cost |

## Security

- Postings through the coordinator; recipes and labels tenant-scoped; deleting sold kits blocked.

## Rollback

Revert code. Wrong assemblies are undone with disassembly; a `Đảo` endpoint for assembly postings is not built (verify already accepts `reversal_of`).

## Next

Phase 17 builds kit/assembly screens; phase 12 reports kit margins.
