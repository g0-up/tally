SET search_path = tally;

-- Reopening may unlock every period: such a reopen event carries no lock date (NULL), which
-- means nothing is locked after it. A lock event always carries its date.

ALTER TABLE period_locks ALTER COLUMN locked_through DROP NOT NULL;
ALTER TABLE period_locks ADD CONSTRAINT ck_period_locks__lock_date CHECK (action = 'reopen' OR locked_through IS NOT NULL);
