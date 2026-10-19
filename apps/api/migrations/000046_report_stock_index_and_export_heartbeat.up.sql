SET search_path = tally;

-- Stock reports sum a SKU's stock over its warehouses.
CREATE INDEX ix_sku_stock__sku ON sku_stock (tenant_id, sku_id);

-- A running export refreshes its heartbeat as it writes, so only one that stopped writing is taken
-- for interrupted, however long it runs.
ALTER TABLE export_jobs ADD COLUMN heartbeat_at timestamptz;
