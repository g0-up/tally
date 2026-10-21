SET search_path = tally;

-- Whether a unit of a SKU is on a document line: the unit locks of the SKU lists and the unit
-- table editor, and deleting a unit.
CREATE INDEX ix_invoice_lines__sku_unit ON invoice_lines (tenant_id, sku_id, unit_id);
CREATE INDEX ix_kit_recipe_items__sku_unit ON kit_recipe_items (tenant_id, sku_id, unit_id);
CREATE INDEX ix_pos_cart_lines__sku_unit ON pos_cart_lines (tenant_id, sku_id, unit_id);
CREATE INDEX ix_purchase_order_lines__sku_unit ON purchase_order_lines (tenant_id, sku_id, unit_id);
CREATE INDEX ix_purchase_return_lines__sku_unit ON purchase_return_lines (tenant_id, sku_id, unit_id);
CREATE INDEX ix_quote_lines__sku_unit ON quote_lines (tenant_id, sku_id, unit_id);
CREATE INDEX ix_receipt_lines__sku_unit ON receipt_lines (tenant_id, sku_id, unit_id);
CREATE INDEX ix_sales_document_lines__sku_unit ON sales_document_lines (tenant_id, sku_id, unit_id);
CREATE INDEX ix_sales_return_lines__sku_unit ON sales_return_lines (tenant_id, sku_id, unit_id);
CREATE INDEX ix_stock_adjustment_lines__sku_unit ON stock_adjustment_lines (tenant_id, sku_id, unit_id);
