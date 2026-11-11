#!/bin/sh
# Test stack of the load and fault scenarios only (test/load/compose.yaml):
# runs once, when the throwaway database is first initialised, after the
# contract stub and the fixtures. It applies the access-store schema in the
# same database and sets the access role's secret from the environment.
set -eu

: "${API_ACCESS_PASSWORD:?API_ACCESS_PASSWORD must be set (scripts/load-test.sh does)}"

psql -v ON_ERROR_STOP=1 --single-transaction --quiet \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -f - <<'SQL'
\getenv access_secret API_ACCESS_PASSWORD
\i /access/0001_access.sql
\i /access/0002_access_role.sql
\i /access/0003_limits.sql
select format('alter role api_access password %L', :'access_secret') \gexec
SQL

echo "access store ready"
