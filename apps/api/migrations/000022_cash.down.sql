SET search_path = tally;

DROP TABLE cash_entries;
ALTER TABLE payments DROP CONSTRAINT fk_payments__payment_method_id;
ALTER TABLE payments DROP CONSTRAINT fk_payments__fund_account_id;
DROP TABLE payment_methods;
DROP TABLE fund_accounts;
