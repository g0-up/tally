SET search_path = tally;

CREATE TABLE reservations (                           -- per SKU, not per color
  id uuid CONSTRAINT pk_reservations PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, sku_id uuid NOT NULL,
  source_type text NOT NULL CONSTRAINT ck_reservations__source_type CHECK (source_type IN ('invoice_line','assembly_order_item','manual')),
  source_id uuid NOT NULL, source_doc_no text,
  qty numeric(18,4) NOT NULL CONSTRAINT ck_reservations__qty CHECK (qty > 0),         -- base unit
  consumed_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_reservations__consumed_qty CHECK (consumed_qty >= 0),
  released_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_reservations__released_qty CHECK (released_qty >= 0),
  status text NOT NULL DEFAULT 'active' CONSTRAINT ck_reservations__status CHECK (status IN ('active','consumed','released')),
  expires_at timestamptz, note text,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_reservations__id UNIQUE (tenant_id, id),
  CONSTRAINT ux_reservations__source_type_source_id UNIQUE (tenant_id, source_type, source_id),
  CONSTRAINT ck_reservations__consumed_qty_released_qty_qty CHECK (consumed_qty + released_qty <= qty),
  CONSTRAINT fk_reservations__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_reservations__warehouse_id FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id)
);
CREATE INDEX ix_reservations__sku_active ON reservations (tenant_id, sku_id) WHERE status = 'active';
ALTER TABLE reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON reservations
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
