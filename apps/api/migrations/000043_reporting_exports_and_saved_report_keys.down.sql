SET search_path = tally;

DROP INDEX ix_stock_moves__date;
DROP INDEX ix_cash_entries__date;

-- The narrower checks below refuse rows the wider ones allowed, so those rows are folded into
-- values the narrower checks accept first. Row security is lifted while they are rewritten, since
-- no tenant is set during a migration.
ALTER TABLE saved_reports NO FORCE ROW LEVEL SECURITY;
DELETE FROM saved_reports WHERE report_key IN ('lot_stock', 'debt_schedule');
ALTER TABLE saved_reports FORCE ROW LEVEL SECURITY;
ALTER TABLE saved_reports DROP CONSTRAINT ck_saved_reports__report_key_values;
ALTER TABLE saved_reports ADD CONSTRAINT ck_saved_reports__report_key
  CHECK (report_key IN ('nxt', 'revenue', 'gross_margin', 'debt_aging', 'cash_flow', 'kits', 'season'));

-- An export file over 20 MiB loses its attachment row and its job expires; the stored object is
-- left for the object store's own cleanup.
ALTER TABLE export_jobs NO FORCE ROW LEVEL SECURITY;
ALTER TABLE attachments NO FORCE ROW LEVEL SECURITY;
UPDATE export_jobs SET status = 'expired', attachment_id = NULL
WHERE attachment_id IN (SELECT id FROM attachments WHERE owner_type = 'export_job' AND size_bytes > 20971520);
DELETE FROM attachments WHERE owner_type = 'export_job' AND size_bytes > 20971520;
UPDATE export_jobs SET status = 'failed' WHERE status = 'cancelled';
ALTER TABLE attachments FORCE ROW LEVEL SECURITY;
ALTER TABLE export_jobs FORCE ROW LEVEL SECURITY;

ALTER TABLE attachments DROP CONSTRAINT ck_attachments__size_bytes_limit;
ALTER TABLE attachments ADD CONSTRAINT ck_attachments__size_bytes
  CHECK (size_bytes > 0 AND size_bytes <= 20971520);

ALTER TABLE export_jobs DROP CONSTRAINT ck_export_jobs__status_values;
ALTER TABLE export_jobs ADD CONSTRAINT ck_export_jobs__status
  CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'expired'));
