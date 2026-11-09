-- The access store, version 2: each plan's rate, burst and quotas, and the
-- usage records of the metering (Spec: Rate Limiting, Quotas and Priority,
-- RATE-002 to RATE-004, RATE-014).
--
-- Applied by the store's owner after 0001 and 0002. A plan that exists
-- before it starts at the most cautious limit, one request a second with no
-- burst and no quota, until `keys plan` sets its real values.

alter table access.plan
    add column rate_per_second integer not null default 1 check (rate_per_second between 1 and 100000),
    add column burst           integer not null default 1 check (burst between 1 and 100000),
    add column quota_day       bigint  not null default 0 check (quota_day >= 0),
    add column quota_month     bigint  not null default 0 check (quota_month >= 0);

alter table access.plan
    alter column rate_per_second drop default,
    alter column burst drop default,
    alter column quota_day drop default,
    alter column quota_month drop default;

-- One row per key, hour (its start, UTC) and operation. The metering adds to
-- a row in one batched write per interval; no request writes here. It
-- holds no client address.
create table access.usage (
    key_id             text        not null references access.api_key,
    hour               timestamptz not null,
    operation          text        not null check (operation ~ '^[A-Za-z][A-Za-z0-9]{0,63}$'),
    requests           bigint      not null check (requests >= 0),
    not_modified       bigint      not null check (not_modified >= 0),
    rate_limited       bigint      not null check (rate_limited >= 0),
    quota_exceeded     bigint      not null check (quota_exceeded >= 0),
    overloaded         bigint      not null check (overloaded >= 0),
    deadline_exhausted bigint      not null check (deadline_exhausted >= 0),
    storage_ms         bigint      not null check (storage_ms >= 0),
    primary key (key_id, hour, operation)
);

create index usage_hour on access.usage (hour);

-- Removing old usage is audited like removing old audit records.
alter table access.audit drop constraint audit_action_check;
alter table access.audit add constraint audit_action_check
    check (action in ('plan_put', 'plan_move', 'issue', 'rotate', 'revoke', 'accept_terms', 'audit_pruned', 'usage_pruned'));

-- The one way to remove usage records: older than the retention, and never
-- younger than 30 days whatever the caller asks. It runs with the owner's
-- rights, so the API's own role needs no delete right on the table.
create function access.prune_usage(keep_days integer) returns bigint
    language plpgsql security definer set search_path = pg_catalog, access as $$
declare
    removed bigint;
begin
    if keep_days < 30 then
        raise exception 'usage records younger than 30 days are kept';
    end if;
    delete from access.usage where hour < now() - make_interval(days => keep_days);
    get diagnostics removed = row_count;
    return removed;
end;
$$;

revoke all on function access.prune_usage(integer) from public;
grant select, insert, update on access.usage to api_access;
grant execute on function access.prune_usage(integer) to api_access;

insert into access.schema_version (version) values (2);
