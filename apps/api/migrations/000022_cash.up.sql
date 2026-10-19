SET search_path = tally;

CREATE TABLE fund_accounts (                          -- seed: "Tiền mặt tại cửa hàng", "Tài khoản ngân hàng"
  id uuid CONSTRAINT pk_fund_accounts PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_fund_accounts__tenant_id REFERENCES tenants (id),
  kind text NOT NULL CONSTRAINT ck_fund_accounts__kind CHECK (kind IN ('cash','bank')),
  name text NOT NULL, bank_name text, account_no text,
  opening_balance numeric(19,4) NOT NULL DEFAULT 0, opening_date date,
  balance numeric(19,4) NOT NULL DEFAULT 0,           -- projection = opening + Σ cash_entries
  active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_fund_accounts__id UNIQUE (tenant_id, id), CONSTRAINT ux_fund_accounts__kind UNIQUE (tenant_id, kind)
);
ALTER TABLE fund_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE fund_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON fund_accounts
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE payment_methods (                        -- Tiền mặt, Chuyển khoản
  id uuid CONSTRAINT pk_payment_methods PRIMARY KEY, tenant_id uuid NOT NULL,
  code text NOT NULL CONSTRAINT ck_payment_methods__code CHECK (code IN ('cash','bank_transfer')),
  name text NOT NULL, fund_account_id uuid NOT NULL,
  requires_verification bool NOT NULL DEFAULT false,  -- bank transfer → pending_verification
  active bool NOT NULL DEFAULT true, created_at timestamptz NOT NULL,
  CONSTRAINT ux_payment_methods__id UNIQUE (tenant_id, id), CONSTRAINT ux_payment_methods__code UNIQUE (tenant_id, code),
  CONSTRAINT fk_payment_methods__fund_account_id FOREIGN KEY (tenant_id, fund_account_id) REFERENCES fund_accounts (tenant_id, id)
);
ALTER TABLE payment_methods ENABLE ROW LEVEL SECURITY;
ALTER TABLE payment_methods FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON payment_methods
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE payments ADD CONSTRAINT fk_payments__fund_account_id
  FOREIGN KEY (tenant_id, fund_account_id) REFERENCES fund_accounts (tenant_id, id);
ALTER TABLE payments ADD CONSTRAINT fk_payments__payment_method_id
  FOREIGN KEY (tenant_id, payment_method_id) REFERENCES payment_methods (tenant_id, id);

CREATE TABLE cash_entries (                           -- append-only; written only for confirmed payments
  id uuid CONSTRAINT pk_cash_entries PRIMARY KEY, tenant_id uuid NOT NULL, fund_account_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL CONSTRAINT ck_cash_entries__amount CHECK (amount <> 0), -- signed: + in, − out
  category text NOT NULL CONSTRAINT ck_cash_entries__category CHECK (category IN ('sales','customer_deposit','debt_collection','supplier_payment',
      'supplier_advance','refund','landed_cost','opening','other')),           -- cash-flow report classification
  payment_id uuid, shift_id uuid,
  business_date date NOT NULL,
  posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_cash_entries__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_cash_entries__fund_account_id FOREIGN KEY (tenant_id, fund_account_id) REFERENCES fund_accounts (tenant_id, id),
  CONSTRAINT fk_cash_entries__payment_id FOREIGN KEY (tenant_id, payment_id) REFERENCES payments (tenant_id, id),
  CONSTRAINT fk_cash_entries__shift_id FOREIGN KEY (tenant_id, shift_id) REFERENCES shifts (tenant_id, id)
);
CREATE INDEX ix_cash_entries__fund_date ON cash_entries (tenant_id, fund_account_id, business_date);
REVOKE UPDATE, DELETE ON cash_entries FROM tally_app;
ALTER TABLE cash_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE cash_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON cash_entries
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
