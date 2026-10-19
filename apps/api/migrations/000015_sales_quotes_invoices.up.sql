SET search_path = tally;

-- Sales line tables carry the goods snapshot columns with sku_id and sku_code_snap nullable (a
-- mix_pack header line has no SKU) plus the sales pricing columns.

CREATE TABLE quotes (                                 -- BG
  id uuid CONSTRAINT pk_quotes PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  customer_party_id uuid NOT NULL,
  quote_date date NOT NULL, valid_until date NOT NULL,
  status text NOT NULL DEFAULT 'draft' CONSTRAINT ck_quotes__status CHECK (status IN ('draft','sent','accepted','rejected','converted')),
  -- 'expired' is derived in Go: status IN ('draft','sent') AND valid_until < business_date (no DB job)
  goods_amount numeric(19,4) NOT NULL DEFAULT 0, discount_amount numeric(19,4) NOT NULL DEFAULT 0,
  tax_amount numeric(19,4) NOT NULL DEFAULT 0, total_amount numeric(19,4) NOT NULL DEFAULT 0,
  campaign_id uuid, note text,
  converted_invoice_id uuid,
  copied_from_id uuid,
  revision_no int NOT NULL DEFAULT 1,                 -- "phiên bản N"; :revise increments it, prior revision snapshot kept in the audit log
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_quotes__id UNIQUE (tenant_id, id), CONSTRAINT ux_quotes__doc_no UNIQUE (tenant_id, doc_no),
  CONSTRAINT ck_quotes__valid_until_quote_date CHECK (valid_until >= quote_date), CONSTRAINT ck_quotes__revision_no CHECK (revision_no >= 1),
  CONSTRAINT ck_quotes__status_converted_invoice_id CHECK ((status = 'converted') = (converted_invoice_id IS NOT NULL)),
  CONSTRAINT fk_quotes__customer_party_id FOREIGN KEY (tenant_id, customer_party_id) REFERENCES customer_profiles (tenant_id, party_id)
);
CREATE INDEX ix_quotes__status ON quotes (tenant_id, status, valid_until);
ALTER TABLE quotes ENABLE ROW LEVEL SECURITY;
ALTER TABLE quotes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON quotes
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE quote_lines (                            -- may pick any of the 7 colors
  id uuid CONSTRAINT pk_quote_lines PRIMARY KEY, tenant_id uuid NOT NULL, quote_id uuid NOT NULL, line_no int NOT NULL,
  sku_id uuid, sku_code_snap text, name_snap text NOT NULL, variant_label_snap text NOT NULL DEFAULT '',   -- sku_id NULL only on a mix_pack header line
  unit_id uuid NOT NULL, unit_name_snap text NOT NULL, unit_factor_snap numeric(18,6) NOT NULL CONSTRAINT ck_quote_lines__unit_factor_snap CHECK (unit_factor_snap > 0),
  qty numeric(18,4) NOT NULL CONSTRAINT ck_quote_lines__qty CHECK (qty > 0),
  base_qty numeric(18,4) NOT NULL,
  color_id uuid, spec_text text,
  CONSTRAINT ck_quote_lines__base_qty_factor CHECK (base_qty = qty * unit_factor_snap),
  CONSTRAINT fk_quote_lines__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_quote_lines__unit_id FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_quote_lines__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  line_kind text NOT NULL DEFAULT 'item'
    CONSTRAINT ck_quote_lines__line_kind CHECK (line_kind IN ('item','mix_pack','mix_flavor','combo','combo_component','gift')),
  parent_line_id uuid,                                -- mix_flavor → mix_pack, combo_component → combo
  mix_key text, mix_group_id uuid,                    -- keeps a split mix pack reopenable as one "thùng mix"
  unit_price numeric(19,4) NOT NULL CONSTRAINT ck_quote_lines__unit_price CHECK (unit_price >= 0),
  discount_amount numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_quote_lines__discount_amount CHECK (discount_amount >= 0),     -- line discount
  order_discount_alloc numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_quote_lines__order_discount_alloc CHECK (order_discount_alloc >= 0),
  tax_rate_snap numeric(7,4) NOT NULL DEFAULT 0, tax_amount numeric(19,4) NOT NULL DEFAULT 0,
  line_amount numeric(19,4) NOT NULL,                 -- after line + allocated order discount, tax per tenant setting
  min_price_snap numeric(19,4),                       -- minimum price evidence
  price_source jsonb NOT NULL DEFAULT '{}',           -- {kind, price_list_id, color_id, unit_id, min_qty, promotions[]}
  CONSTRAINT ck_quote_lines__line_kind_parent_line_id CHECK ((line_kind IN ('mix_flavor','combo_component')) = (parent_line_id IS NOT NULL)),
  CONSTRAINT ck_quote_lines__line_kind_mix_group_id CHECK ((line_kind IN ('mix_pack','mix_flavor')) = (mix_group_id IS NOT NULL)),
  CONSTRAINT ck_quote_lines__sku_id_line_kind CHECK ((sku_id IS NULL) = (line_kind = 'mix_pack')),
  CONSTRAINT fk_quote_lines__mix_group_id FOREIGN KEY (tenant_id, mix_group_id) REFERENCES mix_groups (tenant_id, id),
  CONSTRAINT ux_quote_lines__id UNIQUE (tenant_id, id), CONSTRAINT ux_quote_lines__quote_id_line_no UNIQUE (tenant_id, quote_id, line_no),
  CONSTRAINT fk_quote_lines__quote_id FOREIGN KEY (tenant_id, quote_id) REFERENCES quotes (tenant_id, id),
  CONSTRAINT fk_quote_lines__parent_line_id FOREIGN KEY (tenant_id, parent_line_id) REFERENCES quote_lines (tenant_id, id)
);
ALTER TABLE quote_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE quote_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON quote_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE invoices (                               -- SO, internal sales order ("không phải hóa đơn điện tử")
  id uuid CONSTRAINT pk_invoices PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  customer_party_id uuid NOT NULL,
  payer_party_id uuid NOT NULL,                       -- "Bên trả tiền"; defaults to customer
  invoice_date date NOT NULL, due_date date,
  status text NOT NULL DEFAULT 'draft'
    CONSTRAINT ck_invoices__status CHECK (status IN ('draft','confirmed','partially_delivered','completed','cancelled_remaining')),
  goods_amount numeric(19,4) NOT NULL DEFAULT 0, discount_amount numeric(19,4) NOT NULL DEFAULT 0,
  tax_amount numeric(19,4) NOT NULL DEFAULT 0, total_amount numeric(19,4) NOT NULL DEFAULT 0,
  delivered_amount numeric(19,4) NOT NULL DEFAULT 0,  -- Σ stock outs, projection
  quote_id uuid, campaign_id uuid, copied_from_id uuid,
  note text,
  current_version int NOT NULL DEFAULT 0,             -- 0 = never confirmed; n = latest invoice_versions.version_no
  confirmed_at timestamptz, confirmed_by uuid,
  idempotency_key text,                               -- confirmation key
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_invoices__id UNIQUE (tenant_id, id), CONSTRAINT ux_invoices__doc_no UNIQUE (tenant_id, doc_no), CONSTRAINT ux_invoices__idempotency_key UNIQUE (tenant_id, idempotency_key),
  CONSTRAINT fk_invoices__customer_party_id FOREIGN KEY (tenant_id, customer_party_id) REFERENCES customer_profiles (tenant_id, party_id),
  CONSTRAINT fk_invoices__payer_party_id FOREIGN KEY (tenant_id, payer_party_id) REFERENCES customer_profiles (tenant_id, party_id),
  CONSTRAINT fk_invoices__quote_id FOREIGN KEY (tenant_id, quote_id) REFERENCES quotes (tenant_id, id)
);
CREATE INDEX ix_invoices__customer ON invoices (tenant_id, customer_party_id, invoice_date DESC);
CREATE INDEX ix_invoices__status ON invoices (tenant_id, status, invoice_date DESC);
ALTER TABLE invoices ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoices
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE quotes ADD CONSTRAINT fk_quotes__converted_invoice_id
  FOREIGN KEY (tenant_id, converted_invoice_id) REFERENCES invoices (tenant_id, id);

CREATE TABLE invoice_lines (                          -- columns on screen: Tên SP, Giá, CK, ĐVT, SL, Thành tiền
  id uuid CONSTRAINT pk_invoice_lines PRIMARY KEY, tenant_id uuid NOT NULL, invoice_id uuid NOT NULL, line_no int NOT NULL,
  sku_id uuid, sku_code_snap text, name_snap text NOT NULL, variant_label_snap text NOT NULL DEFAULT '',   -- sku_id NULL only on a mix_pack header line
  unit_id uuid NOT NULL, unit_name_snap text NOT NULL, unit_factor_snap numeric(18,6) NOT NULL CONSTRAINT ck_invoice_lines__unit_factor_snap CHECK (unit_factor_snap > 0),
  qty numeric(18,4) NOT NULL CONSTRAINT ck_invoice_lines__qty CHECK (qty > 0),
  base_qty numeric(18,4) NOT NULL,
  color_id uuid, spec_text text,
  CONSTRAINT ck_invoice_lines__base_qty_factor CHECK (base_qty = qty * unit_factor_snap),
  CONSTRAINT fk_invoice_lines__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_invoice_lines__unit_id FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_invoice_lines__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  line_kind text NOT NULL DEFAULT 'item'
    CONSTRAINT ck_invoice_lines__line_kind CHECK (line_kind IN ('item','mix_pack','mix_flavor','combo','combo_component','gift')),
  parent_line_id uuid,                                -- mix_flavor → mix_pack, combo_component → combo
  mix_key text, mix_group_id uuid,                    -- keeps a split mix pack reopenable as one "thùng mix"
  unit_price numeric(19,4) NOT NULL CONSTRAINT ck_invoice_lines__unit_price CHECK (unit_price >= 0),
  discount_amount numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_invoice_lines__discount_amount CHECK (discount_amount >= 0),     -- line discount
  order_discount_alloc numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_invoice_lines__order_discount_alloc CHECK (order_discount_alloc >= 0),
  tax_rate_snap numeric(7,4) NOT NULL DEFAULT 0, tax_amount numeric(19,4) NOT NULL DEFAULT 0,
  line_amount numeric(19,4) NOT NULL,                 -- after line + allocated order discount, tax per tenant setting
  min_price_snap numeric(19,4),                       -- minimum price evidence
  price_source jsonb NOT NULL DEFAULT '{}',           -- {kind, price_list_id, color_id, unit_id, min_qty, promotions[]}
  CONSTRAINT ck_invoice_lines__line_kind_parent_line_id CHECK ((line_kind IN ('mix_flavor','combo_component')) = (parent_line_id IS NOT NULL)),
  CONSTRAINT ck_invoice_lines__line_kind_mix_group_id CHECK ((line_kind IN ('mix_pack','mix_flavor')) = (mix_group_id IS NOT NULL)),
  CONSTRAINT ck_invoice_lines__sku_id_line_kind CHECK ((sku_id IS NULL) = (line_kind = 'mix_pack')),
  CONSTRAINT fk_invoice_lines__mix_group_id FOREIGN KEY (tenant_id, mix_group_id) REFERENCES mix_groups (tenant_id, id),
  reserved_base_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_invoice_lines__reserved_base_qty CHECK (reserved_base_qty >= 0),
  backorder_base_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_invoice_lines__backorder_base_qty CHECK (backorder_base_qty >= 0),
  delivered_base_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_invoice_lines__delivered_base_qty CHECK (delivered_base_qty >= 0),
  cancelled_base_qty numeric(18,4) NOT NULL DEFAULT 0 CONSTRAINT ck_invoice_lines__cancelled_base_qty CHECK (cancelled_base_qty >= 0),
  CONSTRAINT ux_invoice_lines__id UNIQUE (tenant_id, id), CONSTRAINT ux_invoice_lines__invoice_id_line_no UNIQUE (tenant_id, invoice_id, line_no),
  CONSTRAINT ck_invoice_lines__closed_within_qty CHECK (delivered_base_qty + cancelled_base_qty <= base_qty),
  CONSTRAINT ck_invoice_lines__allocated_within_qty CHECK (reserved_base_qty + backorder_base_qty + delivered_base_qty + cancelled_base_qty <= base_qty
         OR line_kind IN ('mix_pack','combo')),       -- header lines carry no stock
  CONSTRAINT fk_invoice_lines__invoice_id FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id),
  CONSTRAINT fk_invoice_lines__parent_line_id FOREIGN KEY (tenant_id, parent_line_id) REFERENCES invoice_lines (tenant_id, id)
);
ALTER TABLE invoice_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoice_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE invoice_versions (                       -- append-only
  id uuid CONSTRAINT pk_invoice_versions PRIMARY KEY, tenant_id uuid NOT NULL, invoice_id uuid NOT NULL,
  version_no int NOT NULL CONSTRAINT ck_invoice_versions__version_no CHECK (version_no > 0),
  snapshot jsonb NOT NULL,                            -- full header + lines as confirmed
  change_summary text,                                -- required for version_no > 1 (Go)
  material_change bool NOT NULL DEFAULT false,        -- invalidates earlier exception confirmations
  created_by uuid NOT NULL, created_at timestamptz NOT NULL,
  CONSTRAINT ux_invoice_versions__invoice_id_version_no UNIQUE (tenant_id, invoice_id, version_no),
  CONSTRAINT fk_invoice_versions__invoice_id FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
REVOKE UPDATE, DELETE ON invoice_versions FROM tally_app;
ALTER TABLE invoice_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE invoice_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON invoice_versions
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
