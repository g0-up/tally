SET search_path = tally;

CREATE TABLE parties (                                -- one party may be customer and supplier
  id uuid CONSTRAINT pk_parties PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_parties__tenant_id REFERENCES tenants (id),
  kind text NOT NULL DEFAULT 'organization' CONSTRAINT ck_parties__kind CHECK (kind IN ('person','organization')),
  name text NOT NULL, name_key text NOT NULL,
  phone text, phone_key text,                         -- digits only, for search
  email text, tax_code text, address text, note text,
  search_norm text NOT NULL DEFAULT '',               -- name + phone + tax code, no diacritics
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_parties__id UNIQUE (tenant_id, id)
);
CREATE INDEX ix_parties__name ON parties (tenant_id, name_key);
CREATE INDEX ix_parties__phone ON parties (tenant_id, phone_key);
ALTER TABLE parties ENABLE ROW LEVEL SECURITY;
ALTER TABLE parties FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON parties
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE customer_groups (                        -- seed: Khách lẻ (retail), Khách sỉ (wholesale)
  id uuid CONSTRAINT pk_customer_groups PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_customer_groups__tenant_id REFERENCES tenants (id),
  name text NOT NULL, name_key text NOT NULL,
  base_price_kind text NOT NULL CONSTRAINT ck_customer_groups__base_price_kind CHECK (base_price_kind IN ('retail','wholesale')),
  is_system bool NOT NULL DEFAULT false, active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  CONSTRAINT ux_customer_groups__id UNIQUE (tenant_id, id), CONSTRAINT ux_customer_groups__name_key UNIQUE (tenant_id, name_key)
);
ALTER TABLE customer_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_groups FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_groups
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE customer_profiles (
  tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  customer_group_id uuid NOT NULL,
  credit_limit numeric(19,4) CONSTRAINT ck_customer_profiles__credit_limit CHECK (credit_limit IS NULL OR credit_limit >= 0),   -- NULL = no limit
  payment_terms_days int NOT NULL DEFAULT 0 CONSTRAINT ck_customer_profiles__payment_terms_days CHECK (payment_terms_days >= 0),
  status text NOT NULL DEFAULT 'active' CONSTRAINT ck_customer_profiles__status CHECK (status IN ('active','inactive')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT pk_customer_profiles PRIMARY KEY (tenant_id, party_id),
  CONSTRAINT fk_customer_profiles__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id),
  CONSTRAINT fk_customer_profiles__customer_group_id FOREIGN KEY (tenant_id, customer_group_id) REFERENCES customer_groups (tenant_id, id)
);
ALTER TABLE customer_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_profiles FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_profiles
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE supplier_profiles (                      -- Đang giao dịch / Ngừng giao dịch
  tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  payment_terms_days int NOT NULL DEFAULT 0 CONSTRAINT ck_supplier_profiles__payment_terms_days CHECK (payment_terms_days >= 0),
  supplied_goods_note text,                           -- "mặt hàng cung cấp" free text, searchable
  status text NOT NULL DEFAULT 'active' CONSTRAINT ck_supplier_profiles__status CHECK (status IN ('active','inactive')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT pk_supplier_profiles PRIMARY KEY (tenant_id, party_id),
  CONSTRAINT fk_supplier_profiles__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
ALTER TABLE supplier_profiles ENABLE ROW LEVEL SECURITY;
ALTER TABLE supplier_profiles FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON supplier_profiles
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE contacts (
  id uuid CONSTRAINT pk_contacts PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  name text NOT NULL, phone text, email text, role_title text,
  is_primary bool NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  CONSTRAINT ux_contacts__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_contacts__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE UNIQUE INDEX ux_contacts__primary ON contacts (tenant_id, party_id) WHERE is_primary;
ALTER TABLE contacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE contacts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON contacts
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE party_addresses (
  id uuid CONSTRAINT pk_party_addresses PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  label text NOT NULL, address_line text NOT NULL, is_default bool NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_party_addresses__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_party_addresses__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE UNIQUE INDEX ux_party_addresses__default ON party_addresses (tenant_id, party_id) WHERE is_default;
ALTER TABLE party_addresses ENABLE ROW LEVEL SECURITY;
ALTER TABLE party_addresses FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON party_addresses
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
