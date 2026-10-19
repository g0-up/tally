SET search_path = tally;

CREATE TABLE price_lists (
  id uuid CONSTRAINT pk_price_lists PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_price_lists__tenant_id REFERENCES tenants (id),
  name text NOT NULL, name_key text NOT NULL,
  kind text NOT NULL CONSTRAINT ck_price_lists__kind CHECK (kind IN ('retail','wholesale','group','customer','season')),
  customer_group_id uuid, party_id uuid, campaign_id uuid,   -- campaign FK added with campaigns
  priority int NOT NULL DEFAULT 100,
  valid_from date, valid_to date,
  active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_price_lists__id UNIQUE (tenant_id, id), CONSTRAINT ux_price_lists__name_key UNIQUE (tenant_id, name_key),
  CONSTRAINT ck_price_lists__valid_to_valid_from CHECK (valid_to IS NULL OR valid_from IS NULL OR valid_to >= valid_from),
  CONSTRAINT ck_price_lists__kind_customer_group_id CHECK ((kind = 'group') = (customer_group_id IS NOT NULL)),
  CONSTRAINT ck_price_lists__kind_party_id CHECK ((kind = 'customer') = (party_id IS NOT NULL)),
  CONSTRAINT fk_price_lists__customer_group_id FOREIGN KEY (tenant_id, customer_group_id) REFERENCES customer_groups (tenant_id, id),
  CONSTRAINT fk_price_lists__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE UNIQUE INDEX ux_price_lists__base ON price_lists (tenant_id, kind) WHERE kind IN ('retail','wholesale');
ALTER TABLE price_lists ENABLE ROW LEVEL SECURITY;
ALTER TABLE price_lists FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON price_lists
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE price_list_items (
  id uuid CONSTRAINT pk_price_list_items PRIMARY KEY, tenant_id uuid NOT NULL, price_list_id uuid NOT NULL, sku_id uuid NOT NULL,
  color_id uuid,                                      -- NULL = general price; set = color price
  unit_id uuid,                                       -- NULL = base unit
  min_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_price_list_items__min_qty CHECK (min_qty >= 0),   -- tier in base qty
  price numeric(19,4) NOT NULL CONSTRAINT ck_price_list_items__price CHECK (price >= 0),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  CONSTRAINT ux_price_list_items__tier UNIQUE NULLS NOT DISTINCT (tenant_id, price_list_id, sku_id, color_id, unit_id, min_qty),
  CONSTRAINT fk_price_list_items__price_list_id FOREIGN KEY (tenant_id, price_list_id) REFERENCES price_lists (tenant_id, id),
  CONSTRAINT fk_price_list_items__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_price_list_items__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  CONSTRAINT fk_price_list_items__unit_id FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT ux_price_list_items__id UNIQUE (tenant_id, id)
);
CREATE INDEX ix_price_list_items__sku ON price_list_items (tenant_id, sku_id, price_list_id);
ALTER TABLE price_list_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE price_list_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON price_list_items
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE price_changes (                          -- append-only
  id uuid CONSTRAINT pk_price_changes PRIMARY KEY, tenant_id uuid NOT NULL, price_list_id uuid NOT NULL, sku_id uuid NOT NULL,
  color_id uuid, unit_id uuid, min_qty numeric(18,4) NOT NULL,
  old_price numeric(19,4), new_price numeric(19,4),   -- NULL old = added; NULL new = "Bỏ giá riêng"
  source text NOT NULL CONSTRAINT ck_price_changes__source CHECK (source IN ('catalog','purchase_order','product_list','inventory','price_list')),
  source_doc_id uuid, actor_id uuid NOT NULL,
  created_at timestamptz NOT NULL,
  CONSTRAINT ck_price_changes__old_price_new_price CHECK (old_price IS NOT NULL OR new_price IS NOT NULL),
  CONSTRAINT fk_price_changes__price_list_id FOREIGN KEY (tenant_id, price_list_id) REFERENCES price_lists (tenant_id, id),
  CONSTRAINT fk_price_changes__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT ux_price_changes__id UNIQUE (tenant_id, id)
);
CREATE INDEX ix_price_changes__sku ON price_changes (tenant_id, sku_id, created_at DESC);
REVOKE UPDATE, DELETE ON price_changes FROM tally_app;
ALTER TABLE price_changes ENABLE ROW LEVEL SECURITY;
ALTER TABLE price_changes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON price_changes
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE promotions (
  id uuid CONSTRAINT pk_promotions PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_promotions__tenant_id REFERENCES tenants (id),
  code text,                                          -- NULL = automatic promotion
  name text NOT NULL,
  kind text NOT NULL CONSTRAINT ck_promotions__kind CHECK (kind IN ('line_percent','line_amount','order_percent','order_amount','gift','combo_price')),
  value numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_promotions__value CHECK (value >= 0),
  max_discount numeric(19,4) CONSTRAINT ck_promotions__max_discount CHECK (max_discount IS NULL OR max_discount >= 0),   -- trần chiết khấu
  min_order_amount numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_promotions__min_order_amount CHECK (min_order_amount >= 0),
  stackable bool NOT NULL DEFAULT false,              -- cộng dồn rule
  customer_group_id uuid, campaign_id uuid,           -- campaign FK added with campaigns
  valid_from date NOT NULL, valid_to date NOT NULL,
  usage_limit int CONSTRAINT ck_promotions__usage_limit CHECK (usage_limit IS NULL OR usage_limit > 0),
  status text NOT NULL DEFAULT 'active' CONSTRAINT ck_promotions__status CHECK (status IN ('draft','active','inactive')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_promotions__id UNIQUE (tenant_id, id),
  CONSTRAINT ck_promotions__valid_to_valid_from CHECK (valid_to >= valid_from),
  CONSTRAINT ck_promotions__kind_value CHECK (kind NOT IN ('line_percent','order_percent') OR value <= 100),
  CONSTRAINT fk_promotions__customer_group_id FOREIGN KEY (tenant_id, customer_group_id) REFERENCES customer_groups (tenant_id, id)
);
CREATE UNIQUE INDEX ux_promotions__code ON promotions (tenant_id, code) WHERE code IS NOT NULL;
ALTER TABLE promotions ENABLE ROW LEVEL SECURITY;
ALTER TABLE promotions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON promotions
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE promotion_items (                        -- scope SKUs, gift SKUs, combo components
  id uuid CONSTRAINT pk_promotion_items PRIMARY KEY, tenant_id uuid NOT NULL, promotion_id uuid NOT NULL,
  role text NOT NULL CONSTRAINT ck_promotion_items__role CHECK (role IN ('eligible','gift','combo_component')),
  sku_id uuid NOT NULL, qty numeric(18,4) NOT NULL DEFAULT 1 CONSTRAINT ck_promotion_items__qty CHECK (qty > 0),
  CONSTRAINT ux_promotion_items__promotion_id_role_sku_id UNIQUE (tenant_id, promotion_id, role, sku_id),
  CONSTRAINT fk_promotion_items__promotion_id FOREIGN KEY (tenant_id, promotion_id) REFERENCES promotions (tenant_id, id),
  CONSTRAINT fk_promotion_items__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT ux_promotion_items__id UNIQUE (tenant_id, id)
);
ALTER TABLE promotion_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE promotion_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON promotion_items
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE promotion_usages (                       -- usage_limit accounting; one row per applied doc
  id uuid CONSTRAINT pk_promotion_usages PRIMARY KEY, tenant_id uuid NOT NULL, promotion_id uuid NOT NULL,
  doc_type text NOT NULL CONSTRAINT ck_promotion_usages__doc_type CHECK (doc_type IN ('invoice','sales_document')), doc_id uuid NOT NULL,
  discount_amount numeric(19,4) NOT NULL CONSTRAINT ck_promotion_usages__discount_amount CHECK (discount_amount >= 0),
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_promotion_usages__promotion_id_doc_type_doc_id UNIQUE (tenant_id, promotion_id, doc_type, doc_id),
  CONSTRAINT fk_promotion_usages__promotion_id FOREIGN KEY (tenant_id, promotion_id) REFERENCES promotions (tenant_id, id)
);
ALTER TABLE promotion_usages ENABLE ROW LEVEL SECURITY;
ALTER TABLE promotion_usages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON promotion_usages
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
