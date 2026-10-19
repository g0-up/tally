SET search_path = tally;

-- The admin incident list filtered by one status, or by the unresolved ones, newest first.
CREATE INDEX ix_incidents__status_started ON incidents (status, started_at DESC, id DESC);
CREATE INDEX ix_incidents__open_started ON incidents (started_at DESC, id DESC) WHERE resolved_at IS NULL;
-- The support sessions granted to one admin.
CREATE INDEX ix_support_sessions__admin_expires ON support_sessions (admin_id, expires_at DESC);
-- Pages of the product feedback, of every kind or of one, newest first.
CREATE INDEX ix_product_feedback__created ON product_feedback (created_at DESC, id DESC);
CREATE INDEX ix_product_feedback__kind_created ON product_feedback (kind, created_at DESC, id DESC);
