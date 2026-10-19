SET search_path = tally;

-- The document number a notification is about, shown in the notification centre next to its
-- title: the earliest open document of a debt notice, the lot number of an expiry notice.
ALTER TABLE notifications ADD COLUMN doc_no text;
