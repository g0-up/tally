SET search_path = tally;

CREATE TABLE kit_recipes (                            -- versioned recipe of a kit or combo product
  id uuid CONSTRAINT pk_kit_recipes PRIMARY KEY, tenant_id uuid NOT NULL, product_id uuid NOT NULL,
  version_no int NOT NULL CONSTRAINT ck_kit_recipes__version_no CHECK (version_no > 0),
  status text NOT NULL DEFAULT 'draft' CONSTRAINT ck_kit_recipes__status CHECK (status IN ('draft','active','superseded')),
  valid_from date, valid_to date,
  packing_cost numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_kit_recipes__packing_cost CHECK (packing_cost >= 0),   -- capitalised into kit value
  output_sku_id uuid NOT NULL,                        -- the kit SKU (one SKU per kit product)
  note text,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_kit_recipes__id UNIQUE (tenant_id, id), CONSTRAINT ux_kit_recipes__product_id_version_no UNIQUE (tenant_id, product_id, version_no),
  CONSTRAINT fk_kit_recipes__product_id FOREIGN KEY (tenant_id, product_id) REFERENCES products (tenant_id, id),
  CONSTRAINT fk_kit_recipes__output_sku_id FOREIGN KEY (tenant_id, output_sku_id) REFERENCES skus (tenant_id, id)
);
CREATE UNIQUE INDEX ux_kit_recipes__one_active ON kit_recipes (tenant_id, product_id) WHERE status = 'active';
ALTER TABLE kit_recipes ENABLE ROW LEVEL SECURITY;
ALTER TABLE kit_recipes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kit_recipes
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE kit_recipe_items (
  id uuid CONSTRAINT pk_kit_recipe_items PRIMARY KEY, tenant_id uuid NOT NULL, kit_recipe_id uuid NOT NULL, line_no int NOT NULL,
  component_kind text NOT NULL CONSTRAINT ck_kit_recipe_items__component_kind CHECK (component_kind IN ('goods','packaging','basket','accessory','mix_pack')),
  sku_id uuid,                                        -- NULL for mix_pack
  unit_id uuid NOT NULL, qty numeric(18,4) NOT NULL CONSTRAINT ck_kit_recipe_items__qty CHECK (qty > 0),
  unit_factor_snap numeric(18,6) NOT NULL, base_qty numeric(18,4) NOT NULL,
  color_id uuid,                                      -- fixed color; NULL = chosen at assembly/sale
  mix_group_id uuid,                                  -- mix pack component: flavors in kit_recipe_mix_items
  CONSTRAINT ck_kit_recipe_items__base_qty_factor CHECK (base_qty = qty * unit_factor_snap),
  CONSTRAINT ck_kit_recipe_items__component_kind_mix_group_id_sku_id CHECK ((component_kind = 'mix_pack') = (mix_group_id IS NOT NULL AND sku_id IS NULL)),
  CONSTRAINT ux_kit_recipe_items__id UNIQUE (tenant_id, id), CONSTRAINT ux_kit_recipe_items__kit_recipe_id_line_no UNIQUE (tenant_id, kit_recipe_id, line_no),
  CONSTRAINT fk_kit_recipe_items__kit_recipe_id FOREIGN KEY (tenant_id, kit_recipe_id) REFERENCES kit_recipes (tenant_id, id),
  CONSTRAINT fk_kit_recipe_items__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_kit_recipe_items__unit_id FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_kit_recipe_items__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  CONSTRAINT fk_kit_recipe_items__mix_group_id FOREIGN KEY (tenant_id, mix_group_id) REFERENCES mix_groups (tenant_id, id)
);
ALTER TABLE kit_recipe_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE kit_recipe_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kit_recipe_items
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE kit_recipe_mix_items (                   -- per-flavor parts of a mix_pack component
  tenant_id uuid NOT NULL, kit_recipe_item_id uuid NOT NULL, sku_id uuid NOT NULL,
  parts_qty numeric(18,4) NOT NULL CONSTRAINT ck_kit_recipe_mix_items__parts_qty CHECK (parts_qty > 0),   -- in mix group part unit (dây/lốc)
  CONSTRAINT pk_kit_recipe_mix_items PRIMARY KEY (tenant_id, kit_recipe_item_id, sku_id),
  CONSTRAINT fk_kit_recipe_mix_items__kit_recipe_item_id FOREIGN KEY (tenant_id, kit_recipe_item_id) REFERENCES kit_recipe_items (tenant_id, id),
  CONSTRAINT fk_kit_recipe_mix_items__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id)
);
ALTER TABLE kit_recipe_mix_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE kit_recipe_mix_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON kit_recipe_mix_items
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE assembly_orders (                        -- LD
  id uuid CONSTRAINT pk_assembly_orders PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, doc_no text NOT NULL,
  kit_recipe_id uuid NOT NULL, recipe_version_snap int NOT NULL,
  planned_qty numeric(18,4) NOT NULL CONSTRAINT ck_assembly_orders__planned_qty CHECK (planned_qty > 0),
  completed_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_assembly_orders__completed_qty CHECK (completed_qty >= 0),
  disassembled_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_assembly_orders__disassembled_qty CHECK (disassembled_qty >= 0),
  status text NOT NULL DEFAULT 'draft'
    CONSTRAINT ck_assembly_orders__status CHECK (status IN ('draft','reserved','in_progress','partially_completed','completed','closed')),
  planned_date date, campaign_id uuid, invoice_id uuid,   -- optional link to a customer order
  note text,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_assembly_orders__id UNIQUE (tenant_id, id), CONSTRAINT ux_assembly_orders__doc_no UNIQUE (tenant_id, doc_no),
  CONSTRAINT fk_assembly_orders__kit_recipe_id FOREIGN KEY (tenant_id, kit_recipe_id) REFERENCES kit_recipes (tenant_id, id),
  CONSTRAINT fk_assembly_orders__invoice_id FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id),
  CONSTRAINT fk_assembly_orders__warehouse_id FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id)
);
ALTER TABLE assembly_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE assembly_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON assembly_orders
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE assembly_order_items (                   -- planned components; substitutions recorded here
  id uuid CONSTRAINT pk_assembly_order_items PRIMARY KEY, tenant_id uuid NOT NULL, assembly_order_id uuid NOT NULL,
  kit_recipe_item_id uuid,                            -- NULL = substitute added on this order
  substitutes_item_id uuid,
  sku_id uuid NOT NULL, color_id uuid,
  planned_base_qty numeric(18,4) NOT NULL CONSTRAINT ck_assembly_order_items__planned_base_qty CHECK (planned_base_qty >= 0),
  consumed_base_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_assembly_order_items__consumed_base_qty CHECK (consumed_base_qty >= 0),
  CONSTRAINT ux_assembly_order_items__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_assembly_order_items__assembly_order_id FOREIGN KEY (tenant_id, assembly_order_id) REFERENCES assembly_orders (tenant_id, id),
  CONSTRAINT fk_assembly_order_items__kit_recipe_item_id FOREIGN KEY (tenant_id, kit_recipe_item_id) REFERENCES kit_recipe_items (tenant_id, id),
  CONSTRAINT fk_assembly_order_items__substitutes_item_id FOREIGN KEY (tenant_id, substitutes_item_id) REFERENCES assembly_order_items (tenant_id, id),
  CONSTRAINT fk_assembly_order_items__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_assembly_order_items__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id)
);
-- reservations.source_type = 'assembly_order_item' when the order moves to 'reserved'
ALTER TABLE assembly_order_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE assembly_order_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON assembly_order_items
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE assembly_postings (                      -- each completion batch, atomic and balanced
  id uuid CONSTRAINT pk_assembly_postings PRIMARY KEY, tenant_id uuid NOT NULL, assembly_order_id uuid NOT NULL,
  produced_qty numeric(18,4) NOT NULL CONSTRAINT ck_assembly_postings__produced_qty CHECK (produced_qty > 0),
  output_lot_id uuid NOT NULL,                        -- kit lot; expiry ≤ earliest component expiry
  components_cost numeric(19,4) NOT NULL, packing_cost numeric(19,4) NOT NULL DEFAULT 0,
  output_unit_cost numeric(19,4) NOT NULL,            -- (components + packing) / produced_qty
  business_date date NOT NULL,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_assembly_postings__id UNIQUE (tenant_id, id), CONSTRAINT ux_assembly_postings__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT fk_assembly_postings__assembly_order_id FOREIGN KEY (tenant_id, assembly_order_id) REFERENCES assembly_orders (tenant_id, id),
  CONSTRAINT fk_assembly_postings__output_lot_id FOREIGN KEY (tenant_id, output_lot_id) REFERENCES lots (tenant_id, id)
);
ALTER TABLE assembly_postings ENABLE ROW LEVEL SECURITY;
ALTER TABLE assembly_postings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON assembly_postings
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE assembly_posting_items (                 -- actual consumption snapshot per lot and color
  id uuid CONSTRAINT pk_assembly_posting_items PRIMARY KEY, tenant_id uuid NOT NULL, assembly_posting_id uuid NOT NULL,
  assembly_order_item_id uuid NOT NULL,
  sku_id uuid NOT NULL, lot_id uuid NOT NULL, color_id uuid,
  consumed_base_qty numeric(18,4) NOT NULL CONSTRAINT ck_assembly_posting_items__consumed_base_qty CHECK (consumed_base_qty > 0),
  scrap_base_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_assembly_posting_items__scrap_base_qty CHECK (scrap_base_qty >= 0),   -- hao hụt
  unit_cost numeric(19,4) NOT NULL,
  CONSTRAINT ux_assembly_posting_items__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_assembly_posting_items__assembly_posting_id FOREIGN KEY (tenant_id, assembly_posting_id) REFERENCES assembly_postings (tenant_id, id),
  CONSTRAINT fk_assembly_posting_items__assembly_order_item_id FOREIGN KEY (tenant_id, assembly_order_item_id) REFERENCES assembly_order_items (tenant_id, id),
  CONSTRAINT fk_assembly_posting_items__lot_id FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id)
);
ALTER TABLE assembly_posting_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE assembly_posting_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON assembly_posting_items
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE disassemblies (                          -- TG
  id uuid CONSTRAINT pk_disassemblies PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, doc_no text NOT NULL,
  assembly_order_id uuid NOT NULL, kit_lot_id uuid NOT NULL,
  qty numeric(18,4) NOT NULL CONSTRAINT ck_disassemblies__qty CHECK (qty > 0),
  reason_id uuid NOT NULL,
  business_date date NOT NULL,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_disassemblies__id UNIQUE (tenant_id, id), CONSTRAINT ux_disassemblies__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_disassemblies__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT fk_disassemblies__assembly_order_id FOREIGN KEY (tenant_id, assembly_order_id) REFERENCES assembly_orders (tenant_id, id),
  CONSTRAINT fk_disassemblies__kit_lot_id FOREIGN KEY (tenant_id, kit_lot_id) REFERENCES lots (tenant_id, id),
  CONSTRAINT fk_disassemblies__reason_id FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id),
  CONSTRAINT fk_disassemblies__warehouse_id FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id)
);
ALTER TABLE disassemblies ENABLE ROW LEVEL SECURITY;
ALTER TABLE disassemblies FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON disassemblies
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE disassembly_lines (                      -- back to the exact lot + color used (from lot_genealogy)
  id uuid CONSTRAINT pk_disassembly_lines PRIMARY KEY, tenant_id uuid NOT NULL, disassembly_id uuid NOT NULL,
  sku_id uuid NOT NULL, source_lot_id uuid NOT NULL, color_id uuid,
  return_base_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_disassembly_lines__return_base_qty CHECK (return_base_qty >= 0),
  damaged_base_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_disassembly_lines__damaged_base_qty CHECK (damaged_base_qty >= 0),   -- → quarantine lot, never sellable
  unit_cost numeric(19,4) NOT NULL,
  CONSTRAINT ux_disassembly_lines__id UNIQUE (tenant_id, id),
  CONSTRAINT ck_disassembly_lines__return_base_qty_damaged_base_qty CHECK (return_base_qty + damaged_base_qty > 0),
  CONSTRAINT fk_disassembly_lines__disassembly_id FOREIGN KEY (tenant_id, disassembly_id) REFERENCES disassemblies (tenant_id, id),
  CONSTRAINT fk_disassembly_lines__source_lot_id FOREIGN KEY (tenant_id, source_lot_id) REFERENCES lots (tenant_id, id)
);
ALTER TABLE disassembly_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE disassembly_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON disassembly_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE lot_genealogy (                          -- append-only
  id uuid CONSTRAINT pk_lot_genealogy PRIMARY KEY, tenant_id uuid NOT NULL,
  parent_lot_id uuid NOT NULL,                        -- kit lot
  child_lot_id uuid NOT NULL,                         -- component lot (carries color)
  assembly_posting_id uuid NOT NULL,
  child_base_qty numeric(18,4) NOT NULL CONSTRAINT ck_lot_genealogy__child_base_qty CHECK (child_base_qty > 0),
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_lot_genealogy__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_lot_genealogy__parent_lot_id FOREIGN KEY (tenant_id, parent_lot_id) REFERENCES lots (tenant_id, id),
  CONSTRAINT fk_lot_genealogy__child_lot_id FOREIGN KEY (tenant_id, child_lot_id) REFERENCES lots (tenant_id, id),
  CONSTRAINT fk_lot_genealogy__assembly_posting_id FOREIGN KEY (tenant_id, assembly_posting_id) REFERENCES assembly_postings (tenant_id, id)
);
CREATE INDEX ix_lot_genealogy__parent ON lot_genealogy (tenant_id, parent_lot_id);
CREATE INDEX ix_lot_genealogy__child ON lot_genealogy (tenant_id, child_lot_id);
REVOKE UPDATE, DELETE ON lot_genealogy FROM tally_app;
ALTER TABLE lot_genealogy ENABLE ROW LEVEL SECURITY;
ALTER TABLE lot_genealogy FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON lot_genealogy
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
