SET search_path = tally;

CREATE TABLE shifts (                                 -- CA, opened implicitly by first POS sale of the day
  id uuid CONSTRAINT pk_shifts PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  business_date date NOT NULL,
  opened_at timestamptz NOT NULL, closed_at timestamptz,
  opening_cash numeric(19,4) NOT NULL DEFAULT 0, counted_cash numeric(19,4),
  status text NOT NULL DEFAULT 'open' CONSTRAINT ck_shifts__status CHECK (status IN ('open','closed')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_shifts__id UNIQUE (tenant_id, id), CONSTRAINT ux_shifts__doc_no UNIQUE (tenant_id, doc_no)
);
CREATE UNIQUE INDEX ux_shifts__one_open ON shifts (tenant_id) WHERE status = 'open';
ALTER TABLE shifts ENABLE ROW LEVEL SECURITY;
ALTER TABLE shifts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON shifts
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE sales_documents ADD CONSTRAINT fk_sales_documents__shift_id
  FOREIGN KEY (tenant_id, shift_id) REFERENCES shifts (tenant_id, id);

CREATE TABLE pos_carts (                              -- GH: saved cart, no reservation, no posting
  id uuid CONSTRAINT pk_pos_carts PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  customer_party_id uuid, label text,
  status text NOT NULL DEFAULT 'open' CONSTRAINT ck_pos_carts__status CHECK (status IN ('open','sold')),
  total_amount numeric(19,4) NOT NULL DEFAULT 0,
  sales_document_id uuid, copied_from_id uuid,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_pos_carts__id UNIQUE (tenant_id, id), CONSTRAINT ux_pos_carts__doc_no UNIQUE (tenant_id, doc_no),
  CONSTRAINT ck_pos_carts__status_sales_document_id CHECK ((status = 'sold') = (sales_document_id IS NOT NULL)),
  CONSTRAINT fk_pos_carts__customer_party_id FOREIGN KEY (tenant_id, customer_party_id) REFERENCES customer_profiles (tenant_id, party_id),
  CONSTRAINT fk_pos_carts__sales_document_id FOREIGN KEY (tenant_id, sales_document_id) REFERENCES sales_documents (tenant_id, id)
);
ALTER TABLE pos_carts ENABLE ROW LEVEL SECURITY;
ALTER TABLE pos_carts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pos_carts
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE sales_documents ADD CONSTRAINT fk_sales_documents__pos_cart_id
  FOREIGN KEY (tenant_id, pos_cart_id) REFERENCES pos_carts (tenant_id, id);

CREATE TABLE pos_cart_lines (                         -- keeps chosen colors, incl. combo component colors
  id uuid CONSTRAINT pk_pos_cart_lines PRIMARY KEY, tenant_id uuid NOT NULL, pos_cart_id uuid NOT NULL, line_no int NOT NULL,
  sku_id uuid, sku_code_snap text, name_snap text NOT NULL, variant_label_snap text NOT NULL DEFAULT '',   -- sku_id NULL only on a mix_pack header line
  unit_id uuid NOT NULL, unit_name_snap text NOT NULL, unit_factor_snap numeric(18,6) NOT NULL CONSTRAINT ck_pos_cart_lines__unit_factor_snap CHECK (unit_factor_snap > 0),
  qty numeric(18,4) NOT NULL CONSTRAINT ck_pos_cart_lines__qty CHECK (qty > 0),
  base_qty numeric(18,4) NOT NULL,
  color_id uuid, spec_text text,
  CONSTRAINT ck_pos_cart_lines__base_qty_factor CHECK (base_qty = qty * unit_factor_snap),
  CONSTRAINT fk_pos_cart_lines__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  CONSTRAINT fk_pos_cart_lines__unit_id FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_pos_cart_lines__color_id FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  line_kind text NOT NULL DEFAULT 'item'
    CONSTRAINT ck_pos_cart_lines__line_kind CHECK (line_kind IN ('item','mix_pack','mix_flavor','combo','combo_component','gift')),
  parent_line_id uuid,                                -- mix_flavor → mix_pack, combo_component → combo
  mix_key text, mix_group_id uuid,                    -- keeps a split mix pack reopenable as one "thùng mix"
  unit_price numeric(19,4) NOT NULL CONSTRAINT ck_pos_cart_lines__unit_price CHECK (unit_price >= 0),
  discount_amount numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_pos_cart_lines__discount_amount CHECK (discount_amount >= 0),     -- line discount
  order_discount_alloc numeric(19,4) NOT NULL DEFAULT 0 CONSTRAINT ck_pos_cart_lines__order_discount_alloc CHECK (order_discount_alloc >= 0),
  tax_rate_snap numeric(7,4) NOT NULL DEFAULT 0, tax_amount numeric(19,4) NOT NULL DEFAULT 0,
  line_amount numeric(19,4) NOT NULL,                 -- after line + allocated order discount, tax per tenant setting
  min_price_snap numeric(19,4),                       -- minimum price evidence
  price_source jsonb NOT NULL DEFAULT '{}',           -- {kind, price_list_id, color_id, unit_id, min_qty, promotions[]}
  CONSTRAINT ck_pos_cart_lines__line_kind_parent_line_id CHECK ((line_kind IN ('mix_flavor','combo_component')) = (parent_line_id IS NOT NULL)),
  CONSTRAINT ck_pos_cart_lines__line_kind_mix_group_id CHECK ((line_kind IN ('mix_pack','mix_flavor')) = (mix_group_id IS NOT NULL)),
  CONSTRAINT ck_pos_cart_lines__sku_id_line_kind CHECK ((sku_id IS NULL) = (line_kind = 'mix_pack')),
  CONSTRAINT fk_pos_cart_lines__mix_group_id FOREIGN KEY (tenant_id, mix_group_id) REFERENCES mix_groups (tenant_id, id),
  CONSTRAINT ux_pos_cart_lines__id UNIQUE (tenant_id, id), CONSTRAINT ux_pos_cart_lines__pos_cart_id_line_no UNIQUE (tenant_id, pos_cart_id, line_no),
  CONSTRAINT fk_pos_cart_lines__pos_cart_id FOREIGN KEY (tenant_id, pos_cart_id) REFERENCES pos_carts (tenant_id, id),
  CONSTRAINT fk_pos_cart_lines__parent_line_id FOREIGN KEY (tenant_id, parent_line_id) REFERENCES pos_cart_lines (tenant_id, id)
);
ALTER TABLE pos_cart_lines ENABLE ROW LEVEL SECURITY;
ALTER TABLE pos_cart_lines FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pos_cart_lines
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE pos_sync_exceptions (                    -- offline sync "hàng đợi ngoại lệ"
  id uuid CONSTRAINT pk_pos_sync_exceptions PRIMARY KEY, tenant_id uuid NOT NULL,
  offline_client_id text NOT NULL, device_session_id uuid,
  payload jsonb NOT NULL, error_code text NOT NULL, error_detail text,
  status text NOT NULL DEFAULT 'open' CONSTRAINT ck_pos_sync_exceptions__status CHECK (status IN ('open','resolved','discarded')),
  resolution_doc_id uuid, resolved_by uuid, resolved_at timestamptz,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_pos_sync_exceptions__id UNIQUE (tenant_id, id), CONSTRAINT ux_pos_sync_exceptions__offline_client_id UNIQUE (tenant_id, offline_client_id)
);
ALTER TABLE pos_sync_exceptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE pos_sync_exceptions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pos_sync_exceptions
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
