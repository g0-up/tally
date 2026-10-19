SET search_path = tally;

ALTER TABLE sales_documents
  DROP COLUMN change_amount,
  DROP COLUMN cash_tendered_amount;
