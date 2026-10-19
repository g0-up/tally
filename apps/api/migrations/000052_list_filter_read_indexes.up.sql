SET search_path = tally;

-- A user's export jobs, newest first.
CREATE INDEX ix_export_jobs__requested_by ON export_jobs (tenant_id, requested_by, created_at, id);

-- What was paid toward a page of invoices: their earmarked deposits and their sales documents.
CREATE INDEX ix_payments__earmarked_invoice ON payments (tenant_id, earmarked_invoice_id) WHERE earmarked_invoice_id IS NOT NULL;
CREATE INDEX ix_sales_documents__invoice ON sales_documents (tenant_id, invoice_id) WHERE invoice_id IS NOT NULL;

-- Stock adjustment and count lists, newest first, with a summary of each document's lines.
CREATE INDEX ix_stock_adjustments__posted_at ON stock_adjustments (tenant_id, posted_at, id);
CREATE INDEX ix_stock_adjustment_lines__stock_adjustment_id ON stock_adjustment_lines (tenant_id, stock_adjustment_id);
CREATE INDEX ix_stock_counts__created_at ON stock_counts (tenant_id, created_at, id);
