SET search_path = tally;

CREATE TABLE campaigns (
  id uuid CONSTRAINT pk_campaigns PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_campaigns__tenant_id REFERENCES tenants (id),
  name text NOT NULL, name_key text NOT NULL,
  kind text NOT NULL DEFAULT 'season' CONSTRAINT ck_campaigns__kind CHECK (kind IN ('tet','season','promotion','other')),
  starts_on date NOT NULL, ends_on date NOT NULL,
  budget_amount numeric(19,4), target_revenue numeric(19,4), target_margin_percent numeric(7,4),
  status text NOT NULL DEFAULT 'planning' CONSTRAINT ck_campaigns__status CHECK (status IN ('planning','active','closed')),
  closed_at timestamptz,                              -- closing never resets stock, cash or debt
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_campaigns__id UNIQUE (tenant_id, id), CONSTRAINT ux_campaigns__name_key UNIQUE (tenant_id, name_key),
  CONSTRAINT ck_campaigns__ends_on_starts_on CHECK (ends_on >= starts_on)
);
ALTER TABLE campaigns ENABLE ROW LEVEL SECURITY;
ALTER TABLE campaigns FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON campaigns
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
ALTER TABLE price_lists ADD CONSTRAINT fk_price_lists__campaign_id
  FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE promotions ADD CONSTRAINT fk_promotions__campaign_id
  FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE sales_document_lines ADD CONSTRAINT fk_sales_document_lines__campaign_id
  FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE quotes ADD CONSTRAINT fk_quotes__campaign_id
  FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE invoices ADD CONSTRAINT fk_invoices__campaign_id
  FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE sales_documents ADD CONSTRAINT fk_sales_documents__campaign_id
  FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE assembly_orders ADD CONSTRAINT fk_assembly_orders__campaign_id
  FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);
ALTER TABLE obligations ADD CONSTRAINT fk_obligations__campaign_id
  FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id);

CREATE TABLE campaign_attributions (                  -- one primary per financial line; tags for filtering only
  id uuid CONSTRAINT pk_campaign_attributions PRIMARY KEY, tenant_id uuid NOT NULL, campaign_id uuid NOT NULL,
  doc_type text NOT NULL CONSTRAINT ck_campaign_attributions__doc_type CHECK (doc_type IN ('sales_document','sales_return','receipt','assembly_posting','payment')),
  doc_id uuid NOT NULL, doc_line_id uuid,             -- NULL = whole document
  role text NOT NULL CONSTRAINT ck_campaign_attributions__role CHECK (role IN ('primary','tag')),
  created_by uuid NOT NULL, created_at timestamptz NOT NULL,
  CONSTRAINT ux_campaign_attributions__id UNIQUE (tenant_id, id),
  CONSTRAINT ux_campaign_attributions__campaign_doc_line UNIQUE NULLS NOT DISTINCT (tenant_id, campaign_id, doc_type, doc_id, doc_line_id),
  CONSTRAINT fk_campaign_attributions__campaign_id FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id)
);
CREATE UNIQUE INDEX ux_campaign_attributions__one_primary
  ON campaign_attributions (tenant_id, doc_type, doc_id, doc_line_id) NULLS NOT DISTINCT WHERE role = 'primary';
CREATE INDEX ix_campaign_attributions__campaign ON campaign_attributions (tenant_id, campaign_id, role);
ALTER TABLE campaign_attributions ENABLE ROW LEVEL SECURITY;
ALTER TABLE campaign_attributions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON campaign_attributions
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE campaign_checklist_items (
  id uuid CONSTRAINT pk_campaign_checklist_items PRIMARY KEY, tenant_id uuid NOT NULL, campaign_id uuid NOT NULL,
  phase text NOT NULL CONSTRAINT ck_campaign_checklist_items__phase CHECK (phase IN ('before_peak','closing')),
  kind text NOT NULL CONSTRAINT ck_campaign_checklist_items__kind CHECK (kind IN ('stock_count','statement','open_orders','surplus_stock','custom')),
  title text NOT NULL, done_at timestamptz, done_by uuid, sort_order int NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_campaign_checklist_items__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_campaign_checklist_items__campaign_id FOREIGN KEY (tenant_id, campaign_id) REFERENCES campaigns (tenant_id, id)
);
ALTER TABLE campaign_checklist_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE campaign_checklist_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON campaign_checklist_items
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE customer_activities (                    -- append-only timeline
  id uuid CONSTRAINT pk_customer_activities PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  kind text NOT NULL CONSTRAINT ck_customer_activities__kind CHECK (kind IN ('purchase','return','payment','note','care_call','reorder','consent_change')),
  ref_type text, ref_id uuid, summary text NOT NULL,
  created_by uuid, created_at timestamptz NOT NULL,
  CONSTRAINT ux_customer_activities__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_customer_activities__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE INDEX ix_customer_activities__party ON customer_activities (tenant_id, party_id, created_at DESC);
REVOKE UPDATE, DELETE ON customer_activities FROM tally_app;
ALTER TABLE customer_activities ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_activities FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_activities
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE customer_care_tasks (                    -- lịch chăm sóc / tái đặt đơn
  id uuid CONSTRAINT pk_customer_care_tasks PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  due_on date NOT NULL, title text NOT NULL,
  source_campaign_id uuid,                            -- "khách mùa trước"
  status text NOT NULL DEFAULT 'open' CONSTRAINT ck_customer_care_tasks__status CHECK (status IN ('open','done','skipped')),
  done_at timestamptz, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  CONSTRAINT ux_customer_care_tasks__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_customer_care_tasks__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id),
  CONSTRAINT fk_customer_care_tasks__source_campaign_id FOREIGN KEY (tenant_id, source_campaign_id) REFERENCES campaigns (tenant_id, id)
);
ALTER TABLE customer_care_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_care_tasks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_care_tasks
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE consents (                               -- event log; latest row per (party, channel, purpose) wins
  id uuid CONSTRAINT pk_consents PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  channel text NOT NULL CONSTRAINT ck_consents__channel CHECK (channel IN ('phone','sms','zalo','email')),
  purpose text NOT NULL CONSTRAINT ck_consents__purpose CHECK (purpose IN ('debt_reminder','marketing','care')),
  granted bool NOT NULL, source text NOT NULL, evidence text,
  recorded_by uuid NOT NULL, recorded_at timestamptz NOT NULL,
  CONSTRAINT ux_consents__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_consents__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
CREATE INDEX ix_consents__latest ON consents (tenant_id, party_id, channel, purpose, recorded_at DESC);
REVOKE UPDATE, DELETE ON consents FROM tally_app;
ALTER TABLE consents ENABLE ROW LEVEL SECURITY;
ALTER TABLE consents FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON consents
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE loyalty_programs (                       -- optional, one per tenant
  tenant_id uuid CONSTRAINT pk_loyalty_programs PRIMARY KEY CONSTRAINT fk_loyalty_programs__tenant_id REFERENCES tenants (id),
  enabled bool NOT NULL DEFAULT false,
  points_per_amount numeric(19,4) NOT NULL DEFAULT 0, -- points earned per 1 VND of net revenue
  point_value numeric(19,4) NOT NULL DEFAULT 0,       -- VND per point when redeemed as discount
  updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1
);
ALTER TABLE loyalty_programs ENABLE ROW LEVEL SECURITY;
ALTER TABLE loyalty_programs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON loyalty_programs
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE loyalty_entries (                        -- append-only; returns reverse points
  id uuid CONSTRAINT pk_loyalty_entries PRIMARY KEY, tenant_id uuid NOT NULL, party_id uuid NOT NULL,
  points numeric(18,4) NOT NULL CONSTRAINT ck_loyalty_entries__points CHECK (points <> 0),
  kind text NOT NULL CONSTRAINT ck_loyalty_entries__kind CHECK (kind IN ('earn','redeem','reverse','manual')),
  source_type text NOT NULL, source_id uuid NOT NULL, reversal_of uuid,
  business_date date NOT NULL, created_by uuid, created_at timestamptz NOT NULL,
  CONSTRAINT ux_loyalty_entries__id UNIQUE (tenant_id, id),
  CONSTRAINT ux_loyalty_entries__kind_source_type_source_id UNIQUE (tenant_id, kind, source_type, source_id),
  CONSTRAINT fk_loyalty_entries__party_id FOREIGN KEY (tenant_id, party_id) REFERENCES parties (tenant_id, id)
);
REVOKE UPDATE, DELETE ON loyalty_entries FROM tally_app;
ALTER TABLE loyalty_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE loyalty_entries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON loyalty_entries
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
