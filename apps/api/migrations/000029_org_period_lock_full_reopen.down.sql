SET search_path = tally;

-- Fails while a full reopen event exists: that history cannot be expressed without NULL.
ALTER TABLE period_locks DROP CONSTRAINT ck_period_locks__lock_date;
ALTER TABLE period_locks ALTER COLUMN locked_through SET NOT NULL;
