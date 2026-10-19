SET search_path = tally;

ALTER TABLE notifications DROP CONSTRAINT ck_notifications__resolved_by_resolved_at;
ALTER TABLE notifications DROP COLUMN resolved_by;
