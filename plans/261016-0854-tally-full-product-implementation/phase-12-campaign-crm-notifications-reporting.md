---
title: "Phase 12: Campaign, CRM, Notifications & Reporting"
status: todo
priority: P1
effort: 8d
dependsOn: [phase-08-procurement, phase-09-sales-pos, phase-10-debt-cash, phase-11-kits-assembly]
---

# Phase 12 — Campaign, CRM, Notifications & Reporting

## Context links

- [database-schema-debt-cash-crm-saas.md](design/database-schema-debt-cash-crm-saas.md) §3 campaign/CRM, §4 notifications, §5 reporting/onboarding/AI
- [database-schema-catalog-pricing.md](design/database-schema-catalog-pricing.md) price lists and promotions with `campaign_id`
- [database-schema-org-partners.md](design/database-schema-org-partners.md) `outbox_events`, `attachments`
- [architecture.md](design/architecture.md) §3.4 (`notification.ProducerRegistry`, Observer via outbox), `reporting` read models
- [ubiquitous-language.md](design/ubiquitous-language.md) §2 campaign, CRM, report terms
- PRD §6.10 (SEA-01..03), §6.11 (CRM-01..03), NTF-01, §6.13 (RPT-01..11), DATA-04, §7.7 attribution, OPEN-13, OPEN-20

## Overview

Implement the read-heavy and side-effect domains that sit on top of the posting ledgers: campaigns (Tết/season) with period, budget, targets, attribution and checklists; demand tracking that never adds expected quantities to real stock; CRM timeline, consents, care schedule, re-order drafts at current prices, optional loyalty points; the in-app notification centre fed by outbox consumers and a daily sweep; all reports RPT-01..10 as plain-SQL read models with saved layouts and async CSV export; the optional read-only AI Q&A (RPT-11); and print payloads for DATA-04 consumed by web print templates.

## Key insights

- Attribution (§7.7): each financial line has at most one `primary` campaign (`ux_campaign_attributions__one_primary`); `tag` rows filter only and are never summed. Sales posting copies the active campaign of the line's price list/promotion as primary (wired here into `sales` through `campaign.AttributionWriter`); the owner may re-attribute with audit. Procurement receipts carry a purpose tag only — cost enters a season when the goods are sold (COGS on the attributed sales line).
- Closing a campaign only sets `status = 'closed'`; stock, cash and debt continue in the common ledgers. The closing checklist reminds stock count, statements, open orders and surplus stock; it never writes off debt (SEA-03).
- Demand tracking (SEA-02) is a query: confirmed undelivered invoice lines + reservations vs available stock + confirmed PO pending quantity by expected date → shortage per day. Expected receipts are shown separately and never added to `on_hand`.
- Reports are read models in `reporting` built with plain SQL over the ledgers (`stock_moves`, `sales_document_lines`, `obligation_settlements`, `cash_entries`, `cogs_adjustments`). No views, no materialized views, no functions (conventions §1). Sandbox tenants are excluded at the repository (ORG-06).
- Revenue = net revenue from `sales_documents` (BH/HD) minus returns/credit notes, tax separate; invoice (SO) totals are never revenue (RPT-03, §7.1). Gross margin labels state "chưa trừ chi phí vận hành" (OPEN-13) and distinguish provisional vs final when a recost run is pending.
- RPT-01 dashboard excludes walk-in customers (`parties.is_walk_in`) and opening balances (`debt_adjustments.kind = 'opening'`, cash entry `opening`) from debt movement; it has no "Cần xử lý" card; each card links to its detail route.
- RPT-02 NXT is per SKU (colors not split, OPEN-20): opening + Σ in − Σ out = closing, reproducible as-of.
- Notifications: producers register in `notification.ProducerRegistry` (OCP). Upsert by `dedupe_key` (`ON CONFLICT (tenant_id, dedupe_key) DO UPDATE SET updated_at`), resolve when the condition clears. Kinds produced in 1.1: `receivable_overdue`, `payable_due_soon`, `lot_expiring`, `lot_expired` (with color), `below_min_stock`, `surplus_stock`, `pos_sync_exception`, `statement_disputed`, `subscription_status`, `export_ready`. Never "Cần giao", "Tiền thu hộ chưa về", "Sao kê chưa ghép".
- Re-order (CRM-02) creates a draft invoice using current prices, units and pack sizes from `pricing.PriceResolver`; the owner confirms; history is never copied blindly.
- Loyalty (CRM-03) is off by default; earn on `sale.posted`, reverse on `sales_return.posted` with `reversal_of`; debt is never converted to points.
- AI Q&A (RPT-11) runs only when `settings.analytics_ai_enabled` and the plan entitlement allow it; the tool layer exposes read-only report queries (never raw SQL, never writes), answers cite report rows/document numbers, every call is logged in `analytics_queries`, and no PII or document content leaves the tenant unless the owner accepted the AI processor terms (NFR-16).
- DATA-04: the API returns immutable print payloads (sent snapshot versions: `invoice_versions`, `statement_versions`, posted documents); web renders HTML print templates with correct Unicode, units, tax and color names. Re-printing a sent version shows the same numbers.

## Requirements

| ID | Behaviour |
|---|---|
| SEA-01 | Campaign CRUD: name, kind, period, budget, target revenue/margin; attach price lists/promotions; attribution of lines outside the period allowed with audit; no ledger rewrite |
| SEA-02 | Demand tracking by day: confirmed demand, available, pending PO by expected date, shortage; expected never counted as on-hand |
| SEA-03 | Checklists before peak and at close (stock count, statements, open orders, surplus); close does not reset or write off |
| §7.7 | One primary campaign per financial line; tags filter only; season reports never double count |
| CRM-01 | Customer timeline (purchases, returns, payments, notes, care calls, consent changes), group, notes; consent log per channel/purpose |
| CRM-02 | Last-season customer list, care tasks with due dates, re-order draft at current prices requiring confirmation |
| CRM-03 | Optional loyalty: earn, redeem as discount, manual adjust with reason, reverse on return; balance = Σ entries |
| NTF-01 | Notification centre with dedupe, read/resolve/snooze; lot notices show color; links to the source screen |
| RPT-01 | Dashboard thu–chi–công nợ: Ngày / 7 ngày / Tháng / Năm, date picker, prev/next, today; Thu–chi (tổng thu, tổng chi, chênh lệch, tiền trong quỹ) and Công nợ (khách còn nợ, cửa hàng còn nợ NCC, sắp thu, sắp trả) with previous-period comparison and chart series |
| RPT-02 | NXT by SKU, stock by lot/expiry/value/reserved; opening + movement = closing |
| RPT-03 | Sales by product/customer/channel: net revenue after returns and discounts, tax separate |
| RPT-04 | Gross margin and margin %; COGS traceable to moves; provisional/final; "chưa trừ chi phí vận hành" |
| RPT-05 | Debt detail, aging, collection/payment schedule, advances shown separately (reuses `debt.AgingQuery`) |
| RPT-06 | Cash flow by `cash_entries.category` (sales, deposit, collection, supplier payment, refund, landed cost, other) per fund |
| RPT-07 | Kit report: progress, missing components, cost, margin; kit and component revenue never both counted |
| RPT-08 | Season: plan vs actual, surplus stock, season comparison with same definitions, day count and scope stated |
| RPT-09 | Column/filter customisation, saved reports, CSV export async with progress, cancel and expiring link |
| RPT-10 | Buy/clearance suggestions and cash-flow forecast with source, method, confidence; suggestions only |
| RPT-11 | Optional read-only AI Q&A within tenant, cited, logged, can be disabled |
| DATA-04 | Print payloads for PO, invoice, PX/PN, statement, kit label; snapshot of the sent version |

## Architecture

```
campaign/
  model.go          Campaign(Status), Attribution(Role), ChecklistItem(Phase, Kind)
  service.go        CampaignService (CRUD, close, checklist seed per phase)
  attribution.go    AttributionWriter (implements sales/procurement port; one-primary guard), Reattribute
  demand.go         DemandTrackingQuery
  repo.go, http.go, dto.go, errors.go
crm/
  model.go          CustomerActivity(Kind), CareTask, Consent, LoyaltyProgram, LoyaltyEntry
  timeline.go       TimelineProjector (outbox consumer: sale.posted, sales_return.posted, payment.posted)
  consent.go        ConsentService + ConsentChecker (implements debt.ConsentChecker)
  care.go           CareTaskService, LastSeasonCustomersQuery, ReorderDraftBuilder (uses pricing.PriceResolver, sales.InvoiceDrafter)
  loyalty.go        LoyaltyLedger (earn/redeem/reverse/manual), LoyaltyRedemption (implements pricing discount source)
  repo.go, http.go, dto.go
notification/
  model.go          Notification(Kind, Severity)
  producer.go       Producer interface + ProducerRegistry
  producers/        receivable_overdue.go, payable_due_soon.go, lot_expiry.go, min_stock.go, surplus.go, pos_sync.go, statement_disputed.go, subscription.go, export_ready.go
  sweeper.go        Sweeper (daily at tenant midnight: overdue, due soon, expiry, stock thresholds)
  repo.go (upsert by dedupe_key, resolve), http.go, dto.go
reporting/
  period.go         Period (day, 7 days, month, year; prev/next; tenant timezone), Comparison
  dashboard.go      DashboardQuery (RPT-01)
  nxt.go            NXTQuery, LotStockQuery (RPT-02)
  revenue.go        RevenueQuery (RPT-03)
  margin.go         GrossMarginQuery (RPT-04)
  debt.go           adapters over debt.AgingQuery / ForecastQuery (RPT-05)
  cash_flow.go      CashFlowQuery (RPT-06)
  kits.go           KitReportQuery (RPT-07)
  season.go         SeasonReportQuery (RPT-08)
  suggestion.go     BuySuggestion, ClearanceSuggestion, CashForecast (RPT-10) — Strategy per method
  saved.go          SavedReportService + ReportConfigSchema (Specification per report_key)
  export.go         ExportJobService, CSVWriter (streams rows, uploads attachment, signed link, expiry)
  catalog.go        ReportRegistry: report_key → Query + column spec (OCP for new reports)
  repo.go, http.go, dto.go
analytics/
  qa.go             QAService (entitlement + setting gate, tool calls over ReportRegistry, citations, log)
  model_client.go   ModelClient port (adapter reads provider config from env; disabled when unset)
printing/
  registry.go       PrintPayloadRegistry: doc_type → provider; phase 09 `sales.PrintPayloadBuilder` registers sales types
  providers.go      providers for PO, PN, XT, KK/DC, DS version, kit label (call each domain's read port)
```

Routes (`/api/v1`): `GET/POST /campaigns`, `GET/PATCH /campaigns/{id}`, `POST /campaigns/{id}:activate|close`, `GET/PATCH /campaigns/{id}/checklist`, `POST /campaign-attributions`, `GET /campaigns/{id}/demand?from=&to=`; `GET /customers/{id}/timeline`, `GET/POST /customers/{id}/consents`, `GET/POST /care-tasks`, `PATCH /care-tasks/{id}`, `GET /customers/last-season?campaign_id=`, `POST /customers/{id}/reorder-drafts`, `GET/PUT /loyalty/program`, `GET /customers/{id}/loyalty`, `POST /loyalty/adjustments`; `GET /notifications?state=`, `POST /notifications/{id}:read|resolve|snooze`, `GET /notifications/unread-count`; `GET /reports/dashboard?period=&date=`, `GET /reports/{reportKey}?…` (nxt, lot-stock, revenue, gross-margin, debt-aging, debt-schedule, cash-flow, kits, season), `GET /reports/suggestions/{buy|clearance}`, `GET /reports/cash-forecast`, `GET/POST /saved-reports`, `PATCH/DELETE /saved-reports/{id}`, `POST /export-jobs`, `GET /export-jobs/{id}`, `POST /export-jobs/{id}:cancel`, `GET /export-jobs/{id}/download` (signed redirect); `POST /analytics/questions`, `GET /analytics/questions`; the existing `GET /print/{doc_type}/{id}` (phase 09) gains `?version=` and the new doc types through the registry.

## Related files

| Path | Action |
|---|---|
| `apps/api/internal/campaign/*.go` + tests | create |
| `apps/api/internal/crm/*.go` + tests | create |
| `apps/api/internal/notification/**/*.go` + tests | create |
| `apps/api/internal/reporting/*.go` + tests | create |
| `apps/api/internal/analytics/*.go` + tests | create |
| `apps/api/internal/printing/*.go` + tests | create — registry; `sales/print.go` moves behind it unchanged |
| `apps/api/internal/sales/posting.go` | modify — call `AttributionWriter`, emit `sale.posted` payload with line campaign |
| `apps/api/internal/procurement/receipt_posting.go` | modify — purpose tag attribution |
| `apps/api/internal/debt/collection.go` | modify — use `crm.ConsentChecker` |
| `apps/api/internal/worker/consumers.go`, `worker/schedule.go` | modify — register consumers, daily sweep, export job runner |
| `apps/api/internal/platform/objectstore/signed_url.go` | modify only if export download needs a shorter TTL option |
| `apps/api/api/openapi.yaml`, `apps/api/internal/app/routes.go`, `app/wire.go` | modify |

## Implementation steps

1. Campaigns: model, status machine `planning → active → closed`, budget/targets, link price lists/promotions, checklist seeding per phase, close with checklist summary (no ledger effect).
2. Attribution: `AttributionWriter.Primary(docType, docID, lineID, campaignID)` inside the posting tx (sales lines from the matched price list/promotion campaign; receipts as `tag`); re-attribution endpoint (owner, audit, out-of-period allowed with warning).
3. Demand tracking query with per-day shortage; integration test with a pending PO on a later date showing shortage until that date.
4. CRM: timeline projector consuming outbox events with dedupe by event id; notes and care calls; consent log + `ConsentChecker` (latest row wins); wire into debt reminders.
5. Care tasks, last-season customer list (customers with primary-attributed sales in a closed campaign), re-order draft builder (current resolver prices, current units; lines with discontinued SKUs flagged, not copied).
6. Loyalty: program settings, earn on sale, reverse on return (proportional to returned net amount), redeem through a pricing discount source, manual adjust with reason; balance query = Σ entries.
7. Notifications: registry, producers, outbox consumers (`stock.changed`, `lot.expiry_changed`, `obligation.due_changed`, `pos_sync.exception`, `statement.disputed`, `export.finished`), daily sweeper per tenant timezone, resolve-on-clear, unread count, snooze.
8. Reporting foundation: `Period` value object (timezone-aware ranges, previous/next), `ReportRegistry`, column specs, pagination and sort whitelist per report.
9. RPT-01 dashboard: fund balances, thu/chi from `cash_entries` (excluding `opening`), debt totals from obligations (excluding walk-in and opening adjustments from movement), due-soon from schedules; previous-period values; chart series per bucket.
10. RPT-02..08 queries with `EXPLAIN` reviewed on the seeded load dataset; index additions (if any) go into a new migration in this phase, not into functions or views.
11. RPT-09 saved reports (`ReportConfigSchema` validation), export jobs: worker streams rows with keyset pagination into CSV (UTF-8 BOM for Excel), uploads to object storage via `attachments`, sets `expires_at`, notifies `export_ready`; cancel flips status and the runner checks between pages; expired links return `410`.
12. RPT-10 suggestions: buy = (avg daily sales × cover days − available − pending PO) with method/confidence labels; clearance = surplus vs sales velocity and near expiry; cash forecast reuses `debt.ForecastQuery` + promises, separated from confirmed cash.
13. RPT-11: gated QA service, tool schema over registry queries only, citations, log rows, provider adapter disabled when env is unset; tests prove no write path and tenant scoping.
14. Print payloads: registry over the phase 09 builder plus PO, PN, XT, KK/DC, DS version and kit label providers, using sent snapshot versions; color names and unit labels resolved at snapshot time.
15. Tests: attribution no double count, dashboard excludes walk-in/opening, NXT identity, revenue excludes SO totals, margin label, notification dedupe across repeated sweeps, consent gating, loyalty reverse, export expiry, AI disabled → `403 feature_disabled`, isolation for every report and export.

## Todo

- [ ] Campaigns + checklists + close
- [ ] Attribution writer + re-attribution
- [ ] Demand tracking
- [ ] CRM timeline + consents + care tasks + re-order drafts
- [ ] Loyalty ledger
- [ ] Notification registry, producers, consumers, sweeper
- [ ] Reporting foundation (Period, registry)
- [ ] RPT-01 dashboard
- [ ] RPT-02..08 queries + index review
- [ ] Saved reports + async CSV export
- [ ] RPT-10 suggestions + cash forecast
- [ ] RPT-11 AI Q&A (gated)
- [ ] Print payloads (DATA-04)
- [ ] Tests + OpenAPI

## Success criteria & verification

- Unit + integration suites green; `tally ledger verify` clean after the seeded scenarios.
- A sales line attributed to "Tết 2027" primary and tagged "Khuyến mãi tháng 1" contributes once to the season total and zero times to the tag total sum.
- Dashboard for a day with one walk-in credit sale and one opening debt adjustment: "Khách còn nợ" movement excludes both; "Tiền trong quỹ" equals `FundBalanceQuery`.
- NXT for any SKU: opening + Σ in − Σ out = closing for each sampled period on the load dataset.
- Revenue for an SO with two PX documents equals the sum of the two BH documents minus returns; the SO total is never used.
- Running the notification sweep twice yields one notification per dedupe key; selling stock above the minimum resolves `below_min_stock`.
- Export of 200 000 NXT rows completes async, the link expires at `expires_at` and returns `410` afterwards.
- Interactive reports p95 ≤ 5 s on the NFR-04 dataset (measured again in phase 21).
- With `analytics_ai_enabled = false`, `POST /analytics/questions` → `403 feature_disabled`; with it enabled, a question about yesterday's revenue cites BH/HD numbers and writes one `analytics_queries` row.

## Risks

| Risk | Mitigation |
|---|---|
| Slow reports on 1 000 000 moves | Tenant/time composite indexes, keyset pagination, async export, EXPLAIN in review; `sku_stock` projection for current stock |
| Double counting in season reports | One-primary unique index + report tests; tags excluded from sums |
| Notification spam | Dedupe key per condition + resolve-on-clear + snooze |
| AI leaking data | Off by default, entitlement + setting gate, read-only tools, provider terms acknowledgement, log |
| Misreading margin as accounting profit | Fixed label and provisional flag; no "BCTC" wording |

## Security

- All reports, exports and print payloads tenant-scoped through RLS + repository; export download links are signed, short-lived and bound to the tenant; consent required before email reminders and marketing lists; AI calls never include raw document tables and are logged; support sessions see reports only within their scoped read-only role.

## Rollback

Revert code; drop new tables only via a down migration after backup if the phase is abandoned before release. Reports are read-only; wrong attributions are fixed by re-attribution with audit.

## Next

Phase 13 adds SaaS control and settings (incl. `analytics_ai_enabled`); phase 18 builds campaign, notification, report and settings screens; phase 21 load-tests reports.
