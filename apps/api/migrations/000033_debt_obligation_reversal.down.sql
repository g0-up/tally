SET search_path = tally;

ALTER TABLE obligation_settlements DROP CONSTRAINT ck_obligation_settlements__settled_by_type_kinds;
ALTER TABLE obligation_settlements ADD CONSTRAINT ck_obligation_settlements__settled_by_type
  CHECK (settled_by_type IN ('payment','credit_note','debt_offset','debt_adjustment'));

ALTER TABLE obligations DROP CONSTRAINT ck_obligations__reversal_fields;
ALTER TABLE obligations DROP COLUMN reversal_doc_no;
ALTER TABLE obligations DROP COLUMN reversal_reason;
ALTER TABLE obligations DROP COLUMN reversed_on;
