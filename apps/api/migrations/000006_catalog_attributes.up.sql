SET search_path = tally;

CREATE TABLE attributes (
  id uuid CONSTRAINT pk_attributes PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_attributes__tenant_id REFERENCES tenants (id),
  code text NOT NULL CONSTRAINT ck_attributes__code CHECK (code ~ '^[a-z][a-z0-9_]{0,39}$'),   -- JSONB key, immutable after use
  name text NOT NULL,
  data_type text NOT NULL CONSTRAINT ck_attributes__data_type CHECK (data_type IN ('text','number','boolean','date','select','multiselect')),
  unit_label text,
  is_filterable bool NOT NULL DEFAULT false,
  is_searchable bool NOT NULL DEFAULT false,
  status text NOT NULL DEFAULT 'active' CONSTRAINT ck_attributes__status CHECK (status IN ('active','inactive')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_attributes__id UNIQUE (tenant_id, id), CONSTRAINT ux_attributes__code UNIQUE (tenant_id, code)
);                                                    -- seed: brand, origin, flavor, net_weight, volume …
ALTER TABLE attributes ENABLE ROW LEVEL SECURITY;
ALTER TABLE attributes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON attributes
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE attribute_options (
  id uuid CONSTRAINT pk_attribute_options PRIMARY KEY, tenant_id uuid NOT NULL, attribute_id uuid NOT NULL,
  label text NOT NULL, sort_order int NOT NULL DEFAULT 0, active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_attribute_options__id UNIQUE (tenant_id, id), CONSTRAINT ux_attribute_options__attribute_id_label UNIQUE (tenant_id, attribute_id, label),
  CONSTRAINT fk_attribute_options__attribute_id FOREIGN KEY (tenant_id, attribute_id) REFERENCES attributes (tenant_id, id)
);
ALTER TABLE attribute_options ENABLE ROW LEVEL SECURITY;
ALTER TABLE attribute_options FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON attribute_options
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE attribute_sets (                         -- "mẫu ngành": Bánh kẹo, Đồ uống, Giỏ quà …
  id uuid CONSTRAINT pk_attribute_sets PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_attribute_sets__tenant_id REFERENCES tenants (id),
  name text NOT NULL, active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  CONSTRAINT ux_attribute_sets__id UNIQUE (tenant_id, id), CONSTRAINT ux_attribute_sets__name UNIQUE (tenant_id, name)
);
ALTER TABLE attribute_sets ENABLE ROW LEVEL SECURITY;
ALTER TABLE attribute_sets FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON attribute_sets
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE attribute_set_items (
  tenant_id uuid NOT NULL, attribute_set_id uuid NOT NULL, attribute_id uuid NOT NULL,
  is_required bool NOT NULL DEFAULT false, sort_order int NOT NULL DEFAULT 0,
  CONSTRAINT pk_attribute_set_items PRIMARY KEY (tenant_id, attribute_set_id, attribute_id),
  CONSTRAINT fk_attribute_set_items__attribute_set_id FOREIGN KEY (tenant_id, attribute_set_id) REFERENCES attribute_sets (tenant_id, id),
  CONSTRAINT fk_attribute_set_items__attribute_id FOREIGN KEY (tenant_id, attribute_id) REFERENCES attributes (tenant_id, id)
);
ALTER TABLE attribute_set_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE attribute_set_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON attribute_set_items
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE categories (
  id uuid CONSTRAINT pk_categories PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_categories__tenant_id REFERENCES tenants (id), parent_id uuid,
  name text NOT NULL, name_key text NOT NULL,         -- unique sibling names
  attribute_set_id uuid, sort_order int NOT NULL DEFAULT 0,
  active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  CONSTRAINT ux_categories__id UNIQUE (tenant_id, id),
  CONSTRAINT ux_categories__parent_id_name_key UNIQUE NULLS NOT DISTINCT (tenant_id, parent_id, name_key),
  CONSTRAINT ck_categories__parent_id_id CHECK (parent_id IS NULL OR parent_id <> id),
  CONSTRAINT fk_categories__parent_id FOREIGN KEY (tenant_id, parent_id) REFERENCES categories (tenant_id, id),
  CONSTRAINT fk_categories__attribute_set_id FOREIGN KEY (tenant_id, attribute_set_id) REFERENCES attribute_sets (tenant_id, id)
);
ALTER TABLE categories ENABLE ROW LEVEL SECURITY;
ALTER TABLE categories FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON categories
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
