-- The service's own read-only role (NFR-SEC-004).
--
-- The role may read the schema "contract" and nothing else: every session is
-- read-only by default, a role-level statement timeout stops a runaway query
-- even if a client-side cancel is lost, and a connection limit protects the
-- shared store. The password is set by the caller (psql variable
-- reader_password), never in this file.

create role api_reader login
    nosuperuser nocreatedb nocreaterole noinherit noreplication
    connection limit 8;

alter role api_reader set default_transaction_read_only = on;
alter role api_reader set statement_timeout = '2s';
alter role api_reader set idle_in_transaction_session_timeout = '5s';

revoke all on schema public from api_reader;
revoke create on schema public from public;
grant usage on schema contract to api_reader;
grant select on all tables in schema contract to api_reader;
