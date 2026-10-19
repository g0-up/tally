SET search_path = tally;

-- Reversing the source of an obligation (a receipt, a stock-out) keeps its history: the
-- obligation is closed by one settlement of type obligation_reversal for what was still open,
-- after the payments that settled it were reversed off it (their money becomes the party's
-- advance again). Nothing is deleted or rewritten, so statements of earlier periods still show
-- the document. reversed_on, reversal_reason and the number of the reversing document record
-- why it was closed; a reversed obligation is always settled.
ALTER TABLE obligations ADD COLUMN reversed_on date;
ALTER TABLE obligations ADD COLUMN reversal_reason text;
ALTER TABLE obligations ADD COLUMN reversal_doc_no text;
ALTER TABLE obligations ADD CONSTRAINT ck_obligations__reversal_fields
  CHECK ((reversed_on IS NULL) = (reversal_reason IS NULL)
         AND (reversal_doc_no IS NULL OR reversed_on IS NOT NULL)
         AND (reversed_on IS NULL OR status = 'settled'));

ALTER TABLE obligation_settlements DROP CONSTRAINT ck_obligation_settlements__settled_by_type;
ALTER TABLE obligation_settlements ADD CONSTRAINT ck_obligation_settlements__settled_by_type_kinds
  CHECK (settled_by_type IN ('payment','credit_note','debt_offset','debt_adjustment','obligation_reversal'));
