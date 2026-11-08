-- The API's own access-store role. It may read and change plans and keys and
-- record acceptances, but audit records are append-only for it: it may insert
-- and read them, never change or delete them; old ones leave only through
-- access.prune_audit (SEC-045). It sees nothing outside the schema "access".
-- The password is set by the caller (psql variable access_password), never
-- in this file.

create role api_access login
    nosuperuser nocreatedb nocreaterole noinherit noreplication
    connection limit 8;

alter role api_access set statement_timeout = '2s';
alter role api_access set idle_in_transaction_session_timeout = '5s';

revoke all on schema public from api_access;
grant usage on schema access to api_access;
grant select on access.schema_version to api_access;
grant select, insert, update on access.plan, access.api_key to api_access;
grant select, insert on access.terms_acceptance, access.audit to api_access;
grant execute on function access.prune_audit(timestamptz) to api_access;
