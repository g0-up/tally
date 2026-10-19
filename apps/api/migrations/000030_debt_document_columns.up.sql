SET search_path = tally;

-- An advance refund pays back the unallocated part of one earlier payment. refund_of names that
-- payment: the refund consumes its advance (allocated_amount grows by the refund amount) and
-- blocks its reversal until the refund itself is reversed.
ALTER TABLE payments ADD COLUMN refund_of uuid;
ALTER TABLE payments ADD CONSTRAINT fk_payments__refund_of
  FOREIGN KEY (tenant_id, refund_of) REFERENCES payments (tenant_id, id);
ALTER TABLE payments ADD CONSTRAINT ck_payments__purpose_refund_of
  CHECK ((purpose = 'advance_refund') = (refund_of IS NOT NULL));
CREATE INDEX ix_payments__refund_of ON payments (tenant_id, refund_of) WHERE refund_of IS NOT NULL;

-- statement_versions is append-only, so a version cannot be stamped when it is sent after it was
-- generated. The statement records which version went out and when; that version stays frozen
-- like every other, and a later generation supersedes it.
ALTER TABLE statements ADD COLUMN sent_version int;
ALTER TABLE statements ADD COLUMN sent_at timestamptz;
ALTER TABLE statements ADD CONSTRAINT ck_statements__sent_version
  CHECK ((sent_version IS NULL) = (sent_at IS NULL) AND (sent_version IS NULL OR (sent_version > 0 AND sent_version <= current_version)));

-- A payment is reversed in place (status reversed, mirror settlements and cash entries); the
-- business date of the reversal is what statements and as-of reports replay it on.
ALTER TABLE payments ADD COLUMN reversed_on date;
ALTER TABLE payments ADD CONSTRAINT ck_payments__status_reversed_on
  CHECK ((status = 'reversed') = (reversed_on IS NOT NULL));

-- The number of the document a credit note comes from (TH, XT, DG …), shown on statements and in
-- the party ledger next to the credit it gave.
ALTER TABLE credit_notes ADD COLUMN source_doc_no text NOT NULL;
