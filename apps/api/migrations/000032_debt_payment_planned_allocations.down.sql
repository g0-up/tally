SET search_path = tally;

ALTER TABLE payments DROP CONSTRAINT ck_payments__planned_allocations_status;
ALTER TABLE payments DROP COLUMN planned_allocations;
