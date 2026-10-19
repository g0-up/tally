SET search_path = tally;

-- Why a manual hold was last released. Every release and its reason is also in the audit log.
ALTER TABLE reservations ADD COLUMN release_reason text;
