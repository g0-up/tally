SET search_path = tally;

-- Period reports read returns, debt documents, settlements and late cost deltas by date.
CREATE INDEX ix_sales_returns__date ON sales_returns (tenant_id, business_date);
CREATE INDEX ix_obligations__date ON obligations (tenant_id, direction, business_date);
CREATE INDEX ix_obligation_settlements__date ON obligation_settlements (tenant_id, business_date);
CREATE INDEX ix_cogs_adjustments__date ON cogs_adjustments (tenant_id, business_date);

-- The export runner picks queued jobs oldest first; the history lists a tenant's newest first.
CREATE INDEX ix_export_jobs__status ON export_jobs (tenant_id, status, created_at);
CREATE INDEX ix_analytics_queries__created ON analytics_queries (tenant_id, created_at, id);
