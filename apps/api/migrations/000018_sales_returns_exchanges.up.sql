SET search_path = tally;

CREATE TABLE sales_returns (                          -- TH
  id uuid CONSTRAINT pk_sales_returns PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, doc_no text NOT NULL,
  customer_party_id uuid,
  sales_document_id uuid,                             -- NULL = return without a source document
  reason_id uuid NOT NULL,
  business_date date NOT NULL,
  total_amount numeric(19,4) NOT NULL,
  debt_reduction_amount numeric(19,4) NOT NULL DEFAULT 0,   -- reduces open receivable first
  refund_due_amount numeric(19,4) NOT NULL DEFAULT 0,       -- excess → khoản cần hoàn (credit note)
  exchange_id uuid,
  status text NOT NULL DEFAULT 'posted' CONSTRAINT ck_sales_returns__status CHECK (status IN ('posted','reversed')),
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_sales_returns__id UNIQUE (tenant_id, id), CONSTRAINT ux_sales_returns__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_sales_returns__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT ck_sales_returns__amount_split CHECK (debt_reduction_amount + refund_due_amount = total_amount),
  CONSTRAINT fk_sales_returns__sales_document_id FOREIGN KEY (tenant_id, sales_document_id) REFERENCES sales_documents (tenant_id, id),
  CONSTRAINT fk_sales_returns__reason_id FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id),
  CONSTRAINT fk_sales_returns__warehouse_id FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id)
);
ALTER TABLE sales_returns ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_returns FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sales_returns
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE sales_return_lines (
  id uuid CONSTRAINT pk_sales_return_lines PRIMARY KEY, tenant_id uuid NOT NULL, sales_return_id uuid NOT NULL,
  sales_document_line_id uuid,                        -- color comes from here; chosen only for legacy lines without color
  sku_id uuid NOT NULL, sku_code_snap text NOT NULL, name_snap text NOT NULL, variant_label_snap text NOT NULL DEFAULT '',
  unit_id uuid NOT NULL, unit_name_snap text NOT NULL, unit_factor_snap numeric(18,6) NOT NULL CONSTRAINT ck_sales_return_lines__unit_factor_snap CHECK (unit_factor_snap > 0),
  qty numeric(18,4) NOT NULL CONSTRAINT ck_sales_return_lines__qty CHECK (qty > 0),
  base_qty numeric(18,4) NOT NULL,
  color_id uuid, spec_text text,
  CONSTRAINT ck_sales_return_lines__base_qty_factor CHECK (base_qty = qty * unit_factor_snap),
  CONSTRAINT fk_sales_return_lines__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_sales_return_lines__unit_id FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_sales_return_lines__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  unit_price numeric(19,4) NOT NULL, line_amount numeric(19,4) NOT NULL,
  unit_cost numeric(19,4) NOT NULL,                   -- original cost when traceable; sourceless returns use current average, flagged
  cost_source text NOT NULL CONSTRAINT ck_sales_return_lines__cost_source CHECK (cost_source IN ('original','current_average')),
  restock_to text NOT NULL CONSTRAINT ck_sales_return_lines__restock_to CHECK (restock_to IN ('sellable','quarantine')),
  lot_id uuid NOT NULL,                               -- original lot, or its quarantine twin
  CONSTRAINT ux_sales_return_lines__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_sales_return_lines__sales_return_id FOREIGN KEY (tenant_id, sales_return_id) REFERENCES sales_returns (tenant_id, id),
  CONSTRAINT fk_sales_return_lines__sales_document_line_id FOREIGN KEY (tenant_id, sales_document_line_id) REFERENCES sales_document_lines (tenant_id, id),
  CONSTRAINT fk_sales_return_lines__lot_id FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id)
);
ALTER TABLE sales_return_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_return_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sales_return_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE exchanges (                              -- DH: links one return + one new sale
  id uuid CONSTRAINT pk_exchanges PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  customer_party_id uuid,
  original_sales_document_id uuid NOT NULL,
  sales_return_id uuid NOT NULL, new_sales_document_id uuid NOT NULL,
  returned_amount numeric(19,4) NOT NULL, taken_amount numeric(19,4) NOT NULL,
  debt_reduction_amount numeric(19,4) NOT NULL DEFAULT 0,
  customer_pays_amount numeric(19,4) NOT NULL DEFAULT 0,    -- "Khách trả thêm"
  store_refunds_amount numeric(19,4) NOT NULL DEFAULT 0,    -- "Cửa hàng trả lại khách"
  fund_account_id uuid,
  business_date date NOT NULL,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_exchanges__id UNIQUE (tenant_id, id), CONSTRAINT ux_exchanges__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_exchanges__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT ck_exchanges__customer_pays_amount_store_refunds_amount CHECK (customer_pays_amount = 0 OR store_refunds_amount = 0),
  CONSTRAINT fk_exchanges__original_sales_document_id FOREIGN KEY (tenant_id, original_sales_document_id) REFERENCES sales_documents (tenant_id, id),
  CONSTRAINT fk_exchanges__sales_return_id FOREIGN KEY (tenant_id, sales_return_id) REFERENCES sales_returns (tenant_id, id),
  CONSTRAINT fk_exchanges__new_sales_document_id FOREIGN KEY (tenant_id, new_sales_document_id) REFERENCES sales_documents (tenant_id, id)
);
ALTER TABLE exchanges ENABLE ROW LEVEL SECURITY;
ALTER TABLE exchanges FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON exchanges
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE sales_returns ADD CONSTRAINT fk_sales_returns__exchange_id
  FOREIGN KEY (tenant_id, exchange_id) REFERENCES exchanges (tenant_id, id);
