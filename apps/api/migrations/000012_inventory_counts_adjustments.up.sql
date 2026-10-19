SET search_path = tally;

CREATE TABLE stock_counts (                           -- KK
  id uuid CONSTRAINT pk_stock_counts PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL,
  doc_no text NOT NULL,
  scope text NOT NULL CONSTRAINT ck_stock_counts__scope CHECK (scope IN ('all','category','selection')),
  category_id uuid, blind bool NOT NULL DEFAULT false,
  status text NOT NULL DEFAULT 'counting' CONSTRAINT ck_stock_counts__status CHECK (status IN ('counting','pending_post','posted')),
  business_date date NOT NULL, started_at timestamptz NOT NULL, counted_at timestamptz,
  note text,
  idempotency_key text, posted_at timestamptz, posted_by uuid,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_stock_counts__id UNIQUE (tenant_id, id), CONSTRAINT ux_stock_counts__doc_no UNIQUE (tenant_id, doc_no),
  CONSTRAINT ux_stock_counts__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT fk_stock_counts__category_id FOREIGN KEY (tenant_id, category_id) REFERENCES categories (tenant_id, id),
  CONSTRAINT fk_stock_counts__warehouse_id FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id)
);
ALTER TABLE stock_counts ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_counts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_counts
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE stock_count_lines (                      -- one line per SKU × color (× lot when tracked)
  id uuid CONSTRAINT pk_stock_count_lines PRIMARY KEY, tenant_id uuid NOT NULL, stock_count_id uuid NOT NULL,
  sku_id uuid NOT NULL, color_id uuid, lot_id uuid,
  system_qty numeric(18,4) NOT NULL,                  -- snapshot at start; hidden in blind mode
  counted_qty numeric(18,4) CONSTRAINT ck_stock_count_lines__counted_qty CHECK (counted_qty IS NULL OR counted_qty >= 0),
  moved_since_start_qty numeric(18,4) NOT NULL DEFAULT 0,   -- moves after started_at, filled at counting finish
  variance_qty numeric(18,4),                         -- counted − (system + moved_since_start)
  reason_id uuid,
  CONSTRAINT ux_stock_count_lines__id UNIQUE (tenant_id, id),
  CONSTRAINT ux_stock_count_lines__stock_count_id_sku_id_color_id_lot_id UNIQUE NULLS NOT DISTINCT (tenant_id, stock_count_id, sku_id, color_id, lot_id),
  CONSTRAINT ck_stock_count_lines__variance_qty_counted_qty CHECK (variance_qty IS NULL OR counted_qty IS NOT NULL),
  CONSTRAINT ck_stock_count_lines__variance_formula CHECK (variance_qty IS NULL OR variance_qty = counted_qty - system_qty - moved_since_start_qty),
  CONSTRAINT fk_stock_count_lines__stock_count_id FOREIGN KEY (tenant_id, stock_count_id) REFERENCES stock_counts (tenant_id, id),
  CONSTRAINT fk_stock_count_lines__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_stock_count_lines__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  CONSTRAINT fk_stock_count_lines__lot_id FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id),
  CONSTRAINT fk_stock_count_lines__reason_id FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id)
);
ALTER TABLE stock_count_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_count_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_count_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE stock_adjustments (                      -- DC (adjustments, disposal, opening stock)
  id uuid CONSTRAINT pk_stock_adjustments PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL,
  doc_no text NOT NULL,
  kind text NOT NULL CONSTRAINT ck_stock_adjustments__kind CHECK (kind IN ('increase','decrease','damage','loss','gift','internal_use','disposal',
                                     'quarantine_in','quarantine_release','opening','count')),
  reason_id uuid,                                     -- required unless kind IN ('opening','count') (Go)
  stock_count_id uuid,                                -- set when kind = 'count'
  business_date date NOT NULL, note text,
  status text NOT NULL DEFAULT 'posted' CONSTRAINT ck_stock_adjustments__status CHECK (status IN ('posted','reversed')),
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL,
  reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_stock_adjustments__id UNIQUE (tenant_id, id), CONSTRAINT ux_stock_adjustments__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_stock_adjustments__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT ck_stock_adjustments__kind_stock_count_id CHECK ((kind = 'count') = (stock_count_id IS NOT NULL)),
  CONSTRAINT fk_stock_adjustments__reason_id FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id),
  CONSTRAINT fk_stock_adjustments__stock_count_id FOREIGN KEY (tenant_id, stock_count_id) REFERENCES stock_counts (tenant_id, id),
  CONSTRAINT fk_stock_adjustments__warehouse_id FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id)
);
ALTER TABLE stock_adjustments ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_adjustments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_adjustments
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE stock_adjustment_lines (
  id uuid CONSTRAINT pk_stock_adjustment_lines PRIMARY KEY, tenant_id uuid NOT NULL, stock_adjustment_id uuid NOT NULL,
  sku_id uuid NOT NULL, sku_code_snap text NOT NULL, name_snap text NOT NULL, variant_label_snap text NOT NULL DEFAULT '',
  unit_id uuid NOT NULL, unit_name_snap text NOT NULL, unit_factor_snap numeric(18,6) NOT NULL CONSTRAINT ck_stock_adjustment_lines__unit_factor_snap CHECK (unit_factor_snap > 0),
  qty numeric(18,4) NOT NULL CONSTRAINT ck_stock_adjustment_lines__qty CHECK (qty > 0),
  base_qty numeric(18,4) NOT NULL,
  color_id uuid, spec_text text,
  CONSTRAINT ck_stock_adjustment_lines__base_qty_factor CHECK (base_qty = qty * unit_factor_snap),
  CONSTRAINT fk_stock_adjustment_lines__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_stock_adjustment_lines__unit_id FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_stock_adjustment_lines__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  direction smallint NOT NULL CONSTRAINT ck_stock_adjustment_lines__direction CHECK (direction IN (-1, 1)),
  lot_id uuid,                                        -- chosen lot (required when color stock exists)
  lot_no text, expiry_date date,                      -- for increases/opening that create lots
  unit_cost numeric(19,4) CONSTRAINT ck_stock_adjustment_lines__unit_cost CHECK (unit_cost IS NULL OR unit_cost >= 0),   -- opening/increase only
  CONSTRAINT ux_stock_adjustment_lines__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_stock_adjustment_lines__stock_adjustment_id FOREIGN KEY (tenant_id, stock_adjustment_id) REFERENCES stock_adjustments (tenant_id, id),
  CONSTRAINT fk_stock_adjustment_lines__lot_id FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id)
);
ALTER TABLE stock_adjustment_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_adjustment_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_adjustment_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
