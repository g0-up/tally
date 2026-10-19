#!/bin/sh
# Runs once, from /docker-entrypoint-initdb.d, when the postgres data volume is empty.
# Passwords travel as psql variables so roles.sql stays plain SQL without secrets.
# Outside the container (CI) set ROLES_SQL to the repository path of roles.sql and
# PGHOST/PGPORT/PGPASSWORD for the target server.
set -eu

: "${POSTGRES_USER:?POSTGRES_USER is required}"
: "${POSTGRES_DB:?POSTGRES_DB is required}"
: "${TALLY_OWNER_PASSWORD:?TALLY_OWNER_PASSWORD is required}"
: "${TALLY_APP_PASSWORD:?TALLY_APP_PASSWORD is required}"
: "${TALLY_READONLY_PASSWORD:?TALLY_READONLY_PASSWORD is required}"

ROLES_SQL="${ROLES_SQL:-/etc/tally/roles.sql}"

psql --no-psqlrc -v ON_ERROR_STOP=1 \
	--username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
	-v owner_pw="$TALLY_OWNER_PASSWORD" \
	-v app_pw="$TALLY_APP_PASSWORD" \
	-v ro_pw="$TALLY_READONLY_PASSWORD" \
	-f "$ROLES_SQL"
