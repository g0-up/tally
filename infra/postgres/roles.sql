-- Tally database roles and schema. Run once on an empty cluster by postgres/init/01-roles.sh,
-- which passes the passwords as psql variables (owner_pw, app_pw, ro_pw).
-- Plain DDL/DCL only: no functions, procedures, DO blocks or extensions.
-- Append-only revokes are applied per table by the migrations.

\set ON_ERROR_STOP on

CREATE ROLE tally_owner LOGIN PASSWORD :'owner_pw';
CREATE ROLE tally_app LOGIN PASSWORD :'app_pw' NOSUPERUSER NOBYPASSRLS;
CREATE ROLE tally_readonly LOGIN PASSWORD :'ro_pw' NOSUPERUSER NOBYPASSRLS;

-- Lets the API SET LOCAL ROLE tally_readonly for consented support sessions.
GRANT tally_readonly TO tally_app;

CREATE SCHEMA tally AUTHORIZATION tally_owner;

ALTER ROLE tally_owner SET search_path = tally;
ALTER ROLE tally_app SET search_path = tally;
ALTER ROLE tally_readonly SET search_path = tally;

-- Tables created later by tally_owner (migrations) are readable/writable by the app and readable by support.
ALTER DEFAULT PRIVILEGES FOR ROLE tally_owner IN SCHEMA tally GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO tally_app;
ALTER DEFAULT PRIVILEGES FOR ROLE tally_owner IN SCHEMA tally GRANT SELECT ON TABLES TO tally_readonly;

-- USAGE only: neither tally_app nor tally_readonly may create objects in the schema.
GRANT USAGE ON SCHEMA tally TO tally_app, tally_readonly;
