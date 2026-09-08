#!/bin/sh
# Only called on an empty development PostgreSQL volume. Never print credentials.
set -eu
PROXYLOOM_DB_RUNTIME_PASSWORD="$(cat /run/secrets/db_runtime_password)"
PROXYLOOM_DB_MIGRATION_PASSWORD="$(cat /run/secrets/db_migration_password)"
: "${PROXYLOOM_DB_RUNTIME_PASSWORD:?runtime password is empty}"
: "${PROXYLOOM_DB_MIGRATION_PASSWORD:?migration password is empty}"
export PROXYLOOM_DB_RUNTIME_PASSWORD PROXYLOOM_DB_MIGRATION_PASSWORD
psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --no-psqlrc --quiet --set ON_ERROR_STOP=1 <<'SQL'
SET log_statement = 'none';
SET log_min_error_statement = 'panic';
\getenv runtime_password PROXYLOOM_DB_RUNTIME_PASSWORD
\getenv migration_password PROXYLOOM_DB_MIGRATION_PASSWORD
REVOKE ALL ON DATABASE proxyloom FROM PUBLIC;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
CREATE ROLE proxyloom LOGIN PASSWORD :'runtime_password';
CREATE ROLE proxyloom_migrator LOGIN PASSWORD :'migration_password';
GRANT CONNECT ON DATABASE proxyloom TO proxyloom, proxyloom_migrator;
GRANT USAGE ON SCHEMA public TO proxyloom;
GRANT USAGE, CREATE ON SCHEMA public TO proxyloom_migrator;
ALTER DEFAULT PRIVILEGES FOR ROLE proxyloom_migrator IN SCHEMA public GRANT SELECT ON TABLES TO proxyloom;
SQL
unset PROXYLOOM_DB_RUNTIME_PASSWORD PROXYLOOM_DB_MIGRATION_PASSWORD
