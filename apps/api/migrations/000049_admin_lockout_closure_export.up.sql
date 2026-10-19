SET search_path = tally;

-- Wrong TOTP codes in a row since the last sign-in of an operator; enough of them lock the
-- account until locked_until.
ALTER TABLE platform_admins
  ADD COLUMN totp_failed_count integer NOT NULL DEFAULT 0
    CONSTRAINT ck_platform_admins__totp_failed_count CHECK (totp_failed_count >= 0),
  ADD COLUMN locked_until timestamptz,
  ADD CONSTRAINT ck_platform_admins__last_totp_step CHECK (last_totp_step >= 0);

-- A closure whose wait ended takes its final full data export while the store only reads
-- ('exporting'); when that export keeps failing it waits for an operator ('export_failed').
ALTER TABLE account_closure_requests DROP CONSTRAINT ck_account_closure_requests__status;
ALTER TABLE account_closure_requests ADD CONSTRAINT ck_account_closure_requests__statuses
  CHECK (status IN ('requested', 'verified', 'waiting', 'exporting', 'export_failed', 'completed', 'cancelled'));

-- The owner of a closed store downloads its final export through an emailed link.
ALTER TABLE auth_tokens DROP CONSTRAINT ck_auth_tokens__purpose_kinds;
ALTER TABLE auth_tokens ADD CONSTRAINT ck_auth_tokens__purposes
  CHECK (purpose IN ('verify_email', 'reset_password', 'owner_transfer', 'support_consent', 'closure_verify',
    'closure_export'));
