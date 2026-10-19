SET search_path = tally;

-- Who resolved a notification: the owner, or the system when its condition cleared. A notice
-- the owner resolved stays resolved while its condition holds; once the system saw it clear, the
-- condition coming back reopens it.
ALTER TABLE notifications ADD COLUMN resolved_by text
  CONSTRAINT ck_notifications__resolved_by CHECK (resolved_by IN ('user', 'system'));
ALTER TABLE notifications ADD CONSTRAINT ck_notifications__resolved_by_resolved_at
  CHECK ((resolved_by IS NULL) = (resolved_at IS NULL));
