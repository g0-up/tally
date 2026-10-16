---
title: "Phase 13: SaaS Control, Support & Settings"
status: todo
priority: P1
effort: 7d
dependsOn: [phase-12-campaign-crm-notifications-reporting]
---

# Phase 13 — SaaS Control, Support & Settings

## Context links

- [database-schema-debt-cash-crm-saas.md](design/database-schema-debt-cash-crm-saas.md) §6 SaaS & support (platform tables, no RLS), §5 `export_jobs`, `onboarding_steps`
- [database-schema-org-partners.md](design/database-schema-org-partners.md) `platform_admins`, `platform_admin_sessions`, `auth_tokens`, `tenant_settings`, `audit_events`
- [database-conventions.md](design/database-conventions.md) §4 RLS and platform tables, role `tally_readonly`
- [architecture.md](design/architecture.md) §2 processes (`/api/admin/v1`), State `saas.SubscriptionLifecycle`, `EntitlementGuard`, `WithReadOnlyTenantTx`; §7 tenant hosts (admin host, slug retirement)
- PRD §6.15 (SAAS-01..10), SEC-06/07, §14 packaging/onboarding/support, OPEN-09, OPEN-11, OPEN-12, OPEN-15

## Overview

Implement the operator side of the SaaS and the remaining owner settings: plans, subscriptions with a State-pattern lifecycle (trial → active → grace → read-only → closed), server-side entitlements and limits, service billing with idempotent payment callbacks, support tickets and consented read-only support sessions, feature flags with history, daily usage, incidents and a public status endpoint, partners/referrals, product feedback, account closure with a full data export first (OPEN-15), per-tenant restore tooling, the onboarding checklist service (SAAS-04), and the platform admin API with mandatory TOTP.

## Key insights

- Platform tables have no RLS (conventions §4); they are reached only through `db.WithPlatformTx` from `saas` and `admin` packages. Tenant routes read their own subscription through a repository that always filters by the session tenant.
- `SubscriptionLifecycle` (State) owns transitions and their side effects: each state answers `CanPost()`, `CanExport()`, `CanLogin()`. `read_only` blocks every posting command via `EntitlementGuard` in `posting.Coordinator`, keeps reads and exports available, never deletes data or hides debt (SAAS-03). Durations (trial days, grace days, read-only days) come from plan `limits` and are published on the subscription screen.
- Entitlements are resolved server-side (`entitlement.Reader`: active rows by `valid_from/valid_to`, plan modules as default, tenant feature flag overrides last). Modules: `pos`, `kits`, `campaign`, `crm`, `reports_advanced`, `ai_qa`; limits: `skus`, `storage_mb`. UI hiding is cosmetic only (SEC-01).
- Prices are not decided (SAAS-01, OPEN-09): `plans.price` is admin-editable; trials never charge automatically.
- No payment provider is chosen; 1.1 ships a `PaymentProvider` port with a manual bank-transfer adapter (admin marks paid with reference) and a generic signed-webhook adapter. Callbacks are deduplicated by `UNIQUE (provider, event_id)`; applying a paid invoice extends the period exactly once (`saas_invoices.status` transition `open → paid` under row lock).
- Support sessions (SEC-07): the owner grants consent (re-auth, `auth_tokens` purpose `support_consent`), scope `read_only`, chosen modules, default 2 hours; admin reads go through `WithReadOnlyTenantTx` (`SET LOCAL ROLE tally_readonly; SET TRANSACTION READ ONLY`), only `GET` handlers of granted modules, every request audited into the tenant's `audit_events` (visible in Thiết lập → Nhật ký). Owner can revoke at any time; no silent impersonation, no password requests (ticket form warns, messages are scanned for password-like phrases and the owner is warned before sending).
- Account closure (SAAS-08): request → verify (re-auth + emailed one-time token) → `waiting` until `waiting_until` with a `full_data_export` job created automatically → `completed` sets the tenant closed. Physical deletion is an operator CLI step after the retention policy review (OPEN-11) and never runs while `legal_hold`.
- Full data export (OPEN-15 default): a worker job writes one CSV per tenant table (RLS-scoped, UTF-8, documented column dictionary) into a zip attachment with an expiring signed link; allowed in `read_only`.
- Onboarding (SAAS-04): `org.OnboardingService` exposes the seven steps; `opening_stock`, `opening_debt`, `opening_cash` complete from postings in phases 07/10; `first_sale` completes on the first HD; `confirm_opening_balances` requires re-auth, shows totals (stock value by color, receivables, payables, advances, fund balances at cutoff) and then sets a period lock up to the cutoff date. Help videos are static links in the web config.
- Product analytics (SAAS-10): `tenant_settings.product_analytics_enabled` defaults false; 1.1 collects no client tracking events. Feedback stores kind/score/body/page path only, never document content.

## Requirements

| ID | Behaviour |
|---|---|
| SAAS-01 | Plans (year-round, seasonal, add-on, trial), modules and limits enforced server-side; trial does not charge |
| SAAS-02 | Subscribe, renew, upgrade/downgrade with proration preview and explicit terms; invoices; duplicate callbacks never extend or charge twice |
| SAAS-03 | Grace → read-only → closed with published durations; read-only allows reads and exports, blocks postings; no data deletion |
| SAAS-04 | Onboarding checklist with progress, opening-balance confirmation step |
| SAAS-05 | Support tickets with status, messages, attachments, SLA by plan; never ask for passwords; history kept |
| SAAS-06 | Admin: tenant metadata, usage, incidents, billing, feature flags with history; no default access to sales data |
| SAAS-07 | Health, backup/restore per tenant with reconciliation, incident/status page; restore never overwrites another tenant |
| SAAS-08 | Ownership (via phase 04 transfer), closure request/verification/waiting/export, legal hold |
| SAAS-09 | Partners/resellers with referral codes; no data access by default; subscription commission separate from sales |
| SAAS-10 | Feedback, analytics consent; minimal collection, no document content or PII in analytics |
| SEC-06 | Platform admins: TOTP mandatory, separate session cookie and route group served only on `admin.DOMAIN`, session revoke |
| SEC-07 | Consented, scoped, expiring, revocable, read-only support sessions with audit |

## Architecture

```
saas/
  model.go           Plan, Subscription, Entitlement, SaasInvoice, PaymentEvent
  lifecycle.go       SubscriptionLifecycle (State: Trialing, Active, Grace, ReadOnly, Closed) + transition table
  entitlement.go     Reader (implements middleware.EntitlementReader and posting.EntitlementGuard), LimitChecker
  billing.go         BillingService (invoice issue, proration preview, apply payment once)
  provider/          PaymentProvider port; manual_transfer.go; signed_webhook.go (HMAC verify)
  lifecycle_job.go   daily worker job: trial/grace/read-only expiry, notifications `subscription_status`
  usage_job.go       daily tenant_usage_daily (active SKUs, posted documents, storage bytes)
  closure.go         ClosureService (request, verify, wait, complete, cancel, legal hold)
  data_export.go     FullDataExporter (per-table CSV + dictionary → zip attachment)
  repo.go, http.go, dto.go, errors.go
support/
  model.go           Ticket, TicketMessage, SupportSession
  ticket.go          TicketService (SLA from plan, password-phrase warning)
  session.go         SupportSessionService (grant with consent token, revoke, expire), SupportAccess middleware
  repo.go, http.go, dto.go
featureflag/
  reader.go          Reader (tenant override > global, 30 s cache), Writer (append feature_flag_changes)
admin/
  auth.go            AdminAuthService (argon2id, TOTP mandatory, separate host-only cookie `__Host-tally_admin_session` (dev: `tally_admin_session`), accepted only on `admin.DOMAIN`)
  tenants.go         TenantDirectoryQuery (metadata, subscription, usage; never sales tables)
  incidents.go       IncidentService + public StatusQuery
  partners.go        PartnerService, ReferralService (code captured at sign-up)
  feedback.go        FeedbackQuery
  http.go            `/api/admin/v1` routes
org/
  onboarding.go      OnboardingService (steps, progress, completion listeners, confirm opening balances)
cli/
  tenant.go          `tally tenant restore-check`, `tally tenant copy`, `tally tenant purge`
```

Tenant routes (`/api/v1`): `GET /plans`, `GET /subscription`, `GET /entitlements`, `POST /subscription:preview-change`, `POST /subscription:change`, `GET /saas-invoices`, `GET /saas-invoices/{id}`, `POST /saas-invoices/{id}:pay`; `GET/POST /support-tickets`, `GET /support-tickets/{id}`, `POST /support-tickets/{id}/messages`; `GET/POST /support-sessions`, `POST /support-sessions/{id}:revoke`; `POST /data-exports`; `GET/POST /account-closure`, `POST /account-closure:verify|cancel`; `POST /feedback`; `GET /onboarding`, `POST /onboarding/steps/{stepKey}:complete|skip`; public `GET /status`, `POST /webhooks/saas-payments/{provider}`.

Admin routes (`/api/admin/v1`, admin host only — any other host gets `404`): `POST /auth/login`, `POST /auth/totp`, `POST /auth/logout`, `GET /tenants`, `GET /tenants/{id}`, `POST /tenants/{id}/subscription:change|extend|set-status`, `GET/POST /plans`, `PATCH /plans/{id}`, `GET/POST /saas-invoices`, `POST /saas-invoices/{id}:mark-paid|void`, `GET /support-tickets`, `PATCH /support-tickets/{id}`, `POST /support-tickets/{id}/messages`, `GET /tenants/{id}/support-view/{module}/…` (read-only, active session only), `GET/PUT /feature-flags`, `GET /feature-flags/{key}/changes`, `GET/POST /incidents`, `PATCH /incidents/{id}`, `GET/POST /partners`, `GET /partners/{id}/referrals`, `GET /feedback`, `GET /usage?from=&to=`.

## Related files

| Path | Action |
|---|---|
| `apps/api/internal/saas/**/*.go` + tests | create |
| `apps/api/internal/support/*.go` + tests | create |
| `apps/api/internal/featureflag/*.go` + tests | create |
| `apps/api/internal/admin/*.go` + tests | create |
| `apps/api/internal/org/onboarding.go` | modify — service, listeners, confirmation with period lock |
| `apps/api/internal/org/provisioner.go` | modify — trial subscription + entitlements on tenant creation, referral code capture |
| `apps/api/internal/platform/middleware/{entitlement.go,support_readonly.go}` | modify — wire real readers |
| `apps/api/internal/posting/coordinator.go` | modify — `EntitlementGuard` uses `SubscriptionLifecycle.CanPost` |
| `apps/api/internal/catalog/service.go`, `org/service.go` (attachments) | modify — `LimitChecker` for `skus`, `storage_mb` |
| `apps/api/internal/cli/tenant.go`, `cli/admin.go` | create/modify — restore tooling, `tally admin create` (TOTP enrolment printed once as otpauth URI to the operator terminal) |
| `apps/api/internal/worker/schedule.go` | modify — lifecycle, usage, closure, data export jobs |
| `apps/api/api/openapi.yaml`, `apps/api/api/openapi-admin.yaml`, `app/routes.go`, `app/wire.go` | modify/create |
| `docs/operations/tenant-restore.md` | create — runbook (restore-check, copy, reconciliation) |

## Implementation steps

1. Plans come from the global seed rows in migration `000025_saas_platform` (phase 03); `saas` model + repos with `WithPlatformTx`; provisioner creates a `trialing` subscription and entitlement rows.
2. `SubscriptionLifecycle` states and transition table; daily lifecycle job (per tenant timezone) emits `subscription_status` notifications before each transition.
3. Entitlement reader + limit checker; wire middleware (module gate → `403 module_not_in_plan`), coordinator guard (`read_only` → `403 subscription_read_only`), SKU and storage limits (`409 plan_limit_reached`).
4. Billing: invoice number `SV-{yyyymmdd}-{6 chars of the Crockford base32 id}` (platform table, unique constraint, retry on collision), proration preview (day-based, terms text returned), payment provider port, manual transfer adapter, signed webhook adapter; idempotent apply (`saas_payment_events` insert-first, invoice row lock, single period extension).
5. Support tickets (SLA due from plan, password-phrase detector returning a warning flag the web shows before sending) and messages with attachments (phase 04 attachment service, owner type `support_ticket`).
6. Support sessions: grant (re-auth, consent token, modules, expiry ≤ 8 h, default 2 h), revoke, expiry sweep; `SupportAccess` middleware on `/api/admin/v1/tenants/{id}/support-view/*` mounting the tenant `GET` handlers of granted modules inside `WithReadOnlyTenantTx`; audit each request to the tenant's audit log with admin id.
7. Feature flags reader/writer with change history; admin auth (TOTP mandatory, rate limit, separate cookie, session revoke); tenant directory query that selects only `tenants`, `subscriptions`, `tenant_usage_daily`, `entitlements`, and shows each tenant's slug and store link (`urls.Builder.Tenant`).
8. Usage job, incidents + public status endpoint, partners/referrals (referral code on sign-up, commission percent on subscription invoices only), feedback endpoint and admin list.
9. Closure flow + full data exporter (table list generated from migrations metadata in Go, each `SELECT` under tenant tx, CSV + `README.csv` dictionary, zip upload, expiring link, `export_ready` notification; mail links built with `urls.Builder.Tenant(slug, …)`).
10. Onboarding service: step listing with progress, completion listeners (outbox `stock.opening_posted`, `debt.opening_posted`, `cash.opening_set`, `sale.posted` first HD), skip rules (only `catalog_setup` and `first_sale` skippable), confirmation summary + period lock to cutoff.
11. Restore tooling: `restore-check` restores the latest backup into a scratch database container and runs `tally ledger verify --tenant`; `copy` moves one tenant's rows from the scratch DSN into production while the tenant is in maintenance (every statement scoped by `tenant_id`, RLS on, run as `tally_app`); `purge` deletes a closed tenant's rows with the migration role after backup and only without legal hold, inserting the slug into `retired_tenant_slugs` in the same transaction that deletes the `tenants` row (the slug is never issued again) and evicting it from the host cache. Runbook in `docs/operations/tenant-restore.md`.
12. Tests: lifecycle table, read-only blocks posting but allows export, duplicate webhook does not extend twice, limit enforcement, support session expiry/revoke/audit, support write attempt rejected by the database role, admin directory never reads sales tables (query assertion), closure state machine, purge retires the slug and sign-up then rejects it as `taken`, admin API on a tenant host → 404, onboarding confirmation locks the cutoff period, tenant copy never touches another tenant.

## Todo

- [ ] Plans seed + subscription model + provisioning
- [ ] SubscriptionLifecycle + daily lifecycle job
- [ ] Entitlements, limits, posting guard
- [ ] Billing, proration preview, provider port, idempotent webhook
- [ ] Support tickets + messages
- [ ] Support sessions + read-only support view + audit
- [ ] Feature flags + admin auth + tenant directory
- [ ] Usage job, incidents/status, partners/referrals, feedback
- [ ] Account closure + full data export
- [ ] Onboarding service + opening balance confirmation
- [ ] Tenant restore/copy/purge CLI + runbook
- [ ] Tests + OpenAPI (tenant + admin)

## Success criteria & verification

- Unit + integration suites green; `tally ledger verify` unaffected.
- A tenant moved to `read_only` gets `403 subscription_read_only` on `POST /pos/checkout` and can still run `POST /data-exports` and every `GET` report.
- Posting the same webhook event twice extends `current_period_end` once and records one `saas_payment_events` row.
- Creating SKU number limit+1 → `409 plan_limit_reached`.
- With an active support session, admin `GET` on a granted module returns data; any non-GET or an expired/revoked session → `403`; an attempted write through the support transaction fails with a PostgreSQL read-only error; each access appears in the tenant audit log.
- Admin tenant list response contains no product, party, document or amount fields (contract test).
- Closure: request → verify → waiting creates a `full_data_export` job; the zip contains one CSV per tenant table and opens in a spreadsheet with correct Vietnamese text.
- `confirm_opening_balances` rejects without re-auth; after confirmation a back-dated posting before the cutoff → `409 period_locked`.
- `tally tenant restore-check` on the latest backup reports ledger verify clean for a sample tenant.

## Risks

| Risk | Mitigation |
|---|---|
| Support access misuse | Consent token, expiry, read-only DB role, module scope, owner-visible audit, revoke |
| Billing double extension | Insert-first event dedupe + invoice row lock + state transition check |
| Wrong tenant on restore | Scratch DB, tenant-scoped statements under RLS, maintenance flag, reconciliation report |
| Retention/legal uncertainty (OPEN-11) | Purge only by operator CLI after review; legal hold flag blocks it |
| Pricing undecided (OPEN-09) | Admin-editable plans; no hard-coded prices |

## Security

- Platform admin: TOTP mandatory, own host `admin.DOMAIN`, separate host-only cookie and CSRF token, rate limits, IP logged, no access to tenant data outside support sessions; webhook signatures verified with constant-time compare; provider secrets only from env/secret store; tickets never request passwords; exports and closures need re-auth; audit for every admin and support action.

## Rollback

Revert code; schema stays (owned by phase 03). Subscriptions are never deleted on rollback; disable lifecycle jobs with config if a transition bug appears, then fix forward.

## Next

Phase 18 builds the settings, subscription, onboarding and admin screens; phase 21 covers the restore drill and security review.
