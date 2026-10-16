---
title: "Phase 07: Inventory Ledger"
status: todo
priority: P1
effort: 7d
dependsOn: [phase-05-catalog-partners]
---

# Phase 07 — Inventory Ledger

## Context links

- [database-schema-inventory-procurement.md](design/database-schema-inventory-procurement.md) §1 ledger, reservations, §2 counts/adjustments, §4 invariants
- [database-conventions.md](design/database-conventions.md) §6 lock order, append-only list
- [database-schema-debt-cash-crm-saas.md](design/database-schema-debt-cash-crm-saas.md) `cost_recalculation_runs`, `onboarding_steps`
- [architecture.md](design/architecture.md) §3.4 (CostingMethod, LotPicker), §3.5 posting flow
- PRD §6.5 (INV-01..03, INV-05..12), §7.4 stock definitions, §7.5 costing, §7.9 colors, §7.8 reversal

## Overview

Implement the `inventory` domain: lots, the append-only `stock_moves` ledger with `lot_balances` and `sku_stock` projections, moving-average costing, lot picking (exact color, FEFO), reservations with oversell protection, stock adjustments (DC) including opening stock, quarantine and disposal, stock counts (KK) with blind mode and cutoff variance, expiry roll, stock list grouped by color (INV-12), stock card (INV-10), lot trace, low/over-stock signals, backdate recost runs, and `tally ledger verify`. Every other domain changes stock only through `inventory.StockLedger`.

## Key insights

- Ledger is the truth; projections are updated in the same transaction by Go after `SELECT … FOR UPDATE` in the global lock order (`sku_stock` asc → `lots`/`lot_balances` asc → `reservations`). No triggers; `tally ledger verify` proves Σ moves = projections per lot, per color, per SKU, and `stock_value = Σ value_amount`.
- Color lives on the lot. Issuing with a color uses `ExactColorLot` (never substitutes another color, §7.9); issuing without color on a SKU with color stock is rejected with `color_required`; SKUs without color stock use `FefoLot` across colors.
- Average cost is per SKU, not per color: `MovingAverageCosting` recomputes `avg_cost = stock_value / on_hand` on receipts and cost adjustments; issues capture `unit_cost = avg_cost` at posting. Returns referencing the original move reuse its `unit_cost`.
- Reservations are per SKU (OPEN-17). `ReservationService.Reserve` locks `sku_stock` and checks `reserved + qty ≤ sellable` (AT-03); shortage after an expiry roll is surfaced as a warning, never as negative stock.
- Negative stock is always blocked in 1.1 (§7.4), with no override.

## Requirements

| ID | Behaviour |
|---|---|
| INV-01 | Every move records SKU, lot, color, signed base qty, unit cost, value delta, source doc/line, `business_date`, `posted_by`; balances reconcile in total and per color |
| INV-02 | `on_hand`, `quarantine_qty`, `expired_qty`, `sellable`, `reserved`, `available = sellable − reserved` exposed consistently in one read model |
| INV-03 | Manual and automatic (invoice) reservations; release on cancel; concurrent invoices cannot over-reserve |
| INV-05 | KK: scope all/category/selection, blind mode, one line per SKU × color (× lot when tracked); `moved_since_start_qty` at finish; variance needs `count_variance` exception; posting creates a DC kind `count` |
| INV-06 | DC: damage/loss/gift/internal use/increase/decrease with reason; color required when the SKU has color stock; no revenue |
| INV-07 | Lots + expiry; FEFO suggestion; expired lots blocked from sale when `block_expired_lot_sale`; disposal needs `stock_disposal` exception; trace receipt → sales by lot; notifications include color |
| INV-08 | Low stock (`available < min_stock`) and overstock (`on_hand > max_stock`) events to outbox; never auto-creates purchase orders |
| INV-09 | Stock value from cost, never from sale price |
| INV-10 | Stock card: SKU picker, date range presets (default this month), color filter, unit mode "Quy đổi tự động" or fixed unit, opening row, running balance, 4 totals, "Đã khớp sổ và tồn" badge from verify, doc no links |
| INV-11 | Quarantine in/out as moves between normal and quarantine lots; release needs `quarantine_release` exception |
| INV-12 | Stock list one row per SKU with per-color chips + child rows (on hand, sellable, price, value), expand/collapse all, color filter auto-expands, "Chỉ hàng có màu", "giữ X · cách ly Y", edit price buttons (calls `PriceWriter` source `inventory`), stock card links per SKU/color |
| §7.5 | Backdated posting that changes later costs creates a `cost_recalculation_runs` preview; owner confirms; apply writes `cost_adjustment` moves + `cogs_adjustments`; locked periods get adjustments in the open period |
| OPEN-14 | Opening stock entered by hand through the onboarding wizard as DC kind `opening` with unit cost |

## Architecture

```
inventory/
  model.go        Lot, StockMove, LotBalance, SkuStock (Available()), Reservation, StockCount(+Line), StockAdjustment(+Line), MoveType
  ledger.go       StockLedger: Receive, Issue, Transfer(quarantine), CostAdjust, Reverse(docType, docID)
                  each takes []MoveRequest and returns []PostedMove (unit_cost captured)
  costing.go      CostingMethod interface { OnReceive, OnIssue, OnCostAdjust }; MovingAverageCosting
  lotpicker.go    LotPicker interface; ExactColorLot, FefoLot, ExplicitLot (returns, purchase returns, disassembly)
  lots.go         LotFactory (find-or-create by unique tuple; quarantine twin lot)
  reservation.go  ReservationService: Reserve, Consume, Release, Adjust (implements sales.StockReserver, kits port)
  adjustment.go   PostStockAdjustment (PostingCommand), AdjustmentPolicy (reason/color/exception rules per kind)
  count.go        StockCountService (start snapshot, record counts, finish → moved_since_start), PostStockCount (PostingCommand)
  expiry.go       ExpiryRoller (worker job, per tenant midnight, idempotent per business_date)
  recost.go       RecostPlanner (preview impact), PostRecost (apply after confirmation)
  queries.go      StockListQuery (INV-12), StockCardQuery (INV-10, UnitDisplay strategy: AutoConvert | FixedUnit), LotTraceQuery
  signals.go      StockSignalDetector → outbox "stock.low", "stock.over", "lot.expiring", "lot.expired"
  verify.go       LedgerVerifier (used by CLI and integration tests)
  lockers.go      registers sku_stock / lots / reservations lockers with posting.Coordinator
  ports.go, repo.go, http.go, dto.go, errors.go
```

Implements consumer-owned ports declared elsewhere: `catalog.StockByColorReader`, `catalog.UsageReader` (stock part), `sales.StockReserver`, `sales.StockIssuer`, `procurement.StockReceiver`, `kits.StockConsumer`.

Routes (`/api/v1`): `GET /stock` (INV-12 list; filters `q`, `category_id`, `color_id`, `colored_only`, `below_min`, cursor), `GET /stock/{skuId}` (color/lot breakdown), `GET /stock-card?sku_id=&color_id=&from=&to=&unit_mode=auto|fixed&unit_id=`, `GET /lots?sku_id=&state=all|expiring|expired|quarantine&kind=kit&expiring_within=`, `GET /lots/{id}/trace`, `GET/POST /reservations`, `POST /reservations/{id}:release`, `GET/POST /stock-adjustments`, `POST /stock-adjustments/{id}:reverse`, `GET/POST /stock-counts`, `PUT /stock-counts/{id}/lines`, `POST /stock-counts/{id}:finish|recount` (recount reopens selected lines before posting), `POST /stock-counts/{id}:post`, `POST /cost-recalculations` (preview), `POST /cost-recalculations/{id}:confirm|discard`. Posting routes require `Idempotency-Key`; exception-bound routes take `exception_confirmation_id`.

CLI: `tally ledger verify [--tenant] [--sku]` (non-zero exit on mismatch), `tally worker` runs `ExpiryRoller` and `StockSignalDetector` schedules.

## Related files

| Path | Action |
|---|---|
| `apps/api/internal/inventory/*.go` (files above) + `_test.go` + `integration_test.go` | create |
| `apps/api/internal/cli/ledger.go` | create — `ledger verify` |
| `apps/api/internal/cli/worker.go` | modify — schedule expiry roll + signals |
| `apps/api/internal/catalog/search.go`, `edit_rules.go` | modify — use real `StockByColorReader` |
| `apps/api/internal/org/onboarding.go` | modify — `opening_stock` step completes on first posted `opening` DC |
| `apps/api/api/openapi.yaml`, `apps/api/internal/app/routes.go`, `app/wire.go` | modify |

## Implementation steps

1. Model + repos for lots, moves, balances, sku_stock; lockers registered with the coordinator; `LotFactory` find-or-create using `ON CONFLICT DO NOTHING` then select.
2. `StockLedger` core: validate positive base qty, pick lots, lock, write moves, update balances/sku_stock (sellable recomputed), apply costing, guard negative stock (`409 insufficient_stock` with per-color availability), publish `stock.changed`.
3. Costing: moving average with 4 dp; issue value = qty × avg_cost; when on_hand reaches 0 the remaining rounding residue is cleared into the last issue so `stock_value` returns to 0.
4. Lot pickers + expiry guard (`expired_lot_sale` blocked unless setting off; disposal path only via DC).
5. Reservations with concurrency integration test (20 goroutines × reserve on 10 units → Σ reserved ≤ 10).
6. Stock adjustments command per kind with `AdjustmentPolicy`; opening kind creates lots with cost; reversal mirrors moves with `reversal_of` (one reversal per move unique index).
7. Stock counts: start snapshots `system_qty` per SKU × color × lot; blind hides `system_qty` in DTO; finish computes `moved_since_start_qty` from moves after `started_at`; post creates DC `count` lines for non-zero variance, requiring `count_variance` exception when any variance ≠ 0.
8. Quarantine in/release as paired moves into a twin lot with `is_quarantine = true`.
9. Expiry roller job + signals detector (dedupe key `stock.low:<sku>:<business_date>`).
10. Queries: stock list with color aggregation (`lot_balances` grouped by `color_id` in one query joined to `sku_stock`), stock card with opening balance = Σ moves before `from`, unit display strategies (e.g. `17 bó 10 cái (180 cái)`), lot trace via `stock_moves` by `lot_id` + doc lookups.
11. Recost planner/apply for backdated postings (detect: posting with `business_date` earlier than `sku_stock.last_move_at` date of issues) — preview stored, owner confirms with re-auth.
12. `LedgerVerifier` + CLI + integration test after every posting test; property test: random sequence of receipts/issues/adjustments/reversals keeps all invariants.

## Todo

- [ ] Lots/moves/balances repos + lockers
- [ ] StockLedger + negative-stock guard
- [ ] MovingAverageCosting
- [ ] LotPickers + expiry guard
- [ ] Reservations + concurrency test
- [ ] Stock adjustments (incl. opening, disposal, quarantine) + reversal
- [ ] Stock counts (blind, cutoff variance, exception)
- [ ] Expiry roller + stock signals
- [ ] Stock list / stock card / lot trace queries
- [ ] Recost preview/confirm/apply
- [ ] LedgerVerifier + CLI + property test
- [ ] Ports wired into catalog/search; OpenAPI

## Success criteria & verification

- `go test ./internal/inventory/...` and `-tags integration` green, including property test (≥ 500 random operations) with `LedgerVerifier` clean.
- Oversell test: concurrent reserve/issue never makes `on_hand < 0` or Σ reserved > sellable at reserve time (AT-03).
- Issue of "đỏ" when only "xanh" exists → `409 insufficient_stock` listing per-color availability; no substitution.
- Stock card totals: opening + in − out = closing and equals `sku_stock.on_hand` for the same date range ending today; badge "Đã khớp sổ và tồn" true.
- Expired lot issue blocked; disposal without confirmation → `422 exception_required{stock_disposal}`.
- `tally ledger verify` exit 0 on demo tenant.

## Risks

| Risk | Mitigation |
|---|---|
| Deadlocks under POS load | Global lock order + single retry on 40P01; load test in phase 21 |
| Rounding drift in average cost | Residue clearing at zero on hand; verifier checks `stock_value` |
| Backdated postings silently changing margins | Recost always preview + confirm; locked periods adjusted in open period |
| Large stock card ranges | Index `ix_stock_moves__card`; cursor pagination; opening computed by one SUM |

## Security

- All posting through the coordinator (period lock, idempotency, audit); exception-bound actions require confirmation and recent re-auth for disposal/quarantine release; `stock_moves` append-only by grant.

## Rollback

Revert code; ledger data in dev only. Production incidents: reverse wrong documents with `Đảo` (mirror moves); never edit moves.

## Next

Phase 08 receives stock through `StockLedger.Receive`; phase 09 reserves/issues; phase 11 consumes/produces kits; phase 17 builds stock screens.
