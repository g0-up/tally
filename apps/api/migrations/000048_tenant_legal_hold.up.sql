SET search_path = tally;

-- A store under legal hold is never closed or purged, whatever its closure request or
-- subscription says; an operator sets and lifts it.
ALTER TABLE tenants ADD COLUMN legal_hold bool NOT NULL DEFAULT false;
