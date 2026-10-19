SET search_path = tally;

-- A receipt is invoiced by one supplier document: matching it to a second one would count its
-- amount twice in the supplier's reconciliation. A receipt the supplier split over two invoices
-- is matched to the first and the difference shows as the variance of each.
CREATE UNIQUE INDEX ux_supplier_document_receipts__receipt_id ON supplier_document_receipts (tenant_id, receipt_id);
