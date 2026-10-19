SET search_path = tally;

ALTER TABLE sales_returns DROP CONSTRAINT fk_sales_returns__exchange_id;
DROP TABLE exchanges;
DROP TABLE sales_return_lines;
DROP TABLE sales_returns;
