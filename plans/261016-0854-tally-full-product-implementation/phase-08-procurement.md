---
title: "Phase 08: Procurement (Nhập hàng)"
status: done
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
- Landed cost and DG raise the unit cost of the units the line still keeps (received − returned), not of everything received: a later return of those units issues them at the cost they carry in stock. A return leaving at a basis that differs from its moving-average issue cost books the residue as a `cogs_adjustments` row with source `purchase_return` (migration 000039), so nothing stays stranded in stock value. A DG values the line at max(0, new price × qty − line discount), so one line never credits more than its current net value (10 × 10000 less 20000 dropped to 0 credits 80000, not 100000); the unit cost stops at zero, and any rounding shortfall below it goes to a `cogs_adjustments` row with source `purchase_price_adjustment`. A credit larger than what the receipt still owes (after partial payment) follows the existing debt credit flow.
- A receipt is matched to one supplier document at most (unique `supplier_document_receipts (tenant_id, receipt_id)`, migration 000038; 409 `receipt_already_matched`). Matching a receipt twice would count its amount twice in the supplier reconciliation. A receipt the supplier split over two invoices is matched to the first, and each invoice shows its variance. Matching share-locks the receipts before the document, and a reversal checks for a link under its receipt lock, so a reversal and a match of the same receipt serialise.
- Matching compares a document with each receipt's total moved by its DGs. XT is not subtracted: the supplier invoices returns on a separate adjustment invoice, reconciled against the return's credit note.
- Settlement `replacement` applies the credit note to the receipt's payable now. The replacement goods arrive on a new PN at the original price, which records a new payable, so the total owed to the supplier ends where it was. Phase 15 UI guides the user to create that PN.
- An ordered line received in several batches takes its discount cumulatively: each batch carries the whole-đồng discount of everything received so far minus that of the earlier batches, so the batches add up to the ordered discount exactly.
- Document photos use the shared `POST /attachments` flow (org) with owner type `receipt` or `supplier_document`. Org accepts only owner types registered in `org.Deps.AttachmentOwners` (422 `owner_type: invalid`) and checks the owner exists in the tenant inside the insert transaction (422 `owner_id: not_found`). Procurement registers its two types through `procurement.AttachmentOwners()`; `app.Domains.AttachmentOwners()` adds `product` and `party` (signed debt offset record).
- Procurement postings (`POST /receipts`, landed costs, `:reverse`, XT, DG) sit on the posting route group without the entitlement middleware, so a retry of an accepted posting replays after the tenant turned read-only. The coordinator refuses a new posting of a read-only tenant (403 `subscription_read_only`) through `posting.Deps.Entitlements`, which `app.NewDomains` wires from the same `EntitlementReader` (`app.WithEntitlements`). Re-authentication guards `:reverse` only. Close-short posts nothing, and a DG is audited and reversible.
- `procurement.Verify` (`tally ledger verify`) also checks that supplier documents are matched to posted receipts only, each at its total moved by its DGs, with status and variance following from them, and that each receipt line's unit cost × kept units equals its amount + landed cost + DGs − what its returns took out, within the 4-decimal rounding of each cost move.

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

- [x] PO model + status + draft CRUD + line pricing — `orders.go`, `status.go`; `orders_test.go`, `status_test.go`, PO 100 thùng integration
- [x] Receipt posting (lots, colors, conditions, PO projections) — `receipt_posting.go`, migration 000037; `receipt_posting_test.go`, 60+40 → completed, replay, damaged/wrong_item integration
- [x] Payables + pay now + schedules — payable via debt port; schedule/pay-now/advance integration
- [x] Landed cost allocation + cost split — `landed_cost.go`, `cost_split.go`; 33334/33333/33333 and avg_cost 53333.4 integration
- [x] Supplier documents + matching — `matching.go`; variance → matched, obligations unchanged integration
- [x] Purchase returns (exact lot/color) + credit notes — `return_posting.go`; return from the tím lot only, 409 return_exceeds_received integration
- [x] Purchase price adjustments (DG) — `price_adjust.go`; DG after a partial sale: on-hand −6000 / sold −4000 integration
- [x] Close short + late orders — late-orders days_late 5, close-short → closed_short integration
- [x] Receipt reversal rule — reauth, already_reversed, receipt_has_followups, receipt_stock_consumed, PO pending restored integration
- [x] Tests + OpenAPI — `api/openapi.yaml` + contract test; `internal/app/procurement_integration_test.go`, ledger verify clean
- [x] Verifier covers supplier matching and receipt line unit cost — `verify.go`; tampered-row integration (`TestProcurementVerifyReportsTamperedRows`)
- [x] Attachment owners registered and checked — `org.Deps.AttachmentOwners`, `procurement.AttachmentOwners()`; 422 owner_type/owner_id unit + integration
- [x] Postings gated on the subscription — `app.WithEntitlements`; read-only store gets 403 on a new receipt, replay still 201

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
| On-hand vs sold split is approximate when a lot is shared: a SKU that tracks no lots keeps one lot per color (`lot_no` ''), so later receipts add to the same lot and a landed cost or DG on an older receipt counts as held min(lot balance, line's kept qty), not what is left of that receipt (FIFO-blind) | Accepted; the split only moves value between stock and COGS, the totals stay exact and verify clean |
| Receipts do not store `prices_include_tax`: a DG raising a net-0 line infers the tax basis from the receipt total, and falls back to the store's current setting when the receipt has no tax (only net-0 taxed lines, or 0% elsewhere) and the store changed the setting after posting | Accepted (rare); follow-up: snapshot `prices_include_tax` on receipts (`PricedDoc.PricesIncludeTax` already carries it) in a later migration |
| Verify checks on-hand + sold = carried per line but not the split against stock moves or `cogs_adjustments` | Accepted; follow-up check |

## Security

- Posting through coordinator (period lock, idempotency, audit); attachments tenant-checked; supplier bank details never logged.

## Rollback

Revert code. Production mistakes are corrected by `Đảo`, XT or DG documents; posted receipts are never edited.

## Next

Phase 12 reports read receipts/payables; phase 15 builds the purchase order, receipt and supplier screens.
