SET search_path = tally;

CREATE TABLE stock_outs (                             -- PX: posted on creation, many per invoice
  id uuid CONSTRAINT pk_stock_outs PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, doc_no text NOT NULL,
  invoice_id uuid NOT NULL,
  business_date date NOT NULL, note text,
  status text NOT NULL DEFAULT 'posted' CONSTRAINT ck_stock_outs__status CHECK (status IN ('posted','reversed')),
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_stock_outs__id UNIQUE (tenant_id, id), CONSTRAINT ux_stock_outs__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_stock_outs__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT fk_stock_outs__invoice_id FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id),
  CONSTRAINT fk_stock_outs__warehouse_id FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id)
);
ALTER TABLE stock_outs ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_outs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_outs
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE stock_out_lines (
  id uuid CONSTRAINT pk_stock_out_lines PRIMARY KEY, tenant_id uuid NOT NULL, stock_out_id uuid NOT NULL,
  invoice_line_id uuid NOT NULL,
  sku_id uuid NOT NULL, color_id uuid,                -- exact color of the invoice line; never substituted
  base_qty numeric(18,4) NOT NULL CONSTRAINT ck_stock_out_lines__base_qty CHECK (base_qty > 0),   -- ≤ reserved not yet delivered (Go)
  CONSTRAINT ux_stock_out_lines__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_stock_out_lines__stock_out_id FOREIGN KEY (tenant_id, stock_out_id) REFERENCES stock_outs (tenant_id, id),
  CONSTRAINT fk_stock_out_lines__invoice_line_id FOREIGN KEY (tenant_id, invoice_line_id) REFERENCES invoice_lines (tenant_id, id)
);
ALTER TABLE stock_out_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE stock_out_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON stock_out_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE sales_documents (                        -- BH (from PX) / HD (POS): revenue + receivable event
  id uuid CONSTRAINT pk_sales_documents PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  source text NOT NULL CONSTRAINT ck_sales_documents__source CHECK (source IN ('stock_out','pos','pos_offline','exchange')),
  stock_out_id uuid, invoice_id uuid, pos_cart_id uuid, shift_id uuid,   -- shift/cart FKs added with POS tables
  customer_party_id uuid,                             -- NULL = khách lẻ at POS
  payer_party_id uuid,
  business_date date NOT NULL,
  goods_amount numeric(19,4) NOT NULL, discount_amount numeric(19,4) NOT NULL DEFAULT 0,
  tax_amount numeric(19,4) NOT NULL DEFAULT 0, total_amount numeric(19,4) NOT NULL,
  cogs_amount numeric(19,4) NOT NULL DEFAULT 0,       -- Σ moves at posting cost
  campaign_id uuid,
  offline_client_id text, offline_created_at timestamptz,   -- offline POS sync evidence
  status text NOT NULL DEFAULT 'posted' CONSTRAINT ck_sales_documents__status CHECK (status IN ('posted','reversed','partially_reversed')),
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_sales_documents__id UNIQUE (tenant_id, id), CONSTRAINT ux_sales_documents__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_sales_documents__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT ck_sales_documents__source_stock_out_id CHECK ((source = 'stock_out') = (stock_out_id IS NOT NULL)),
  CONSTRAINT fk_sales_documents__stock_out_id FOREIGN KEY (tenant_id, stock_out_id) REFERENCES stock_outs (tenant_id, id),
  CONSTRAINT fk_sales_documents__invoice_id FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id),
  CONSTRAINT fk_sales_documents__customer_party_id FOREIGN KEY (tenant_id, customer_party_id) REFERENCES customer_profiles (tenant_id, party_id)
);
CREATE UNIQUE INDEX ux_sales_documents__stock_out ON sales_documents (tenant_id, stock_out_id) WHERE stock_out_id IS NOT NULL;
CREATE INDEX ix_sales_documents__date ON sales_documents (tenant_id, business_date, id);
CREATE INDEX ix_sales_documents__customer ON sales_documents (tenant_id, customer_party_id, business_date);
ALTER TABLE sales_documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_documents FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sales_documents
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE sales_document_lines (                   -- financial lines; stock_moves.doc_line_id points here
  id uuid CONSTRAINT pk_sales_document_lines PRIMARY KEY, tenant_id uuid NOT NULL, sales_document_id uuid NOT NULL, line_no int NOT NULL,
  invoice_line_id uuid, stock_out_line_id uuid,
  sku_id uuid, sku_code_snap text, name_snap text NOT NULL, variant_label_snap text NOT NULL DEFAULT '',   -- sku_id NULL only on a mix_pack header line
  unit_id uuid NOT NULL, unit_name_snap text NOT NULL, unit_factor_snap numeric(18,6) NOT NULL CONSTRAINT ck_sales_document_lines__unit_factor_snap CHECK (unit_factor_snap > 0),
  qty numeric(18,4) NOT NULL CONSTRAINT ck_sales_document_lines__qty CHECK (qty > 0),
  base_qty numeric(18,4) NOT NULL,
  color_id uuid, spec_text text,
  CONSTRAINT ck_sales_document_lines__base_qty_factor CHECK (base_qty = qty * unit_factor_snap),
  CONSTRAINT fk_sales_document_lines__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_sales_document_lines__unit_id FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_sales_document_lines__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  line_kind text NOT NULL DEFAULT 'item'
    CONSTRAINT ck_sales_document_lines__line_kind CHECK (line_kind IN ('item','mix_pack','mix_flavor','combo','combo_component','gift')),
  parent_line_id uuid,                                -- mix_flavor → mix_pack, combo_component → combo
  mix_key text, mix_group_id uuid,                    -- keeps a split mix pack reopenable as one "thùng mix"
  unit_price numeric(19,4) NOT NULL CONSTRAINT ck_sales_document_lines__unit_price CHECK (unit_price >= 0),
  discount_amount numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_sales_document_lines__discount_amount CHECK (discount_amount >= 0),     -- line discount
  order_discount_alloc numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_sales_document_lines__order_discount_alloc CHECK (order_discount_alloc >= 0),
  tax_rate_snap numeric(7,4) NOT NULL DEFAULT 0, tax_amount numeric(19,4) NOT NULL DEFAULT 0,
  line_amount numeric(19,4) NOT NULL,                 -- after line + allocated order discount, tax per tenant setting
  min_price_snap numeric(19,4),                       -- minimum price evidence
  price_source jsonb NOT NULL DEFAULT '{}',           -- {kind, price_list_id, color_id, unit_id, min_qty, promotions[]}
  CONSTRAINT ck_sales_document_lines__line_kind_parent_line_id CHECK ((line_kind IN ('mix_flavor','combo_component')) = (parent_line_id IS NOT NULL)),
  CONSTRAINT ck_sales_document_lines__line_kind_mix_group_id CHECK ((line_kind IN ('mix_pack','mix_flavor')) = (mix_group_id IS NOT NULL)),
  CONSTRAINT ck_sales_document_lines__sku_id_line_kind CHECK ((sku_id IS NULL) = (line_kind = 'mix_pack')),
  CONSTRAINT fk_sales_document_lines__mix_group_id FOREIGN KEY (tenant_id, mix_group_id) REFERENCES mix_groups (tenant_id, id),
  cogs_amount numeric(19,4) NOT NULL DEFAULT 0,
  returned_base_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_sales_document_lines__returned_base_qty CHECK (returned_base_qty >= 0),   -- return ≤ sold guard
  campaign_id uuid,                                   -- one primary attribution; FK added with campaigns
  CONSTRAINT ux_sales_document_lines__id UNIQUE (tenant_id, id), CONSTRAINT ux_sales_document_lines__sales_document_id_line_no UNIQUE (tenant_id, sales_document_id, line_no),
  CONSTRAINT ck_sales_document_lines__returned_base_qty_base_qty CHECK (returned_base_qty <= base_qty),
  CONSTRAINT fk_sales_document_lines__sales_document_id FOREIGN KEY (tenant_id, sales_document_id) REFERENCES sales_documents (tenant_id, id),
  CONSTRAINT fk_sales_document_lines__parent_line_id FOREIGN KEY (tenant_id, parent_line_id) REFERENCES sales_document_lines (tenant_id, id),
  CONSTRAINT fk_sales_document_lines__invoice_line_id FOREIGN KEY (tenant_id, invoice_line_id) REFERENCES invoice_lines (tenant_id, id),
  CONSTRAINT fk_sales_document_lines__stock_out_line_id FOREIGN KEY (tenant_id, stock_out_line_id) REFERENCES stock_out_lines (tenant_id, id)
);
ALTER TABLE sales_document_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE sales_document_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sales_document_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
