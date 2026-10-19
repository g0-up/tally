SET search_path = tally;

CREATE TABLE purchase_orders (                        -- PO
  id uuid CONSTRAINT pk_purchase_orders PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  supplier_party_id uuid NOT NULL,
  order_date date NOT NULL, expected_date date,
  status text NOT NULL DEFAULT 'draft' CONSTRAINT ck_purchase_orders__status CHECK (status IN ('draft','confirmed','partially_received','completed','closed_short')),
  goods_amount numeric(19,4) NOT NULL DEFAULT 0, discount_amount numeric(19,4) NOT NULL DEFAULT 0,
  tax_amount numeric(19,4) NOT NULL DEFAULT 0, total_amount numeric(19,4) NOT NULL DEFAULT 0,
  note text,
  close_reason_id uuid, closed_at timestamptz,
  confirmed_at timestamptz, confirmed_by uuid,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_purchase_orders__id UNIQUE (tenant_id, id), CONSTRAINT ux_purchase_orders__doc_no UNIQUE (tenant_id, doc_no),
  CONSTRAINT ck_purchase_orders__status_close_reason_id CHECK (status <> 'closed_short' OR close_reason_id IS NOT NULL),
  CONSTRAINT fk_purchase_orders__supplier_party_id FOREIGN KEY (tenant_id, supplier_party_id) REFERENCES supplier_profiles (tenant_id, party_id),
  CONSTRAINT fk_purchase_orders__close_reason_id FOREIGN KEY (tenant_id, close_reason_id) REFERENCES reasons (tenant_id, id)
);
CREATE INDEX ix_purchase_orders__supplier ON purchase_orders (tenant_id, supplier_party_id, status);
CREATE INDEX ix_purchase_orders__expected ON purchase_orders (tenant_id, expected_date) WHERE status IN ('confirmed','partially_received');
ALTER TABLE purchase_orders ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_orders FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_orders
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE purchase_order_lines (                   -- column order on screen: Tên SP, ĐVT, SL, Quy cách, Đơn giá, Thành tiền, Giá bán lẻ
  id uuid CONSTRAINT pk_purchase_order_lines PRIMARY KEY, tenant_id uuid NOT NULL, purchase_order_id uuid NOT NULL, line_no int NOT NULL,
  sku_id uuid NOT NULL, sku_code_snap text NOT NULL, name_snap text NOT NULL, variant_label_snap text NOT NULL DEFAULT '',
  unit_id uuid NOT NULL, unit_name_snap text NOT NULL, unit_factor_snap numeric(18,6) NOT NULL CONSTRAINT ck_purchase_order_lines__unit_factor_snap CHECK (unit_factor_snap > 0),
  qty numeric(18,4) NOT NULL CONSTRAINT ck_purchase_order_lines__qty CHECK (qty > 0),
  base_qty numeric(18,4) NOT NULL,
  color_id uuid, spec_text text,
  CONSTRAINT ck_purchase_order_lines__base_qty_factor CHECK (base_qty = qty * unit_factor_snap),
  CONSTRAINT fk_purchase_order_lines__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_purchase_order_lines__unit_id FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_purchase_order_lines__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  unit_price numeric(19,4) NOT NULL CONSTRAINT ck_purchase_order_lines__unit_price CHECK (unit_price >= 0),
  discount_amount numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_purchase_order_lines__discount_amount CHECK (discount_amount >= 0),
  tax_rate_snap numeric(7,4) NOT NULL DEFAULT 0,
  line_amount numeric(19,4) NOT NULL,
  retail_price_snap numeric(19,4),                    -- "Giá bán lẻ" shown/edited on the line
  received_base_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_purchase_order_lines__received_base_qty CHECK (received_base_qty >= 0),   -- projection
  returned_base_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_purchase_order_lines__returned_base_qty CHECK (returned_base_qty >= 0),   -- projection
  CONSTRAINT ux_purchase_order_lines__id UNIQUE (tenant_id, id), CONSTRAINT ux_purchase_order_lines__purchase_order_id_line_no UNIQUE (tenant_id, purchase_order_id, line_no),
  CONSTRAINT fk_purchase_order_lines__purchase_order_id FOREIGN KEY (tenant_id, purchase_order_id) REFERENCES purchase_orders (tenant_id, id)
);
-- Đặt / nhận / trả / còn chờ: base_qty, received_base_qty, returned_base_qty, max(0, base_qty − received_base_qty)
ALTER TABLE purchase_order_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_order_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_order_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE receipts (                               -- PN
  id uuid CONSTRAINT pk_receipts PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, doc_no text NOT NULL,
  supplier_party_id uuid NOT NULL, purchase_order_id uuid,   -- NULL = nhập nhanh
  business_date date NOT NULL,
  goods_amount numeric(19,4) NOT NULL, discount_amount numeric(19,4) NOT NULL DEFAULT 0,
  tax_amount numeric(19,4) NOT NULL DEFAULT 0,
  landed_cost_amount numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_receipts__landed_cost_amount CHECK (landed_cost_amount >= 0),
  total_amount numeric(19,4) NOT NULL,                -- payable created at posting
  paid_now_amount numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_receipts__paid_now_amount CHECK (paid_now_amount >= 0),
  due_date date,
  status text NOT NULL DEFAULT 'posted' CONSTRAINT ck_receipts__status CHECK (status IN ('posted','reversed','partially_reversed')),
  note text,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL,
  reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_receipts__id UNIQUE (tenant_id, id), CONSTRAINT ux_receipts__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_receipts__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT ck_receipts__paid_now_amount_total_amount CHECK (paid_now_amount <= total_amount),
  CONSTRAINT fk_receipts__supplier_party_id FOREIGN KEY (tenant_id, supplier_party_id) REFERENCES supplier_profiles (tenant_id, party_id),
  CONSTRAINT fk_receipts__purchase_order_id FOREIGN KEY (tenant_id, purchase_order_id) REFERENCES purchase_orders (tenant_id, id),
  CONSTRAINT fk_receipts__warehouse_id FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id)
);
CREATE INDEX ix_receipts__supplier_date ON receipts (tenant_id, supplier_party_id, business_date);
ALTER TABLE receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON receipts
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE receipt_lines (
  id uuid CONSTRAINT pk_receipt_lines PRIMARY KEY, tenant_id uuid NOT NULL, receipt_id uuid NOT NULL, line_no int NOT NULL,
  purchase_order_line_id uuid,
  sku_id uuid NOT NULL, sku_code_snap text NOT NULL, name_snap text NOT NULL, variant_label_snap text NOT NULL DEFAULT '',
  unit_id uuid NOT NULL, unit_name_snap text NOT NULL, unit_factor_snap numeric(18,6) NOT NULL CONSTRAINT ck_receipt_lines__unit_factor_snap CHECK (unit_factor_snap > 0),
  qty numeric(18,4) NOT NULL CONSTRAINT ck_receipt_lines__qty CHECK (qty > 0),
  base_qty numeric(18,4) NOT NULL,
  color_id uuid, spec_text text,
  CONSTRAINT ck_receipt_lines__base_qty_factor CHECK (base_qty = qty * unit_factor_snap),
  CONSTRAINT fk_receipt_lines__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_receipt_lines__unit_id FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_receipt_lines__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  condition text NOT NULL DEFAULT 'accepted' CONSTRAINT ck_receipt_lines__condition CHECK (condition IN ('accepted','damaged','wrong_item','excess')),
  lot_no text NOT NULL DEFAULT '', expiry_date date,  -- required by SKU mode (Go)
  lot_id uuid NOT NULL,                               -- resolved/created at posting; damaged → quarantine lot
  unit_price numeric(19,4) NOT NULL CONSTRAINT ck_receipt_lines__unit_price CHECK (unit_price >= 0),
  discount_amount numeric(19,4) NOT NULL DEFAULT 0,
  tax_rate_snap numeric(7,4) NOT NULL DEFAULT 0,
  line_amount numeric(19,4) NOT NULL,
  landed_cost_amount numeric(19,4) NOT NULL DEFAULT 0,   -- allocated share
  unit_cost numeric(19,4) NOT NULL,                   -- (line_amount + landed) / base_qty, Go-rounded
  returned_base_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_receipt_lines__returned_base_qty CHECK (returned_base_qty >= 0),
  CONSTRAINT ux_receipt_lines__id UNIQUE (tenant_id, id), CONSTRAINT ux_receipt_lines__receipt_id_line_no UNIQUE (tenant_id, receipt_id, line_no),
  CONSTRAINT ck_receipt_lines__returned_base_qty_base_qty CHECK (returned_base_qty <= base_qty),
  CONSTRAINT fk_receipt_lines__receipt_id FOREIGN KEY (tenant_id, receipt_id) REFERENCES receipts (tenant_id, id),
  CONSTRAINT fk_receipt_lines__purchase_order_line_id FOREIGN KEY (tenant_id, purchase_order_line_id) REFERENCES purchase_order_lines (tenant_id, id),
  CONSTRAINT fk_receipt_lines__lot_id FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id)
);
ALTER TABLE receipt_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE receipt_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON receipt_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
