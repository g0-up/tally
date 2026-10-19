SET search_path = tally;

DROP INDEX ix_stock_counts__created_at;
DROP INDEX ix_stock_adjustment_lines__stock_adjustment_id;
DROP INDEX ix_stock_adjustments__posted_at;
DROP INDEX ix_sales_documents__invoice;
DROP INDEX ix_payments__earmarked_invoice;
DROP INDEX ix_export_jobs__requested_by;
