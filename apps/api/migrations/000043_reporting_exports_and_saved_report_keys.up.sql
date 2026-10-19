SET search_path = tally;

-- An export the owner cancels stops between pages and keeps its own status.
ALTER TABLE export_jobs DROP CONSTRAINT ck_export_jobs__status;
ALTER TABLE export_jobs ADD CONSTRAINT ck_export_jobs__status_values
  CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'expired', 'cancelled'));

-- An uploaded file stays within 20 MiB; a generated export file may reach 512 MiB.
ALTER TABLE attachments DROP CONSTRAINT ck_attachments__size_bytes;
ALTER TABLE attachments ADD CONSTRAINT ck_attachments__size_bytes_limit
  CHECK (size_bytes > 0 AND (size_bytes <= 20971520 OR (owner_type = 'export_job' AND size_bytes <= 536870912)));

-- Lot stock and the collection/payment schedule are saved reports too.
ALTER TABLE saved_reports DROP CONSTRAINT ck_saved_reports__report_key;
ALTER TABLE saved_reports ADD CONSTRAINT ck_saved_reports__report_key_values
  CHECK (report_key IN ('nxt', 'lot_stock', 'revenue', 'gross_margin', 'debt_aging', 'debt_schedule', 'cash_flow', 'kits', 'season'));

-- Period reports read the cash and stock ledgers of every fund and SKU by date.
CREATE INDEX ix_cash_entries__date ON cash_entries (tenant_id, business_date);
CREATE INDEX ix_stock_moves__date ON stock_moves (tenant_id, business_date);
