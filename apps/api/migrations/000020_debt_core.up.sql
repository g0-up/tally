SET search_path = tally;

-- Balance is never typed in: receivable/payable = Σ obligations.amount − Σ obligation_settlements.amount.
-- settled_amount / allocated_amount columns are projections maintained by posting and checked by the
-- ledger verifier.

CREATE TABLE obligations (
  id uuid CONSTRAINT pk_obligations PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  direction text NOT NULL CONSTRAINT ck_obligations__direction CHECK (direction IN ('receivable','payable')),
  source_type text NOT NULL CONSTRAINT ck_obligations__source_type CHECK (source_type IN ('sales_document','receipt','landed_cost_allocation',
      'purchase_price_adjustment','debt_adjustment','opening_balance','refund_due','debt_offset')),
  source_id uuid NOT NULL, source_doc_no text NOT NULL,
  business_date date NOT NULL, due_date date NOT NULL,
  amount numeric(19,4) NOT NULL CONSTRAINT ck_obligations__amount CHECK (amount > 0),
  settled_amount numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_obligations__settled_amount CHECK (settled_amount >= 0),   -- projection
  status text NOT NULL DEFAULT 'open' CONSTRAINT ck_obligations__status CHECK (status IN ('open','settled')),
  campaign_id uuid,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_obligations__id UNIQUE (tenant_id, id),
  CONSTRAINT ux_obligations__source_type_source_id_direction UNIQUE (tenant_id, source_type, source_id, direction),   -- created once per source
  CONSTRAINT ck_obligations__settled_amount_amount CHECK (settled_amount <= amount),
  CONSTRAINT ck_obligations__status_settled_amount_amount CHECK ((status = 'settled') = (settled_amount = amount)),
  CONSTRAINT fk_obligations__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE INDEX ix_obligations__party_open ON obligations (tenant_id, party_id, direction, due_date) WHERE status = 'open';
CREATE INDEX ix_obligations__due ON obligations (tenant_id, direction, due_date) WHERE status = 'open';
ALTER TABLE obligations ENABLE ROW LEVEL SECURITY;
ALTER TABLE obligations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON obligations
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE obligation_schedules (                   -- multi-installment terms
  id uuid CONSTRAINT pk_obligation_schedules PRIMARY KEY, tenant_id uuid NOT NULL, obligation_id uuid NOT NULL,
  installment_no int NOT NULL CONSTRAINT ck_obligation_schedules__installment_no CHECK (installment_no > 0),
  due_date date NOT NULL, amount numeric(19,4) NOT NULL CONSTRAINT ck_obligation_schedules__amount CHECK (amount > 0),
  CONSTRAINT ux_obligation_schedules__obligation_id_installment_no UNIQUE (tenant_id, obligation_id, installment_no),
  CONSTRAINT fk_obligation_schedules__obligation_id FOREIGN KEY (tenant_id, obligation_id) REFERENCES obligations (tenant_id, id),
  CONSTRAINT ux_obligation_schedules__id UNIQUE (tenant_id, id)
);
-- Aging uses schedule due dates of the unpaid part, settled oldest-installment-first; as-of reports replay settlements ≤ as-of date
ALTER TABLE obligation_schedules ENABLE ROW LEVEL SECURITY;
ALTER TABLE obligation_schedules FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON obligation_schedules
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE payments (                               -- PT (direction in) / PC (direction out)
  id uuid CONSTRAINT pk_payments PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  direction text NOT NULL CONSTRAINT ck_payments__direction CHECK (direction IN ('in','out')),
  party_id uuid,                                      -- NULL only for POS walk-in cash sales
  purpose text NOT NULL CONSTRAINT ck_payments__purpose CHECK (purpose IN ('settlement','advance','deposit','refund','advance_refund','opening_advance')),
  fund_account_id uuid NOT NULL, payment_method_id uuid NOT NULL,   -- FKs added with the cash tables
  amount numeric(19,4) NOT NULL CONSTRAINT ck_payments__amount CHECK (amount > 0),
  allocated_amount numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_payments__allocated_amount CHECK (allocated_amount >= 0),   -- projection
  status text NOT NULL DEFAULT 'confirmed'
    CONSTRAINT ck_payments__status CHECK (status IN ('pending_verification','confirmed','reversed')),   -- bank transfer awaits verification
  earmarked_invoice_id uuid,                          -- deposit reserved for one invoice (credit check)
  source_type text CONSTRAINT ck_payments__source_type CHECK (source_type IN ('pos','invoice','debt','sales_return','exchange','purchase','landed_cost')),
  source_id uuid,
  business_date date NOT NULL, reference text, note text,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  confirmed_at timestamptz,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_payments__id UNIQUE (tenant_id, id), CONSTRAINT ux_payments__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_payments__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT ck_payments__allocated_amount_amount CHECK (allocated_amount <= amount),
  CONSTRAINT fk_payments__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id),
  CONSTRAINT fk_payments__earmarked_invoice_id FOREIGN KEY (tenant_id, earmarked_invoice_id) REFERENCES invoices (tenant_id, id)
);
CREATE INDEX ix_payments__party ON payments (tenant_id, party_id, direction, business_date);
CREATE INDEX ix_payments__unallocated ON payments (tenant_id, party_id, direction) WHERE status = 'confirmed';
-- Advance (ứng trước) = confirmed payment amount − allocated_amount; never revenue
ALTER TABLE payments ENABLE ROW LEVEL SECURITY;
ALTER TABLE payments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payments
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE credit_notes (                           -- giảm trừ: returns, post-sale discount, supplier credit
  id uuid CONSTRAINT pk_credit_notes PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  direction text NOT NULL CONSTRAINT ck_credit_notes__direction CHECK (direction IN ('receivable','payable')),   -- which ledger it reduces
  source_type text NOT NULL CONSTRAINT ck_credit_notes__source_type CHECK (source_type IN ('sales_return','purchase_return','purchase_price_adjustment','post_sale_discount')),
  source_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL CONSTRAINT ck_credit_notes__amount CHECK (amount > 0),
  allocated_amount numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_credit_notes__allocated_amount CHECK (allocated_amount >= 0),
  business_date date NOT NULL, reason_id uuid,
  posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_credit_notes__id UNIQUE (tenant_id, id), CONSTRAINT ux_credit_notes__source_type_source_id UNIQUE (tenant_id, source_type, source_id),
  CONSTRAINT ck_credit_notes__allocated_amount_amount CHECK (allocated_amount <= amount),
  CONSTRAINT fk_credit_notes__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id),
  CONSTRAINT fk_credit_notes__reason_id FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id)
);
-- Unallocated credit after open debt is reduced = "khoản cần hoàn" (refund_due obligation in the opposite direction)
ALTER TABLE credit_notes ENABLE ROW LEVEL SECURITY;
ALTER TABLE credit_notes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON credit_notes
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE debt_adjustments (                       -- DN
  id uuid CONSTRAINT pk_debt_adjustments PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL, party_id uuid NOT NULL,
  direction text NOT NULL CONSTRAINT ck_debt_adjustments__direction CHECK (direction IN ('receivable','payable')),
  kind text NOT NULL CONSTRAINT ck_debt_adjustments__kind CHECK (kind IN ('increase','decrease','write_off','dispute_settlement','opening_balance')),
  amount numeric(19,4) NOT NULL CONSTRAINT ck_debt_adjustments__amount CHECK (amount > 0),
  target_obligation_id uuid,                          -- decrease/write-off target
  reason_id uuid NOT NULL,
  opening_cutoff_date date,                           -- opening_balance only
  business_date date NOT NULL, note text,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_debt_adjustments__id UNIQUE (tenant_id, id), CONSTRAINT ux_debt_adjustments__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_debt_adjustments__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT ck_debt_adjustments__kind_opening_cutoff_date CHECK ((kind = 'opening_balance') = (opening_cutoff_date IS NOT NULL)),
  CONSTRAINT fk_debt_adjustments__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id),
  CONSTRAINT fk_debt_adjustments__target_obligation_id FOREIGN KEY (tenant_id, target_obligation_id) REFERENCES obligations (tenant_id, id),
  CONSTRAINT fk_debt_adjustments__reason_id FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id)
);
ALTER TABLE debt_adjustments ENABLE ROW LEVEL SECURITY;
ALTER TABLE debt_adjustments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON debt_adjustments
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE debt_offsets (                           -- BT: explicit, two-sided, never default netting
  id uuid CONSTRAINT pk_debt_offsets PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL, party_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL CONSTRAINT ck_debt_offsets__amount CHECK (amount > 0),
  receivable_obligation_id uuid NOT NULL, payable_obligation_id uuid NOT NULL,
  attachment_id uuid,                                 -- signed biên bản
  business_date date NOT NULL, note text,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_debt_offsets__id UNIQUE (tenant_id, id), CONSTRAINT ux_debt_offsets__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_debt_offsets__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT fk_debt_offsets__receivable_obligation_id FOREIGN KEY (tenant_id, receivable_obligation_id) REFERENCES obligations (tenant_id, id),
  CONSTRAINT fk_debt_offsets__payable_obligation_id FOREIGN KEY (tenant_id, payable_obligation_id) REFERENCES obligations (tenant_id, id),
  CONSTRAINT fk_debt_offsets__attachment_id FOREIGN KEY (tenant_id, attachment_id) REFERENCES attachments (tenant_id, id),
  CONSTRAINT fk_debt_offsets__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
ALTER TABLE debt_offsets ENABLE ROW LEVEL SECURITY;
ALTER TABLE debt_offsets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON debt_offsets
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE obligation_settlements (                 -- append-only; reversal = negative row with reversal_of
  id uuid CONSTRAINT pk_obligation_settlements PRIMARY KEY, tenant_id uuid NOT NULL, obligation_id uuid NOT NULL,
  settled_by_type text NOT NULL CONSTRAINT ck_obligation_settlements__settled_by_type CHECK (settled_by_type IN ('payment','credit_note','debt_offset','debt_adjustment')),
  settled_by_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL CONSTRAINT ck_obligation_settlements__amount CHECK (amount <> 0),
  business_date date NOT NULL,
  allocation_mode text NOT NULL CONSTRAINT ck_obligation_settlements__allocation_mode CHECK (allocation_mode IN ('manual','suggested_oldest_due','auto_earmarked')),
  posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_obligation_settlements__id UNIQUE (tenant_id, id),
  CONSTRAINT ck_obligation_settlements__amount_reversal_of CHECK ((amount < 0) = (reversal_of IS NOT NULL)),
  CONSTRAINT fk_obligation_settlements__obligation_id FOREIGN KEY (tenant_id, obligation_id) REFERENCES obligations (tenant_id, id),
  CONSTRAINT fk_obligation_settlements__reversal_of FOREIGN KEY (tenant_id, reversal_of) REFERENCES obligation_settlements (tenant_id, id)
);
CREATE INDEX ix_obligation_settlements__obligation ON obligation_settlements (tenant_id, obligation_id);
CREATE INDEX ix_obligation_settlements__source ON obligation_settlements (tenant_id, settled_by_type, settled_by_id);
CREATE UNIQUE INDEX ux_obligation_settlements__one_reversal ON obligation_settlements (tenant_id, reversal_of) WHERE reversal_of IS NOT NULL;
REVOKE UPDATE, DELETE ON obligation_settlements FROM tally_app;
ALTER TABLE obligation_settlements ENABLE ROW LEVEL SECURITY;
ALTER TABLE obligation_settlements FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON obligation_settlements
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
