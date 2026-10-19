SET search_path = tally;

ALTER TABLE quotes DROP CONSTRAINT fk_quotes__converted_invoice_id;
DROP TABLE invoice_versions;
DROP TABLE invoice_lines;
DROP TABLE invoices;
DROP TABLE quote_lines;
DROP TABLE quotes;
