# Schema — Inventory Ledger & Procurement

Inherits brainstorm §5 (inventory, procurement rows) and §5.1 (stock ledger). **Δ** = mechanical change for the no-function rule; **+** = addition with the PRD requirement that needs it. All tables are tenant tables with the RLS block ([conventions](database-conventions.md) §4). Snapshot line columns are the standard set from conventions §3, written here as `<snapshot columns>` and expanded in migrations as:

```sql
  sku_id uuid NOT NULL, sku_code_snap text NOT NULL, name_snap text NOT NULL, variant_label_snap text NOT NULL DEFAULT '',
  unit_id uuid NOT NULL, unit_name_snap text NOT NULL, unit_factor_snap numeric(18,6) NOT NULL CHECK (unit_factor_snap > 0),
  qty numeric(18,4) NOT NULL CHECK (qty > 0),
  base_qty numeric(18,4) NOT NULL,                    -- Δ plain column, Go computes; CHECK below guards it
  color_id uuid, spec_text text,
  CHECK (base_qty = qty * unit_factor_snap),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id),
  FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id)
```

Price/discount/tax/mix columns are added per line type where relevant. Migrations: `000010_inventory_ledger`, `000011_inventory_reservations`, `000012_inventory_counts_adjustments`, `000013_procurement_orders_receipts`, `000014_procurement_documents_returns`.

## 1. Stock ledger (brainstorm §5.1)

```sql
CREATE TABLE lots (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, sku_id uuid NOT NULL,
  lot_no text NOT NULL DEFAULT '',                    -- '' for SKUs without lot tracking (one default lot per color)
  expiry_date date,
  color_id uuid,                                      -- NULL = Chưa gán màu
  is_quarantine bool NOT NULL DEFAULT false,
  source_doc_type text, source_doc_line_id uuid,      -- + first receipt line (traceability, INV-07)
  received_on date NOT NULL,                          -- + FEFO tie-breaker
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE NULLS NOT DISTINCT (tenant_id, warehouse_id, sku_id, lot_no, expiry_date, color_id, is_quarantine),
  FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id)
);
CREATE INDEX ix_lots__sku_color ON lots (tenant_id, sku_id, color_id, expiry_date);
CREATE INDEX ix_lots__expiry ON lots (tenant_id, expiry_date) WHERE expiry_date IS NOT NULL;

CREATE TABLE stock_moves (                            -- append-only
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL,
  sku_id uuid NOT NULL, lot_id uuid NOT NULL,
  color_id uuid,                                      -- copied from lot for reports and invariant checks
  qty numeric(18,4) NOT NULL,                         -- signed, base unit; 0 only for cost_adjustment
  unit_cost numeric(19,4) NOT NULL CHECK (unit_cost >= 0),
  value_amount numeric(19,4) NOT NULL,                -- + signed inventory value delta (qty × cost, or cost-only delta)
  move_type text NOT NULL CHECK (move_type IN ('receipt','purchase_return','sale','sales_return',
      'adjustment_in','adjustment_out','count_gain','count_loss','assembly_consume','assembly_produce',
      'disassembly_consume','disassembly_return','quarantine_out','quarantine_in','opening',
      'cost_adjustment','reversal')),                 -- + cost_adjustment for PUR-06/PUR-10 after receipt
  doc_type text NOT NULL, doc_id uuid NOT NULL, doc_line_id uuid,
  business_date date NOT NULL,
  posted_at timestamptz NOT NULL, posted_by uuid NOT NULL,
  reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  CHECK ((move_type = 'cost_adjustment') = (qty = 0)),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  FOREIGN KEY (tenant_id, reversal_of) REFERENCES stock_moves (tenant_id, id)
);
CREATE INDEX ix_stock_moves__card ON stock_moves (tenant_id, sku_id, business_date, id);   -- stock card (INV-10)
CREATE INDEX ix_stock_moves__doc ON stock_moves (tenant_id, doc_type, doc_id);             -- trace back
CREATE INDEX ix_stock_moves__lot ON stock_moves (tenant_id, lot_id, business_date);
CREATE UNIQUE INDEX ux_stock_moves__one_reversal ON stock_moves (tenant_id, reversal_of) WHERE reversal_of IS NOT NULL;
REVOKE UPDATE, DELETE ON stock_moves FROM tally_app;

CREATE TABLE lot_balances (                           -- projection
  tenant_id uuid NOT NULL, lot_id uuid NOT NULL,
  sku_id uuid NOT NULL, color_id uuid,                -- + denormalised for per-color totals (INV-12)
  qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (qty >= 0),
  updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, lot_id),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id)
);
CREATE INDEX ix_lot_balances__sku_color ON lot_balances (tenant_id, sku_id, color_id) WHERE qty > 0;

CREATE TABLE sku_stock (                              -- projection and lock point
  tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, sku_id uuid NOT NULL,
  on_hand numeric(18,4) NOT NULL DEFAULT 0 CHECK (on_hand >= 0),
  quarantine_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (quarantine_qty >= 0),   -- + "cách ly Y" (INV-12)
  expired_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (expired_qty >= 0),         -- + rolled daily by worker
  sellable numeric(18,4) NOT NULL DEFAULT 0 CHECK (sellable >= 0),
  reserved numeric(18,4) NOT NULL DEFAULT 0 CHECK (reserved >= 0),
  avg_cost numeric(19,4) NOT NULL DEFAULT 0 CHECK (avg_cost >= 0),
  stock_value numeric(19,4) NOT NULL DEFAULT 0,       -- + Σ value_amount, basis of avg_cost
  last_move_at timestamptz,
  updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  PRIMARY KEY (tenant_id, warehouse_id, sku_id),
  CHECK (sellable = on_hand - quarantine_qty - expired_qty),
  -- reserved ≤ sellable is checked by inventory.ReservationService under FOR UPDATE (AT-03), not by CHECK:
  -- an expiry roll or quarantine may lower sellable below existing reservations, which must surface as a shortage warning
  FOREIGN KEY (tenant_id, warehouse_id) REFERENCES warehouses (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id)
);
```

- `Available` (Tồn có thể bán) = `sellable − reserved`, computed in Go and SQL reads; not stored.
- `expired_qty` is moved by `inventory.ExpiryRoller` (worker job at tenant midnight, idempotent per `business_date`); the posting guard also checks `lots.expiry_date` directly, so a late roll never allows an expired sale (INV-07).
- Per-color stock (INV-01/12) = `lot_balances` grouped by `color_id`; `tally ledger verify` asserts Σ moves = balances per lot, per color and per SKU, and `stock_value` = Σ `value_amount`.

```sql
CREATE TABLE reservations (                           -- per SKU, not per color (OPEN-17)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, sku_id uuid NOT NULL,
  source_type text NOT NULL CHECK (source_type IN ('invoice_line','assembly_order_item','manual')),
  source_id uuid NOT NULL, source_doc_no text,
  qty numeric(18,4) NOT NULL CHECK (qty > 0),         -- base unit
  consumed_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (consumed_qty >= 0),
  released_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (released_qty >= 0),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','consumed','released')),
  expires_at timestamptz, note text,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  UNIQUE (tenant_id, source_type, source_id),
  CHECK (consumed_qty + released_qty <= qty),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id)
);
CREATE INDEX ix_reservations__sku_active ON reservations (tenant_id, sku_id) WHERE status = 'active';
```

`sku_stock.reserved` = Σ (`qty − consumed_qty − released_qty`) of active reservations, maintained in the same transaction.

## 2. Counts and adjustments

```sql
CREATE TABLE stock_counts (                           -- KK (INV-05)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL,
  doc_no text NOT NULL,
  scope text NOT NULL CHECK (scope IN ('all','category','selection')),
  category_id uuid, blind bool NOT NULL DEFAULT false,
  status text NOT NULL DEFAULT 'counting' CHECK (status IN ('counting','pending_post','posted')),
  business_date date NOT NULL, started_at timestamptz NOT NULL, counted_at timestamptz,
  note text,
  idempotency_key text, posted_at timestamptz, posted_by uuid,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no),
  UNIQUE (tenant_id, idempotency_key),
  FOREIGN KEY (tenant_id, category_id) REFERENCES categories (tenant_id, id)
);

CREATE TABLE stock_count_lines (                      -- one line per SKU × color (× lot when tracked)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, stock_count_id uuid NOT NULL,
  sku_id uuid NOT NULL, color_id uuid, lot_id uuid,
  system_qty numeric(18,4) NOT NULL,                  -- snapshot at start; hidden in blind mode
  counted_qty numeric(18,4) CHECK (counted_qty IS NULL OR counted_qty >= 0),
  moved_since_start_qty numeric(18,4) NOT NULL DEFAULT 0,   -- + moves after started_at, filled at counting finish
  variance_qty numeric(18,4),                         -- counted − (system + moved_since_start)
  reason_id uuid,
  UNIQUE (tenant_id, id),
  UNIQUE NULLS NOT DISTINCT (tenant_id, stock_count_id, sku_id, color_id, lot_id),
  CHECK (variance_qty IS NULL OR counted_qty IS NOT NULL),
  CHECK (variance_qty IS NULL OR variance_qty = counted_qty - system_qty - moved_since_start_qty),
  FOREIGN KEY (tenant_id, stock_count_id) REFERENCES stock_counts (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id),
  FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id)
);

CREATE TABLE stock_adjustments (                      -- DC (INV-06, INV-11, opening stock)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL,
  doc_no text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('increase','decrease','damage','loss','gift','internal_use','disposal',
                                     'quarantine_in','quarantine_release','opening','count')),
  reason_id uuid,                                     -- required unless kind IN ('opening','count') (Go)
  stock_count_id uuid,                                -- set when kind = 'count'
  business_date date NOT NULL, note text,
  status text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','reversed')),
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL,
  reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  CHECK ((kind = 'count') = (stock_count_id IS NOT NULL)),
  FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id),
  FOREIGN KEY (tenant_id, stock_count_id) REFERENCES stock_counts (tenant_id, id)
);

CREATE TABLE stock_adjustment_lines (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, stock_adjustment_id uuid NOT NULL,
  <snapshot columns>,
  direction smallint NOT NULL CHECK (direction IN (-1, 1)),
  lot_id uuid,                                        -- chosen lot (required when color stock exists, INV-06)
  lot_no text, expiry_date date,                      -- for increases/opening that create lots
  unit_cost numeric(19,4) CHECK (unit_cost IS NULL OR unit_cost >= 0),   -- opening/increase only
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, stock_adjustment_id) REFERENCES stock_adjustments (tenant_id, id),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id)
);
```

Posting rules: `disposal`, `quarantine_release` and count variances require an `exception_confirmations` row bound to the document version (INV-05/07/11); decreases never create revenue (INV-06).

## 3. Procurement

```sql
CREATE TABLE purchase_orders (                        -- PO (PUR-02/04/11)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  supplier_party_id uuid NOT NULL,
  order_date date NOT NULL, expected_date date,
  status text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','confirmed','partially_received','completed','closed_short')),
  goods_amount numeric(19,4) NOT NULL DEFAULT 0, discount_amount numeric(19,4) NOT NULL DEFAULT 0,
  tax_amount numeric(19,4) NOT NULL DEFAULT 0, total_amount numeric(19,4) NOT NULL DEFAULT 0,
  note text,
  close_reason_id uuid, closed_at timestamptz,
  confirmed_at timestamptz, confirmed_by uuid,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no),
  CHECK (status <> 'closed_short' OR close_reason_id IS NOT NULL),
  FOREIGN KEY (tenant_id, supplier_party_id) REFERENCES supplier_profiles (tenant_id, party_id),
  FOREIGN KEY (tenant_id, close_reason_id) REFERENCES reasons (tenant_id, id)
);
CREATE INDEX ix_purchase_orders__supplier ON purchase_orders (tenant_id, supplier_party_id, status);
CREATE INDEX ix_purchase_orders__expected ON purchase_orders (tenant_id, expected_date) WHERE status IN ('confirmed','partially_received');

CREATE TABLE purchase_order_lines (                   -- column order on screen: Tên SP, ĐVT, SL, Quy cách, Đơn giá, Thành tiền, Giá bán lẻ
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, purchase_order_id uuid NOT NULL, line_no int NOT NULL,
  <snapshot columns>,
  unit_price numeric(19,4) NOT NULL CHECK (unit_price >= 0),
  discount_amount numeric(19,4) NOT NULL DEFAULT 0 CHECK (discount_amount >= 0),
  tax_rate_snap numeric(7,4) NOT NULL DEFAULT 0,
  line_amount numeric(19,4) NOT NULL,
  retail_price_snap numeric(19,4),                    -- + "Giá bán lẻ" shown/edited on the line (PRC-08)
  received_base_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (received_base_qty >= 0),   -- + projection
  returned_base_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (returned_base_qty >= 0),   -- + projection
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, purchase_order_id, line_no),
  FOREIGN KEY (tenant_id, purchase_order_id) REFERENCES purchase_orders (tenant_id, id)
);
-- Đặt / nhận / trả / còn chờ (PUR-04): base_qty, received_base_qty, returned_base_qty, max(0, base_qty − received_base_qty)

CREATE TABLE receipts (                               -- PN (PUR-03/04/05/07)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, doc_no text NOT NULL,
  supplier_party_id uuid NOT NULL, purchase_order_id uuid,   -- NULL = nhập nhanh
  business_date date NOT NULL,
  goods_amount numeric(19,4) NOT NULL, discount_amount numeric(19,4) NOT NULL DEFAULT 0,
  tax_amount numeric(19,4) NOT NULL DEFAULT 0,
  landed_cost_amount numeric(19,4) NOT NULL DEFAULT 0 CHECK (landed_cost_amount >= 0),
  total_amount numeric(19,4) NOT NULL,                -- payable created at posting
  paid_now_amount numeric(19,4) NOT NULL DEFAULT 0 CHECK (paid_now_amount >= 0),
  due_date date,
  status text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','reversed','partially_reversed')),
  note text,
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL,
  reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  CHECK (paid_now_amount <= total_amount),
  FOREIGN KEY (tenant_id, supplier_party_id) REFERENCES supplier_profiles (tenant_id, party_id),
  FOREIGN KEY (tenant_id, purchase_order_id) REFERENCES purchase_orders (tenant_id, id)
);
CREATE INDEX ix_receipts__supplier_date ON receipts (tenant_id, supplier_party_id, business_date);

CREATE TABLE receipt_lines (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, receipt_id uuid NOT NULL, line_no int NOT NULL,
  purchase_order_line_id uuid,
  <snapshot columns>,
  condition text NOT NULL DEFAULT 'accepted' CHECK (condition IN ('accepted','damaged','wrong_item','excess')),   -- + PUR-04
  lot_no text NOT NULL DEFAULT '', expiry_date date,  -- required by SKU mode (PUR-05, Go)
  lot_id uuid NOT NULL,                               -- resolved/created at posting; damaged → quarantine lot
  unit_price numeric(19,4) NOT NULL CHECK (unit_price >= 0),
  discount_amount numeric(19,4) NOT NULL DEFAULT 0,
  tax_rate_snap numeric(7,4) NOT NULL DEFAULT 0,
  line_amount numeric(19,4) NOT NULL,
  landed_cost_amount numeric(19,4) NOT NULL DEFAULT 0,   -- allocated share
  unit_cost numeric(19,4) NOT NULL,                   -- (line_amount + landed) / base_qty, Go-rounded
  returned_base_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (returned_base_qty >= 0),
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, receipt_id, line_no),
  CHECK (returned_base_qty <= base_qty),
  FOREIGN KEY (tenant_id, receipt_id) REFERENCES receipts (tenant_id, id),
  FOREIGN KEY (tenant_id, purchase_order_line_id) REFERENCES purchase_order_lines (tenant_id, id),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id)
);

CREATE TABLE landed_cost_allocations (                -- PUR-06
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, receipt_id uuid NOT NULL,
  cost_type text NOT NULL CHECK (cost_type IN ('shipping','loading','other')),
  description text, amount numeric(19,4) NOT NULL CHECK (amount > 0),
  method text NOT NULL DEFAULT 'by_value' CHECK (method IN ('by_value','by_qty')),
  payee_party_id uuid,                                -- NULL = paid in cash, no payable
  business_date date NOT NULL,
  posted_at timestamptz NOT NULL, posted_by uuid NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, receipt_id) REFERENCES receipts (tenant_id, id),
  FOREIGN KEY (tenant_id, payee_party_id) REFERENCES parties (tenant_id, id)
);

CREATE TABLE landed_cost_allocation_lines (           -- + Σ amount = allocation.amount (Go, verify)
  tenant_id uuid NOT NULL, landed_cost_allocation_id uuid NOT NULL, receipt_line_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL CHECK (amount >= 0),
  on_hand_amount numeric(19,4) NOT NULL,              -- portion still in stock → cost_adjustment move
  sold_amount numeric(19,4) NOT NULL,                 -- portion already sold → COGS adjustment
  PRIMARY KEY (tenant_id, landed_cost_allocation_id, receipt_line_id),
  CHECK (on_hand_amount + sold_amount = amount),
  FOREIGN KEY (tenant_id, landed_cost_allocation_id) REFERENCES landed_cost_allocations (tenant_id, id),
  FOREIGN KEY (tenant_id, receipt_line_id) REFERENCES receipt_lines (tenant_id, id)
);

CREATE TABLE supplier_documents (                     -- PUR-08: NCC invoice matched to receipts
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, supplier_party_id uuid NOT NULL,
  document_no text NOT NULL, document_date date NOT NULL,
  goods_amount numeric(19,4) NOT NULL, tax_amount numeric(19,4) NOT NULL DEFAULT 0,
  total_amount numeric(19,4) NOT NULL,
  match_status text NOT NULL DEFAULT 'unmatched' CHECK (match_status IN ('unmatched','matched','variance')),
  variance_amount numeric(19,4) NOT NULL DEFAULT 0,
  note text,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, supplier_party_id, document_no),
  FOREIGN KEY (tenant_id, supplier_party_id) REFERENCES supplier_profiles (tenant_id, party_id)
);

CREATE TABLE supplier_document_receipts (             -- + one document ↔ many receipts
  tenant_id uuid NOT NULL, supplier_document_id uuid NOT NULL, receipt_id uuid NOT NULL,
  matched_amount numeric(19,4) NOT NULL,
  PRIMARY KEY (tenant_id, supplier_document_id, receipt_id),
  FOREIGN KEY (tenant_id, supplier_document_id) REFERENCES supplier_documents (tenant_id, id),
  FOREIGN KEY (tenant_id, receipt_id) REFERENCES receipts (tenant_id, id)
);

CREATE TABLE purchase_returns (                       -- XT (PUR-09)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, warehouse_id uuid NOT NULL, doc_no text NOT NULL,
  supplier_party_id uuid NOT NULL, receipt_id uuid NOT NULL,
  reason_id uuid NOT NULL,
  settlement text NOT NULL CHECK (settlement IN ('credit_payable','refund','replacement')),
  business_date date NOT NULL,
  total_amount numeric(19,4) NOT NULL,
  status text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','reversed')),
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  FOREIGN KEY (tenant_id, supplier_party_id) REFERENCES supplier_profiles (tenant_id, party_id),
  FOREIGN KEY (tenant_id, receipt_id) REFERENCES receipts (tenant_id, id),
  FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id)
);

CREATE TABLE purchase_return_lines (                  -- exact lot and color of the receipt line
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, purchase_return_id uuid NOT NULL,
  receipt_line_id uuid NOT NULL, lot_id uuid NOT NULL,
  <snapshot columns>,
  unit_cost numeric(19,4) NOT NULL, line_amount numeric(19,4) NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, purchase_return_id) REFERENCES purchase_returns (tenant_id, id),
  FOREIGN KEY (tenant_id, receipt_line_id) REFERENCES receipt_lines (tenant_id, id),
  FOREIGN KEY (tenant_id, lot_id) REFERENCES lots (tenant_id, id)
);

CREATE TABLE purchase_price_adjustments (             -- + DG (PUR-09 post-purchase discount, PUR-10 final price)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, doc_no text NOT NULL,
  supplier_party_id uuid NOT NULL, receipt_id uuid NOT NULL,
  kind text NOT NULL CHECK (kind IN ('final_price','post_purchase_discount')),
  reason_id uuid NOT NULL,
  business_date date NOT NULL,
  payable_delta numeric(19,4) NOT NULL,               -- + increases, − decreases payable (credit note when −)
  status text NOT NULL DEFAULT 'posted' CHECK (status IN ('posted','reversed')),
  idempotency_key text NOT NULL, posted_at timestamptz NOT NULL, posted_by uuid NOT NULL, reversal_of uuid,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, doc_no), UNIQUE (tenant_id, idempotency_key),
  FOREIGN KEY (tenant_id, supplier_party_id) REFERENCES supplier_profiles (tenant_id, party_id),
  FOREIGN KEY (tenant_id, receipt_id) REFERENCES receipts (tenant_id, id),
  FOREIGN KEY (tenant_id, reason_id) REFERENCES reasons (tenant_id, id)
);

CREATE TABLE purchase_price_adjustment_lines (
  tenant_id uuid NOT NULL, purchase_price_adjustment_id uuid NOT NULL, receipt_line_id uuid NOT NULL,
  old_unit_price numeric(19,4) NOT NULL, new_unit_price numeric(19,4) NOT NULL CHECK (new_unit_price >= 0),
  base_qty numeric(18,4) NOT NULL,
  on_hand_amount numeric(19,4) NOT NULL,              -- → cost_adjustment stock move
  sold_amount numeric(19,4) NOT NULL,                 -- → COGS adjustment in reporting
  PRIMARY KEY (tenant_id, purchase_price_adjustment_id, receipt_line_id),
  FOREIGN KEY (tenant_id, purchase_price_adjustment_id) REFERENCES purchase_price_adjustments (tenant_id, id),
  FOREIGN KEY (tenant_id, receipt_line_id) REFERENCES receipt_lines (tenant_id, id)
);

CREATE TABLE cogs_adjustments (                       -- + cost delta for quantities already sold (PUR-06/10, RPT gross margin)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, sku_id uuid NOT NULL,
  amount numeric(19,4) NOT NULL,
  source_doc_type text NOT NULL CHECK (source_doc_type IN ('landed_cost_allocation','purchase_price_adjustment')),
  source_doc_id uuid NOT NULL, business_date date NOT NULL, created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id)
);
REVOKE UPDATE, DELETE ON cogs_adjustments FROM tally_app;
```

## 4. Posting invariants (Go, `procurement.ReceiptPosting`, `inventory.Ledger`)

| Invariant | Mechanism |
|---|---|
| Re-pressing confirm never posts twice (PUR-03) | `UNIQUE (tenant_id, idempotency_key)` + `idempotency_keys` response replay |
| Receipt line color goes onto the lot; same lot no. + other color = another lot (PUR-05) | `lots` unique tuple includes `color_id` |
| Payable created once at receipt; supplier document never re-creates it (PUR-08) | `debt.ObligationFactory` keyed by `(source_type='receipt', source_id)` unique in `obligations` |
| Purchase return ships the exact lot + color of the receipt line (PUR-09) | `purchase_return_lines.lot_id` copied from `receipt_lines.lot_id`; returned ≤ received |
| Average cost per SKU, not per color (§7.5) | `inventory.MovingAverageCosting` updates `sku_stock.avg_cost` from `stock_value / on_hand` |
| Landed cost / final price split on-hand vs sold (PUR-06/10) | allocation lines → `cost_adjustment` moves + `cogs_adjustments` |
| Closing a PO short needs a reason and never cancels received docs (PUR-11) | `CHECK` + `procurement.PurchaseOrderPolicy` |
| Lock order | `doc_sequences` → `sku_stock` (sku asc) → `lots`/`lot_balances` → `reservations` → `obligations` → `payments` → `fund_accounts` |
