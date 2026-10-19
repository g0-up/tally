SET search_path = tally;

CREATE TABLE landed_cost_allocations (
  id uuid CONSTRAINT pk_landed_cost_allocations PRIMARY KEY, tenant_id uuid NOT NULL, receipt_id uuid NOT NULL,
  cost_type text NOT NULL CONSTRAINT ck_landed_cost_allocations__cost_type CHECK (cost_type IN ('shipping','loading','other')),
  description text, amount numeric(19,4) NOT NULL CONSTRAINT ck_landed_cost_allocations__amount CHECK (amount > 0),
  method text NOT NULL DEFAULT 'by_value' CONSTRAINT ck_landed_cost_allocations__method CHECK (method IN ('by_value','by_qty')),
  payee_party_id uuid,                                -- NULL = paid in cash, no payable
  business_date date NOT NULL,
  posted_at timestamptz NOT NULL, posted_by uuid NOT NULL,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_landed_cost_allocations__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_landed_cost_allocations__receipt_id FOREIGN KEY (tenant_id, receipt_id) REFERENCES receipts (tenant_id, id),
  CONSTRAINT fk_landed_cost_allocations__payee_party_id FOREIGN KEY (tenant_id, payee_party_id) REFERENCES parties (tenant_id, id)
);
ALTER TABLE landed_cost_allocations ENABLE ROW LEVEL SECURITY;
ALTER TABLE landed_cost_allocations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON landed_cost_allocations
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE landed_cost_allocation_lines (           -- Σ amount = allocation.amount (Go, verify)
  tenant_id uuid NOT NULL, landed_cost_allocation_id uuid NOT NULL, receipt_line_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL CONSTRAINT ck_landed_cost_allocation_lines__amount CHECK (amount >= 0),
  on_hand_amount numeric(19,4) NOT NULL,              -- portion still in stock → cost_adjustment move
  sold_amount numeric(19,4) NOT NULL,                 -- portion already sold → COGS adjustment
  CONSTRAINT pk_landed_cost_allocation_lines PRIMARY KEY (tenant_id, landed_cost_allocation_id, receipt_line_id),
  CONSTRAINT ck_landed_cost_allocation_lines__amount_split CHECK (on_hand_amount + sold_amount = amount),
  CONSTRAINT fk_landed_cost_allocation_lines__landed_cost_allocation_id FOREIGN KEY (tenant_id, landed_cost_allocation_id) REFERENCES landed_cost_allocations (tenant_id, id),
  CONSTRAINT fk_landed_cost_allocation_lines__receipt_line_id FOREIGN KEY (tenant_id, receipt_line_id) REFERENCES receipt_lines (tenant_id, id)
);
ALTER TABLE landed_cost_allocation_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE landed_cost_allocation_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON landed_cost_allocation_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE supplier_documents (                     -- supplier invoice matched to receipts
  id uuid CONSTRAINT pk_supplier_documents PRIMARY KEY, tenant_id uuid NOT NULL, supplier_party_id uuid NOT NULL,
  document_no text NOT NULL, document_date date NOT NULL,
  goods_amount numeric(19,4) NOT NULL, tax_amount numeric(19,4) NOT NULL DEFAULT 0,
  total_amount numeric(19,4) NOT NULL,
  match_status text NOT NULL DEFAULT 'unmatched' CONSTRAINT ck_supplier_documents__match_status CHECK (match_status IN ('unmatched','matched','variance')),
  variance_amount numeric(19,4) NOT NULL DEFAULT 0,
  note text,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_supplier_documents__id UNIQUE (tenant_id, id), CONSTRAINT ux_supplier_documents__supplier_party_id_document_no UNIQUE (tenant_id, supplier_party_id, document_no),
  CONSTRAINT fk_supplier_documents__supplier_party_id FOREIGN KEY (tenant_id, supplier_party_id) REFERENCES supplier_profiles (tenant_id, party_id)
);
ALTER TABLE supplier_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE supplier_documents FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON supplier_documents
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE supplier_document_receipts (             -- one document ↔ many receipts
  tenant_id uuid NOT NULL, supplier_document_id uuid NOT NULL, receipt_id uuid NOT NULL,
  matched_amount numeric(19,4) NOT NULL,
  CONSTRAINT pk_supplier_document_receipts PRIMARY KEY (tenant_id, supplier_document_id, receipt_id),
  CONSTRAINT fk_supplier_document_receipts__supplier_document_id FOREIGN KEY (tenant_id, supplier_document_id) REFERENCES supplier_documents (tenant_id, id),
  CONSTRAINT fk_supplier_document_receipts__receipt_id FOREIGN KEY (tenant_id, receipt_id) REFERENCES receipts (tenant_id, id)
);
ALTER TABLE supplier_document_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE supplier_document_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON supplier_document_receipts
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE purchase_returns (                       -- XT
  id uuid CONSTRAINT pk_purchase_returns PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, doc_no text NOT NULL,
  supplier_party_id uuid NOT NULL, receipt_id uuid NOT NULL,
  reason_id uuid NOT NULL,
  settlement text NOT NULL CONSTRAINT ck_purchase_returns__settlement CHECK (settlement IN ('credit_payable','refund','replacement')),
  business_date date NOT NULL,
  total_amount numeric(19,4) NOT NULL,
  status text NOT NULL DEFAULT 'posted' CONSTRAINT ck_purchase_returns__status CHECK (status IN ('posted','reversed')),
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_purchase_returns__id UNIQUE (tenant_id, id), CONSTRAINT ux_purchase_returns__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_purchase_returns__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT fk_purchase_returns__supplier_party_id FOREIGN KEY (tenant_id, supplier_party_id) REFERENCES supplier_profiles (tenant_id, party_id),
  CONSTRAINT fk_purchase_returns__receipt_id FOREIGN KEY (tenant_id, receipt_id) REFERENCES receipts (tenant_id, id),
  CONSTRAINT fk_purchase_returns__reason_id FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id),
  CONSTRAINT fk_purchase_returns__warehouse_id FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id)
);
ALTER TABLE purchase_returns ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_returns FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_returns
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE purchase_return_lines (                  -- exact lot and color of the receipt line
  id uuid CONSTRAINT pk_purchase_return_lines PRIMARY KEY, tenant_id uuid NOT NULL, purchase_return_id uuid NOT NULL,
  receipt_line_id uuid NOT NULL, lot_id uuid NOT NULL,
  sku_id uuid NOT NULL, sku_code_snap text NOT NULL, name_snap text NOT NULL, variant_label_snap text NOT NULL DEFAULT '',
  unit_id uuid NOT NULL, unit_name_snap text NOT NULL, unit_factor_snap numeric(18,6) NOT NULL CONSTRAINT ck_purchase_return_lines__unit_factor_snap CHECK (unit_factor_snap > 0),
  qty numeric(18,4) NOT NULL CONSTRAINT ck_purchase_return_lines__qty CHECK (qty > 0),
  base_qty numeric(18,4) NOT NULL,
  color_id uuid, spec_text text,
  CONSTRAINT ck_purchase_return_lines__base_qty_factor CHECK (base_qty = qty * unit_factor_snap),
  CONSTRAINT fk_purchase_return_lines__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_purchase_return_lines__unit_id FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_purchase_return_lines__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  unit_cost numeric(19,4) NOT NULL, line_amount numeric(19,4) NOT NULL,
  CONSTRAINT ux_purchase_return_lines__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_purchase_return_lines__purchase_return_id FOREIGN KEY (tenant_id, purchase_return_id) REFERENCES purchase_returns (tenant_id, id),
  CONSTRAINT fk_purchase_return_lines__receipt_line_id FOREIGN KEY (tenant_id, receipt_line_id) REFERENCES receipt_lines (tenant_id, id),
  CONSTRAINT fk_purchase_return_lines__lot_id FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id)
);
ALTER TABLE purchase_return_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_return_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_return_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE purchase_price_adjustments (             -- DG (post-purchase discount, final price)
  id uuid CONSTRAINT pk_purchase_price_adjustments PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  supplier_party_id uuid NOT NULL, receipt_id uuid NOT NULL,
  kind text NOT NULL CONSTRAINT ck_purchase_price_adjustments__kind CHECK (kind IN ('final_price','post_purchase_discount')),
  reason_id uuid NOT NULL,
  business_date date NOT NULL,
  payable_delta numeric(19,4) NOT NULL,               -- + increases, − decreases payable (credit note when −)
  status text NOT NULL DEFAULT 'posted' CONSTRAINT ck_purchase_price_adjustments__status CHECK (status IN ('posted','reversed')),
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_purchase_price_adjustments__id UNIQUE (tenant_id, id), CONSTRAINT ux_purchase_price_adjustments__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_purchase_price_adjustments__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT fk_purchase_price_adjustments__supplier_party_id FOREIGN KEY (tenant_id, supplier_party_id) REFERENCES supplier_profiles (tenant_id, party_id),
  CONSTRAINT fk_purchase_price_adjustments__receipt_id FOREIGN KEY (tenant_id, receipt_id) REFERENCES receipts (tenant_id, id),
  CONSTRAINT fk_purchase_price_adjustments__reason_id FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id)
);
ALTER TABLE purchase_price_adjustments ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_price_adjustments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_price_adjustments
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE purchase_price_adjustment_lines (
  tenant_id uuid NOT NULL, purchase_price_adjustment_id uuid NOT NULL, receipt_line_id uuid NOT NULL,
  old_unit_price numeric(19,4) NOT NULL, new_unit_price numeric(19,4) NOT NULL CONSTRAINT ck_purchase_price_adjustment_lines__new_unit_price CHECK (new_unit_price >= 0),
  base_qty numeric(18,4) NOT NULL,
  on_hand_amount numeric(19,4) NOT NULL,              -- → cost_adjustment stock move
  sold_amount numeric(19,4) NOT NULL,                 -- → COGS adjustment in reporting
  CONSTRAINT pk_purchase_price_adjustment_lines PRIMARY KEY (tenant_id, purchase_price_adjustment_id, receipt_line_id),
  CONSTRAINT fk_purchase_price_adjustment_lines__adjustment_id FOREIGN KEY (tenant_id, purchase_price_adjustment_id) REFERENCES purchase_price_adjustments (tenant_id, id),
  CONSTRAINT fk_purchase_price_adjustment_lines__receipt_line_id FOREIGN KEY (tenant_id, receipt_line_id) REFERENCES receipt_lines (tenant_id, id)
);
ALTER TABLE purchase_price_adjustment_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE purchase_price_adjustment_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON purchase_price_adjustment_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE cogs_adjustments (                       -- cost delta for quantities already sold (gross margin)
  id uuid CONSTRAINT pk_cogs_adjustments PRIMARY KEY, tenant_id uuid NOT NULL, sku_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL,
  source_doc_type text NOT NULL CONSTRAINT ck_cogs_adjustments__source_doc_type CHECK (source_doc_type IN ('landed_cost_allocation','purchase_price_adjustment')),
  source_doc_id uuid NOT NULL, business_date date NOT NULL, created_at timestamptz NOT NULL,
  CONSTRAINT ux_cogs_adjustments__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_cogs_adjustments__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id)
);
REVOKE UPDATE, DELETE ON cogs_adjustments FROM tally_app;
ALTER TABLE cogs_adjustments ENABLE ROW LEVEL SECURITY;
ALTER TABLE cogs_adjustments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON cogs_adjustments
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
