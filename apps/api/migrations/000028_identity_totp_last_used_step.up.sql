SET search_path = tally;

-- A TOTP code stays valid for its whole time window (and one step either side), so the step of
-- the last accepted code is stored and a code whose step is not greater is refused: one code
-- signs in once. NULL means no code was accepted yet for the current secret.

ALTER TABLE mfa_totp_secrets ADD COLUMN last_used_step bigint
  CONSTRAINT ck_mfa_totp_secrets__last_used_step CHECK (last_used_step >= 0);
