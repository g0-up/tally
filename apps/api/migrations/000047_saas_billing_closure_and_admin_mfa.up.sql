SET search_path = tally;

-- Lifecycle durations and the support SLA are published plan terms, kept in limits beside the
-- module limits. A seasonal plan also states how many days one season lasts.
UPDATE plans SET limits = limits || '{"trial_days":14,"grace_days":7,"read_only_days":60,"support_sla_hours":48}'::jsonb
WHERE code = 'trial';
UPDATE plans SET limits = limits || '{"grace_days":7,"read_only_days":60,"support_sla_hours":24}'::jsonb
WHERE kind IN ('year_round', 'seasonal');
UPDATE plans SET limits = limits || '{"period_days":120}'::jsonb WHERE code = 'seasonal';

-- An invoice states the plan it buys and what paying it does to the subscription, so a payment
-- applies the purchase it was issued for.
ALTER TABLE saas_invoices ADD COLUMN plan_id uuid CONSTRAINT fk_saas_invoices__plan_id REFERENCES plans (id);
ALTER TABLE saas_invoices ADD COLUMN purpose text NOT NULL DEFAULT 'new_period'
  CONSTRAINT ck_saas_invoices__purpose CHECK (purpose IN ('new_period', 'renewal', 'plan_change', 'addon'));
CREATE INDEX ix_saas_invoices__tenant_issued ON saas_invoices (tenant_id, issued_at);
CREATE INDEX ix_support_tickets__tenant_created ON support_tickets (tenant_id, created_at);
CREATE INDEX ix_support_ticket_messages__ticket ON support_ticket_messages (ticket_id, created_at);
CREATE INDEX ix_support_sessions__tenant_granted ON support_sessions (tenant_id, granted_at);
CREATE INDEX ix_account_closure_requests__tenant ON account_closure_requests (tenant_id, created_at);

-- Skippable onboarding steps record who skipped them, apart from completion.
ALTER TABLE onboarding_steps ADD COLUMN skipped_at timestamptz;
ALTER TABLE onboarding_steps ADD COLUMN skipped_by uuid;

-- A closure is verified with an emailed one-time token.
ALTER TABLE auth_tokens DROP CONSTRAINT ck_auth_tokens__purpose;
ALTER TABLE auth_tokens ADD CONSTRAINT ck_auth_tokens__purpose_kinds
  CHECK (purpose IN ('verify_email', 'reset_password', 'owner_transfer', 'support_consent', 'closure_verify'));

-- The last accepted TOTP step of an admin, so one code never signs in twice.
ALTER TABLE platform_admins ADD COLUMN last_totp_step bigint;

-- A tenant under maintenance (a restore copy in progress) refuses every write.
ALTER TABLE tenants ADD COLUMN maintenance bool NOT NULL DEFAULT false;
