SET search_path = tally;

-- A change with no value removes a store's override, so the store follows the global value
-- again. The global value itself is never removed.
ALTER TABLE feature_flag_changes ALTER COLUMN enabled DROP NOT NULL;
ALTER TABLE feature_flag_changes ADD CONSTRAINT ck_feature_flag_changes__removal_is_override
  CHECK (enabled IS NOT NULL OR tenant_id IS NOT NULL);
