SET search_path = tally;

ALTER TABLE mfa_totp_secrets DROP COLUMN last_used_step;
