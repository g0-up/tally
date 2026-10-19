SET search_path = tally;

CREATE TABLE stores (                                 -- exactly one per tenant
  id uuid CONSTRAINT pk_stores PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_stores__tenant_id REFERENCES tenants (id),
  name text NOT NULL, address text, phone text,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_stores__id UNIQUE (tenant_id, id), CONSTRAINT ux_stores__tenant_id UNIQUE (tenant_id)
);
ALTER TABLE stores ENABLE ROW LEVEL SECURITY;
ALTER TABLE stores FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stores
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE warehouses (                             -- exactly one; id kept on every stock row
  id uuid CONSTRAINT pk_warehouses PRIMARY KEY, tenant_id uuid NOT NULL, store_id uuid NOT NULL,
  name text NOT NULL,                                 -- "Kho cửa hàng"
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  CONSTRAINT ux_warehouses__id UNIQUE (tenant_id, id), CONSTRAINT ux_warehouses__tenant_id UNIQUE (tenant_id),
  CONSTRAINT fk_warehouses__store_id FOREIGN KEY (tenant_id, store_id) REFERENCES stores (tenant_id, id)
);
ALTER TABLE warehouses ENABLE ROW LEVEL SECURITY;
ALTER TABLE warehouses FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON warehouses
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE tenant_settings (
  tenant_id uuid CONSTRAINT pk_tenant_settings PRIMARY KEY CONSTRAINT fk_tenant_settings__tenant_id REFERENCES tenants (id),
  prices_include_tax bool NOT NULL DEFAULT true,
  rounding_mode text NOT NULL DEFAULT 'line' CONSTRAINT ck_tenant_settings__rounding_mode CHECK (rounding_mode IN ('line','document')),
  default_payment_terms_days int NOT NULL DEFAULT 0 CONSTRAINT ck_tenant_settings__default_payment_terms_days CHECK (default_payment_terms_days >= 0),
  block_expired_lot_sale bool NOT NULL DEFAULT true,
  expiry_warning_days int NOT NULL DEFAULT 30 CONSTRAINT ck_tenant_settings__expiry_warning_days CHECK (expiry_warning_days >= 0),
  offline_pos_enabled bool NOT NULL DEFAULT false,
  offline_pos_max_amount numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_tenant_settings__offline_pos_max_amount CHECK (offline_pos_max_amount >= 0),
  offline_pos_max_hours int NOT NULL DEFAULT 12 CONSTRAINT ck_tenant_settings__offline_pos_max_hours CHECK (offline_pos_max_hours BETWEEN 0 AND 72),
  analytics_ai_enabled bool NOT NULL DEFAULT false,   -- AI analytics opt-in
  product_analytics_enabled bool NOT NULL DEFAULT false,  -- usage analytics consent
  updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1
);
ALTER TABLE tenant_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tenant_settings
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE doc_sequences (                          -- row-locked document number issuer
  tenant_id uuid NOT NULL CONSTRAINT fk_doc_sequences__tenant_id REFERENCES tenants (id),
  prefix text NOT NULL CONSTRAINT ck_doc_sequences__prefix CHECK (prefix ~ '^[A-Z]{2,3}$'),
  next_value bigint NOT NULL DEFAULT 1 CONSTRAINT ck_doc_sequences__next_value CHECK (next_value > 0),
  CONSTRAINT pk_doc_sequences PRIMARY KEY (tenant_id, prefix)
);
ALTER TABLE doc_sequences ENABLE ROW LEVEL SECURITY;
ALTER TABLE doc_sequences FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON doc_sequences
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
