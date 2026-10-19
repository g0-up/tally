SET search_path = tally;

ALTER TABLE stock_count_lines DROP CONSTRAINT ck_stock_count_lines__new_lot_without_lot_id;
ALTER TABLE stock_count_lines DROP CONSTRAINT ck_stock_count_lines__new_lot_no_not_empty;
ALTER TABLE stock_count_lines DROP COLUMN new_expiry_date, DROP COLUMN new_lot_no;
