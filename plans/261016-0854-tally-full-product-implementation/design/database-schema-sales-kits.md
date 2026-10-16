# Schema — Sales, POS & Kits

Inherits brainstorm §5 (sales and kit rows). **Δ** = change for the no-function rule; **+** = addition with its PRD reason. All tables are tenant tables with the RLS block ([conventions](database-conventions.md) §4). `<snapshot columns>` is the goods-line block from [inventory & procurement](database-schema-inventory-procurement.md) (header). Sales lines additionally carry `<sales pricing columns>`:

```sql
  line_kind text NOT NULL DEFAULT 'item'
    CHECK (line_kind IN ('item','mix_pack','mix_flavor','combo','combo_component','gift')),
  parent_line_id uuid,                                -- mix_flavor → mix_pack, combo_component → combo
  mix_key text, mix_group_id uuid,                    -- keeps a split mix pack reopenable as one "thùng mix" (§7.10)
  unit_price numeric(19,4) NOT NULL CHECK (unit_price >= 0),
  discount_amount numeric(19,4) NOT NULL DEFAULT 0 CHECK (discount_amount >= 0),     -- line discount
  order_discount_alloc numeric(19,4) NOT NULL DEFAULT 0 CHECK (order_discount_alloc >= 0),
  tax_rate_snap numeric(7,4) NOT NULL DEFAULT 0, tax_amount numeric(19,4) NOT NULL DEFAULT 0,
  line_amount numeric(19,4) NOT NULL,                 -- after line + allocated order discount, tax per tenant setting
  min_price_snap numeric(19,4),                       -- PRC-04 evidence
  price_source jsonb NOT NULL DEFAULT '{}',           -- {kind, price_list_id, color_id, unit_id, min_qty, promotions[]}
  CHECK ((line_kind IN ('mix_flavor','combo_component')) = (parent_line_id IS NOT NULL)),
  CHECK ((line_kind IN ('mix_pack','mix_flavor')) = (mix_group_id IS NOT NULL)),
  CHECK ((sku_id IS NULL) = (line_kind = 'mix_pack')),
  FOREIGN KEY (tenant_id, mix_group_id) REFERENCES mix_groups (tenant_id, id)
```

On sales line tables the snapshot block relaxes `sku_id` and `sku_code_snap` to nullable: a `mix_pack` header line has no SKU, uses the mix group's `pack_unit_id` with `unit_factor_snap = units_per_pack`, and `name_snap` = mix group name. Every other line kind has a SKU.

Calculation order (PRD §7.3) is one Go type, `pricing.DocumentCalculator`: line price → line discount → allocate order discount by eligible value → tax base per `tenant_settings.prices_include_tax` → rounding per `rounding_mode` with remainder on the largest line. Mix packs: `mix_pack` line carries the pack price and pack count; flavor children carry parts and are what posting turns into stock moves (§7.10). Migrations: `000015_sales_quotes_invoices`, `000016_sales_stock_outs_documents`, `000017_sales_pos`, `000018_sales_returns_exchanges`, `000019_kits`.

## 1. Quotes (PRC-05)

```sql
CREATE TABLE quotes (                                 -- BG
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  customer_party_id uuid NOT NULL,
  quote_date date NOT NULL, valid_until date NOT NULL,
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','sent','accepted','rejected','converted')),
  -- 'expired' is derived in Go: status IN ('draft','sent') AND valid_until < business_date (Δ no DB job)
  goods_amount numeric(19,4) NOT NULL DEFAULT 0, discount_amount numeric(19,4) NOT NULL DEFAULT 0,
  tax_amount numeric(19,4) NOT NULL DEFAULT 0, total_amount numeric(19,4) NOT NULL DEFAULT 0,
  campaign_id uuid, note text,
  converted_invoice_id uuid,
  copied_from_id uuid,
  revision_no int NOT NULL DEFAULT 1,                 -- "phiên bản N"; :revise increments it, prior revision snapshot kept in audit_log
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no),
  CHECK (valid_until >= quote_date), CHECK (revision_no >= 1),
  CHECK ((status = 'converted') = (converted_invoice_id IS NOT NULL)),
  FOREIGN KEY (tenant_id, customer_party_id) REFERENCES customer_profiles (tenant_id, party_id)
);
CREATE INDEX ix_quotes__status ON quotes (tenant_id, status, valid_until);

CREATE TABLE quote_lines (                            -- may pick any of the 7 colors (§7.9)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, quote_id uuid NOT NULL, line_no int NOT NULL,
  <snapshot columns>, <sales pricing columns>,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, quote_id, line_no),
  FOREIGN KEY (tenant_id, quote_id) REFERENCES quotes (tenant_id, id),
  FOREIGN KEY (tenant_id, parent_line_id) REFERENCES quote_lines (tenant_id, id)
);
```

## 2. Invoices (SAL-01/02/03/10)

```sql
CREATE TABLE invoices (                               -- SO, internal sales order ("không phải hóa đơn điện tử")
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  customer_party_id uuid NOT NULL,
  payer_party_id uuid NOT NULL,                       -- "Bên trả tiền"; defaults to customer
  invoice_date date NOT NULL, due_date date,
  status text NOT NULL DEFAULT 'draft'
    CHECK (status IN ('draft','confirmed','partially_delivered','completed','cancelled_remaining')),
  goods_amount numeric(19,4) NOT NULL DEFAULT 0, discount_amount numeric(19,4) NOT NULL DEFAULT 0,
  tax_amount numeric(19,4) NOT NULL DEFAULT 0, total_amount numeric(19,4) NOT NULL DEFAULT 0,
  delivered_amount numeric(19,4) NOT NULL DEFAULT 0,  -- + Σ stock outs, projection
  quote_id uuid, campaign_id uuid, copied_from_id uuid,
  note text,
  current_version int NOT NULL DEFAULT 0,             -- 0 = never confirmed; n = latest invoice_versions.version_no
  confirmed_at timestamptz, confirmed_by uuid,
  idempotency_key text,                               -- confirmation key
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  FOREIGN KEY (tenant_id, customer_party_id) REFERENCES customer_profiles (tenant_id, party_id),
  FOREIGN KEY (tenant_id, payer_party_id) REFERENCES customer_profiles (tenant_id, party_id),
  FOREIGN KEY (tenant_id, quote_id) REFERENCES quotes (tenant_id, id)
);
CREATE INDEX ix_invoices__customer ON invoices (tenant_id, customer_party_id, invoice_date DESC);
CREATE INDEX ix_invoices__status ON invoices (tenant_id, status, invoice_date DESC);
ALTER TABLE quotes ADD FOREIGN KEY (tenant_id, converted_invoice_id) REFERENCES invoices (tenant_id, id);

CREATE TABLE invoice_lines (                          -- columns on screen: Tên SP, Giá, CK, ĐVT, SL, Thành tiền
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, invoice_id uuid NOT NULL, line_no int NOT NULL,
  <snapshot columns>, <sales pricing columns>,
  reserved_base_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (reserved_base_qty >= 0),    -- + SAL-03
  backorder_base_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (backorder_base_qty >= 0),  -- + SAL-03
  delivered_base_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (delivered_base_qty >= 0),
  cancelled_base_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (cancelled_base_qty >= 0),
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, invoice_id, line_no),
  CHECK (delivered_base_qty + cancelled_base_qty <= base_qty),
  CHECK (reserved_base_qty + backorder_base_qty + delivered_base_qty + cancelled_base_qty <= base_qty
         OR line_kind IN ('mix_pack','combo')),       -- header lines carry no stock
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id),
  FOREIGN KEY (tenant_id, parent_line_id) REFERENCES invoice_lines (tenant_id, id)
);

CREATE TABLE invoice_versions (                       -- append-only (SAL-10, §7.8)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, invoice_id uuid NOT NULL,
  version_no int NOT NULL CHECK (version_no > 0),
  snapshot jsonb NOT NULL,                            -- full header + lines as confirmed
  change_summary text,                                -- required for version_no > 1 (Go)
  material_change bool NOT NULL DEFAULT false,        -- invalidates earlier exception confirmations
  created_by uuid NOT NULL, created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, invoice_id, version_no),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);
REVOKE UPDATE, DELETE ON invoice_versions FROM tally_app;
```

Draft edit/delete allowed only while `delivered_base_qty = 0` on all lines and no payment is earmarked (SAL-01); `sales.InvoiceEditSpec` owns the rule. "Khách đã thanh toán" creates a `payments` row with `earmarked_invoice_id` (advance until stock out, §7.6).

## 3. Stock outs and sales documents (SAL-04, §7.1)

```sql
CREATE TABLE stock_outs (                             -- PX: posted on creation, many per invoice
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, doc_no text NOT NULL,
  invoice_id uuid NOT NULL,
  business_date date NOT NULL, note text,
  status text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','reversed')),
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);

CREATE TABLE stock_out_lines (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, stock_out_id uuid NOT NULL,
  invoice_line_id uuid NOT NULL,
  sku_id uuid NOT NULL, color_id uuid,                -- exact color of the invoice line; never substituted
  base_qty numeric(18,4) NOT NULL CHECK (base_qty > 0),   -- ≤ reserved not yet delivered (Go)
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, stock_out_id) REFERENCES stock_outs (tenant_id, id),
  FOREIGN KEY (tenant_id, invoice_line_id) REFERENCES invoice_lines (tenant_id, id)
);

CREATE TABLE sales_documents (                        -- + BH (from PX) / HD (POS): revenue + receivable event
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  source text NOT NULL CHECK (source IN ('stock_out','pos','pos_offline','exchange')),
  stock_out_id uuid, invoice_id uuid, pos_cart_id uuid, shift_id uuid,
  customer_party_id uuid,                             -- NULL = khách lẻ at POS
  payer_party_id uuid,
  business_date date NOT NULL,
  goods_amount numeric(19,4) NOT NULL, discount_amount numeric(19,4) NOT NULL DEFAULT 0,
  tax_amount numeric(19,4) NOT NULL DEFAULT 0, total_amount numeric(19,4) NOT NULL,
  cogs_amount numeric(19,4) NOT NULL DEFAULT 0,       -- Σ moves at posting cost
  campaign_id uuid,
  offline_client_id text, offline_created_at timestamptz,   -- + SAL-13 sync evidence
  status text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','reversed','partially_reversed')),
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  CHECK ((source = 'stock_out') = (stock_out_id IS NOT NULL)),
  FOREIGN KEY (tenant_id, stock_out_id) REFERENCES stock_outs (tenant_id, id),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id),
  FOREIGN KEY (tenant_id, customer_party_id) REFERENCES customer_profiles (tenant_id, party_id)
);
CREATE UNIQUE INDEX ux_sales_documents__stock_out ON sales_documents (tenant_id, stock_out_id) WHERE stock_out_id IS NOT NULL;
CREATE INDEX ix_sales_documents__date ON sales_documents (tenant_id, business_date, id);
CREATE INDEX ix_sales_documents__customer ON sales_documents (tenant_id, customer_party_id, business_date);

CREATE TABLE sales_document_lines (                   -- financial lines; stock_moves.doc_line_id points here
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, sales_document_id uuid NOT NULL, line_no int NOT NULL,
  invoice_line_id uuid, stock_out_line_id uuid,
  <snapshot columns>, <sales pricing columns>,
  cogs_amount numeric(19,4) NOT NULL DEFAULT 0,
  returned_base_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (returned_base_qty >= 0),   -- SAL-08 guard
  campaign_id uuid,                                   -- one primary attribution (§7.7)
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, sales_document_id, line_no),
  CHECK (returned_base_qty <= base_qty),
  FOREIGN KEY (tenant_id, sales_document_id) REFERENCES sales_documents (tenant_id, id),
  FOREIGN KEY (tenant_id, parent_line_id) REFERENCES sales_document_lines (tenant_id, id),
  FOREIGN KEY (tenant_id, invoice_line_id) REFERENCES invoice_lines (tenant_id, id),
  FOREIGN KEY (tenant_id, stock_out_line_id) REFERENCES stock_out_lines (tenant_id, id)
);
```

Combo lines (KIT-03): `combo` line holds revenue; `combo_component` children move stock (fixed color from recipe, or color chosen at the counter) and carry `cogs_amount`; revenue is never added for components (§7.5).

## 4. POS (SAL-05/13/14)

```sql
CREATE TABLE shifts (                                 -- CA, opened implicitly by first POS sale of the day
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  business_date date NOT NULL,
  opened_at timestamptz NOT NULL, closed_at timestamptz,
  opening_cash numeric(19,4) NOT NULL DEFAULT 0, counted_cash numeric(19,4),
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','closed')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no)
);
CREATE UNIQUE INDEX ux_shifts__one_open ON shifts (tenant_id) WHERE status = 'open';
ALTER TABLE sales_documents ADD FOREIGN KEY (tenant_id, shift_id) REFERENCES shifts (tenant_id, id);

CREATE TABLE pos_carts (                              -- GH: saved cart, no reservation, no posting
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  customer_party_id uuid, label text,
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','sold')),
  total_amount numeric(19,4) NOT NULL DEFAULT 0,
  sales_document_id uuid, copied_from_id uuid,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no),
  CHECK ((status = 'sold') = (sales_document_id IS NOT NULL)),
  FOREIGN KEY (tenant_id, customer_party_id) REFERENCES customer_profiles (tenant_id, party_id),
  FOREIGN KEY (tenant_id, sales_document_id) REFERENCES sales_documents (tenant_id, id)
);
ALTER TABLE sales_documents ADD FOREIGN KEY (tenant_id, pos_cart_id) REFERENCES pos_carts (tenant_id, id);

CREATE TABLE pos_cart_lines (                         -- keeps chosen colors, incl. combo component colors
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, pos_cart_id uuid NOT NULL, line_no int NOT NULL,
  <snapshot columns>, <sales pricing columns>,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, pos_cart_id, line_no),
  FOREIGN KEY (tenant_id, pos_cart_id) REFERENCES pos_carts (tenant_id, id),
  FOREIGN KEY (tenant_id, parent_line_id) REFERENCES pos_cart_lines (tenant_id, id)
);

CREATE TABLE pos_sync_exceptions (                    -- + SAL-13 "hàng đợi ngoại lệ"
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL,
  offline_client_id text NOT NULL, device_session_id uuid,
  payload jsonb NOT NULL, error_code text NOT NULL, error_detail text,
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','resolved','discarded')),
  resolution_doc_id uuid, resolved_by uuid, resolved_at timestamptz,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, offline_client_id)
);
```

## 5. Returns and exchanges (SAL-08/09/15)

```sql
CREATE TABLE sales_returns (                          -- TH
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, doc_no text NOT NULL,
  customer_party_id uuid,
  sales_document_id uuid,                             -- NULL = return without source (SAL-09)
  reason_id uuid NOT NULL,
  business_date date NOT NULL,
  total_amount numeric(19,4) NOT NULL,
  debt_reduction_amount numeric(19,4) NOT NULL DEFAULT 0,   -- reduces open receivable first
  refund_due_amount numeric(19,4) NOT NULL DEFAULT 0,       -- excess → khoản cần hoàn (credit note)
  exchange_id uuid,
  status text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','reversed')),
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  CHECK (debt_reduction_amount + refund_due_amount = total_amount),
  FOREIGN KEY (tenant_id, sales_document_id) REFERENCES sales_documents (tenant_id, id),
  FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id)
);

CREATE TABLE sales_return_lines (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, sales_return_id uuid NOT NULL,
  sales_document_line_id uuid,                        -- color comes from here; chosen only for legacy lines without color
  <snapshot columns>,
  unit_price numeric(19,4) NOT NULL, line_amount numeric(19,4) NOT NULL,
  unit_cost numeric(19,4) NOT NULL,                   -- original cost when traceable; SAL-09 uses current avg, flagged
  cost_source text NOT NULL CHECK (cost_source IN ('original','current_average')),
  restock_to text NOT NULL CHECK (restock_to IN ('sellable','quarantine')),
  lot_id uuid NOT NULL,                               -- original lot, or its quarantine twin
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, sales_return_id) REFERENCES sales_returns (tenant_id, id),
  FOREIGN KEY (tenant_id, sales_document_line_id) REFERENCES sales_document_lines (tenant_id, id),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id)
);

CREATE TABLE exchanges (                              -- DH: links one return + one new sale
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
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
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  CHECK (customer_pays_amount = 0 OR store_refunds_amount = 0),
  FOREIGN KEY (tenant_id, original_sales_document_id) REFERENCES sales_documents (tenant_id, id),
  FOREIGN KEY (tenant_id, sales_return_id) REFERENCES sales_returns (tenant_id, id),
  FOREIGN KEY (tenant_id, new_sales_document_id) REFERENCES sales_documents (tenant_id, id)
);
ALTER TABLE sales_returns ADD FOREIGN KEY (tenant_id, exchange_id) REFERENCES exchanges (tenant_id, id);
```

Exchange settlement (§7.6, OPEN-18 default): returned value first reduces the original document's open receivable; remainder refunds through the chosen fund; the new sale is collected in full through the same fund. `sales.ExchangeService` posts return, new sale and exchange in one transaction, satisfying `returned_amount − debt_reduction − store_refunds + customer_pays = taken_amount`.

## 6. Kits and assembly (KIT-01..10)

```sql
CREATE TABLE kit_recipes (                            -- versioned recipe of a kit or combo product
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, product_id uuid NOT NULL,
  version_no int NOT NULL CHECK (version_no > 0),
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','active','superseded')),
  valid_from date, valid_to date,
  packing_cost numeric(19,4) NOT NULL DEFAULT 0 CHECK (packing_cost >= 0),   -- capitalised into kit value
  output_sku_id uuid NOT NULL,                        -- the kit SKU (one SKU per kit product)
  note text,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, product_id, version_no),
  FOREIGN KEY (tenant_id, product_id) REFERENCES products (tenant_id, id),
  FOREIGN KEY (tenant_id, output_sku_id) REFERENCES skus (tenant_id, id)
);
CREATE UNIQUE INDEX ux_kit_recipes__one_active ON kit_recipes (tenant_id, product_id) WHERE status = 'active';

CREATE TABLE kit_recipe_items (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, kit_recipe_id uuid NOT NULL, line_no int NOT NULL,
  component_kind text NOT NULL CHECK (component_kind IN ('goods','packaging','basket','accessory','mix_pack')),
  sku_id uuid,                                        -- NULL for mix_pack
  unit_id uuid NOT NULL, qty numeric(18,4) NOT NULL CHECK (qty > 0),
  unit_factor_snap numeric(18,6) NOT NULL, base_qty numeric(18,4) NOT NULL,
  color_id uuid,                                      -- fixed color; NULL = chosen at assembly/sale
  mix_group_id uuid,                                  -- mix pack component: flavors in kit_recipe_mix_items
  CHECK (base_qty = qty * unit_factor_snap),
  CHECK ((component_kind = 'mix_pack') = (mix_group_id IS NOT NULL AND sku_id IS NULL)),
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, kit_recipe_id, line_no),
  FOREIGN KEY (tenant_id, kit_recipe_id) REFERENCES kit_recipes (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  FOREIGN KEY (tenant_id, mix_group_id) REFERENCES mix_groups (tenant_id, id)
);

CREATE TABLE kit_recipe_mix_items (                   -- + per-flavor parts of a mix_pack component (§7.10)
  tenant_id uuid NOT NULL, kit_recipe_item_id uuid NOT NULL, sku_id uuid NOT NULL,
  parts_qty numeric(18,4) NOT NULL CHECK (parts_qty > 0),   -- in mix group part unit (dây/lốc)
  PRIMARY KEY (tenant_id, kit_recipe_item_id, sku_id),
  FOREIGN KEY (tenant_id, kit_recipe_item_id) REFERENCES kit_recipe_items (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id)
);

CREATE TABLE assembly_orders (                        -- LD (KIT-04/05/06/07)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, doc_no text NOT NULL,
  kit_recipe_id uuid NOT NULL, recipe_version_snap int NOT NULL,
  planned_qty numeric(18,4) NOT NULL CHECK (planned_qty > 0),
  completed_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (completed_qty >= 0),
  disassembled_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (disassembled_qty >= 0),
  status text NOT NULL DEFAULT 'draft'
    CHECK (status IN ('draft','reserved','in_progress','partially_completed','completed','closed')),
  planned_date date, campaign_id uuid, invoice_id uuid,   -- optional link to a customer order (KIT-09)
  note text,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no),
  FOREIGN KEY (tenant_id, kit_recipe_id) REFERENCES kit_recipes (tenant_id, id),
  FOREIGN KEY (tenant_id, invoice_id) REFERENCES invoices (tenant_id, id)
);

CREATE TABLE assembly_order_items (                   -- planned components; substitutions recorded here (KIT-05)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, assembly_order_id uuid NOT NULL,
  kit_recipe_item_id uuid,                            -- NULL = substitute added on this order
  substitutes_item_id uuid,
  sku_id uuid NOT NULL, color_id uuid,
  planned_base_qty numeric(18,4) NOT NULL CHECK (planned_base_qty >= 0),
  consumed_base_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (consumed_base_qty >= 0),
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, assembly_order_id) REFERENCES assembly_orders (tenant_id, id),
  FOREIGN KEY (tenant_id, kit_recipe_item_id) REFERENCES kit_recipe_items (tenant_id, id),
  FOREIGN KEY (tenant_id, substitutes_item_id) REFERENCES assembly_order_items (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id)
);
-- reservations.source_type = 'assembly_order_item' when the order moves to 'reserved'

CREATE TABLE assembly_postings (                      -- each completion batch, atomic and balanced (KIT-02/04)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, assembly_order_id uuid NOT NULL,
  produced_qty numeric(18,4) NOT NULL CHECK (produced_qty > 0),
  output_lot_id uuid NOT NULL,                        -- kit lot; expiry ≤ earliest component expiry (KIT-08)
  components_cost numeric(19,4) NOT NULL, packing_cost numeric(19,4) NOT NULL DEFAULT 0,
  output_unit_cost numeric(19,4) NOT NULL,            -- (components + packing) / produced_qty
  business_date date NOT NULL,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, idempotency_key),
  FOREIGN KEY (tenant_id, assembly_order_id) REFERENCES assembly_orders (tenant_id, id),
  FOREIGN KEY (tenant_id, output_lot_id) REFERENCES lots (tenant_id, id)
);

CREATE TABLE assembly_posting_items (                 -- actual consumption snapshot per lot and color
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, assembly_posting_id uuid NOT NULL,
  assembly_order_item_id uuid NOT NULL,
  sku_id uuid NOT NULL, lot_id uuid NOT NULL, color_id uuid,
  consumed_base_qty numeric(18,4) NOT NULL CHECK (consumed_base_qty > 0),
  scrap_base_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (scrap_base_qty >= 0),   -- hao hụt (KIT-06)
  unit_cost numeric(19,4) NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, assembly_posting_id) REFERENCES assembly_postings (tenant_id, id),
  FOREIGN KEY (tenant_id, assembly_order_item_id) REFERENCES assembly_order_items (tenant_id, id),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id)
);

CREATE TABLE disassemblies (                          -- + TG (KIT-06)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, doc_no text NOT NULL,
  assembly_order_id uuid NOT NULL, kit_lot_id uuid NOT NULL,
  qty numeric(18,4) NOT NULL CHECK (qty > 0),
  reason_id uuid NOT NULL,
  business_date date NOT NULL,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  FOREIGN KEY (tenant_id, assembly_order_id) REFERENCES assembly_orders (tenant_id, id),
  FOREIGN KEY (tenant_id, kit_lot_id) REFERENCES lots (tenant_id, id),
  FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id)
);

CREATE TABLE disassembly_lines (                      -- back to the exact lot + color used (from lot_genealogy)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, disassembly_id uuid NOT NULL,
  sku_id uuid NOT NULL, source_lot_id uuid NOT NULL, color_id uuid,
  return_base_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (return_base_qty >= 0),
  damaged_base_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (damaged_base_qty >= 0),   -- → quarantine lot, never sellable
  unit_cost numeric(19,4) NOT NULL,
  UNIQUE (tenant_id, id),
  CHECK (return_base_qty + damaged_base_qty > 0),
  FOREIGN KEY (tenant_id, disassembly_id) REFERENCES disassemblies (tenant_id, id),
  FOREIGN KEY (tenant_id, source_lot_id) REFERENCES lots (tenant_id, id)
);

CREATE TABLE lot_genealogy (                          -- append-only (KIT-07/08)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL,
  parent_lot_id uuid NOT NULL,                        -- kit lot
  child_lot_id uuid NOT NULL,                         -- component lot (carries color)
  assembly_posting_id uuid NOT NULL,
  child_base_qty numeric(18,4) NOT NULL CHECK (child_base_qty > 0),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, parent_lot_id) REFERENCES lots (tenant_id, id),
  FOREIGN KEY (tenant_id, child_lot_id) REFERENCES lots (tenant_id, id),
  FOREIGN KEY (tenant_id, assembly_posting_id) REFERENCES assembly_postings (tenant_id, id)
);
CREATE INDEX ix_lot_genealogy__parent ON lot_genealogy (tenant_id, parent_lot_id);
CREATE INDEX ix_lot_genealogy__child ON lot_genealogy (tenant_id, child_lot_id);
REVOKE UPDATE, DELETE ON lot_genealogy FROM tally_app;
```

## 7. Invariants enforced in Go

| Invariant | Owner |
|---|---|
| Colored stock requires a color before confirm/sell; only colors with sellable stock offered; one color left → auto-pick (SAL-02/05, §7.9) | `sales.ColorRequirementSpec` + `inventory.ColorAvailabilityReader` |
| Stock out ≤ reserved − delivered; shortage of the chosen color fails, never substitutes (SAL-04) | `sales.StockOutService` under `sku_stock FOR UPDATE` |
| One stock out posts moves, sales document BH, receivable, and consumes earmarked advances atomically | `posting.Coordinator` (Template Method) |
| Credit limit check = open receivable + confirmed-undelivered credit orders − advances earmarked to this order (§7.6) | `debt.CreditLimitPolicy` |
| Offline POS: cash only (no transfer, no credit), caps from `tenant_settings` (SAL-13) | `sales.OfflineSyncService` |
| Return ≤ sold per color; color from source line (SAL-08) | `sales_document_lines.returned_base_qty` CHECK + `sales.ReturnPolicy` |
| Draft kit/combo not sellable, not listed in POS/invoice pickers (KIT-10) | `products.status = 'draft'` filter in `catalog.PickerQuery` |
| Assembly consumes and produces in one balanced posting; planned orders consume nothing (KIT-02/04) | `kits.AssemblyPostingService` |
| Kit lot expiry = earliest component lot expiry (KIT-08) | `kits.KitLotFactory` |
| Disassembly returns the exact component lot/color; damaged parts go to quarantine (KIT-06) | `kits.DisassemblyService` reading `lot_genealogy` |
