# Database Conventions — PostgreSQL 16, no procedural code

Inherits brainstorm §3 (`plans/reports/brainstorm-261016-1528-catalog-database-design.md`). This file adds the user constraint **"no triggers and no functions at the database layer"** and resolves every place where the brainstorm sketch relied on one. Schema files: [org & partners](database-schema-org-partners.md), [catalog & pricing](database-schema-catalog-pricing.md), [inventory & procurement](database-schema-inventory-procurement.md), [sales & kits](database-schema-sales-kits.md), [debt, cash, CRM, SaaS](database-schema-debt-cash-crm-saas.md).

## 1. The rule

The database stores data and enforces structure. All behaviour lives in Go.

| Forbidden in migrations | Allowed |
|---|---|
| `CREATE FUNCTION`, `CREATE PROCEDURE`, `CREATE TRIGGER`, `CREATE EVENT TRIGGER`, `CREATE RULE`, `CREATE AGGREGATE` | Tables, columns, `PRIMARY KEY`, `FOREIGN KEY`, `UNIQUE`, `UNIQUE NULLS NOT DISTINCT`, partial unique index |
| `DO $$ … $$` blocks, any `LANGUAGE plpgsql/sql` | `CHECK` using only comparison, arithmetic, `IN`, `IS NULL`, `AND/OR/NOT` |
| `CREATE EXTENSION` (pg_trgm, unaccent, pgcrypto … install functions) | B-tree and GIN indexes on plain columns, including `jsonb_path_ops` on a jsonb column |
| Column `DEFAULT` that calls a function (`now()`, `gen_random_uuid()`, `nextval` via `serial`/`identity`) | Constant defaults (`DEFAULT 0`, `DEFAULT false`, `DEFAULT 'draft'`, `DEFAULT '{}'`) |
| `GENERATED ALWAYS AS (…) STORED` columns | `GRANT` / `REVOKE`, roles, `ALTER TABLE … ENABLE/FORCE ROW LEVEL SECURITY` |
| Expression indexes (`lower(name)`) | `CREATE POLICY … USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)` — the one sanctioned built-in call, see §4 |
| Views and materialized views with logic | Reads, writes and `SELECT … FOR UPDATE` issued by Go |

Enforcement: `apps/api/migrations/migrations_lint_test.go` scans every `*.up.sql` and fails on the forbidden patterns (case-insensitive regexes for each row of the left column; `current_setting(` is allowed only inside a `CREATE POLICY` statement). The test runs in `make test` and CI.

## 2. Where the brainstorm logic moves

| Brainstorm sketch | Replacement (Go-owned) |
|---|---|
| `DEFAULT now()` on `created_at`/`updated_at` | `platform/clock.Clock` sets both on insert/update in the repository; tests inject a fixed clock |
| `id uuid` generated in Go (UUIDv7) | Unchanged: `platform/ids.New()`; no DB default |
| `UNIQUE (tenant_id, lower(name))` expression index | Column `name_key text NOT NULL` = `textnorm.Key(name)` (trim, collapse spaces, lowercase, strip Vietnamese diacritics, `đ→d`); `UNIQUE (tenant_id, name_key)` |
| `search_norm` + `gin_trgm_ops` (pg_trgm) | `search_norm` built by `catalog.SearchIndexer`; query `search_norm LIKE $1` with `%term%` built in Go, index `(tenant_id, status)`; exact barcode/code by unique b-tree. Gate: p95 ≤ 1 s with 20 000 SKU (AT/NFR-03). Fallback if the gate fails: Go-maintained `sku_search_tokens(tenant_id, token, sku_id)` with `text_pattern_ops` prefix index — still no extension |
| `base_qty` generated `= qty * unit_factor_snap` | Plain column; Go computes `qty × factor` and rejects results with more than 4 decimals; `CHECK (base_qty = qty * unit_factor_snap)` guards it (operators only) |
| `CHECK (jsonb_typeof(attributes) = 'object')` | `catalog.AttributeValidator` (Go) rejects non-object payloads; DB keeps `DEFAULT '{}'` and `NOT NULL` |
| `updated_at` / `version` triggers | Repository `UPDATE … SET version = version + 1, updated_at = $now WHERE id = $id AND version = $expected`; zero rows → `apperr.Conflict` |
| Projections `lot_balances`, `sku_stock`, obligation/fund balances | Written by `posting` in the same transaction as the source rows; rebuilt by `tally ledger rebuild`; checked by `tally ledger verify` |
| Document numbering | `docseq.Issuer`: `INSERT … ON CONFLICT DO NOTHING` then `UPDATE doc_sequences SET next_value = next_value + 1 WHERE … RETURNING next_value - 1` inside the posting transaction (row lock gives gap-free numbers per tenant/prefix) |
| Append-only enforcement | `REVOKE UPDATE, DELETE` on append-only tables from `tally_app` (§5); Go never issues those statements |
| Quote auto-expiry | Derived in Go (`QuoteStatus(q, today)`), never stored by a scheduled DB job |

## 3. Table conventions (inherited, restated)

- `id uuid PRIMARY KEY` (UUIDv7 from Go) and `tenant_id uuid NOT NULL` on every tenant table; `UNIQUE (tenant_id, id)` on every table referenced by others.
- Every foreign key between tenant tables is composite: `FOREIGN KEY (tenant_id, x_id) REFERENCES x (tenant_id, id)`. No FK may join two tenants.
- Money `numeric(19,4)`, quantity `numeric(18,4)`, factor `numeric(18,6)`, percent `numeric(7,4)`. Go uses `shopspring/decimal` through `platform/money` (`Money`, `Qty`, `Factor`) value objects; VND rounds to 0 decimals only at display/settlement boundaries defined in `money.RoundVND`.
- `created_at timestamptz NOT NULL`, `updated_at timestamptz NOT NULL` (mutable tables), `business_date date NOT NULL` on documents (tenant timezone from `tenants.timezone`).
- `version int NOT NULL DEFAULT 1` on mutable aggregates.
- Posted documents: `doc_no text NOT NULL`, `idempotency_key text NOT NULL`, `posted_at timestamptz`, `posted_by uuid`, `reversal_of uuid`, `UNIQUE (tenant_id, doc_no)`, `UNIQUE (tenant_id, idempotency_key)`.
- Status: `text NOT NULL` + `CHECK (status IN (…))`; never PostgreSQL enums. Vocabulary in [ubiquitous-language.md](ubiquitous-language.md) §3.
- Deactivate, never delete, anything referenced by a document (`active bool` for lookups, `status` for aggregates).
- Snapshot columns on every goods line (brainstorm §5): `sku_id, sku_code_snap, name_snap, variant_label_snap, unit_id, unit_name_snap, unit_factor_snap, qty, base_qty, color_id, unit_price, discount_amount, spec_text, price_source jsonb, mix_key, mix_group_id`. Written once from Go; never recomputed from catalog.
- Naming: tables plural snake_case. Every constraint and index carries an explicit name: `pk_<table>`, `fk_<table>__<cols>`, `ux_<table>__<cols>` (unique constraint or unique index), `ck_<table>__<cols>`, `ix_<table>__<cols>`. The suffix is the constrained columns joined by `_` with a leading `tenant_id` dropped (`fk_stock_moves__sku_id`, `ux_skus__code`); a check names the columns it reads (`ck_stock_moves__qty`). Where the column list is long or ambiguous the migration uses a short semantic suffix instead (`ux_lots__identity`, `ck_stock_count_lines__variance_formula`, `ck_<table>__base_qty_factor` for `base_qty = qty * unit_factor_snap`). Names stay within PostgreSQL's 63-byte identifier limit and are unique across the schema, so a `23505`/`23503`/`23514` error maps back to one rule in Go (`platform/db` error mapping). The schema sketches in these design files mostly leave constraints unnamed for readability; the migrations are the source of truth for names. Enforced by the migration lint test (`migrations_lint_test.go`) and by catalog checks against a migrated database (`catalog_guard_integration_test.go`).
- Every index on a tenant table leads with `tenant_id`. Exceptions, each listed with its reason in the lint test: primary keys on `id` (UUIDv7 is globally unique), `ix_products__attributes` (GIN on `jsonb` can carry the `uuid` tenant_id only through the `btree_gin` extension, which §1 forbids; queries still filter `tenant_id` through RLS) and `ux_attachments__storage_key` (the object-store key is unique across the whole bucket). Platform tables (§4) are not tenant tables and follow their own access paths.

## 4. Tenant isolation (RLS)

Every tenant table gets, in the same migration that creates it:

```sql
ALTER TABLE stock_moves ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_moves FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_moves
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
```

- `current_setting` is a PostgreSQL built-in, not a user-defined function; it is the only built-in call allowed in DDL and only inside policies. `missing_ok = true` plus `NULLIF(…, '')` turn an unset tenant (never set, or reset to `''` after a previous transaction on a pooled connection) into `NULL`, so a forgotten `WithTenantTx` matches zero rows and can never read another tenant.
- `platform/db.WithTenantTx(ctx, tenantID, fn)` opens a transaction, runs `SELECT set_config('app.tenant_id', $1, true)` (transaction-local), then calls `fn(tx)`. Repositories accept `db.Querier` and never open transactions themselves.
- Platform tables (`tenants`, `retired_tenant_slugs`, `users`, `sessions`, `auth_tokens`, `mfa_*`, `platform_admins`, `plans`, `subscriptions`, `entitlements`, `saas_invoices`, `saas_payment_events`, `support_tickets`, `support_ticket_messages`, `support_sessions`, `feature_flags`, `feature_flag_changes`, `tenant_usage_daily`, `account_closure_requests`, `incidents`, `partners`, `tenant_referrals`, `login_attempts`, `platform_admin_sessions`, `product_feedback`) are not under RLS because authentication runs before a tenant is known. Only `identity` and `saas` repositories touch them, via `db.WithPlatformTx`, and every query filters `tenant_id` explicitly. Isolation tests cover both paths.

## 5. Roles and privileges

Created by `infra/postgres/roles.sql` (runs once on an empty volume) and asserted by migrations:

| Role | Use | Privileges |
|---|---|---|
| `tally_owner` | Owns schema; runs `tally migrate` | DDL. Never used by the API process |
| `tally_app` | API, worker, CLI ledger commands | `SELECT, INSERT, UPDATE, DELETE` on mutable tables; `SELECT, INSERT` only on append-only tables; `NOSUPERUSER NOBYPASSRLS` |
| `tally_readonly` | Support read-only sessions (SEC-07), backups | `SELECT` only; still subject to RLS |

Append-only tables (UPDATE/DELETE revoked from `tally_app`): `stock_moves`, `price_changes`, `audit_events`, `cash_entries`, `exception_confirmations`, `invoice_versions`, `statement_versions`, `lot_genealogy`, `loyalty_entries`, `obligation_settlements`, `customer_activities`, `cogs_adjustments`, `consents`, `feature_flag_changes`, `period_locks` (a lock is lifted by a later row, never by editing the old one). `outbox_events` stays mutable because the worker marks rows processed.

## 6. Concurrency and lock order

Posting acquires row locks in one global order to avoid deadlocks: `doc_sequences` → `sku_stock` (ascending `sku_id`) → `lots`/`lot_balances` (ascending `lot_id`) → `reservations` → `obligations` (ascending `id`) → `payments` → `fund_accounts` (ascending `id`). Optimistic `version` checks guard user edits of drafts; pessimistic `FOR UPDATE` guards ledger projections.

## 7. Idempotency

`idempotency_keys (tenant_id, scope, key)` lives inside the posting transaction. The transaction first looks the key up: a completed, unexpired key with the same `request_hash` returns the stored response immediately, before the entitlement and period-lock guards, so a retry of a committed request is never turned into an error by a lock or a read-only subscription that arrived after the commit. Otherwise the guards run, then the key row is inserted (`INSERT … ON CONFLICT DO NOTHING`, then `SELECT … FOR UPDATE`) before the document number and row locks. A same-key request still in flight waits on that row and then replays; a different hash returns `409 idempotency_key_reused`. Only committed `2xx` responses are stored; a failed command rolls its key row back. Keys expire after 7 days (`tally maintenance purge-idempotency`). Offline POS sends its local id as the key (PRD §11.3).

## 8. Migrations

- golang-migrate v4, files in `apps/api/migrations/`, embedded with `iofs`, named `NNNNNN_<domain>_<subject>.up.sql` / `.down.sql`. One version sequence across domains.
- Order: platform & org → partners → catalog → pricing → inventory → procurement → sales → kits → debt & cash → campaign & CRM → notifications & outbox → SaaS → privileges & RLS assertions → seed reference data that is global (plans).
- Tenant seed data (7 colors, units, base attribute sets, retail/wholesale price lists, two fund accounts, reasons, payment methods, store, warehouse) is inserted by Go `org.TenantProvisioner` in `tally tenant create` and at sign-up, not by SQL, so it goes through the same validation and audit.
- Down migrations exist for every up and are exercised in CI (`migrate up` → `down -all` → `up`). Production never runs `down`; rollback is restore-from-backup.
- Backup before any schema or data change: `make db-backup` (pg_dump custom format to `infra/backups/`), and `tally migrate up` refuses to run unless `--backup-file` points at a non-empty `pg_dump` custom-format file (starts with the `PGDMP` signature) modified within the last hour. The guard is waived only when `APP_ENV` is explicitly `development` or `test`; an unset or blank `APP_ENV` is treated as guarded, so an operator who forgets the variable cannot skip the backup.
