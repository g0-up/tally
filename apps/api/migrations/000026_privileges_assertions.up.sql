SET search_path = tally;

-- Re-asserts the role model for every table, independent of the default privileges that were in
-- effect when each table was created. Every statement is idempotent.

GRANT USAGE ON SCHEMA tally TO tally_app, tally_readonly;
REVOKE CREATE ON SCHEMA tally FROM tally_app, tally_readonly;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA tally TO tally_app;
GRANT SELECT ON ALL TABLES IN SCHEMA tally TO tally_readonly;

-- Append-only tables: tally_app may only read and insert.
REVOKE UPDATE, DELETE ON
  stock_moves, price_changes, audit_events, period_locks, cash_entries, exception_confirmations,
  invoice_versions, statement_versions, lot_genealogy, loyalty_entries, obligation_settlements,
  customer_activities, cogs_adjustments, consents, feature_flag_changes
FROM tally_app;

-- Platform tables are outside RLS, so the read-only support role never sees them.
REVOKE ALL ON
  tenants, retired_tenant_slugs, users, sessions, auth_tokens, mfa_totp_secrets, mfa_recovery_codes,
  login_attempts, platform_admins, platform_admin_sessions,
  plans, subscriptions, entitlements, saas_invoices, saas_payment_events, support_tickets,
  support_ticket_messages, support_sessions, feature_flags, feature_flag_changes, tenant_usage_daily,
  account_closure_requests, product_feedback, incidents, partners, tenant_referrals
FROM tally_readonly;

-- The migration bookkeeping table belongs to the owner alone.
REVOKE ALL ON schema_migrations FROM tally_app, tally_readonly;
