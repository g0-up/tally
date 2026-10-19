SET search_path = tally;

CREATE TABLE mix_groups (
  id uuid CONSTRAINT pk_mix_groups PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_mix_groups__tenant_id REFERENCES tenants (id),
  name text NOT NULL, name_key text NOT NULL,         -- replaces a lower(name) expression index
  pack_unit_id uuid NOT NULL, units_per_pack int NOT NULL,   -- 1 thùng = 48 hộp
  part_unit_id uuid NOT NULL, units_per_part int NOT NULL,   -- 1 dây = 4 hộp
  piece_unit_id uuid NOT NULL,                        -- the shared "đơn vị lẻ" (hộp) all flavors use
  source_product_id uuid,                             -- filled when created from a variant product
  status text NOT NULL DEFAULT 'active' CONSTRAINT ck_mix_groups__status CHECK (status IN ('active','inactive')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_mix_groups__id UNIQUE (tenant_id, id), CONSTRAINT ux_mix_groups__name_key UNIQUE (tenant_id, name_key),
  CONSTRAINT ck_mix_groups__units_per_part_units_per_pack CHECK (units_per_part > 0 AND units_per_pack % units_per_part = 0
         AND units_per_pack / units_per_part >= 2),
  CONSTRAINT fk_mix_groups__pack_unit_id FOREIGN KEY (tenant_id, pack_unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_mix_groups__part_unit_id FOREIGN KEY (tenant_id, part_unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_mix_groups__piece_unit_id FOREIGN KEY (tenant_id, piece_unit_id) REFERENCES units (tenant_id, id),
  CONSTRAINT fk_mix_groups__source_product_id FOREIGN KEY (tenant_id, source_product_id) REFERENCES products (tenant_id, id)
);
ALTER TABLE mix_groups ENABLE ROW LEVEL SECURITY;
ALTER TABLE mix_groups FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON mix_groups
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE mix_group_items (
  tenant_id uuid NOT NULL, mix_group_id uuid NOT NULL, sku_id uuid NOT NULL,
  flavor_label text NOT NULL, sort_order int NOT NULL DEFAULT 0,
  active bool NOT NULL DEFAULT true,                  -- flavors already sold are deactivated, never removed
  CONSTRAINT pk_mix_group_items PRIMARY KEY (tenant_id, mix_group_id, sku_id),
  CONSTRAINT ux_mix_group_items__mix_group_id_flavor_label UNIQUE (tenant_id, mix_group_id, flavor_label),
  CONSTRAINT fk_mix_group_items__mix_group_id FOREIGN KEY (tenant_id, mix_group_id) REFERENCES mix_groups (tenant_id, id),
  CONSTRAINT fk_mix_group_items__sku_id FOREIGN KEY (tenant_id, sku_id) REFERENCES skus (tenant_id, id)
);
ALTER TABLE mix_group_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE mix_group_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON mix_group_items
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
