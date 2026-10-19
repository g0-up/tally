SET search_path = tally;

-- A POS sale keeps the cash the customer handed over and the change given back, as printed on
-- the receipt. Both stay 0 on documents settled without cash.
ALTER TABLE sales_documents
  ADD COLUMN cash_tendered_amount numeric(19,4) NOT NULL DEFAULT 0
    CONSTRAINT ck_sales_documents__cash_tendered_amount CHECK (cash_tendered_amount >= 0),
  ADD COLUMN change_amount numeric(19,4) NOT NULL DEFAULT 0
    CONSTRAINT ck_sales_documents__change_amount CHECK (change_amount >= 0);
