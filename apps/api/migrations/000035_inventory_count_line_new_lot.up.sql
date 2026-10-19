SET search_path = tally;

-- A count gain on a SKU that tracks lots or expiry and never held a lot of the counted color opens
-- a new lot: the line records the lot number and expiry the counter read on the shelf. A line on a
-- counted lot carries neither.
ALTER TABLE stock_count_lines ADD COLUMN new_lot_no text, ADD COLUMN new_expiry_date date;
ALTER TABLE stock_count_lines ADD CONSTRAINT ck_stock_count_lines__new_lot_no_not_empty
  CHECK (new_lot_no IS NULL OR new_lot_no <> '');
ALTER TABLE stock_count_lines ADD CONSTRAINT ck_stock_count_lines__new_lot_without_lot_id
  CHECK (lot_id IS NULL OR (new_lot_no IS NULL AND new_expiry_date IS NULL));
