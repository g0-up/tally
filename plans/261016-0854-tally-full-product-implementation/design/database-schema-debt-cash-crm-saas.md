# Schema — Debt, Cash, Campaign, CRM, Notifications, Reporting, SaaS

Inherits brainstorm §5 (debt, cash, campaign/CRM, notification, SaaS rows). **+** = addition with its PRD reason. §1–§5 are tenant tables with the RLS block ([conventions](database-conventions.md) §4); §6 are platform tables (no RLS, `db.WithPlatformTx` only). Migrations: `000020_debt_core`, `000021_debt_statements`, `000022_cash`, `000023_campaign_crm`, `000024_notifications_reporting`, `000025_saas_platform`, `000026_privileges_assertions`.

## 1. Debt (DEBT-01..13, PRD §7.6)

Balance is never typed in: receivable/payable = Σ `obligations.amount` − Σ `obligation_settlements.amount` (DEBT-01). `settled_amount` columns are projections maintained by `posting` and checked by `tally ledger verify`.

```sql
CREATE TABLE obligations (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  direction text NOT NULL CHECK (direction IN ('receivable','payable')),
  source_type text NOT NULL CHECK (source_type IN ('sales_document','receipt','landed_cost_allocation',
      'purchase_price_adjustment','debt_adjustment','opening_balance','refund_due','debt_offset')),
  source_id uuid NOT NULL, source_doc_no text NOT NULL,
  business_date date NOT NULL, due_date date NOT NULL,
  amount numeric(19,4) NOT NULL CHECK (amount > 0),
  settled_amount numeric(19,4) NOT NULL DEFAULT 0 CHECK (settled_amount >= 0),   -- projection
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','settled')),
  campaign_id uuid,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, source_type, source_id, direction),   -- created once per source (PUR-08)
  CHECK (settled_amount <= amount),
  CHECK ((status = 'settled') = (settled_amount = amount)),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE INDEX ix_obligations__party_open ON obligations (tenant_id, party_id, direction, due_date) WHERE status = 'open';
CREATE INDEX ix_obligations__due ON obligations (tenant_id, direction, due_date) WHERE status = 'open';

CREATE TABLE obligation_schedules (                   -- + multi-installment terms (DEBT-03, PUR-07, SAL-06)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, obligation_id uuid NOT NULL,
  installment_no int NOT NULL CHECK (installment_no > 0),
  due_date date NOT NULL, amount numeric(19,4) NOT NULL CHECK (amount > 0),
  UNIQUE (tenant_id, obligation_id, installment_no),
  FOREIGN KEY (tenant_id, obligation_id) REFERENCES obligations (tenant_id, id),
  UNIQUE (tenant_id, id)
);
-- Aging (DEBT-03) uses schedule due dates of the unpaid part, settled oldest-installment-first; as-of reports replay settlements ≤ as-of date

CREATE TABLE payments (                               -- PT (direction in) / PC (direction out)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  direction text NOT NULL CHECK (direction IN ('in','out')),
  party_id uuid,                                      -- NULL only for POS walk-in cash sales
  purpose text NOT NULL CHECK (purpose IN ('settlement','advance','deposit','refund','advance_refund','opening_advance')),
  fund_account_id uuid NOT NULL, payment_method_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL CHECK (amount > 0),
  allocated_amount numeric(19,4) NOT NULL DEFAULT 0 CHECK (allocated_amount >= 0),   -- projection
  status text NOT NULL DEFAULT 'confirmed'
    CHECK (status IN ('pending_verification','confirmed','reversed')),   -- CASH-04 bank transfer
  earmarked_invoice_id uuid,                          -- deposit reserved for one invoice (§7.6 credit check)
  source_type text CHECK (source_type IN ('pos','invoice','debt','sales_return','exchange','purchase','landed_cost')),
  source_id uuid,
  business_date date NOT NULL, reference text, note text,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  confirmed_at timestamptz,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  CHECK (allocated_amount <= amount),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id),
  FOREIGN KEY (tenant_id, earmarked_invoice_id) REFERENCES invoices (tenant_id, id)
);
CREATE INDEX ix_payments__party ON payments (tenant_id, party_id, direction, business_date);
CREATE INDEX ix_payments__unallocated ON payments (tenant_id, party_id, direction) WHERE status = 'confirmed';
-- Advance (ứng trước) = confirmed payment amount − allocated_amount; never revenue (DEBT-05)

CREATE TABLE credit_notes (                           -- giảm trừ: returns, post-sale discount, supplier credit
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  direction text NOT NULL CHECK (direction IN ('receivable','payable')),   -- which ledger it reduces
  source_type text NOT NULL CHECK (source_type IN ('sales_return','purchase_return','purchase_price_adjustment','post_sale_discount')),
  source_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL CHECK (amount > 0),
  allocated_amount numeric(19,4) NOT NULL DEFAULT 0 CHECK (allocated_amount >= 0),
  business_date date NOT NULL, reason_id uuid,
  posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, source_type, source_id),
  CHECK (allocated_amount <= amount),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id),
  FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id)
);
-- Unallocated credit after open debt is reduced = "khoản cần hoàn" (refund_due obligation in the opposite direction)

CREATE TABLE debt_adjustments (                       -- DN (DEBT-02/09)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL, party_id uuid NOT NULL,
  direction text NOT NULL CHECK (direction IN ('receivable','payable')),
  kind text NOT NULL CHECK (kind IN ('increase','decrease','write_off','dispute_settlement','opening_balance')),
  amount numeric(19,4) NOT NULL CHECK (amount > 0),
  target_obligation_id uuid,                          -- decrease/write-off target
  reason_id uuid NOT NULL,
  opening_cutoff_date date,                           -- opening_balance only (DEBT-02)
  business_date date NOT NULL, note text,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  CHECK ((kind = 'opening_balance') = (opening_cutoff_date IS NOT NULL)),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id),
  FOREIGN KEY (tenant_id, target_obligation_id) REFERENCES obligations (tenant_id, id),
  FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id)
);

CREATE TABLE debt_offsets (                           -- + BT (DEBT-10): explicit, two-sided, never default netting
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL, party_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL CHECK (amount > 0),
  receivable_obligation_id uuid NOT NULL, payable_obligation_id uuid NOT NULL,
  attachment_id uuid,                                 -- signed biên bản
  business_date date NOT NULL, note text,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  FOREIGN KEY (tenant_id, receivable_obligation_id) REFERENCES obligations (tenant_id, id),
  FOREIGN KEY (tenant_id, payable_obligation_id) REFERENCES obligations (tenant_id, id),
  FOREIGN KEY (tenant_id, attachment_id) REFERENCES attachments (tenant_id, id),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);

CREATE TABLE obligation_settlements (                 -- append-only; reversal = negative row with reversal_of
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, obligation_id uuid NOT NULL,
  settled_by_type text NOT NULL CHECK (settled_by_type IN ('payment','credit_note','debt_offset','debt_adjustment')),
  settled_by_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL CHECK (amount <> 0),
  business_date date NOT NULL,
  allocation_mode text NOT NULL CHECK (allocation_mode IN ('manual','suggested_oldest_due','auto_earmarked')),
  posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  CHECK ((amount < 0) = (reversal_of IS NOT NULL)),
  FOREIGN KEY (tenant_id, obligation_id) REFERENCES obligations (tenant_id, id),
  FOREIGN KEY (tenant_id, reversal_of) REFERENCES obligation_settlements (tenant_id, id)
);
CREATE INDEX ix_obligation_settlements__obligation ON obligation_settlements (tenant_id, obligation_id);
CREATE INDEX ix_obligation_settlements__source ON obligation_settlements (tenant_id, settled_by_type, settled_by_id);
CREATE UNIQUE INDEX ux_obligation_settlements__one_reversal ON obligation_settlements (tenant_id, reversal_of) WHERE reversal_of IS NOT NULL;
REVOKE UPDATE, DELETE ON obligation_settlements FROM tally_app;

CREATE TABLE statements (                             -- DS (DEBT-07)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL, party_id uuid NOT NULL,
  direction text NOT NULL CHECK (direction IN ('receivable','payable')),
  period_from date NOT NULL, period_to date NOT NULL,
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','sent','confirmed','disputed','superseded')),
  current_version int NOT NULL DEFAULT 0, dispute_note text,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no),
  CHECK (period_to >= period_from),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);

CREATE TABLE statement_versions (                     -- append-only: sent numbers never change silently
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, statement_id uuid NOT NULL,
  version_no int NOT NULL CHECK (version_no > 0),
  opening_balance numeric(19,4) NOT NULL, closing_balance numeric(19,4) NOT NULL,
  snapshot jsonb NOT NULL,                            -- transactions as of generation
  sent_at timestamptz, created_by uuid NOT NULL, created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, statement_id, version_no),
  FOREIGN KEY (tenant_id, statement_id) REFERENCES statements (tenant_id, id),
  UNIQUE (tenant_id, id)
);
REVOKE UPDATE, DELETE ON statement_versions FROM tally_app;

CREATE TABLE collection_notes (                       -- + DEBT-08/13: contact notes, payment promises
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  direction text NOT NULL CHECK (direction IN ('receivable','payable')),
  kind text NOT NULL CHECK (kind IN ('note','call','promise','reminder_sent')),
  body text, promised_date date, promised_amount numeric(19,4) CHECK (promised_amount IS NULL OR promised_amount > 0),
  channel text CHECK (channel IN ('phone','sms','zalo','email','in_person')),
  dedupe_key text,                                    -- reminder retries never send twice
  created_by uuid NOT NULL, created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  CHECK (kind <> 'promise' OR promised_date IS NOT NULL),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE UNIQUE INDEX ux_collection_notes__dedupe ON collection_notes (tenant_id, dedupe_key) WHERE dedupe_key IS NOT NULL;
```

Rules in Go: no allocation across parties or beyond open amount / unallocated payment (DEBT-04); allocation suggestion oldest-due (Strategy `debt.OldestDueFirst`), user confirms; reversing a payment (DEBT-11) writes negative settlements and is blocked while its refund/transfer successor is unreversed; write-off needs `exception_confirmations` kind `debt_write_off`; forecast (DEBT-13) separates `promise` notes and schedules from confirmed cash.

## 2. Cash (CASH-01/02/04/06)

```sql
CREATE TABLE fund_accounts (                          -- seed: "Tiền mặt tại cửa hàng", "Tài khoản ngân hàng"
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  kind text NOT NULL CHECK (kind IN ('cash','bank')),
  name text NOT NULL, bank_name text, account_no text,
  opening_balance numeric(19,4) NOT NULL DEFAULT 0, opening_date date,
  balance numeric(19,4) NOT NULL DEFAULT 0,           -- projection = opening + Σ cash_entries
  active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, kind)
);

CREATE TABLE payment_methods (                        -- CAT-07: Tiền mặt, Chuyển khoản
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL,
  code text NOT NULL CHECK (code IN ('cash','bank_transfer')),
  name text NOT NULL, fund_account_id uuid NOT NULL,
  requires_verification bool NOT NULL DEFAULT false,  -- bank transfer → pending_verification
  active bool NOT NULL DEFAULT true, created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, code),
  FOREIGN KEY (tenant_id, fund_account_id) REFERENCES fund_accounts (tenant_id, id)
);
ALTER TABLE payments ADD FOREIGN KEY (tenant_id, fund_account_id) REFERENCES fund_accounts (tenant_id, id);
ALTER TABLE payments ADD FOREIGN KEY (tenant_id, payment_method_id) REFERENCES payment_methods (tenant_id, id);

CREATE TABLE cash_entries (                           -- append-only; written only for confirmed payments
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, fund_account_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL CHECK (amount <> 0), -- signed: + in, − out
  category text NOT NULL CHECK (category IN ('sales','customer_deposit','debt_collection','supplier_payment',
      'supplier_advance','refund','landed_cost','opening','other')),           -- RPT-06 classification
  payment_id uuid, shift_id uuid,
  business_date date NOT NULL,
  posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, fund_account_id) REFERENCES fund_accounts (tenant_id, id),
  FOREIGN KEY (tenant_id, payment_id) REFERENCES payments (tenant_id, id),
  FOREIGN KEY (tenant_id, shift_id) REFERENCES shifts (tenant_id, id)
);
CREATE INDEX ix_cash_entries__fund_date ON cash_entries (tenant_id, fund_account_id, business_date);
REVOKE UPDATE, DELETE ON cash_entries FROM tally_app;
```

POS change (tiền thừa, CASH-04) is not stored as an entry: the payment records the net amount; tendered/change go into the sales document snapshot. The fund opening balance is the `fund_accounts.opening_balance` column (editable only while the fund has no entries, else `409 fund_has_entries`), never a `cash_entries` row and never revenue; `balance = opening_balance + Σ cash_entries` is what `tally ledger verify` checks.

## 3. Campaign & CRM (SEA-01..03, CRM-01..03, PRD §7.7)

```sql
CREATE TABLE campaigns (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  name text NOT NULL, name_key text NOT NULL,
  kind text NOT NULL DEFAULT 'season' CHECK (kind IN ('tet','season','promotion','other')),
  starts_on date NOT NULL, ends_on date NOT NULL,
  budget_amount numeric(19,4), target_revenue numeric(19,4), target_margin_percent numeric(7,4),
  status text NOT NULL DEFAULT 'planning' CHECK (status IN ('planning','active','closed')),
  closed_at timestamptz,                              -- closing never resets stock, cash or debt
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, name_key),
  CHECK (ends_on >= starts_on)
);
-- campaign_id of tables created before campaigns gets its FK here (fk_<table>__campaign_id)
ALTER TABLE price_lists ADD FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE promotions ADD FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE sales_document_lines ADD FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE quotes ADD FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE invoices ADD FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE sales_documents ADD FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE assembly_orders ADD FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE obligations ADD FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);

CREATE TABLE campaign_attributions (                  -- one primary per financial line; tags for filtering only
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, campaign_id uuid NOT NULL,
  doc_type text NOT NULL CHECK (doc_type IN ('sales_document','sales_return','receipt','assembly_posting','payment')),
  doc_id uuid NOT NULL, doc_line_id uuid,             -- NULL = whole document
  role text NOT NULL CHECK (role IN ('primary','tag')),
  created_by uuid NOT NULL, created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE NULLS NOT DISTINCT (tenant_id, campaign_id, doc_type, doc_id, doc_line_id),
  FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id)
);
CREATE UNIQUE INDEX ux_campaign_attributions__one_primary
  ON campaign_attributions (tenant_id, doc_type, doc_id, doc_line_id) NULLS NOT DISTINCT WHERE role = 'primary';
CREATE INDEX ix_campaign_attributions__campaign ON campaign_attributions (tenant_id, campaign_id, role);

CREATE TABLE campaign_checklist_items (               -- + SEA-03
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, campaign_id uuid NOT NULL,
  phase text NOT NULL CHECK (phase IN ('before_peak','closing')),
  kind text NOT NULL CHECK (kind IN ('stock_count','statement','open_orders','surplus_stock','custom')),
  title text NOT NULL, done_at timestamptz, done_by uuid, sort_order int NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id)
);

CREATE TABLE customer_activities (                    -- append-only timeline (CRM-01)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('purchase','return','payment','note','care_call','reorder','consent_change')),
  ref_type text, ref_id uuid, summary text NOT NULL,
  created_by uuid, created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE INDEX ix_customer_activities__party ON customer_activities (tenant_id, party_id, created_at DESC);
REVOKE UPDATE, DELETE ON customer_activities FROM tally_app;

CREATE TABLE customer_care_tasks (                    -- + CRM-02 lịch chăm sóc / tái đặt đơn
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  due_on date NOT NULL, title text NOT NULL,
  source_campaign_id uuid,                            -- "khách mùa trước"
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','done','skipped')),
  done_at timestamptz, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id),
  FOREIGN KEY (tenant_id, source_campaign_id) REFERENCES campaigns (tenant_id, id)
);

CREATE TABLE consents (                               -- event log; latest row per (party, channel, purpose) wins
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  channel text NOT NULL CHECK (channel IN ('phone','sms','zalo','email')),
  purpose text NOT NULL CHECK (purpose IN ('debt_reminder','marketing','care')),
  granted bool NOT NULL, source text NOT NULL, evidence text,
  recorded_by uuid NOT NULL, recorded_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE INDEX ix_consents__latest ON consents (tenant_id, party_id, channel, purpose, recorded_at DESC);
REVOKE UPDATE, DELETE ON consents FROM tally_app;

CREATE TABLE loyalty_programs (                       -- + CRM-03 optional, one per tenant
  tenant_id uuid PRIMARY KEY REFERENCES tenants (id),
  enabled bool NOT NULL DEFAULT false,
  points_per_amount numeric(19,4) NOT NULL DEFAULT 0, -- points earned per 1 VND of net revenue
  point_value numeric(19,4) NOT NULL DEFAULT 0,       -- VND per point when redeemed as discount
  updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1
);

CREATE TABLE loyalty_entries (                        -- append-only; returns reverse points
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  points numeric(18,4) NOT NULL CHECK (points <> 0),
  kind text NOT NULL CHECK (kind IN ('earn','redeem','reverse','manual')),
  source_type text NOT NULL, source_id uuid NOT NULL, reversal_of uuid,
  business_date date NOT NULL, created_by uuid, created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, kind, source_type, source_id),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
REVOKE UPDATE, DELETE ON loyalty_entries FROM tally_app;
```

## 4. Notifications (NTF-01)

```sql
CREATE TABLE notifications (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  kind text NOT NULL CHECK (kind IN ('receivable_overdue','payable_due_soon','lot_expiring','lot_expired',
      'below_min_stock','surplus_stock','pos_sync_exception','statement_disputed','subscription_status','export_ready')),
  dedupe_key text NOT NULL,                           -- e.g. 'lot_expiring:<lot_id>:<expiry>'
  severity text NOT NULL CHECK (severity IN ('info','warn','bad')),
  title text NOT NULL, body text,
  entity_type text, entity_id uuid, color_id uuid,    -- lot notices carry the color (INV-07)
  action_path text,                                   -- web route to open
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  read_at timestamptz, resolved_at timestamptz, snoozed_until timestamptz,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, dedupe_key)
);
CREATE INDEX ix_notifications__inbox ON notifications (tenant_id, resolved_at, created_at DESC);
```

Producers: worker consumes `outbox_events` (`stock.changed`, `lot.expiry_changed`, `obligation.due_changed`) plus a daily tenant-midnight sweep; upsert by `dedupe_key` (`ON CONFLICT DO UPDATE SET updated_at`), resolve when the condition clears. Never produces "Cần giao", "Tiền thu hộ chưa về", "Sao kê chưa ghép".

## 5. Reporting, onboarding, AI (RPT-01..11, SAAS-04)

```sql
CREATE TABLE saved_reports (                          -- RPT-09 custom columns and filters
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  report_key text NOT NULL CHECK (report_key IN ('nxt','revenue','gross_margin','debt_aging','cash_flow','kits','season')),
  name text NOT NULL, config jsonb NOT NULL,          -- validated by reporting.ReportConfigSchema
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, report_key, name)
);

CREATE TABLE export_jobs (                            -- async CSV (RPT-09), data export (SAAS-03/08)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  kind text NOT NULL CHECK (kind IN ('report_csv','full_data_export')),
  params jsonb NOT NULL,
  status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','running','succeeded','failed','expired')),
  attachment_id uuid, row_count bigint, error text,
  requested_by uuid NOT NULL, created_at timestamptz NOT NULL,
  started_at timestamptz, finished_at timestamptz, expires_at timestamptz,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, attachment_id) REFERENCES attachments (tenant_id, id)
);

CREATE TABLE cost_recalculation_runs (                -- + §7.5 backdate recost: preview → owner confirms → apply
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  sku_ids uuid[] NOT NULL, from_date date NOT NULL,
  status text NOT NULL DEFAULT 'previewed' CHECK (status IN ('previewed','confirmed','applied','discarded')),
  impact jsonb NOT NULL,                              -- per SKU old/new avg cost, COGS delta
  run_version int NOT NULL,
  requested_by uuid NOT NULL, confirmed_by uuid,
  created_at timestamptz NOT NULL, applied_at timestamptz,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, run_version)
);

CREATE TABLE onboarding_steps (                       -- + SAAS-04 checklist incl. opening-balance confirmation
  tenant_id uuid NOT NULL REFERENCES tenants (id),
  step_key text NOT NULL CHECK (step_key IN ('store_profile','catalog_setup','opening_stock','opening_debt',
      'opening_cash','first_sale','confirm_opening_balances')),
  completed_at timestamptz, completed_by uuid,
  PRIMARY KEY (tenant_id, step_key)
);

CREATE TABLE analytics_queries (                      -- + RPT-11 audit of read-only AI Q&A (only if enabled)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  question text NOT NULL, answer text, cited_refs jsonb NOT NULL DEFAULT '[]',
  model text NOT NULL, asked_by uuid NOT NULL, created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id)
);
```

Dashboard (RPT-01), NXT (RPT-02), revenue (RPT-03), gross margin (RPT-04), aging (RPT-05), cash flow (RPT-06), kits (RPT-07), season (RPT-08) are read models in `reporting` built with plain SQL over the ledgers; no views (conventions §1). Demo tenants (`tenants.is_sandbox`) are excluded at the reporting repository (ORG-06). `uuid[]` is a plain column type; it calls no function.

## 6. SaaS & support (platform tables, no RLS)

```sql
CREATE TABLE plans (                                  -- global seed by migration (plans only)
  id uuid PRIMARY KEY, code text NOT NULL UNIQUE, name text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('year_round','seasonal','addon','trial')),
  billing_period text NOT NULL CHECK (billing_period IN ('month','season','year','none')),
  price numeric(19,4) NOT NULL DEFAULT 0,             -- price not decided (SAAS-01): admin-editable
  modules text[] NOT NULL DEFAULT '{}',               -- 'pos','kits','campaign','crm','reports_advanced','ai_qa'
  limits jsonb NOT NULL DEFAULT '{}',                 -- {"skus":20000,"storage_mb":2048}
  active bool NOT NULL DEFAULT true, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);
-- Seed (000025_saas_platform), fixed ids 00000000-0000-7000-8000-00000000000N, price 0 everywhere
-- until pricing is decided (OPEN-09; admin-editable), limits {"skus":20000,"storage_mb":2048} unless noted:
--   1 trial        'Dùng thử'   trial      none   {pos,kits,campaign,crm,reports_advanced}
--   2 store        'Cửa hàng'   year_round year   {pos}
--   3 wholesale    'Bán sỉ'     year_round year   {pos,campaign,crm,reports_advanced}
--   4 gift_basket  'Giỏ quà'    year_round year   {pos,kits,campaign,crm,reports_advanced}
--   5 seasonal     'Theo mùa'   seasonal   season {pos,kits,campaign,crm,reports_advanced}
--   6 addon_ai_qa  'Hỏi đáp AI' addon      month  {ai_qa}, limits {}

CREATE TABLE subscriptions (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id), plan_id uuid NOT NULL REFERENCES plans (id),
  status text NOT NULL CHECK (status IN ('trialing','active','grace','read_only','closed')),
  current_period_start date NOT NULL, current_period_end date NOT NULL,
  trial_ends_on date, grace_ends_on date, read_only_ends_on date,
  cancel_at_period_end bool NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX ux_subscriptions__one_live ON subscriptions (tenant_id) WHERE status <> 'closed' AND plan_id IS NOT NULL;

CREATE TABLE entitlements (                           -- server-side module/limit gate (SAAS-01)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  key text NOT NULL, value jsonb NOT NULL,            -- true / number limit
  source_subscription_id uuid REFERENCES subscriptions (id),
  source_plan_id uuid REFERENCES plans (id),          -- the add-on plan that granted the row; null for the base plan
  valid_from date NOT NULL, valid_to date,
  created_at timestamptz NOT NULL,
  CONSTRAINT ck_entitlements__single_source CHECK (source_subscription_id IS NULL OR source_plan_id IS NULL),
  -- plan and add-on rows for one module may start the same day; each keeps its own period
  CONSTRAINT ux_entitlements__key_valid_from_source
    UNIQUE NULLS NOT DISTINCT (tenant_id, key, valid_from, source_subscription_id, source_plan_id)
);

CREATE TABLE saas_invoices (                          -- service billing, separate from store finance
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  number text NOT NULL UNIQUE, subscription_id uuid REFERENCES subscriptions (id),
  period_start date NOT NULL, period_end date NOT NULL,
  amount numeric(19,4) NOT NULL, proration_note text,
  status text NOT NULL CHECK (status IN ('draft','open','paid','void')),
  provider text, provider_ref text,
  issued_at timestamptz NOT NULL, paid_at timestamptz,
  UNIQUE (provider, provider_ref)
);

CREATE TABLE saas_payment_events (                    -- + SAAS-02 duplicate callbacks never extend twice
  id uuid PRIMARY KEY, provider text NOT NULL, event_id text NOT NULL,
  saas_invoice_id uuid REFERENCES saas_invoices (id),
  payload jsonb NOT NULL, received_at timestamptz NOT NULL, processed_at timestamptz,
  UNIQUE (provider, event_id)
);

CREATE TABLE support_tickets (                        -- SAAS-05
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  number text NOT NULL UNIQUE, subject text NOT NULL,
  status text NOT NULL CHECK (status IN ('open','waiting_customer','in_progress','resolved','closed')),
  priority text NOT NULL CHECK (priority IN ('low','normal','high','urgent')),
  sla_due_at timestamptz, assigned_admin_id uuid REFERENCES platform_admins (id),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);

CREATE TABLE support_ticket_messages (
  id uuid PRIMARY KEY, ticket_id uuid NOT NULL REFERENCES support_tickets (id),
  author_type text NOT NULL CHECK (author_type IN ('owner','platform_admin')), author_id uuid NOT NULL,
  body text NOT NULL, attachment_ids uuid[] NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL
);

CREATE TABLE support_sessions (                       -- SEC-07: consented, scoped, expiring, read-only by default
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  admin_id uuid NOT NULL REFERENCES platform_admins (id), ticket_id uuid REFERENCES support_tickets (id),
  scope text NOT NULL DEFAULT 'read_only' CHECK (scope IN ('read_only')),   -- write scope out of 1.1
  modules text[] NOT NULL DEFAULT '{}',
  consent_token_id uuid NOT NULL REFERENCES auth_tokens (id),
  granted_at timestamptz NOT NULL, expires_at timestamptz NOT NULL, revoked_at timestamptz,
  CHECK (expires_at > granted_at)
);

CREATE TABLE feature_flags (                          -- SAAS-06; history in feature_flag_changes
  key text NOT NULL, tenant_id uuid REFERENCES tenants (id),
  enabled bool NOT NULL, updated_at timestamptz NOT NULL,
  UNIQUE NULLS NOT DISTINCT (key, tenant_id)
);

CREATE TABLE feature_flag_changes (                   -- append-only
  id uuid PRIMARY KEY, key text NOT NULL, tenant_id uuid,
  enabled bool NOT NULL, admin_id uuid NOT NULL REFERENCES platform_admins (id), created_at timestamptz NOT NULL
);
REVOKE UPDATE, DELETE ON feature_flag_changes FROM tally_app;

CREATE TABLE tenant_usage_daily (                     -- SAAS-06 usage, no sales content
  tenant_id uuid NOT NULL REFERENCES tenants (id), usage_date date NOT NULL,
  active_skus int NOT NULL, documents_posted int NOT NULL, storage_bytes bigint NOT NULL,
  PRIMARY KEY (tenant_id, usage_date)
);

CREATE TABLE account_closure_requests (               -- SAAS-08
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  requested_by uuid NOT NULL, verified_at timestamptz,
  export_job_id uuid,                                 -- full data export offered first (OPEN-15 default)
  waiting_until date NOT NULL, legal_hold bool NOT NULL DEFAULT false,
  status text NOT NULL CHECK (status IN ('requested','verified','waiting','completed','cancelled')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);

CREATE TABLE product_feedback (                       -- + SAAS-10: minimal, no document content
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  kind text NOT NULL CHECK (kind IN ('idea','bug','praise','nps')),
  score int CHECK (score IS NULL OR (score >= 0 AND score <= 10)),
  body text, page_path text, created_at timestamptz NOT NULL
);
-- Consent flags live in tenant_settings: product_analytics_enabled (SAAS-10), analytics_ai_enabled (RPT-11/NFR-16/OPEN-12); both default false

CREATE TABLE incidents (                              -- SAAS-07 status page
  id uuid PRIMARY KEY, title text NOT NULL,
  status text NOT NULL CHECK (status IN ('investigating','identified','monitoring','resolved')),
  impact text NOT NULL CHECK (impact IN ('none','minor','major','critical')),
  started_at timestamptz NOT NULL, resolved_at timestamptz, updates jsonb NOT NULL DEFAULT '[]'
);

CREATE TABLE partners (                               -- SAAS-09 reseller/referral, no data access by default
  id uuid PRIMARY KEY, name text NOT NULL, referral_code text NOT NULL UNIQUE,
  commission_percent numeric(7,4) NOT NULL DEFAULT 0, active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL
);

CREATE TABLE tenant_referrals (
  tenant_id uuid PRIMARY KEY REFERENCES tenants (id), partner_id uuid NOT NULL REFERENCES partners (id),
  referred_at timestamptz NOT NULL
);
```

Subscription state machine (`saas.SubscriptionLifecycle`, State pattern): `trialing → active → grace → read_only → closed`; `read_only` blocks every posting command in `posting.Coordinator` via an `EntitlementGuard` and keeps exports available (SAAS-03). Support middleware opens tenant transactions with `SET LOCAL ROLE tally_readonly` while a support session is active (SEC-07).
