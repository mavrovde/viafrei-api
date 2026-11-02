#!/bin/sh
# Development database only: runs once, when the volume is first initialised
# (docker-entrypoint-initdb.d), after the contract stub and the read-only role.
# It sets the role's password from the environment, loads the synthetic
# fixture set and then publishes the contract version, all in one
# transaction: the version row is written last, so a database that carries a
# contract version always carries the whole fixture set with it.
set -eu

: "${API_READER_PASSWORD:?API_READER_PASSWORD must be set (see .env.example)}"

psql -v ON_ERROR_STOP=1 --single-transaction --quiet \
  --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v reader_password="$API_READER_PASSWORD" \
  -v contract_version="$(cat /seed/CONTRACT_VERSION)" \
  -f - <<'SQL'
alter role api_reader password :'reader_password';
\i /seed/seed.sql
insert into contract.contract_info (contract_version, published_at) values (:'contract_version', now());
SQL

echo "fixtures loaded: $(tr -d '\n' < /seed/manifest.json | sed -n 's/.*"checksum": *"\([^"]*\)".*/\1/p')"
