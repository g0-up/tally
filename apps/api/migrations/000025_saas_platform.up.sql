SET search_path = tally;

-- Platform tables: no RLS. Only the identity and saas repositories touch them, through platform
-- transactions that filter tenant_id explicitly. tally_readonly never reads them.

CREATE TABLE plans (                                  -- global seed by migration (plans only)
  id uuid CONSTRAINT pk_plans PRIMARY KEY, code text NOT NULL CONSTRAINT ux_plans__code UNIQUE, name text NOT NULL,
  kind text NOT NULL CONSTRAINT ck_plans__kind CHECK (kind IN ('year_round','seasonal','addon','trial')),
  billing_period text NOT NULL CONSTRAINT ck_plans__billing_period CHECK (billing_period IN ('month','season','year','none')),
  price numeric(19,4) NOT NULL DEFAULT 0,             -- price not decided: admin-editable
  modules text[] NOT NULL DEFAULT '{}',               -- 'pos','kits','campaign','crm','reports_advanced','ai_qa'
  limits jsonb NOT NULL DEFAULT '{}',                 -- {"skus":20000,"storage_mb":2048}
  active bool NOT NULL DEFAULT true, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);

CREATE TABLE subscriptions (
  id uuid CONSTRAINT pk_subscriptions PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_subscriptions__tenant_id REFERENCES tenants (id), plan_id uuid NOT NULL CONSTRAINT fk_subscriptions__plan_id REFERENCES plans (id),
  status text NOT NULL CONSTRAINT ck_subscriptions__status CHECK (status IN ('trialing','active','grace','read_only','closed')),
  current_period_start date NOT NULL, current_period_end date NOT NULL,
  trial_ends_on date, grace_ends_on date, read_only_ends_on date,
  cancel_at_period_end bool NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL, version int NOT NULL DEFAULT 1,
  CONSTRAINT ux_subscriptions__id UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX ux_subscriptions__one_live ON subscriptions (tenant_id) WHERE status <> 'closed' AND plan_id IS NOT NULL;

CREATE TABLE entitlements (                           -- server-side module/limit gate
  id uuid CONSTRAINT pk_entitlements PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_entitlements__tenant_id REFERENCES tenants (id),
  key text NOT NULL, value jsonb NOT NULL,            -- true / number limit
  source_subscription_id uuid CONSTRAINT fk_entitlements__source_subscription_id REFERENCES subscriptions (id),
  valid_from date NOT NULL, valid_to date,
  created_at timestamptz NOT NULL,
  CONSTRAINT ux_entitlements__key_valid_from UNIQUE (tenant_id, key, valid_from)
);

CREATE TABLE saas_invoices (                          -- service billing, separate from store finance
  id uuid CONSTRAINT pk_saas_invoices PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_saas_invoices__tenant_id REFERENCES tenants (id),
  number text NOT NULL CONSTRAINT ux_saas_invoices__number UNIQUE, subscription_id uuid CONSTRAINT fk_saas_invoices__subscription_id REFERENCES subscriptions (id),
  period_start date NOT NULL, period_end date NOT NULL,
  amount numeric(19,4) NOT NULL, proration_note text,
  status text NOT NULL CONSTRAINT ck_saas_invoices__status CHECK (status IN ('draft','open','paid','void')),
  provider text, provider_ref text,
  issued_at timestamptz NOT NULL, paid_at timestamptz,
  CONSTRAINT ux_saas_invoices__provider_provider_ref UNIQUE (provider, provider_ref)
);

CREATE TABLE saas_payment_events (                    -- duplicate callbacks never extend twice
  id uuid CONSTRAINT pk_saas_payment_events PRIMARY KEY, provider text NOT NULL, event_id text NOT NULL,
  saas_invoice_id uuid CONSTRAINT fk_saas_payment_events__saas_invoice_id REFERENCES saas_invoices (id),
  payload jsonb NOT NULL, received_at timestamptz NOT NULL, processed_at timestamptz,
  CONSTRAINT ux_saas_payment_events__provider_event_id UNIQUE (provider, event_id)
);

CREATE TABLE support_tickets (
  id uuid CONSTRAINT pk_support_tickets PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_support_tickets__tenant_id REFERENCES tenants (id),
  number text NOT NULL CONSTRAINT ux_support_tickets__number UNIQUE, subject text NOT NULL,
  status text NOT NULL CONSTRAINT ck_support_tickets__status CHECK (status IN ('open','waiting_customer','in_progress','resolved','closed')),
  priority text NOT NULL CONSTRAINT ck_support_tickets__priority CHECK (priority IN ('low','normal','high','urgent')),
  sla_due_at timestamptz, assigned_admin_id uuid CONSTRAINT fk_support_tickets__assigned_admin_id REFERENCES platform_admins (id),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);

CREATE TABLE support_ticket_messages (
  id uuid CONSTRAINT pk_support_ticket_messages PRIMARY KEY, ticket_id uuid NOT NULL CONSTRAINT fk_support_ticket_messages__ticket_id REFERENCES support_tickets (id),
  author_type text NOT NULL CONSTRAINT ck_support_ticket_messages__author_type CHECK (author_type IN ('owner','platform_admin')), author_id uuid NOT NULL,
  body text NOT NULL, attachment_ids uuid[] NOT NULL DEFAULT '{}', created_at timestamptz NOT NULL
);

CREATE TABLE support_sessions (                       -- consented, scoped, expiring, read-only by default
  id uuid CONSTRAINT pk_support_sessions PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_support_sessions__tenant_id REFERENCES tenants (id),
  admin_id uuid NOT NULL CONSTRAINT fk_support_sessions__admin_id REFERENCES platform_admins (id), ticket_id uuid CONSTRAINT fk_support_sessions__ticket_id REFERENCES support_tickets (id),
  scope text NOT NULL DEFAULT 'read_only' CONSTRAINT ck_support_sessions__scope CHECK (scope IN ('read_only')),   -- write scope out of 1.1
  modules text[] NOT NULL DEFAULT '{}',
  consent_token_id uuid NOT NULL CONSTRAINT fk_support_sessions__consent_token_id REFERENCES auth_tokens (id),
  granted_at timestamptz NOT NULL, expires_at timestamptz NOT NULL, revoked_at timestamptz,
  CONSTRAINT ck_support_sessions__expires_at_granted_at CHECK (expires_at > granted_at)
);

CREATE TABLE feature_flags (                          -- history in feature_flag_changes
  key text NOT NULL, tenant_id uuid CONSTRAINT fk_feature_flags__tenant_id REFERENCES tenants (id),
  enabled bool NOT NULL, updated_at timestamptz NOT NULL,
  CONSTRAINT ux_feature_flags__key_tenant_id UNIQUE NULLS NOT DISTINCT (key, tenant_id)
);

CREATE TABLE feature_flag_changes (                   -- append-only
  id uuid CONSTRAINT pk_feature_flag_changes PRIMARY KEY, key text NOT NULL, tenant_id uuid,
  enabled bool NOT NULL, admin_id uuid NOT NULL CONSTRAINT fk_feature_flag_changes__admin_id REFERENCES platform_admins (id), created_at timestamptz NOT NULL
);
REVOKE UPDATE, DELETE ON feature_flag_changes FROM tally_app;

CREATE TABLE tenant_usage_daily (                     -- usage only, no sales content
  tenant_id uuid NOT NULL CONSTRAINT fk_tenant_usage_daily__tenant_id REFERENCES tenants (id), usage_date date NOT NULL,
  active_skus int NOT NULL, documents_posted int NOT NULL, storage_bytes bigint NOT NULL,
  CONSTRAINT pk_tenant_usage_daily PRIMARY KEY (tenant_id, usage_date)
);

CREATE TABLE account_closure_requests (
  id uuid CONSTRAINT pk_account_closure_requests PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_account_closure_requests__tenant_id REFERENCES tenants (id),
  requested_by uuid NOT NULL, verified_at timestamptz,
  export_job_id uuid,                                 -- full data export offered first
  waiting_until date NOT NULL, legal_hold bool NOT NULL DEFAULT false,
  status text NOT NULL CONSTRAINT ck_account_closure_requests__status CHECK (status IN ('requested','verified','waiting','completed','cancelled')),
  created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);

CREATE TABLE product_feedback (                       -- minimal, no document content
  id uuid CONSTRAINT pk_product_feedback PRIMARY KEY, tenant_id uuid NOT NULL CONSTRAINT fk_product_feedback__tenant_id REFERENCES tenants (id),
  kind text NOT NULL CONSTRAINT ck_product_feedback__kind CHECK (kind IN ('idea','bug','praise','nps')),
  score int CONSTRAINT ck_product_feedback__score CHECK (score IS NULL OR (score >= 0 AND score <= 10)),
  body text, page_path text, created_at timestamptz NOT NULL
);
-- Consent flags live in tenant_settings: product_analytics_enabled, analytics_ai_enabled; both default false

CREATE TABLE incidents (                              -- status page
  id uuid CONSTRAINT pk_incidents PRIMARY KEY, title text NOT NULL,
  status text NOT NULL CONSTRAINT ck_incidents__status CHECK (status IN ('investigating','identified','monitoring','resolved')),
  impact text NOT NULL CONSTRAINT ck_incidents__impact CHECK (impact IN ('none','minor','major','critical')),
  started_at timestamptz NOT NULL, resolved_at timestamptz, updates jsonb NOT NULL DEFAULT '[]'
);

CREATE TABLE partners (                               -- reseller/referral, no data access by default
  id uuid CONSTRAINT pk_partners PRIMARY KEY, name text NOT NULL, referral_code text NOT NULL CONSTRAINT ux_partners__referral_code UNIQUE,
  commission_percent numeric(7,4) NOT NULL DEFAULT 0, active bool NOT NULL DEFAULT true,
  created_at timestamptz NOT NULL
);

CREATE TABLE tenant_referrals (
  tenant_id uuid CONSTRAINT pk_tenant_referrals PRIMARY KEY CONSTRAINT fk_tenant_referrals__tenant_id REFERENCES tenants (id), partner_id uuid NOT NULL CONSTRAINT fk_tenant_referrals__partner_id REFERENCES partners (id),
  referred_at timestamptz NOT NULL
);

REVOKE ALL ON plans, subscriptions, entitlements, saas_invoices, saas_payment_events, support_tickets,
  support_ticket_messages, support_sessions, feature_flags, feature_flag_changes, tenant_usage_daily,
  account_closure_requests, product_feedback, incidents, partners, tenant_referrals FROM tally_readonly;

-- Global plan catalogue. Packages are a commercial hypothesis and prices are not decided, so every
-- price starts at 0 and stays admin-editable. Constant ids and literal timestamps keep the seed
-- reproducible; limits are public and never block essential work during peak season.
INSERT INTO plans (id, code, name, kind, billing_period, price, modules, limits, active, created_at, updated_at) VALUES
  ('00000000-0000-7000-8000-000000000001', 'trial', 'Dùng thử', 'trial', 'none', 0,
   '{pos,kits,campaign,crm,reports_advanced}', '{"skus":20000,"storage_mb":2048}', true,
   '2026-10-16 00:00:00+00', '2026-10-16 00:00:00+00'),
  ('00000000-0000-7000-8000-000000000002', 'store', 'Cửa hàng', 'year_round', 'year', 0,
   '{pos}', '{"skus":20000,"storage_mb":2048}', true,
   '2026-10-16 00:00:00+00', '2026-10-16 00:00:00+00'),
  ('00000000-0000-7000-8000-000000000003', 'wholesale', 'Bán sỉ', 'year_round', 'year', 0,
   '{pos,campaign,crm,reports_advanced}', '{"skus":20000,"storage_mb":2048}', true,
   '2026-10-16 00:00:00+00', '2026-10-16 00:00:00+00'),
  ('00000000-0000-7000-8000-000000000004', 'gift_basket', 'Giỏ quà', 'year_round', 'year', 0,
   '{pos,kits,campaign,crm,reports_advanced}', '{"skus":20000,"storage_mb":2048}', true,
   '2026-10-16 00:00:00+00', '2026-10-16 00:00:00+00'),
  ('00000000-0000-7000-8000-000000000005', 'seasonal', 'Theo mùa', 'seasonal', 'season', 0,
   '{pos,kits,campaign,crm,reports_advanced}', '{"skus":20000,"storage_mb":2048}', true,
   '2026-10-16 00:00:00+00', '2026-10-16 00:00:00+00'),
  ('00000000-0000-7000-8000-000000000006', 'addon_ai_qa', 'Hỏi đáp AI', 'addon', 'month', 0,
   '{ai_qa}', '{}', true,
   '2026-10-16 00:00:00+00', '2026-10-16 00:00:00+00');
