SET search_path = tally;

ALTER TABLE receipt_lines DROP CONSTRAINT ck_receipt_lines__lot_id_condition;
ALTER TABLE receipt_lines ALTER COLUMN lot_id SET NOT NULL;
