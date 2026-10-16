---
title: "Phase 10: Debt & Cash"
status: todo
priority: P1
effort: 6d
dependsOn: [phase-05-catalog-partners]
---

# Phase 10 — Debt & Cash

## Context links

- [database-schema-debt-cash-crm-saas.md](design/database-schema-debt-cash-crm-saas.md) §1 debt, §2 cash
- [database-conventions.md](design/database-conventions.md) §6 lock order (obligations → payments → fund_accounts)
- [architecture.md](design/architecture.md) §3.4 (`debt.AllocationStrategy`, `debt.ObligationFactory`, State `StatementStatus`)
- [ubiquitous-language.md](design/ubiquitous-language.md) §2 debt/cash terms, §5 prefixes PT, PC, DN, BT, DS
- PRD §6.8 (DEBT-01..11, 13), §6.9 (CASH-01, 02, 04, 06), §7.6 allocation rules, OPEN-14

## Overview

Implement the `debt` and `cash` domains that every posting domain uses: obligations (receivable/payable) with schedules, payments PT/PC with purposes and verification state, allocations through append-only settlements, credit notes and refund dues, debt adjustments DN (incl. opening balances and write-offs), explicit offsets BT, payment reversal and re-allocation, credit-limit policy, statements DS with immutable versions, collection notes and payment promises, aging and forecast queries, fund accounts and payment methods with cash entries. This phase runs before procurement and sales so their postings create real obligations and payments.

## Key insights

- Balances are never typed: open = `amount − settled_amount`, where `settled_amount` is a projection of `obligation_settlements` maintained in the same tx and checked by `tally ledger verify` (DEBT-01). No automatic two-way netting.
- Unallocated confirmed payments are advances (ứng trước), never revenue (DEBT-05). Allocation suggestion = `OldestDueFirst` strategy; the owner confirms (`allocation_mode = 'suggested_oldest_due'`); earmarked deposits allocate automatically to their invoice's documents (`auto_earmarked`).
- Bank transfer with `requires_verification` creates `pending_verification` payments that neither allocate nor write cash entries until confirmed (CASH-04).
- Reversal writes negative settlement rows (`reversal_of`) and a mirror cash entry; blocked while a later refund/transfer that used the money is unreversed (DEBT-11).
- There is no Tiền menu in 1.1: PT/PC are created from POS, invoice, debt screen, returns, exchanges, receipts; fund balances appear on dashboard and cash-flow report (CASH-02, RPT-06).
- Reminders: no third-party sending in 1.1 (integrations removed). A reminder produces the message text for the owner to copy/send and logs `reminder_sent` with a dedupe key; email reminders go through `mailer` only when a `consents` row allows `debt_reminder` by email.

## Requirements

| ID | Behaviour |
|---|---|
| DEBT-01 | Receivable/payable ledger by party and document; total = Σ detail; no auto netting |
| DEBT-02 | Opening debt and opening advances with cutoff date, source note, owner confirmation (onboarding wizard, OPEN-14); no revenue or stock value created |
| DEBT-03 | Due dates, installments, aging by due date of unpaid part (oldest installment first); as-of report reproducible by replaying settlements ≤ as-of |
| DEBT-04 | Collect/pay many times, allocate to many documents in the Công nợ screen; no over-allocation of document or payment; remainder = advance |
| DEBT-05 | Customer deposits / supplier advances, refund of advance (`advance_refund`), re-allocation |
| DEBT-06 | `CreditLimitPolicy`: open receivable + confirmed undelivered credit orders − advances earmarked to this order > limit → `credit_limit_exceeded` exception; overdue warning |
| DEBT-07 | Statement DS for period: opening, closing, transactions; versions immutable once sent; confirmed/disputed states |
| DEBT-08 | Collection notes, promises, reminder logging with dedupe, consent-aware email; no public debt info |
| DEBT-09 | DN decrease/increase/write-off/dispute settlement with reason; write-off needs `debt_write_off` exception; audit |
| DEBT-10 | BT offset between receivable and payable of the same party with attachment (biên bản) and owner confirmation; both ledgers keep entries |
| DEBT-11 | Reverse payment restores balances exactly; blocked if money used by an unreversed successor |
| DEBT-13 | Forecast of collections/payments by due dates and promises, separated from confirmed cash |
| CASH-01 | Two seeded funds (Tiền mặt tại cửa hàng, Tài khoản ngân hàng) with opening balance (cash entry `opening`, not revenue) |
| CASH-02 | Payment recorder used by POS, invoice, debt, return, exchange, purchase; posts once (idempotency) |
| CASH-04 | Multi-method split, change, refunds; pending transfer verification queue |

## Architecture

```
debt/
  model.go          Obligation, ObligationSchedule, Payment(Purpose, Status), CreditNote, DebtAdjustment, DebtOffset, Settlement, Statement(+Version), CollectionNote
  factory.go        ObligationFactory (Receivable/Payable from source; schedule builder)
  allocation.go     AllocationStrategy: OldestDueFirst, Manual, Earmarked; Allocator (guards: same party, same direction, ≤ open, ≤ unallocated)
  recorders.go      ReceivableRecorder, PayableRecorder, PaymentRecorder, CreditNoteRecorder (consumer ports implemented here)
  credit_limit.go   CreditLimitPolicy
  postings.go       PostPayment, ConfirmPayment, ReversePayment, PostAllocation, PostDebtAdjustment, PostDebtOffset, PostRefund (PostingCommands)
  statement.go      StatementService (generate version, send, confirm, dispute, supersede); StatementStatus
  collection.go     CollectionService (notes, promises, reminder text builder, dedupe)
  queries.go        PartyLedgerQuery, AgingQuery(as_of), ForecastQuery, AdvancesQuery, PendingVerificationQuery
  lockers.go, ports.go, repo.go, http.go, dto.go, errors.go
cash/
  model.go          FundAccount, PaymentMethod, CashEntry(Category)
  recorder.go       CashEntryWriter (called by debt postings for confirmed payments; maps purpose/source → category)
  seed.go           implements org.FundSeeder (2 funds + 2 methods)
  queries.go        FundBalanceQuery (dashboard), CashFlowQuery (RPT-06 base)
  repo.go, service.go, http.go, dto.go
```

Routes (`/api/v1`): `GET /debts/parties?direction=&overdue=` (summary), `GET /debts/parties/{partyId}` (documents, schedules, advances), `GET /debts/aging?direction=&as_of=`, `GET /debts/forecast?from=&to=`, `POST /payments` (PT/PC, optional allocations), `POST /payments/{id}:confirm|reverse`, `GET /payments/pending-verification`, `POST /allocations:suggest`, `POST /allocations`, `POST /debt-adjustments`, `POST /debt-offsets`, `POST /refunds`, `GET/POST /statements`, `POST /statements/{id}:generate|send|confirm|dispute`, `GET /statements/{id}/versions/{n}`, `GET/POST /parties/{id}/collection-notes`, `POST /parties/{id}/reminders` (returns text, logs once per dedupe key), `GET /funds`, `PATCH /funds/{id}` (opening balance before first entry only), `GET /payment-methods`. Posting routes need `Idempotency-Key`; write-off and offsets need re-auth.

## Related files

| Path | Action |
|---|---|
| `apps/api/internal/debt/*.go` + tests | create |
| `apps/api/internal/cash/*.go` + tests | create |
| `apps/api/internal/org/provisioner.go` | modify — wire fund seeder |
| `apps/api/internal/org/onboarding.go` | modify — `opening_debt`, `opening_cash` steps |
| `apps/api/internal/cli/ledger.go` | modify — verify obligations/settlements/payments/fund balances |
| `apps/api/internal/platform/mailer/templates/debt_reminder.{txt,html}.tmpl` | create |
| `apps/api/api/openapi.yaml`, `apps/api/internal/app/routes.go`, `app/wire.go` | modify |

## Implementation steps

1. Cash: funds, methods, seeder, `CashEntryWriter` with category mapping table (sales, customer_deposit, debt_collection, supplier_payment, supplier_advance, refund, landed_cost, opening, other); fund balance projection under `fund_accounts FOR UPDATE`.
2. Debt model + repos + lockers (obligations asc → payments → fund_accounts).
3. `ObligationFactory` + schedules; recorders exposed as ports for procurement/sales; uniqueness `(source_type, source_id, direction)` surfaces as idempotent no-op.
4. Payments: post with purpose, method → fund, status by method; confirm pending; allocations via `Allocator` + strategies; settlements append-only, projections updated.
5. Credit notes + refund due: allocate credit to open obligations of the same party/direction first, excess → `refund_due` obligation in the opposite direction; refund payment settles it.
6. DN adjustments (opening balance with cutoff; write-off exception; dispute settlement), BT offsets (two settlements, attachment required).
7. Reversal with successor check (refund or re-allocation using the payment's money).
8. `CreditLimitPolicy` with earmark rule; overdue flags.
9. Statements: generate snapshot version, send marks `sent_at` (version frozen), regenerate creates a new version and supersedes; confirm/dispute.
10. Collection notes, promises, reminder text, consent-checked email, dedupe.
11. Queries: party ledger, aging as-of (replay settlements ≤ as_of, installments oldest first), forecast (schedules + promises vs confirmed cash), advances.
12. Verifier extension + tests: allocation guards, oldest-due suggestion, earmark auto allocation, reversal restores balances, offset keeps both ledgers, aging as-of reproducibility, statement immutability, isolation.

## Todo

- [ ] Cash funds/methods/entries + seeder
- [ ] Obligations + factory + schedules + recorders
- [ ] Payments + verification + allocations + settlements
- [ ] Credit notes + refund due + refunds
- [ ] Debt adjustments (opening, write-off) + offsets
- [ ] Payment reversal with successor check
- [ ] Credit limit policy
- [ ] Statements + versions
- [ ] Collection notes + reminders
- [ ] Ledger/aging/forecast queries
- [ ] Verifier + tests + OpenAPI

## Success criteria & verification

- Unit + integration suites green; `tally ledger verify` checks Σ settlements = `settled_amount`, Σ allocations = `allocated_amount`, fund balance = opening + Σ entries.
- Allocating 5 000 000 against obligations 3 000 000 (due 01/10) and 4 000 000 (due 15/10) suggests 3 000 000 + 2 000 000; allocating 6 000 000 to the second → `422 allocation_exceeds_open`.
- Pending transfer: no cash entry, no allocation until confirm; confirm creates both once.
- Reverse a payment whose advance was refunded → `409 payment_has_successor`; after reversing the refund, reversal succeeds and balances return to pre-payment values.
- Aging as of 30/09 is identical before and after settlements posted on 05/10.
- Sent statement version cannot change; regenerate yields version 2 and status of v1 `superseded`.

## Risks

| Risk | Mitigation |
|---|---|
| Hidden cash via adjustments | DN needs reason + audit; reports show adjustments separately from collections |
| Projection drift | Same-tx updates under lock + verifier in CI and ops |
| Credit limit false positives | Earmarked-only advance rule, exception path, clear message listing components |

## Security

- Owner session + re-auth for write-off, offset, payment reversal; reminders contain only the party's own data; no debt info in public URLs; append-only grants on settlements, statement versions, cash entries.

## Rollback

Revert code. Wrong financial entries are corrected by reversal documents, never by updates.

## Next

Phases 08 and 09 call the recorders; phase 12 builds debt/cash reports and notifications; phase 18 builds the Công nợ screens.
