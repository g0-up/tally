SET search_path = tally;

ALTER TABLE export_jobs DROP COLUMN heartbeat_at;
DROP INDEX ix_sku_stock__sku;
