---
title: "Phase 03: Database Schema & Migrations"
status: todo
priority: P1
effort: 6d
dependsOn: [phase-02-api-platform-foundation]
---

# Phase 03 — Database Schema & Migrations

## Context links

- [database-conventions.md](design/database-conventions.md) — the rule (no functions/triggers/DO/extensions/function defaults/generated columns/expression indexes/views), RLS block, roles, append-only list, migration rules
- Schema files (DDL is authoritative, copy verbatim then add RLS blocks):
  [org & partners](design/database-schema-org-partners.md) · [catalog & pricing](design/database-schema-catalog-pricing.md) · [inventory & procurement](design/database-schema-inventory-procurement.md) · [sales & kits](design/database-schema-sales-kits.md) · [debt, cash, CRM, SaaS](design/database-schema-debt-cash-crm-saas.md)
- Brainstorm §3, §4.2, §5, §5.1: `plans/reports/brainstorm-261016-1528-catalog-database-design.md`
- PRD §10 data model, NFR-01, NFR-02, NFR-13, NFR-14, NFR-15

## Overview

Translate every DDL block of the five schema files into golang-migrate up/down pairs in one version sequence, enforce the no-procedural-SQL rule with a lint test, assert RLS on every tenant table and append-only privileges, and prove reversibility (up → down → up) and tenant isolation in CI. Business behaviour is not implemented here; each domain phase builds repositories on these tables.

## Key insights

- Full schema up front avoids FK churn between later phases (e.g. `price_lists.campaign_id` FK is added in `000023` after `campaigns` exists, exactly as the schema file says).
- The lint test is the guard that makes the user constraint durable: it fails the build on `CREATE FUNCTION|PROCEDURE|TRIGGER|EVENT TRIGGER|RULE|AGGREGATE|EXTENSION`, `DO $$`, `LANGUAGE`, `GENERATED`, `DEFAULT now()`/`gen_random_uuid()`/`nextval`/`serial`/`identity`, `CREATE VIEW`/`MATERIALIZED VIEW`, expression indexes (`ON t ((`/`lower(`/`upper(`/`coalesce(` inside index column lists) and `current_setting(` outside `CREATE POLICY`.
- A second guard test reads `pg_catalog` after migrating: zero rows in `pg_proc` for schema `tally`, zero rows in `pg_trigger` (non-internal), zero in `pg_extension` beyond `plpgsql`, and every table with a `tenant_id` column has `relrowsecurity` and `relforcerowsecurity` plus a `tenant_isolation` policy.
- `CHECK (prefix ~ '^[A-Z]{2,3}$')` and `CHECK (base_qty = qty * unit_factor_snap)` are operators, allowed.

## Requirements

| ID | Behaviour delivered here |
|---|---|
| NFR-01 | RLS + FORCE RLS + policy on every tenant table; composite tenant FKs; isolation test per table |
| NFR-02 | Append-only ledgers via REVOKE; unique idempotency keys; one-reversal partial uniques |
| NFR-13 | Every tenant index leads with `tenant_id`; date/time indexes on ledgers |
| NFR-14 | Reversible migrations, CI up/down/up, backup guard (phase 02) |
| NFR-15 | `numeric` scales per conventions; `version` columns; snapshot columns on lines |
| CAT-01..11, PRC-*, PUR-*, INV-*, SAL-*, KIT-*, DEBT-*, CASH-*, SEA-*, CRM-*, NTF-01, RPT-09, SAAS-*, SEC-03..07, DATA-05 | Tables and constraints as specified in the schema files |

## Architecture — migration sequence

| Version | Name | Source section |
|---|---|---|
| 000001 | `platform_identity` | org-partners §1 (tenants incl. immutable `slug` + format CHECK, retired_tenant_slugs, users, sessions, auth_tokens, mfa_*, login_attempts, platform_admins, platform_admin_sessions) |
| 000002 | `org_core` | org-partners §2 (stores, warehouses, tenant_settings, doc_sequences) |
| 000003 | `org_control` | org-partners §3 (idempotency_keys, audit_events, period_locks, exception_confirmations, attachments, outbox_events, reasons) |
| 000004 | `partners` | org-partners §4 |
| 000005 | `catalog_lookups` | catalog-pricing (colors, units, tax_rates) |
| 000006 | `catalog_attributes` | attributes, options, sets, items, categories |
| 000007 | `catalog_products_skus` | products, variant axes, skus, variant values, sku_units, barcodes, images (`sku_search_tokens` is **not** created here: catalog-pricing §3.1 adds it as a later migration only if the phase 21 search gate fails) |
| 000008 | `catalog_mix_groups` | mix_groups, mix_group_items |
| 000009 | `pricing` | price_lists, items, price_changes, promotions, promotion_items, promotion_usages |
| 000010 | `inventory_ledger` | lots, stock_moves, lot_balances, sku_stock |
| 000011 | `inventory_reservations` | reservations |
| 000012 | `inventory_counts_adjustments` | stock_counts(+lines), stock_adjustments(+lines) |
| 000013 | `procurement_orders_receipts` | purchase_orders(+lines), receipts(+lines) |
| 000014 | `procurement_documents_returns` | landed_cost_allocations(+lines), supplier_documents(+receipts), purchase_returns(+lines), purchase_price_adjustments(+lines), cogs_adjustments |
| 000015 | `sales_quotes_invoices` | quotes(+lines), invoices(+lines), invoice_versions |
| 000016 | `sales_stock_outs_documents` | stock_outs(+lines), sales_documents(+lines) |
| 000017 | `sales_pos` | shifts, pos_carts(+lines), pos_sync_exceptions |
| 000018 | `sales_returns_exchanges` | sales_returns(+lines), exchanges |
| 000019 | `kits` | kit_recipes(+items, mix items), assembly_orders(+items), assembly_postings(+items), disassemblies(+lines), lot_genealogy |
| 000020 | `debt_core` | obligations, schedules, payments, credit_notes, debt_adjustments, debt_offsets, obligation_settlements |
| 000021 | `debt_statements` | statements, statement_versions, collection_notes |
| 000022 | `cash` | fund_accounts, payment_methods, payments FKs, cash_entries |
| 000023 | `campaign_crm` | campaigns (+ ALTER price_lists/promotions FK), attributions, checklist, customer_activities, care tasks, consents, loyalty |
| 000024 | `notifications_reporting` | notifications, saved_reports, export_jobs, cost_recalculation_runs, onboarding_steps, analytics_queries |
| 000025 | `saas_platform` | plans (+ global seed rows), subscriptions, entitlements, saas_invoices, saas_payment_events, support_*, feature_flags(+changes), tenant_usage_daily, account_closure_requests, product_feedback, incidents, partners, tenant_referrals |
| 000026 | `privileges_assertions` | idempotent `GRANT`/`REVOKE` re-assertion for all tables (append-only list), `GRANT SELECT` to `tally_readonly` |

Forward references: when a table needs an FK to a table created later (e.g. `payments.earmarked_invoice_id` is fine because invoices exist at 000015; `sales_document_lines.campaign_id` → campaigns), the FK is added by `ALTER TABLE … ADD CONSTRAINT` in the later migration and dropped in its down file.

Every migration file starts with `SET search_path = tally;` and contains, per tenant table, the RLS block from conventions §4. Platform tables (conventions §4 list) get no RLS but `REVOKE ALL … FROM tally_readonly`.

## Related files

| Path | Action |
|---|---|
| `apps/api/migrations/embed.go` | create — `//go:embed *.sql` `var FS embed.FS` |
| `apps/api/migrations/0000NN_<name>.up.sql` / `.down.sql` (26 pairs) | create |
| `apps/api/migrations/migrations_lint_test.go` | create — regex lint over `*.up.sql` and `*.down.sql` |
| `apps/api/migrations/catalog_guard_integration_test.go` | create — pg_catalog assertions (no proc/trigger/extension, RLS on every `tenant_id` table, append-only privileges via `has_table_privilege`) |
| `apps/api/migrations/reversibility_integration_test.go` | create — up, down -all, up |
| `apps/api/migrations/isolation_integration_test.go` | create — for every tenant table: insert a row for tenant A as owner role with RLS bypass via `SET app.tenant_id`, then query as `tally_app` with tenant B → 0 rows; insert with mismatched tenant → policy violation |
| `apps/api/internal/cli/migrate.go` | modify — `up`, `down N`, `version`, `force` via `migrations.FS` |
| `apps/api/internal/testkit/postgres.go` | modify — run init roles SQL then migrations as `tally_owner` |
| `docs/database.md` | create — short index linking the plan design files and listing tables per domain (generated table list from `information_schema` by `make db-docs`) |

## Implementation steps

1. Write `migrations_lint_test.go` first (red), with fixture strings proving each forbidden pattern is caught and each allowed pattern (policy `current_setting`, regex CHECK, partial unique index, `NULLS NOT DISTINCT`, `jsonb_path_ops` GIN on a column) passes.
2. Write migrations 000001–000004; run lint + reversibility; write isolation test harness that discovers tenant tables from `information_schema.columns WHERE column_name = 'tenant_id'`.
3. Write 000005–000009 (catalog, pricing); confirm `UNIQUE NULLS NOT DISTINCT` and partial uniques (`ux_price_lists__base_kind`, default sku units) exist.
4. Write 000010–000014 (inventory, procurement) incl. `REVOKE UPDATE, DELETE ON stock_moves, cogs_adjustments …`, `CHECK (base_qty = qty * unit_factor_snap)` on every snapshot line, `ux_stock_moves__one_reversal`.
5. Write 000015–000019 (sales, kits) incl. `invoice_versions`, `lot_genealogy` revokes, `ux_shifts__one_open`.
6. Write 000020–000024 (debt, cash, campaign/CRM, notifications/reporting) incl. deferred FKs to campaigns.
7. Write 000025 (SaaS platform) with global plan seed rows using constant UUIDs and literal timestamps (no `now()`); 000026 privileges re-assertion.
8. Add `catalog_guard_integration_test.go`; fix every failure by editing DDL, never by weakening the guard.
9. Wire CI job `migrations`: lint, guard, reversibility, isolation against `postgres:16-alpine`.
10. `make db-docs` generates the table index in `docs/database.md`; commit the generated file.

## Todo

- [ ] Lint test with positive/negative fixtures
- [ ] 000001–000004 platform/org/control/partners
- [ ] 000005–000009 catalog/pricing
- [ ] 000010–000014 inventory/procurement
- [ ] 000015–000019 sales/kits
- [ ] 000020–000024 debt/cash/campaign/CRM/notifications/reporting
- [ ] 000025 SaaS platform + plans seed (literal values)
- [ ] 000026 privileges assertions
- [ ] pg_catalog guard test
- [ ] Reversibility test
- [ ] Per-table isolation test (auto-discovered)
- [ ] `tally migrate` subcommands on embedded FS
- [ ] `docs/database.md` + `make db-docs`

## Success criteria & verification

- `go test ./migrations -run TestMigrationsLint` passes; a deliberately added `CREATE FUNCTION` in a temp fixture fails it.
- `go test -tags integration ./migrations/...` passes: 0 functions/triggers/extensions; every tenant table RLS-forced with policy; append-only tables deny UPDATE/DELETE to `tally_app` (`has_table_privilege` false); isolation test covers 100 % of tenant tables (count printed).
- `tally migrate up && tally migrate down -all && tally migrate up` succeeds locally against `make db-up`.
- `make db-backup` executed before the first migrate on any non-empty database (runbook step).

## Risks

| Risk | Mitigation |
|---|---|
| DDL drift between design files and migrations | Migrations are copied from design files; any change updates the design file in the same commit |
| `NULLS NOT DISTINCT` partial index syntax differences | PG16 pinned; integration test creates duplicates to assert rejection |
| RLS policy blocks the migrator | Migrations run as `tally_owner` (table owner; FORCE RLS applies to owner only for DML, DDL unaffected); seed rows for platform tables only |
| Down migrations missing deferred FK drops | Reversibility test in CI |

## Security

- `tally_app` has no DDL; `tally_readonly` cannot read platform auth tables. RLS default-deny when `app.tenant_id` unset. No PII in seed data.

## Rollback

Development: `tally migrate down`. Production: never run `down`; restore from the dump taken by `make prod-migrate` (`make db-restore FILE=…`), then redeploy the previous image.

## Next

Phases 04–13 implement repositories and services on these tables; each adds integration tests for its invariants and extends `tally ledger verify`.
