SET search_path = tally;

-- Fails while a replaced tenant exists: its status cannot be expressed without 'replaced'.
ALTER TABLE tenants DROP CONSTRAINT ck_tenants__replaced_sandbox;
ALTER TABLE tenants DROP CONSTRAINT ck_tenants__status_kinds;
ALTER TABLE tenants ADD CONSTRAINT ck_tenants__status
  CHECK (status IN ('active','read_only','closing','closed'));
