SET search_path = tally;

-- A wrong_item receipt line records goods the supplier sent by mistake for the claim; they never
-- enter stock, so the line has no lot. Every other line is received into a lot (damaged lines
-- into the quarantine twin), so it always names one.
ALTER TABLE receipt_lines ALTER COLUMN lot_id DROP NOT NULL;
ALTER TABLE receipt_lines ADD CONSTRAINT ck_receipt_lines__lot_id_condition
  CHECK ((lot_id IS NULL) = (condition = 'wrong_item'));
