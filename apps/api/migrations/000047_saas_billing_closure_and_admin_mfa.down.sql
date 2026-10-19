SET search_path = tally;

ALTER TABLE tenants DROP COLUMN maintenance;
ALTER TABLE platform_admins DROP COLUMN last_totp_step;

DELETE FROM auth_tokens WHERE purpose = 'closure_verify';
ALTER TABLE auth_tokens DROP CONSTRAINT ck_auth_tokens__purpose_kinds;
ALTER TABLE auth_tokens ADD CONSTRAINT ck_auth_tokens__purpose
  CHECK (purpose IN ('verify_email', 'reset_password', 'owner_transfer', 'support_consent'));

ALTER TABLE onboarding_steps DROP COLUMN skipped_by;
ALTER TABLE onboarding_steps DROP COLUMN skipped_at;

DROP INDEX ix_account_closure_requests__tenant;
DROP INDEX ix_support_sessions__tenant_granted;
DROP INDEX ix_support_ticket_messages__ticket;
DROP INDEX ix_support_tickets__tenant_created;
DROP INDEX ix_saas_invoices__tenant_issued;
ALTER TABLE saas_invoices DROP COLUMN purpose;
ALTER TABLE saas_invoices DROP COLUMN plan_id;

UPDATE plans SET limits = limits - 'period_days' - 'trial_days' - 'grace_days' - 'read_only_days' - 'support_sla_hours';
