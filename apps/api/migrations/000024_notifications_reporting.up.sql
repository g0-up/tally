SET search_path = tally;

CREATE TABLE notifications (
  id uuid CONSTRAINT pk_notifications PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_notifications__tenant_id REFERENCES tenants (id),
  kind text NOT NULL CONSTRAINT ck_notifications__kind CHECK (kind IN ('receivable_overdue','payable_due_soon','lot_expiring','lot_expired',
      'below_min_stock','surplus_stock','pos_sync_exception','statement_disputed','subscription_status','export_ready')),
  dedupe_key text NOT NULL,                           -- e.g. 'lot_expiring:<lot_id>:<expiry>'
  severity text NOT NULL CONSTRAINT ck_notifications__severity CHECK (severity IN ('info','warn','bad')),
  title text NOT NULL, body text,
  entity_type text, entity_id uuid, color_id uuid,    -- lot notices carry the color
  action_path text,                                   -- web route to open
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  read_at timestamptz, resolved_at timestamptz, snoozed_until timestamptz,
  CONSTRAINT ux_notifications__id UNIQUE (tenant_id, id), CONSTRAINT ux_notifications__dedupe_key UNIQUE (tenant_id, dedupe_key)
);
CREATE INDEX ix_notifications__inbox ON notifications (tenant_id, resolved_at, created_at DESC);
ALTER TABLE notifications ENABLE ROW LEVEL SECURITY;
ALTER TABLE notifications FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON notifications
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE saved_reports (                          -- custom report columns and filters
  id uuid CONSTRAINT pk_saved_reports PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_saved_reports__tenant_id REFERENCES tenants (id),
  report_key text NOT NULL CONSTRAINT ck_saved_reports__report_key CHECK (report_key IN ('nxt','revenue','gross_margin','debt_aging','cash_flow','kits','season')),
  name text NOT NULL, config jsonb NOT NULL,          -- validated by reporting.ReportConfigSchema
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
  CONSTRAINT ux_saved_reports__id UNIQUE (tenant_id, id), CONSTRAINT ux_saved_reports__report_key_name UNIQUE (tenant_id, report_key, name)
);
ALTER TABLE saved_reports ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_reports FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON saved_reports
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE export_jobs (                            -- async report CSV and full data export
  id uuid CONSTRAINT pk_export_jobs PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_export_jobs__tenant_id REFERENCES tenants (id),
  kind text NOT NULL CONSTRAINT ck_export_jobs__kind CHECK (kind IN ('report_csv','full_data_export')),
  params jsonb NOT NULL,
  status text NOT NULL DEFAULT 'queued' CONSTRAINT ck_export_jobs__status CHECK (status IN ('queued','running','succeeded','failed','expired')),
  attachment_id uuid, row_count bigint, error text,
  requested_by uuid NOT NULL, created_at timestamptz NOT NULL,
  started_at timestamptz, finished_at timestamptz, expires_at timestamptz,
  CONSTRAINT ux_export_jobs__id UNIQUE (tenant_id, id),
  CONSTRAINT fk_export_jobs__attachment_id FOREIGN KEY (tenant_id, attachment_id) REFERENCES attachments (tenant_id, id)
);
ALTER TABLE export_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE export_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON export_jobs
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE cost_recalculation_runs (                -- backdate recost: preview → owner confirms → apply
  id uuid CONSTRAINT pk_cost_recalculation_runs PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_cost_recalculation_runs__tenant_id REFERENCES tenants (id),
  sku_ids uuid[] NOT NULL, from_date date NOT NULL,
  status text NOT NULL DEFAULT 'previewed' CONSTRAINT ck_cost_recalculation_runs__status CHECK (status IN ('previewed','confirmed','applied','discarded')),
  impact jsonb NOT NULL,                              -- per SKU old/new avg cost, COGS delta
  run_version int NOT NULL,
  requested_by uuid NOT NULL, confirmed_by uuid,
  created_at timestamptz NOT NULL, applied_at timestamptz,
  CONSTRAINT ux_cost_recalculation_runs__id UNIQUE (tenant_id, id), CONSTRAINT ux_cost_recalculation_runs__run_version UNIQUE (tenant_id, run_version)
);
ALTER TABLE cost_recalculation_runs ENABLE ROW LEVEL SECURITY;
ALTER TABLE cost_recalculation_runs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON cost_recalculation_runs
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE onboarding_steps (                       -- onboarding checklist incl. opening-balance confirmation
  tenant_id uuid NOT NULL CONSTRAINT fk_onboarding_steps__tenant_id REFERENCES tenants (id),
  step_key text NOT NULL CONSTRAINT ck_onboarding_steps__step_key CHECK (step_key IN ('store_profile','catalog_setup','opening_stock','opening_debt',
      'opening_cash','first_sale','confirm_opening_balances')),
  completed_at timestamptz, completed_by uuid,
  CONSTRAINT pk_onboarding_steps PRIMARY KEY (tenant_id, step_key)
);
ALTER TABLE onboarding_steps ENABLE ROW LEVEL SECURITY;
ALTER TABLE onboarding_steps FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON onboarding_steps
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

CREATE TABLE analytics_queries (                      -- audit of read-only AI Q&A (only if enabled)
  id uuid CONSTRAINT pk_analytics_queries PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_analytics_queries__tenant_id REFERENCES tenants (id),
  question text NOT NULL, answer text, cited_refs jsonb NOT NULL DEFAULT '[]',
  model text NOT NULL, asked_by uuid NOT NULL, created_at timestamptz NOT NULL,
  CONSTRAINT ux_analytics_queries__id UNIQUE (tenant_id, id)
);
ALTER TABLE analytics_queries ENABLE ROW LEVEL SECURITY;
ALTER TABLE analytics_queries FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON analytics_queries
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
