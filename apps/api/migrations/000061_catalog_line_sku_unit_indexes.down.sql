SET search_path = tally;

DROP INDEX ix_invoice_lines__sku_unit;
DROP INDEX ix_kit_recipe_items__sku_unit;
DROP INDEX ix_pos_cart_lines__sku_unit;
DROP INDEX ix_purchase_order_lines__sku_unit;
DROP INDEX ix_purchase_return_lines__sku_unit;
DROP INDEX ix_quote_lines__sku_unit;
DROP INDEX ix_receipt_lines__sku_unit;
DROP INDEX ix_sales_document_lines__sku_unit;
DROP INDEX ix_sales_return_lines__sku_unit;
DROP INDEX ix_stock_adjustment_lines__sku_unit;
