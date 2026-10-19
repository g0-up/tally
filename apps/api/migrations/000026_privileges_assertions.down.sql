SET search_path = tally;

-- Earlier migrations keep their own grants and revokes, so the only state to restore is the
-- default-privilege access to the migration bookkeeping table.
GRANT SELECT, INSERT, UPDATE, DELETE ON schema_migrations TO tally_app;
GRANT SELECT ON schema_migrations TO tally_readonly;
