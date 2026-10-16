---
title: "Phase 08: Procurement (Nhập hàng)"
status: todo
priority: P1
effort: 6d
dependsOn: [phase-06-pricing, phase-07-inventory-ledger, phase-10-debt-cash]
---

# Phase 08 — Procurement (Nhập hàng)

## Context links

- [database-schema-inventory-procurement.md](design/database-schema-inventory-procurement.md) §3 procurement, §4 posting invariants
- [database-schema-debt-cash-crm-saas.md](design/database-schema-debt-cash-crm-saas.md) §1 obligations/payments/credit notes (payables)
- [architecture.md](design/architecture.md) §3.4 State `PurchaseOrderStatus`, §3.5 posting flow
- [ubiquitous-language.md](design/ubiquitous-language.md) §2 procurement terms, §5 prefixes PO, PN, XT, DG
- PRD §6.4 (PUR-02..11; PUR-01 removed), CAT-11, PRC-08, §7.5 costing

## Overview

Implement the `procurement` domain: purchase orders (PO) with the fixed line column order, receipts (PN) — quick receipts without an order and receipts against an order in several batches — with lot/expiry/color/condition, landed cost allocation, supplier documents matched to receipts, purchase returns (XT) by receipt line with exact lot and color, purchase price adjustments (DG) for final price and post-purchase discount, and closing an order short. Receipts create payables and optional immediate payments through debt ports; costs flow into moving average via `inventory.StockLedger`.

## Key insights

- "Nhập hàng" replaces "Mua hàng"; there is no purchase request (PUR-01 removed). Low-stock hints stay in notifications only.
- Draft PO has no ledger effect; confirming needs no approval (PUR-02). Status machine: `draft → confirmed → partially_received → completed`, `confirmed|partially_received → closed_short` (reason required), owned by `PurchaseOrderStatus` (State).
- One SKU in several colors = several PO/receipt lines; the receipt line color becomes the lot color; same lot no. with another color = another lot (PUR-05).
- Damaged receipt lines go into a quarantine twin lot; `wrong_item` lines are not received into stock (recorded for the supplier claim); `excess` lines are received and flagged.
- Payable is created once per receipt (`obligations` unique on source). Supplier documents only match; they never create another payable (PUR-08). Price differences go through DG, not by editing receipts.
- Landed cost and DG split value into on-hand (`cost_adjustment` move) and already-sold (`cogs_adjustments`) parts using the receipt lot's remaining quantity at posting time (§7.5).

## Requirements

| ID | Behaviour |
|---|---|
| PUR-02 | PO: supplier picker shows only trading suppliers; expected date; lines in order **Tên SP, ĐVT, Số lượng, Quy cách, Đơn giá, Thành tiền, Giá bán lẻ**; color chip per line; confirm without approval |
| CAT-11 / PRC-08 | Quick-create product from a PO line (catalog endpoint); editing "Giá bán lẻ" on a line calls `PriceWriter` with source `purchase_order` (only new transactions) |
| PUR-03 | Quick receipt without PO or receipt against PO; repeated confirm never re-posts (idempotency key + unique) |
| PUR-04 | Multiple batches; per-line ordered/received/returned/pending; condition accepted/damaged/wrong_item/excess |
| PUR-05 | Lot no., expiry required by SKU mode; color written to lot; unit conversion snapshot; document photo attachments |
| PUR-06 | Landed cost (shipping/loading/other) allocated by value (default) or qty; Σ lines = cost exactly; optional payee creates a payable |
| PUR-07 | Pay now in full/part (PC with `source_type = 'purchase'`), supplier advance usage, installment schedule; remaining payable traceable per receipt |
| PUR-08 | Supplier document with many receipts; match status matched/variance; variance resolved by DG |
| PUR-09 | Purchase return by receipt line: exact lot + color; returned ≤ received − returned; settlement credit payable / refund / replacement; credit note created |
| PUR-10 | DG final price: new unit price per receipt line → payable delta, on-hand/sold split, logged |
| PUR-11 | Expected-date tracking, late supplier list, close short with reason; received documents untouched |

## Architecture

```
procurement/
  model.go          PurchaseOrder(+Line), Receipt(+Line, Condition), LandedCostAllocation(+Line), SupplierDocument, PurchaseReturn(+Line), PurchasePriceAdjustment(+Line)
  status.go         PurchaseOrderStatus transition table
  receipt_posting.go PostReceipt (PostingCommand): validate lines by SKU mode → LotFactory → StockLedger.Receive
                    → update PO line projections → PayableRecorder.Record → optional PaymentRecorder.PayNow → audit/outbox
  landed_cost.go    AllocationStrategy (ByValue, ByQty) with largest-remainder; PostLandedCost
  return_posting.go PostPurchaseReturn: ExplicitLot issue at receipt unit_cost → CreditNoteRecorder
  price_adjust.go   PostPurchasePriceAdjustment (DG)
  matching.go       SupplierDocumentMatcher
  ports.go          StockReceiver, StockIssuer, CostAdjuster (inventory); PayableRecorder, PaymentRecorder, CreditNoteRecorder (debt); PriceWriter (pricing)
  queries.go        PO list with ordered/received/pending, late suppliers, receipt lookup for returns
  repo.go, service.go, http.go, dto.go, errors.go
```

Routes (`/api/v1`): `GET/POST /purchase-orders`, `GET/PATCH/DELETE /purchase-orders/{id}` (draft only, If-Match), `PUT /purchase-orders/{id}/lines`, `POST /purchase-orders/{id}:confirm|close-short`, `GET /purchase-orders/{id}/receivable-lines`, `GET/POST /receipts` (POST = post; `purchase_order_id` optional), `POST /receipts/{id}:reverse`, `POST /receipts/{id}/landed-costs`, `GET/POST /supplier-documents`, `POST /supplier-documents/{id}/receipts`, `GET/POST /purchase-returns`, `GET/POST /purchase-price-adjustments`, `GET /procurement/late-orders`. Posting routes require `Idempotency-Key`.

## Related files

| Path | Action |
|---|---|
| `apps/api/internal/procurement/*.go` + tests | create |
| `apps/api/internal/debt/recorders.go` | modify — expose `PayableRecorder`, `CreditNoteRecorder` used here |
| `apps/api/internal/inventory/ledger.go` | modify only if a missing port method is found (CostAdjust per lot) |
| `apps/api/internal/partner/repo.go` | modify — supplier search by supplied item (join `purchase_order_lines`) |
| `apps/api/api/openapi.yaml`, `apps/api/internal/app/routes.go`, `app/wire.go` | modify |

## Implementation steps

1. PO model, status machine, draft CRUD with version checks; line totals through `pricing.DocumentCalculator` (purchase mode: no promotions, tax per line); "Giá bán lẻ" edit → `PriceWriter`.
2. Receipt posting command: SKU-mode validation (`track_lot` → lot no., `track_expiry` → expiry), base qty = qty × factor, unit cost = (line amount + landed share) / base qty; lots via `LotFactory`; damaged → quarantine lot; PO projections `received_base_qty` updated under row lock; PO status recomputed.
3. Payables: `PayableRecorder.Record(source receipt, total − paid now, due date or schedule)`; pay now → PC payment allocated to that obligation in the same tx.
4. Landed cost: allocation strategy, Σ check, split on-hand/sold per receipt line using lot balance, write `cost_adjustment` moves and `cogs_adjustments`; optional payee payable (`landed_cost_allocation` source).
5. Supplier documents + matcher (Σ receipt totals vs document total → `matched` or `variance`).
6. Purchase return: pick receipt lines, enforce remaining qty, issue exact lot at original unit cost, update `returned_base_qty` on receipt and PO lines, credit note (`purchase_return`) and refund/replacement handling.
7. DG: per-line new price, payable delta (+ obligation or − credit note), cost split like landed cost.
8. Close short with reason; late-orders query by `expected_date < business_date`.
9. Receipt reversal (`Đảo`) only while no later issue consumed its lots below the received quantity; otherwise suggest purchase return.
10. Tests: multi-batch receipt with colors, re-press confirm replay, damaged into quarantine, landed cost sum exactness, return of exact color, DG after partial sale (on-hand vs sold split), payable created once even with supplier document, isolation.

## Todo

- [ ] PO model + status + draft CRUD + line pricing
- [ ] Receipt posting (lots, colors, conditions, PO projections)
- [ ] Payables + pay now + schedules
- [ ] Landed cost allocation + cost split
- [ ] Supplier documents + matching
- [ ] Purchase returns (exact lot/color) + credit notes
- [ ] Purchase price adjustments (DG)
- [ ] Close short + late orders
- [ ] Receipt reversal rule
- [ ] Tests + OpenAPI

## Success criteria & verification

- Integration suite green; `tally ledger verify` clean after every procurement test (stock and debt).
- PO 100 thùng (factor 24) received 60 + 40 in two receipts → line shows ordered 2 400, received 2 400, pending 0; status `completed`.
- Second POST with the same `Idempotency-Key` returns the first response with `Idempotent-Replay: true` and no new moves.
- Landed cost 100 000 over three lines (values 1:1:1) allocates 33 334 / 33 333 / 33 333; average cost updated.
- Return of a "tím" receipt line issues from the "tím" lot only; returning more than remaining → `409 return_exceeds_received`.
- Supplier document attached to two receipts leaves payable totals unchanged.

## Risks

| Risk | Mitigation |
|---|---|
| Reversal after stock already sold | Rule in step 9 + guidance to purchase return |
| Rounding in unit cost | 4 dp storage, residue to last line, verifier |
| Dependency on debt phase | Phase 10 core (obligations, payments, credit notes) precedes this phase |

## Security

- Posting through coordinator (period lock, idempotency, audit); attachments tenant-checked; supplier bank details never logged.

## Rollback

Revert code. Production mistakes are corrected by `Đảo`, XT or DG documents; posted receipts are never edited.

## Next

Phase 12 reports read receipts/payables; phase 15 builds the purchase order, receipt and supplier screens.
