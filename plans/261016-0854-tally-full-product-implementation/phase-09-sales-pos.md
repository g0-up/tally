---
title: "Phase 09: Sales, POS, Returns & Exchanges"
status: done
priority: P1
effort: 9d
dependsOn: [phase-06-pricing, phase-07-inventory-ledger, phase-10-debt-cash]
---

# Phase 09 — Sales, POS, Returns & Exchanges

## Context links

- [database-schema-sales-kits.md](design/database-schema-sales-kits.md) header (sales pricing columns), §1 quotes, §2 invoices, §3 stock outs/sales documents, §4 POS, §5 returns/exchanges, §7 invariants
- [database-schema-debt-cash-crm-saas.md](design/database-schema-debt-cash-crm-saas.md) §1 payments/credit notes, §2 cash entries
- [architecture.md](design/architecture.md) §3.4 (State `InvoiceStatus`, Factory `SalesDocumentFactory`), §3.5 posting flow
- [ubiquitous-language.md](design/ubiquitous-language.md) §2 sales terms (Hóa đơn, Phiếu xuất kho, Chứng từ bán, Giỏ hàng, Đổi hàng), §5 prefixes BG, SO, PX, BH, HD, GH, TH, DH, CA
- PRD §6.6 (SAL-01..10, 13..15), PRC-04/05/07, CASH-02/04/06, §7.1 recognition, §7.6 credit, §7.9 colors, §7.10 mix packs

## Overview

Implement the `sales` domain end to end on the server: quotes (BG), invoices (SO) with "Khách đã thanh toán", confirmation with exception checks, versioned changes after confirmation, reservations/backorder, one-step stock outs (PX) that post moves + sales document (BH) + receivable and consume earmarked advances, POS checkout (HD) with implicit shift (CA), saved POS carts (GH), returns (TH) with color from the source line, exchanges (DH) posting return + new sale + exchange in one transaction, offline POS sync with an exception queue, and internal print data for documents. Mix packs and combo lines are handled by the shared line model.

## Key insights

- Revenue and receivable are recognised by the **sales document** (BH from a stock out, HD from POS, or the new sale of an exchange), never by the invoice itself (§7.1). Invoices are commitments: they reserve stock, hold the agreed prices, and collect advances.
- "Khách đã thanh toán" on an invoice creates a PT payment with `purpose = 'deposit'` and `earmarked_invoice_id`; each stock out allocates earmarked advances to the new receivable first. Payment state label (Chưa thanh toán / Thanh toán một phần / Đã thanh toán đủ / Thừa tiền) is derived in Go from totals.
- Colors: `ColorRequirementSpec` blocks confirm/checkout when a colored SKU line has no color; stock out issues exactly that color via `ExactColorLot`; quotes may use any of the 7 colors.
- Mix packs: a `mix_pack` header line carries price and pack count; `mix_flavor` children carry parts and produce stock moves; `mix_key` keeps the pack reopenable as one unit in edit screens (§7.10). `MixPackExpander` turns a pack selection into children using `catalog.MixGroupPolicy`.
- Combos (from phase 11 recipes): `combo` line has revenue; `combo_component` children move stock and carry COGS; `ComboExpander` is a port filled by `kits` (until phase 11 lands, combos are not sellable because no active recipe exists — no stub data).
- Shift is opened implicitly by the first POS sale of the business date (`ux_shifts__one_open`); no close/count UI in 1.1 (CASH-06, OPEN-13). The worker closes the previous day's shift at tenant midnight.
- Offline POS (SAL-13): server endpoint accepts batches of offline sales with `offline_client_id`; cash only (PRD SAL-13, §11.3: electronic payments need partner confirmation), caps from `tenant_settings`; failures go to `pos_sync_exceptions`; idempotent by `offline_client_id`. The client side is phase 19.

## Requirements

| ID | Behaviour |
|---|---|
| PRC-05 | Quote states draft → sent → accepted/rejected → converted; `expired` derived when draft/sent and past `valid_until`; edit/delete only draft/sent; copy; convert to invoice (locks quote); no reservation, no revenue; line columns **Tên SP, ĐVT, Số lượng, Quy cách, Đơn giá, CK, Thành tiền, Giá bán lẻ**; any of 7 colors; mix packs |
| SAL-01 | Invoice: customer, payer, lines (**Tên SP, Giá, CK, ĐVT, SL, Thành tiền**), color, mix pack, note; draft no ledger; paid amount → remaining + payment state; edit/delete while undelivered and unpaid; copy to new draft; locked after first stock out |
| SAL-02 | Confirm checks stock, credit limit (`debt.CreditLimitPolicy`), min price (`pricing.MinPriceGuard`), color requirement; exceptions require owner confirmation (`credit_limit_exceeded`, `below_min_price`) |
| SAL-03 | Confirm reserves what is sellable and records backorder for the rest per line |
| SAL-04 | Stock out (PX) many per invoice; qty ≤ reserved − delivered; posts moves (exact color), BH, receivable, consumes earmarked advance; shows "Phiếu xuất kho" list on invoice; color shortage fails |
| SAL-05 | POS checkout: scan/search (grid ≤ 30 tiles, color dots), customer, unit switch, line discount, cash/transfer/credit; atomic; colored SKUs need a color with stock (auto-pick when one color left); "+ Thêm màu khác" splits, same color merges; price by color and customer group |
| SAL-06 | Credit sale, deposit, partial payment, collection schedule (`obligation_schedules`) |
| SAL-07 | Print payloads for quote/invoice/PX/BH/HD/TH/DH with label "Chứng từ nội bộ — không phải hóa đơn điện tử" (rendering in phase 16) |
| SAL-08 | Return full/partial; lines split by sold color from source; legacy lines without color ask for color; ≤ sold − returned per color; restock sellable or quarantine; reduce debt first, excess → refund due credit note |
| SAL-09 | Return without source document: reason required; cost = current average flagged `current_average` |
| SAL-10 | Cancel undelivered remainder (releases reservations); post-confirmation changes create `invoice_versions` with change summary; material change invalidates earlier exception confirmations |
| SAL-13 | Offline sync endpoint, cash only (no transfer, no credit), caps, exception queue, resolve/discard by owner |
| SAL-14 | POS carts: save, show customer, edit/delete/copy; list Đang soạn/Đã bán; no reservation; keep colors incl. combo component colors; sold carts locked, copy only |
| SAL-15 | Exchange: returned part (by color, "Đổi sang màu khác" pre-adds same SKU), taken part (picker, color with stock, price by customer and color); shows returned/taken value, "Khách trả thêm" or "Cửa hàng trả lại khách", reduces old debt; one posting creates TH + new BH + DH |
| CASH-02/04 | Payments created from POS, invoice, return, exchange post once; multi-method split sums to total; tendered/change in snapshot; transfer = `pending_verification` |
| CASH-06 | Implicit shift; no open/close UI |

## Architecture

```
sales/
  model.go            Quote, Invoice, InvoiceVersion, StockOut, SalesDocument, PosCart, Shift, SalesReturn, Exchange, PosSyncException
  lines.go            SalesLine (shared by quote/invoice/cart/document), LineKind, MixKey; MixPackExpander, ComboExpander port
  status.go           QuoteStatus, InvoiceStatus transition tables (State)
  specs.go            ColorRequirementSpec, InvoiceEditSpec, QuoteEditSpec, ReturnPolicy, OfflineSalePolicy
  pricing_adapter.go  builds pricing.DocumentDraft from lines; applies server totals
  quote_service.go    QuoteService (CRUD, send, accept/reject, copy, convert)
  invoice_service.go  InvoiceService (draft CRUD, paid amount, copy), ConfirmInvoice (PostingCommand), ReviseInvoice, CancelRemainder
  stock_out.go        PostStockOut (PostingCommand) → inventory.StockIssuer, SalesDocumentFactory.FromStockOut, debt ports
  pos.go              PosCheckout (PostingCommand), ShiftService.EnsureOpen, PosCartService
  returns.go          PostSalesReturn (PostingCommand)
  exchange.go         PostExchange (composite command: return + new sale + exchange row)
  offline_sync.go     OfflineSyncService (batch, per-sale idempotency, exception queue)
  documents.go        SalesDocumentFactory (FromStockOut, FromPos, FromExchange)
  print.go            PrintPayloadBuilder (per document type, internal-document label)
  ports.go            StockReserver, StockIssuer, StockReceiver, ColorAvailabilityReader (inventory); ReceivableRecorder, PaymentRecorder, CreditNoteRecorder, CreditLimitPolicy (debt); PriceResolver, DocumentCalculator, MinPriceGuard, PromotionUsageRecorder (pricing); CampaignAttributor (campaign, phase 12); ComboExpander (kits, phase 11)
  repo.go, queries.go, http.go, dto.go, errors.go
```

Routes (`/api/v1`): quotes `GET/POST /quotes`, `GET/PATCH/DELETE /quotes/{id}`, `POST /quotes/{id}:send|accept|reject|revise|copy|convert`, `GET /quotes/{id}/history` (status and revision history from `audit_log`); invoices `GET/POST /invoices`, `GET/PATCH/DELETE /invoices/{id}`, `PUT /invoices/{id}/lines`, `PUT /invoices/{id}/paid-amount`, `POST /invoices/{id}:check|confirm|revise|reserve-more|cancel-remainder|copy` (`:check` = dry run of color/min-price/credit guards, no writes; `:reserve-more` = reserve backordered qty that became sellable), `GET /invoices/{id}/versions`; stock outs `POST /invoices/{id}/stock-outs`, `GET /stock-outs/{id}`, `POST /stock-outs/{id}:reverse`; sales documents `GET /sales-documents`, `GET /sales-documents/{id}`; POS `POST /pos/checkout`, `GET /pos/bootstrap` (tiles, settings, price snapshot for offline cache), `GET/POST /pos/carts`, `GET/PATCH/DELETE /pos/carts/{id}`, `POST /pos/carts/{id}:copy|checkout`, `POST /pos/offline-sync`, `GET /pos/sync-exceptions`, `POST /pos/sync-exceptions/{id}:resolve|discard`; returns `GET/POST /sales-returns`, `GET /sales-documents/{id}/returnable-lines`; exchanges `GET/POST /exchanges`, `POST /exchanges:preview`; print `GET /print/{doc_type}/{id}`. All posting routes require `Idempotency-Key`; confirm/checkout accept `exception_confirmation_ids[]`.

## Related files

| Path | Action |
|---|---|
| `apps/api/internal/sales/*.go` + tests + `integration_test.go` | create |
| `apps/api/internal/debt/recorders.go`, `credit_limit.go` | modify — receivable recorder, earmarked advance consumption, credit policy |
| `apps/api/internal/cash/recorder.go` | modify — cash entries for POS/return/exchange payments with `shift_id` |
| `apps/api/internal/cli/worker.go` | modify — close previous shift at tenant midnight |
| `apps/api/api/openapi.yaml`, `apps/api/internal/app/routes.go`, `app/wire.go` | modify |

## Implementation steps

1. Shared `SalesLine` model with snapshot + pricing columns, `MixPackExpander`, line merge/split rules (same SKU+color+unit merges; "+ Thêm màu khác" splits) and pricing adapter to `DocumentCalculator`.
2. Quotes: CRUD, status machine with derived expiry, copy, convert (creates draft invoice copying lines, quote → converted in the same tx).
3. Invoices: draft CRUD with `InvoiceEditSpec`; paid amount → deposit payment (earmarked) create/replace while draft; payment state derivation.
4. `ConfirmInvoice`: color spec, `MinPriceGuard`, `CreditLimitPolicy`, exception consumption, reservation per line (`reserved` vs `backorder`), `invoice_versions` v1, status `confirmed`.
5. `ReviseInvoice` (version n+1, change summary, material change flag, re-run guards) and `CancelRemainder` (release reservations, `cancelled_base_qty`).
6. `PostStockOut`: qty ≤ reserved − delivered per line; issue exact color; BH via `SalesDocumentFactory.FromStockOut` with COGS from posted moves; receivable for total − earmarked advances consumed; promotion usage; invoice projections + status (`partially_delivered`/`completed`); outbox `sale.posted`.
7. POS: `ShiftService.EnsureOpen`, `PosCheckout` (resolve prices server-side, color spec, issue FEFO/exact color, HD document, payments split by method, change in snapshot, credit sale → receivable, walk-in party NULL); carts CRUD/copy and checkout-from-cart marking `sold`.
8. Returns: returnable lines per source document line and color; `ReturnPolicy`; restock to original lot or quarantine twin; cost original or current average; debt reduction via credit note allocation then refund due; optional immediate refund payment.
9. Exchange: preview endpoint (values, pays/refunds, debt reduction) and composite posting (TH + new sale document `source = 'exchange'` + DH + payments) in one coordinator transaction; OPEN-18 default: return first reduces open receivable of the original document, remainder refunded, new sale collected in full.
10. Offline sync: validate `OfflineSalePolicy` (cash only — no credit or transfer, ≤ `offline_pos_max_amount`, created within `offline_pos_max_hours`, session `offline_until` valid), post each sale with idempotency by `offline_client_id`; failures to `pos_sync_exceptions`; resolve by posting a corrected sale or discard with audit.
11. Print payloads with tenant header, doc lines (mix packs grouped by `mix_key`), totals, payment state, internal label.
12. Tests: AT-style flows — invoice with deposit → two stock outs → receivable net of deposit; credit limit exception path; POS concurrency oversell per color; cart lifecycle; return exceeding sold color rejected; exchange totals both directions; offline batch replay; mix pack expand and return per flavor; ledger verify after each.

## Todo

- [x] Shared sales line model + mix pack expander + pricing adapter
- [x] Quotes (status, expiry, copy, convert)
- [x] Invoices draft + paid amount + payment state
- [x] Confirm (color/min price/credit, reservation/backorder, version 1)
- [x] Revise + cancel remainder
- [x] Stock out → BH + receivable + advance consumption
- [x] POS checkout + implicit shift + carts
- [x] Returns (color from source, quarantine, credit/refund)
- [x] Exchanges (preview + composite posting)
- [x] Offline sync + exception queue
- [x] Print payloads
- [x] Integration tests + ledger verify + OpenAPI

## Success criteria & verification

- Integration suite green; `tally ledger verify` (stock + debt + cash) clean after every test.
- Invoice 10 000 000 with paid 3 000 000: after stock outs of 60% and 40%, receivable open = 7 000 000, deposit fully allocated, payment state "Thanh toán một phần".
- Confirm with colored SKU line without color → `422 color_required`; POS with one remaining color auto-picks it.
- 20 concurrent POS checkouts on 10 units of "vàng" → exactly 10 units sold, others `409 insufficient_stock` (AT-03 analogue).
- Return of 3 "đỏ" when 2 were sold → `409 return_exceeds_sold`.
- Exchange returned 500 000 vs taken 650 000 → "Khách trả thêm 150 000"; one TH, one BH (`source = 'exchange'`), one DH created; replay with same key creates nothing new.
- Offline batch replayed twice posts once; a credit or transfer sale in the batch lands in `pos_sync_exceptions` with `offline_payment_not_allowed`.

## Risks

| Risk | Mitigation |
|---|---|
| Phase size | Steps 2–3 (quotes, drafts) can run in parallel with 6–7 once the line model exists |
| POS latency | `ResolveMany`, one lock pass per SKU set, p95 checkout target < 300 ms checked in phase 21 |
| Exchange settlement disputes (OPEN-18) | Default implemented and documented in plan decisions; preview shows exact effect before posting |
| Combo dependency on kits | `ComboExpander` port; no combo sale until phase 11 provides recipes |

## Security

- All postings through coordinator with period lock, idempotency, audit; exception confirmations hash-bound; offline sync requires a session with valid `offline_until`; print payloads tenant-checked; walk-in payments never expose other customers.

## Rollback

Revert code. Business corrections use `Đảo` (stock out, POS document), returns, or invoice revisions; posted rows are never edited.

## Next

Phase 11 supplies combo recipes; phase 12 adds campaign attribution and CRM timeline consumers of `sale.posted`; phase 16 builds sales and POS screens; phase 19 builds the offline client.
