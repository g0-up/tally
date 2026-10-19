SET search_path = tally;

CREATE TABLE lots (
  id uuid CONSTRAINT pk_lots PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, sku_id uuid NOT NULL,
  lot_no text NOT NULL DEFAULT '',                    -- '' for SKUs without lot tracking (one default lot per color)
  expiry_date date,
  color_id uuid,                                      -- NULL = Chưa gán màu
  is_quarantine bool NOT NULL DEFAULT false,
  source_doc_type text, source_doc_line_id uuid,      -- first receipt line (traceability)
  received_on date NOT NULL,                          -- FEFO tie-breaker
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_lots__id UNIQUE (tenant_id, id),
  CONSTRAINT ux_lots__identity UNIQUE NULLS NOT DISTINCT (tenant_id, warehouse_id, sku_id, lot_no, expiry_date, color_id, is_quarantine),
  CONSTRAINT fk_lots__warehouse_id FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id),
  CONSTRAINT fk_lots__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_lots__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id)
);
CREATE INDEX ix_lots__sku_color ON lots (tenant_id, sku_id, color_id, expiry_date);
CREATE INDEX ix_lots__expiry ON lots (tenant_id, expiry_date) WHERE expiry_date IS NOT NULL;
ALTER TABLE lots ENABLE ROW LEVEL SECURITY;
ALTER TABLE lots FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON lots
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE stock_moves (                            -- append-only
  id uuid CONSTRAINT pk_stock_moves PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL,
  sku_id uuid NOT NULL, lot_id uuid NOT NULL,
  color_id uuid,                                      -- copied from lot for reports and invariant checks
  qty numeric(18,4) NOT NULL,                         -- signed, base unit; 0 only for cost_adjustment
  unit_cost numeric(19,4) NOT NULL CONSTRAINT ck_stock_moves__unit_cost CHECK (unit_cost >= 0),
  value_amount numeric(19,4) NOT NULL,                -- signed inventory value delta (qty × cost, or cost-only delta)
  move_type text NOT NULL CONSTRAINT ck_stock_moves__move_type CHECK (move_type IN ('receipt','purchase_return','sale','sales_return',
      'adjustment_in','adjustment_out','count_gain','count_loss','assembly_consume','assembly_produce',
      'disassembly_consume','disassembly_return','quarantine_out','quarantine_in','opening',
      'cost_adjustment','reversal')),                 -- cost_adjustment after receipt (landed cost, final price)
  doc_type text NOT NULL, doc_id uuid NOT NULL, doc_line_id uuid,
  business_date date NOT NULL,
  posted_at timestamptz NOT NULL, posted_by uuid NOT NULL,
  reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_stock_moves__id UNIQUE (tenant_id, id),
  CONSTRAINT ck_stock_moves__move_type_qty CHECK ((move_type = 'cost_adjustment') = (qty = 0)),
  CONSTRAINT fk_stock_moves__lot_id FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id),
  CONSTRAINT fk_stock_moves__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_stock_moves__reversal_of FOREIGN KEY (tenant_id, reversal_of) REFERENCES stock_moves (tenant_id, id),
  CONSTRAINT fk_stock_moves__warehouse_id FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id)
);
CREATE INDEX ix_stock_moves__card ON stock_moves (tenant_id, sku_id, business_date, id);   -- stock card
CREATE INDEX ix_stock_moves__doc ON stock_moves (tenant_id, doc_type, doc_id);             -- trace back
CREATE INDEX ix_stock_moves__lot ON stock_moves (tenant_id, lot_id, business_date);
CREATE UNIQUE INDEX ux_stock_moves__one_reversal ON stock_moves (tenant_id, reversal_of) WHERE reversal_of IS NOT NULL;
REVOKE UPDATE, DELETE ON stock_moves FROM tally_app;
ALTER TABLE stock_moves ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_moves FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_moves
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE lot_balances (                           -- projection
  tenant_id uuid NOT NULL, lot_id uuid NOT NULL,
  sku_id uuid NOT NULL, color_id uuid,                -- denormalised for per-color totals
  qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_lot_balances__qty CHECK (qty >= 0),
  updated_at timestamptz NOT NULL,
  CONSTRAINT pk_lot_balances PRIMARY KEY (tenant_id, lot_id),
  CONSTRAINT fk_lot_balances__lot_id FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id)
);
CREATE INDEX ix_lot_balances__sku_color ON lot_balances (tenant_id, sku_id, color_id) WHERE qty > 0;
ALTER TABLE lot_balances ENABLE ROW LEVEL SECURITY;
ALTER TABLE lot_balances FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON lot_balances
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE sku_stock (                              -- projection and lock point
  tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, sku_id uuid NOT NULL,
  on_hand numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_sku_stock__on_hand CHECK (on_hand >= 0),
  quarantine_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_sku_stock__quarantine_qty CHECK (quarantine_qty >= 0),   -- "cách ly Y"
  expired_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_sku_stock__expired_qty CHECK (expired_qty >= 0),         -- rolled daily by worker
  sellable numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_sku_stock__sellable CHECK (sellable >= 0),
  reserved numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_sku_stock__reserved CHECK (reserved >= 0),
  avg_cost numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_sku_stock__avg_cost CHECK (avg_cost >= 0),
  stock_value numeric(19,4) NOT NULL DEFAULT 0,       -- Σ value_amount, basis of avg_cost
  last_move_at timestamptz,
  updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT pk_sku_stock PRIMARY KEY (tenant_id, warehouse_id, sku_id),
  CONSTRAINT ck_sku_stock__sellable_on_hand_quarantine_qty_expired_qty CHECK (sellable = on_hand - quarantine_qty - expired_qty),
  -- reserved ≤ sellable is checked by inventory.ReservationService under FOR UPDATE, not by CHECK:
  -- an expiry roll or quarantine may lower sellable below existing reservations, which must surface as a shortage warning
  CONSTRAINT fk_sku_stock__warehouse_id FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id),
  CONSTRAINT fk_sku_stock__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id)
);
ALTER TABLE sku_stock ENABLE ROW LEVEL SECURITY;
ALTER TABLE sku_stock FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sku_stock
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
