SET search_path = tally;

-- auth_tokens is reached by account as well as by hash: a sandbox reset moves every token of the
-- account in one UPDATE, and moving or removing a users row checks fk_auth_tokens__user_id. Without
-- this index both scan the table of every tenant.
CREATE INDEX ix_auth_tokens__user ON auth_tokens (tenant_id, user_id);
