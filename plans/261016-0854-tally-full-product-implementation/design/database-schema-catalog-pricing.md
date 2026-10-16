# Schema — Catalog & Pricing

Inherits brainstorm §4.2–§4.4 table-for-table. Changes from the brainstorm sketch are mechanical consequences of the no-function rule ([conventions](database-conventions.md) §2) and are marked **Δ**. Tables not in the brainstorm are marked **+** with the PRD requirement that needs them. Every table is a tenant table and gets the RLS block (conventions §4).

Migrations: `000005_catalog_lookups`, `000006_catalog_attributes`, `000007_catalog_products_skus`, `000008_catalog_mix_groups`, `000009_pricing`.

## 1. Lookups

```sql
CREATE TABLE colors (                                 -- seed: xanh, do, tim, vang, nau, den, trang (CAT-09)
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  code text NOT NULL, name text NOT NULL, hex char(7) NOT NULL,
  sort_order int NOT NULL DEFAULT 0, is_system bool NOT NULL DEFAULT false,
  active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,                    -- Δ timestamps set by Go
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, code)
);

CREATE TABLE units (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  name text NOT NULL,
  name_key text NOT NULL,                             -- Δ replaces lower(name) expression index
  active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, name_key)
);                                                    -- seed: cái, hộp, thùng, dây, lốc, chai, gói, kg, bó

CREATE TABLE tax_rates (                              -- + PRC-06: rate snapshot on every line
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  name text NOT NULL, name_key text NOT NULL,
  rate_percent numeric(7,4) NOT NULL CHECK (rate_percent >= 0 AND rate_percent <= 100),
  active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, name_key)
);                                                    -- seed: "Không chịu thuế" 0, "VAT 8%", "VAT 10%"
```

## 2. Dynamic attributes

```sql
CREATE TABLE attributes (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  code text NOT NULL CHECK (code ~ '^[a-z][a-z0-9_]{0,39}$'),   -- JSONB key, immutable after use
  name text NOT NULL,
  data_type text NOT NULL CHECK (data_type IN ('text','number','boolean','date','select','multiselect')),
  unit_label text,
  is_filterable bool NOT NULL DEFAULT false,
  is_searchable bool NOT NULL DEFAULT false,
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, code)
);                                                    -- seed: brand, origin, flavor, net_weight, volume …

CREATE TABLE attribute_options (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, attribute_id uuid NOT NULL,
  label text NOT NULL, sort_order int NOT NULL DEFAULT 0, active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, attribute_id, label),
  FOREIGN KEY (tenant_id, attribute_id) REFERENCES attributes (tenant_id, id)
);

CREATE TABLE attribute_sets (                         -- "mẫu ngành": Bánh kẹo, Đồ uống, Giỏ quà …
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  name text NOT NULL, active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, name)
);

CREATE TABLE attribute_set_items (
  tenant_id uuid NOT NULL, attribute_set_id uuid NOT NULL, attribute_id uuid NOT NULL,
  is_required bool NOT NULL DEFAULT false, sort_order int NOT NULL DEFAULT 0,
  PRIMARY KEY (tenant_id, attribute_set_id, attribute_id),
  FOREIGN KEY (tenant_id, attribute_set_id) REFERENCES attribute_sets (tenant_id, id),
  FOREIGN KEY (tenant_id, attribute_id) REFERENCES attributes (tenant_id, id)
);

CREATE TABLE categories (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id), parent_id uuid,
  name text NOT NULL, name_key text NOT NULL,         -- + unique sibling names
  attribute_set_id uuid, sort_order int NOT NULL DEFAULT 0,
  active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  UNIQUE NULLS NOT DISTINCT (tenant_id, parent_id, name_key),
  CHECK (parent_id IS NULL OR parent_id <> id),
  FOREIGN KEY (tenant_id, parent_id) REFERENCES categories (tenant_id, id),
  FOREIGN KEY (tenant_id, attribute_set_id) REFERENCES attribute_sets (tenant_id, id)
);
```

Attribute sets do not inherit from parent categories (decided in brainstorm). Cycle prevention in the category tree is `catalog.CategoryService` (walks ancestors before re-parenting).

## 3. Products and SKUs

```sql
CREATE TABLE products (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  type text NOT NULL CHECK (type IN ('goods','material','kit','combo')),
  name text NOT NULL,
  name_key text NOT NULL,                             -- Δ CAT-11 duplicate-name block without lower()
  category_id uuid, attribute_set_id uuid,
  attributes jsonb NOT NULL DEFAULT '{}',             -- Δ object shape checked by catalog.AttributeValidator
  description text,
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('draft','active','inactive')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, name_key),
  FOREIGN KEY (tenant_id, category_id) REFERENCES categories (tenant_id, id),
  FOREIGN KEY (tenant_id, attribute_set_id) REFERENCES attribute_sets (tenant_id, id)
);
CREATE INDEX ix_products__attributes ON products USING gin (attributes jsonb_path_ops);
CREATE INDEX ix_products__category ON products (tenant_id, category_id, status);

CREATE TABLE product_variant_axes (
  tenant_id uuid NOT NULL, product_id uuid NOT NULL, attribute_id uuid NOT NULL,
  position smallint NOT NULL CHECK (position BETWEEN 1 AND 3),   -- + max 3 axes keeps the matrix usable
  PRIMARY KEY (tenant_id, product_id, attribute_id),
  UNIQUE (tenant_id, product_id, position),
  FOREIGN KEY (tenant_id, product_id) REFERENCES products (tenant_id, id),
  FOREIGN KEY (tenant_id, attribute_id) REFERENCES attributes (tenant_id, id)
);  -- select attributes only; axes locked once any SKU of the product has a stock move

CREATE TABLE skus (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, product_id uuid NOT NULL,
  code text NOT NULL,
  variant_key text NOT NULL DEFAULT '',               -- option ids ordered by axis position joined by '|'
  display_name text NOT NULL,                         -- "Sữa TH 180ml – Dâu", built by catalog.SKUNamer
  variant_label text NOT NULL DEFAULT '',             -- "Dâu" (snapshot source for variant_label_snap)
  base_unit_id uuid NOT NULL,
  qty_scale smallint NOT NULL DEFAULT 0 CHECK (qty_scale BETWEEN 0 AND 4),
  track_lot bool NOT NULL DEFAULT false, track_expiry bool NOT NULL DEFAULT false,
  ref_cost numeric(19,4) CHECK (ref_cost IS NULL OR ref_cost >= 0),
  min_price numeric(19,4) CHECK (min_price IS NULL OR min_price >= 0),     -- one level (OPEN-19)
  min_stock numeric(18,4) NOT NULL DEFAULT 0 CHECK (min_stock >= 0),
  tax_rate_id uuid,                                   -- + PRC-06; NULL = tenant default (0)
  spec_text text,                                     -- + CAT-03 "quy cách" default for lines
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
  search_norm text NOT NULL DEFAULT '',               -- built by catalog.SearchIndexer
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, code),
  UNIQUE (tenant_id, product_id, variant_key),
  CHECK (NOT track_expiry OR track_lot),              -- expiry lives on lots
  FOREIGN KEY (tenant_id, product_id) REFERENCES products (tenant_id, id),
  FOREIGN KEY (tenant_id, base_unit_id) REFERENCES units (tenant_id, id),
  FOREIGN KEY (tenant_id, tax_rate_id) REFERENCES tax_rates (tenant_id, id)
);
CREATE INDEX ix_skus__status ON skus (tenant_id, status);   -- Δ replaces gin_trgm_ops (no pg_trgm)
CREATE INDEX ix_skus__product ON skus (tenant_id, product_id);

CREATE TABLE sku_variant_values (
  tenant_id uuid NOT NULL, sku_id uuid NOT NULL, attribute_id uuid NOT NULL, option_id uuid NOT NULL,
  PRIMARY KEY (tenant_id, sku_id, attribute_id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  FOREIGN KEY (tenant_id, attribute_id) REFERENCES attributes (tenant_id, id),
  FOREIGN KEY (tenant_id, option_id) REFERENCES attribute_options (tenant_id, id)
);

CREATE TABLE sku_units (
  tenant_id uuid NOT NULL, sku_id uuid NOT NULL, unit_id uuid NOT NULL,
  factor numeric(18,6) NOT NULL CHECK (factor > 0),   -- 1 of this unit = factor base units
  is_base bool NOT NULL DEFAULT false, active bool NOT NULL DEFAULT true,
  is_default_sale bool NOT NULL DEFAULT false,        -- + POS/hóa đơn default unit
  is_default_purchase bool NOT NULL DEFAULT false,    -- + PO/receipt default unit
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  PRIMARY KEY (tenant_id, sku_id, unit_id),
  CHECK (NOT is_base OR factor = 1),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id)
);
CREATE UNIQUE INDEX ux_sku_units__base ON sku_units (tenant_id, sku_id) WHERE is_base;
CREATE UNIQUE INDEX ux_sku_units__default_sale ON sku_units (tenant_id, sku_id) WHERE is_default_sale;
CREATE UNIQUE INDEX ux_sku_units__default_purchase ON sku_units (tenant_id, sku_id) WHERE is_default_purchase;

CREATE TABLE sku_barcodes (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, sku_id uuid NOT NULL, unit_id uuid NOT NULL,
  code text NOT NULL,
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, code),
  FOREIGN KEY (tenant_id, sku_id, unit_id) REFERENCES sku_units (tenant_id, sku_id, unit_id)
);

CREATE TABLE product_images (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, product_id uuid NOT NULL, sku_id uuid,
  attachment_id uuid NOT NULL,                        -- Δ storage_key lives on attachments (signed URL, scan)
  sort_order int NOT NULL DEFAULT 0, created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, id),
  FOREIGN KEY (tenant_id, product_id) REFERENCES products (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  FOREIGN KEY (tenant_id, attachment_id) REFERENCES attachments (tenant_id, id)
);
```

### 3.1 Search fallback (created only if the NFR-03 gate fails)

```sql
CREATE TABLE sku_search_tokens (                      -- + maintained by catalog.SearchIndexer
  tenant_id uuid NOT NULL, token text NOT NULL, sku_id uuid NOT NULL,
  PRIMARY KEY (tenant_id, token, sku_id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id)
);
CREATE INDEX ix_sku_search_tokens__prefix ON sku_search_tokens (tenant_id, token text_pattern_ops);
```

Phase 05 ships `LIKE '%term%'` on `search_norm`; phase 21 load test decides whether this migration is added. The `catalog.SearchRepository` interface hides which one is used.

## 4. Mix groups (CAT-10, PRD §7.10)

```sql
CREATE TABLE mix_groups (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  name text NOT NULL, name_key text NOT NULL,         -- Δ replaces lower(name)
  pack_unit_id uuid NOT NULL, units_per_pack int NOT NULL,   -- 1 thùng = 48 hộp
  part_unit_id uuid NOT NULL, units_per_part int NOT NULL,   -- 1 dây = 4 hộp
  piece_unit_id uuid NOT NULL,                        -- + the shared "đơn vị lẻ" (hộp) all flavors use
  source_product_id uuid,                             -- + filled when created from a variant product
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, name_key),
  CHECK (units_per_part > 0 AND units_per_pack % units_per_part = 0
         AND units_per_pack / units_per_part >= 2),
  FOREIGN KEY (tenant_id, pack_unit_id) REFERENCES units (tenant_id, id),
  FOREIGN KEY (tenant_id, part_unit_id) REFERENCES units (tenant_id, id),
  FOREIGN KEY (tenant_id, piece_unit_id) REFERENCES units (tenant_id, id),
  FOREIGN KEY (tenant_id, source_product_id) REFERENCES products (tenant_id, id)
);

CREATE TABLE mix_group_items (
  tenant_id uuid NOT NULL, mix_group_id uuid NOT NULL, sku_id uuid NOT NULL,
  flavor_label text NOT NULL, sort_order int NOT NULL DEFAULT 0,
  active bool NOT NULL DEFAULT true,                  -- + flavors already sold are deactivated, never removed
  PRIMARY KEY (tenant_id, mix_group_id, sku_id),
  UNIQUE (tenant_id, mix_group_id, flavor_label),
  FOREIGN KEY (tenant_id, mix_group_id) REFERENCES mix_groups (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id)
);
```

Go (`catalog.MixGroupPolicy`): ≥ 2 active flavors; every flavor SKU has `piece_unit_id` as base unit (or a `sku_units` row with factor 1); pack spec immutable once used on a document.

## 5. Pricing (PRC-01/02/07/08)

```sql
CREATE TABLE price_lists (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  name text NOT NULL, name_key text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('retail','wholesale','group','customer','season')),
  customer_group_id uuid, party_id uuid, campaign_id uuid,
  priority int NOT NULL DEFAULT 100,
  valid_from date, valid_to date,
  active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id), UNIQUE (tenant_id, name_key),
  CHECK (valid_to IS NULL OR valid_from IS NULL OR valid_to >= valid_from),
  CHECK ((kind = 'group') = (customer_group_id IS NOT NULL)),
  CHECK ((kind = 'customer') = (party_id IS NOT NULL)),
  FOREIGN KEY (tenant_id, customer_group_id) REFERENCES customer_groups (tenant_id, id),
  FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE UNIQUE INDEX ux_price_lists__base ON price_lists (tenant_id, kind) WHERE kind IN ('retail','wholesale');
-- campaign FK added by the campaign migration: ALTER TABLE price_lists ADD FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id)

CREATE TABLE price_list_items (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, price_list_id uuid NOT NULL, sku_id uuid NOT NULL,
  color_id uuid,                                      -- NULL = general price; set = color price (PRC-07)
  unit_id uuid,                                       -- NULL = base unit
  min_qty numeric(18,4) NOT NULL DEFAULT 0 CHECK (min_qty >= 0),   -- tier in base qty
  price numeric(19,4) NOT NULL CHECK (price >= 0),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  UNIQUE NULLS NOT DISTINCT (tenant_id, price_list_id, sku_id, color_id, unit_id, min_qty),
  FOREIGN KEY (tenant_id, price_list_id) REFERENCES price_lists (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id),
  FOREIGN KEY (tenant_id, color_id) REFERENCES colors (tenant_id, id),
  FOREIGN KEY (tenant_id, unit_id) REFERENCES units (tenant_id, id)
);
CREATE INDEX ix_price_list_items__sku ON price_list_items (tenant_id, sku_id, price_list_id);

CREATE TABLE price_changes (                          -- append-only
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, price_list_id uuid NOT NULL, sku_id uuid NOT NULL,
  color_id uuid, unit_id uuid, min_qty numeric(18,4) NOT NULL,
  old_price numeric(19,4), new_price numeric(19,4),   -- NULL old = added; NULL new = "Bỏ giá riêng"
  source text NOT NULL CHECK (source IN ('catalog','purchase_order','product_list','inventory','price_list')),
  source_doc_id uuid, actor_id uuid NOT NULL,
  created_at timestamptz NOT NULL,                    -- Δ no DEFAULT now()
  CHECK (old_price IS NOT NULL OR new_price IS NOT NULL),
  FOREIGN KEY (tenant_id, price_list_id) REFERENCES price_lists (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id)
);
CREATE INDEX ix_price_changes__sku ON price_changes (tenant_id, sku_id, created_at DESC);
REVOKE UPDATE, DELETE ON price_changes FROM tally_app;
```

### 5.1 Resolver order (one Go strategy chain, returns price + `PriceSource`)

1. Candidate lists by precedence: customer list → customer-group list → active season/campaign list (by `business_date` within `valid_from..valid_to`) → base list (`retail` or `wholesale` from `customer_groups.base_price_kind`). Same level → lower `priority` wins, then newest `created_at`.
2. Inside the first list with a matching row: matching `color_id` beats `color_id IS NULL`; a row with matching `unit_id` beats base-unit price × factor; largest `min_qty` ≤ base quantity (unit converted first, PRC-02).
3. "Bỏ giá riêng" deletes the color row and writes `price_changes`; "Áp dụng cho tất cả màu" deletes all color rows of that SKU in that list in the same transaction.
4. No list matches → `price_source.kind = 'manual'` with price 0; UI requires entry.

## 6. Promotions (+ PRC-03)

```sql
CREATE TABLE promotions (
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants (id),
  code text,                                          -- NULL = automatic promotion
  name text NOT NULL,
  kind text NOT NULL CHECK (kind IN ('line_percent','line_amount','order_percent','order_amount','gift','combo_price')),
  value numeric(19,4) NOT NULL DEFAULT 0 CHECK (value >= 0),
  max_discount numeric(19,4) CHECK (max_discount IS NULL OR max_discount >= 0),   -- trần chiết khấu
  min_order_amount numeric(19,4) NOT NULL DEFAULT 0 CHECK (min_order_amount >= 0),
  stackable bool NOT NULL DEFAULT false,              -- cộng dồn rule
  customer_group_id uuid, campaign_id uuid,
  valid_from date NOT NULL, valid_to date NOT NULL,
  usage_limit int CHECK (usage_limit IS NULL OR usage_limit > 0),
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('draft','active','inactive')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  UNIQUE (tenant_id, id),
  CHECK (valid_to >= valid_from),
  CHECK (kind NOT IN ('line_percent','order_percent') OR value <= 100),
  FOREIGN KEY (tenant_id, customer_group_id) REFERENCES customer_groups (tenant_id, id)
);
CREATE UNIQUE INDEX ux_promotions__code ON promotions (tenant_id, code) WHERE code IS NOT NULL;

CREATE TABLE promotion_items (                        -- scope SKUs, gift SKUs, combo components
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, promotion_id uuid NOT NULL,
  role text NOT NULL CHECK (role IN ('eligible','gift','combo_component')),
  sku_id uuid NOT NULL, qty numeric(18,4) NOT NULL DEFAULT 1 CHECK (qty > 0),
  UNIQUE (tenant_id, promotion_id, role, sku_id),
  FOREIGN KEY (tenant_id, promotion_id) REFERENCES promotions (tenant_id, id),
  FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id)
);

CREATE TABLE promotion_usages (                       -- usage_limit accounting; one row per applied doc
  id uuid PRIMARY KEY, tenant_id uuid NOT NULL, promotion_id uuid NOT NULL,
  doc_type text NOT NULL CHECK (doc_type IN ('invoice','sales_document')), doc_id uuid NOT NULL,
  discount_amount numeric(19,4) NOT NULL CHECK (discount_amount >= 0),
  created_at timestamptz NOT NULL,
  UNIQUE (tenant_id, promotion_id, doc_type, doc_id),
  FOREIGN KEY (tenant_id, promotion_id) REFERENCES promotions (tenant_id, id)
);
```

`pricing.PromotionEngine` evaluates eligible promotions (Strategy per `kind`), applies non-stackable best-first, caps by `max_discount`, and writes the applied set into each line's `price_source.promotions` snapshot. Usage counting locks the `promotions` row `FOR UPDATE` when `usage_limit` is set.

## 7. Edit rules (brainstorm §4.4 → Go Specifications)

| Object | Specification (package `catalog`) | Blocks |
|---|---|---|
| Attribute | `AttributeChangeSpec` | `code`/`data_type` change once any product uses it; delete once used |
| Option | `OptionDeleteSpec` | Delete while referenced by product JSON or `sku_variant_values` |
| Attribute set | `AttributeSetChangeSpec` | Removing an attribute hides, never deletes stored values |
| Variant axis | `VariantAxisChangeSpec` | Add/remove axis once any SKU of the product has a stock move; new option → new SKU allowed |
| SKU | `SKUChangeSpec` | Delete once on any document; toggle `track_lot` while on-hand ≠ 0 (CAT-03) |
| Unit conversion | `UnitConversionSpec` | Change base unit once moves exist; factor change allowed (lines keep snapshot) |
| Mix group | `MixGroupChangeSpec` | Pack spec change, removing a sold flavor, delete once used |

Each spec takes a `catalog.UsageReader` port implemented by inventory/sales/procurement repositories (DIP: catalog never imports those packages).
