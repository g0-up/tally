SET search_path = tally;

CREATE TABLE colors (                                 -- seed: xanh, do, tim, vang, nau, den, trang
  id uuid CONSTRAINT pk_colors PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_colors__tenant_id REFERENCES tenants (id),
  code text NOT NULL, name text NOT NULL, hex char(7) NOT NULL,
  sort_order int NOT NULL DEFAULT 0, is_system bool NOT NULL DEFAULT false,
  active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,                    -- timestamps set by Go
  CONSTRAINT ux_colors__id UNIQUE (tenant_id, id), CONSTRAINT ux_colors__code UNIQUE (tenant_id, code)
);
ALTER TABLE colors ENABLE ROW LEVEL SECURITY;
ALTER TABLE colors FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON colors
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE units (
  id uuid CONSTRAINT pk_units PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_units__tenant_id REFERENCES tenants (id),
  name text NOT NULL,
  name_key text NOT NULL,                             -- replaces a lower(name) expression index
  active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_units__id UNIQUE (tenant_id, id), CONSTRAINT ux_units__name_key UNIQUE (tenant_id, name_key)
);                                                    -- seed: cái, hộp, thùng, dây, lốc, chai, gói, kg, bó
ALTER TABLE units ENABLE ROW LEVEL SECURITY;
ALTER TABLE units FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON units
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE tax_rates (                              -- rate snapshot on every line
  id uuid CONSTRAINT pk_tax_rates PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_tax_rates__tenant_id REFERENCES tenants (id),
  name text NOT NULL, name_key text NOT NULL,
  rate_percent numeric(7,4) NOT NULL CONSTRAINT ck_tax_rates__rate_percent CHECK (rate_percent >= 0 AND rate_percent <= 100),
  active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_tax_rates__id UNIQUE (tenant_id, id), CONSTRAINT ux_tax_rates__name_key UNIQUE (tenant_id, name_key)
);                                                    -- seed: "Không chịu thuế" 0, "VAT 8%", "VAT 10%"
ALTER TABLE tax_rates ENABLE ROW LEVEL SECURITY;
ALTER TABLE tax_rates FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tax_rates
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
