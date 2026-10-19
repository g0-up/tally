SET search_path = tally;

ALTER TABLE credit_notes DROP COLUMN source_doc_no;

ALTER TABLE payments DROP CONSTRAINT ck_payments__status_reversed_on;
ALTER TABLE payments DROP COLUMN reversed_on;

ALTER TABLE statements DROP CONSTRAINT ck_statements__sent_version;
ALTER TABLE statements DROP COLUMN sent_at;
ALTER TABLE statements DROP COLUMN sent_version;

DROP INDEX ix_payments__refund_of;
ALTER TABLE payments DROP CONSTRAINT ck_payments__purpose_refund_of;
ALTER TABLE payments DROP CONSTRAINT fk_payments__refund_of;
ALTER TABLE payments DROP COLUMN refund_of;
