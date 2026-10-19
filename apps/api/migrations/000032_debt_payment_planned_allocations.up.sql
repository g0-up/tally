SET search_path = tally;

-- A payment waiting for verification (an incoming bank transfer) allocates nothing and moves no
-- fund until the owner confirms it; planned_allocations keeps the allocations the cashier chose
-- ([{obligation_id, amount, mode}]) so the confirmation applies them, capped by what is still
-- open then. The plan is cleared when the payment is confirmed or reversed.
ALTER TABLE payments ADD COLUMN planned_allocations jsonb;
ALTER TABLE payments ADD CONSTRAINT ck_payments__planned_allocations_status
  CHECK (planned_allocations IS NULL OR status = 'pending_verification');
