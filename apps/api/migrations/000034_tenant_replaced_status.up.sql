SET search_path = tally;

-- A replaced tenant is a sandbox that a reset superseded with a new tenant. It keeps every row it
-- ever wrote (ledgers and audit are append-only), but it no longer owns a host, an account or a
-- session. Only a sandbox can be replaced: a real store is closed, never superseded.

ALTER TABLE tenants DROP CONSTRAINT ck_tenants__status;
ALTER TABLE tenants ADD CONSTRAINT ck_tenants__status_kinds
  CHECK (status IN ('active','read_only','closing','closed','replaced'));
ALTER TABLE tenants ADD CONSTRAINT ck_tenants__replaced_sandbox CHECK (status <> 'replaced' OR is_sandbox);
