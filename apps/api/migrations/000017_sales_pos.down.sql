SET search_path = tally;

ALTER TABLE sales_documents DROP CONSTRAINT fk_sales_documents__pos_cart_id;
ALTER TABLE sales_documents DROP CONSTRAINT fk_sales_documents__shift_id;
DROP TABLE pos_sync_exceptions;
DROP TABLE pos_cart_lines;
DROP TABLE pos_carts;
DROP TABLE shifts;
