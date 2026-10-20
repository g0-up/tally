SET search_path = tally;

-- The barcodes of given SKUs: the POS tiles, the product lists and the barcode list of a SKU.
CREATE INDEX ix_sku_barcodes__sku ON sku_barcodes (tenant_id, sku_id);
