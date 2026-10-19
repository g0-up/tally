SET search_path = tally;

CREATE TABLE products (
  id uuid CONSTRAINT pk_products PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_products__tenant_id REFERENCES tenants (id),
  type text NOT NULL CONSTRAINT ck_products__type CHECK (type IN ('goods','material','kit','combo')),
  name text NOT NULL,
  name_key text NOT NULL,                             -- duplicate-name block without an expression index
  category_id uuid, attribute_set_id uuid,
  attributes jsonb NOT NULL DEFAULT '{}',             -- object shape checked by catalog.AttributeValidator
  description text,
  status text NOT NULL DEFAULT 'active' CONSTRAINT ck_products__status CHECK (status IN ('draft','active','inactive')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_products__id UNIQUE (tenant_id, id), CONSTRAINT ux_products__name_key UNIQUE (tenant_id, name_key),
  CONSTRAINT fk_products__category_id FOREIGN KEY (tenant_id, category_id) REFERENCES categories (tenant_id, id),
  CONSTRAINT fk_products__attribute_set_id FOREIGN KEY (tenant_id, attribute_set_id) REFERENCES attribute_sets (tenant_id, id)
);
-- The one tenant index not led by tenant_id: a GIN index can only add tenant_id through the
-- btree_gin extension, which the schema does not install. Attribute filters still run under the
-- tenant policy, so rows of other tenants never come back.
CREATE INDEX ix_products__attributes ON products USING gin (attributes jsonb_path_ops);
CREATE INDEX ix_products__category ON products (tenant_id, category_id, status);
ALTER TABLE products ENABLE ROW LEVEL SECURITY;
ALTER TABLE products FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON products
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE product_variant_axes (
  tenant_id uuid NOT NULL, product_id uuid NOT NULL, attribute_id uuid NOT NULL,
  position smallint NOT NULL CONSTRAINT ck_product_variant_axes__position CHECK (position BETWEEN 1 AND 3),   -- max 3 axes keeps the matrix usable
  CONSTRAINT pk_product_variant_axes PRIMARY KEY (tenant_id, product_id, attribute_id),
  CONSTRAINT ux_product_variant_axes__product_id_position UNIQUE (tenant_id, product_id, position),
  CONSTRAINT fk_product_variant_axes__product_id FOREIGN KEY (tenant_id, product_id) REFERENCES products (tenant_id, id),
  CONSTRAINT fk_product_variant_axes__attribute_id FOREIGN KEY (tenant_id, attribute_id) REFERENCES attributes (tenant_id, id)
);  -- select attributes only; axes locked once any SKU of the product has a stock move
ALTER TABLE product_variant_axes ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_variant_axes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON product_variant_axes
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE skus (
  id uuid CONSTRAINT pk_skus PRIMARY KEY, tenant_id uuid NOT NULL, product_id uuid NOT NULL,
  code text NOT NULL,
  variant_key text NOT NULL DEFAULT '',               -- option ids ordered by axis position joined by '|'
  display_name text NOT NULL,                         -- "Sữa TH 180ml – Dâu", built by catalog.SKUNamer
  variant_label text NOT NULL DEFAULT '',             -- "Dâu" (snapshot source for variant_label_snap)
  base_unit_id uuid NOT NULL,
  qty_scale smallint NOT NULL DEFAULT 0 CONSTRAINT ck_skus__qty_scale CHECK (qty_scale BETWEEN 0 AND 4),
  track_lot bool NOT NULL DEFAULT false, track_expiry bool NOT NULL DEFAULT false,
  ref_cost numeric(19,4) CONSTRAINT ck_skus__ref_cost CHECK (ref_cost IS NULL OR ref_cost >= 0),
  min_price numeric(19,4) CONSTRAINT ck_skus__min_price CHECK (min_price IS NULL OR min_price >= 0),     -- one level
  min_stock numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_skus__min_stock CHECK (min_stock >= 0),
  tax_rate_id uuid,                                   -- NULL = tenant default (0)
  spec_text text,                                     -- "quy cách" default for lines
  status text NOT NULL DEFAULT 'active' CONSTRAINT ck_skus__status CHECK (status IN ('active','inactive')),
  search_norm text NOT NULL DEFAULT '',               -- built by catalog.SearchIndexer
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_skus__id UNIQUE (tenant_id, id), CONSTRAINT ux_skus__code UNIQUE (tenant_id, code),
  CONSTRAINT ux_skus__product_id_variant_key UNIQUE (tenant_id, product_id, variant_key),
  CONSTRAINT ck_skus__track_expiry_track_lot CHECK (NOT track_expiry OR track_lot),              -- expiry lives on lots
  CONSTRAINT fk_skus__product_id FOREIGN KEY (tenant_id, product_id) REFERENCES products (tenant_id, id),
  CONSTRAINT fk_skus__base_unit_id FOREIGN KEY (tenant_id, base_unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_skus__tax_rate_id FOREIGN KEY (tenant_id, tax_rate_id) REFERENCES tax_rates (tenant_id, id)
);
CREATE INDEX ix_skus__status ON skus (tenant_id, status);   -- search_norm LIKE filter runs on this
CREATE INDEX ix_skus__product ON skus (tenant_id, product_id);
ALTER TABLE skus ENABLE ROW LEVEL SECURITY;
ALTER TABLE skus FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON skus
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE sku_variant_values (
  tenant_id uuid NOT NULL, sku_id uuid NOT NULL, attribute_id uuid NOT NULL, option_id uuid NOT NULL,
  CONSTRAINT pk_sku_variant_values PRIMARY KEY (tenant_id, sku_id, attribute_id),
  CONSTRAINT fk_sku_variant_values__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_sku_variant_values__attribute_id FOREIGN KEY (tenant_id, attribute_id) REFERENCES attributes (tenant_id, id),
  CONSTRAINT fk_sku_variant_values__option_id FOREIGN KEY (tenant_id, option_id) REFERENCES attribute_options (tenant_id, id)
);
ALTER TABLE sku_variant_values ENABLE ROW LEVEL SECURITY;
ALTER TABLE sku_variant_values FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sku_variant_values
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE sku_units (
  tenant_id uuid NOT NULL, sku_id uuid NOT NULL, unit_id uuid NOT NULL,
  factor numeric(18,6) NOT NULL CONSTRAINT ck_sku_units__factor CHECK (factor > 0),   -- 1 of this unit = factor base units
  is_base bool NOT NULL DEFAULT false, active bool NOT NULL DEFAULT true,
  is_default_sale bool NOT NULL DEFAULT false,        -- POS/invoice default unit
  is_default_purchase bool NOT NULL DEFAULT false,    -- PO/receipt default unit
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  CONSTRAINT pk_sku_units PRIMARY KEY (tenant_id, sku_id, unit_id),
  CONSTRAINT ck_sku_units__is_base_factor CHECK (NOT is_base OR factor = 1),
  CONSTRAINT fk_sku_units__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_sku_units__unit_id FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id)
);
CREATE UNIQUE INDEX ux_sku_units__base ON sku_units (tenant_id, sku_id) WHERE is_base;
CREATE UNIQUE INDEX ux_sku_units__default_sale ON sku_units (tenant_id, sku_id) WHERE is_default_sale;
CREATE UNIQUE INDEX ux_sku_units__default_purchase ON sku_units (tenant_id, sku_id) WHERE is_default_purchase;
ALTER TABLE sku_units ENABLE ROW LEVEL SECURITY;
ALTER TABLE sku_units FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sku_units
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE sku_barcodes (
  id uuid CONSTRAINT pk_sku_barcodes PRIMARY KEY, tenant_id uuid NOT NULL, sku_id uuid NOT NULL, unit_id uuid NOT NULL,
  code text NOT NULL,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_sku_barcodes__code UNIQUE (tenant_id, code),
  CONSTRAINT fk_sku_barcodes__sku_id_unit_id FOREIGN KEY (tenant_id, sku_id, unit_id) REFERENCES sku_units (tenant_id, sku_id, unit_id)
);
ALTER TABLE sku_barcodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE sku_barcodes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sku_barcodes
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE product_images (
  id uuid CONSTRAINT pk_product_images PRIMARY KEY, tenant_id uuid NOT NULL, product_id uuid NOT NULL, sku_id uuid,
  attachment_id uuid NOT NULL,                        -- storage_key lives on attachments (signed URL, scan)
  sort_order int NOT NULL DEFAULT 0, created_at timestamptz NOT NULL,
  CONSTRAINT ux_product_images__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_product_images__product_id FOREIGN KEY (tenant_id, product_id) REFERENCES products (tenant_id, id),
  CONSTRAINT fk_product_images__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_product_images__attachment_id FOREIGN KEY (tenant_id, attachment_id) REFERENCES attachments (tenant_id, id)
);
ALTER TABLE product_images ENABLE ROW LEVEL SECURITY;
ALTER TABLE product_images FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON product_images
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
