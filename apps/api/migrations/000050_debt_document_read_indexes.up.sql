SET search_path = tally;

-- The payment, DN, BT and statement lists read a tenant's newest documents first.
CREATE INDEX ix_payments__posted ON payments (tenant_id, posted_at, id);
CREATE INDEX ix_debt_adjustments__posted ON debt_adjustments (tenant_id, posted_at, id);
CREATE INDEX ix_debt_offsets__posted ON debt_offsets (tenant_id, posted_at, id);
CREATE INDEX ix_statements__created ON statements (tenant_id, created_at, id);
