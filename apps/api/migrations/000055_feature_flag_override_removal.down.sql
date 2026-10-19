SET search_path = tally;

ALTER TABLE feature_flag_changes DROP CONSTRAINT ck_feature_flag_changes__removal_is_override;
DELETE FROM feature_flag_changes WHERE enabled IS NULL;
ALTER TABLE feature_flag_changes ALTER COLUMN enabled SET NOT NULL;
