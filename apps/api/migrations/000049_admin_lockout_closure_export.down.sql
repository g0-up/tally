SET search_path = tally;

DELETE FROM auth_tokens WHERE purpose = 'closure_export';
ALTER TABLE auth_tokens DROP CONSTRAINT ck_auth_tokens__purposes;
ALTER TABLE auth_tokens ADD CONSTRAINT ck_auth_tokens__purpose_kinds
  CHECK (purpose IN ('verify_email', 'reset_password', 'owner_transfer', 'support_consent', 'closure_verify'));

-- An exporting closure waits again; the next pass queues its final export anew.
UPDATE account_closure_requests SET status = 'waiting' WHERE status IN ('exporting', 'export_failed');
ALTER TABLE account_closure_requests DROP CONSTRAINT ck_account_closure_requests__statuses;
ALTER TABLE account_closure_requests ADD CONSTRAINT ck_account_closure_requests__status
  CHECK (status IN ('requested', 'verified', 'waiting', 'completed', 'cancelled'));

ALTER TABLE platform_admins
  DROP CONSTRAINT ck_platform_admins__last_totp_step,
  DROP COLUMN locked_until,
  DROP COLUMN totp_failed_count;
