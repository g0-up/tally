SET search_path = tally;

DROP TABLE exception_confirmation_uses;
ALTER TABLE exception_confirmations DROP COLUMN note;
ALTER TABLE exception_confirmations DROP COLUMN payload_hash;
