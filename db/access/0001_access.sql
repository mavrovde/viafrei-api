-- The access store, version 1: the API's own plans, keys, terms acceptances
-- and audit records (Spec: Authentication and Authorisation). It holds no
-- secret: a key is stored as the MAC of its secret under a pepper that lives
-- outside this store (SEC-010).
--
-- Applied by the store's owner, never by the API's own role (0002).

create schema if not exists access;

create table access.schema_version (
    version    integer primary key,
    applied_at timestamptz not null default now()
);

-- Plans are data, changeable without a deployment (SEC-021).
create table access.plan (
    plan_name  text primary key check (plan_name ~ '^[a-z][a-z0-9_-]{0,31}$'),
    use_class  text not null check (use_class in ('consumer', 'non_commercial', 'commercial')),
    tier       text not null check (tier in ('critical', 'standard', 'bulk')),
    scopes     text[] not null,
    sources    text[] not null,
    changed_at timestamptz not null
);

create table access.api_key (
    key_id         text primary key check (key_id ~ '^[0-9A-Za-z]{10}$'),
    environment    text not null check (environment in ('live', 'test')),
    plan_name      text not null references access.plan,
    label          text not null,
    contact        text not null,
    mac            bytea not null check (length(mac) = 32),
    previous_mac   bytea check (length(previous_mac) = 32),
    previous_until timestamptz,
    issued_at      timestamptz not null,
    expires_at     timestamptz not null,
    revoked_at     timestamptz,
    revoke_reason  text,
    changed_at     timestamptz not null,
    check ((previous_mac is null) = (previous_until is null)),
    check ((revoked_at is null) = (revoke_reason is null))
);

create index api_key_changed_at on access.api_key (changed_at);
create index plan_changed_at on access.plan (changed_at);

-- One row per accepted terms version of one source family (SEC-034).
create table access.terms_acceptance (
    key_id        text not null references access.api_key,
    source_family text not null,
    terms_version text not null,
    accepted_at   timestamptz not null,
    primary key (key_id, source_family, terms_version)
);

-- Every lifecycle action, written in the transaction of the change
-- (SEC-044). No secret, no MAC, no client address.
create table access.audit (
    audit_id    bigint generated always as identity primary key,
    at          timestamptz not null,
    action      text not null check (action in ('plan_put', 'plan_move', 'issue', 'rotate', 'revoke', 'accept_terms', 'audit_pruned')),
    key_id      text,
    plan_before text,
    plan_after  text,
    actor       text not null,
    reason      text
);

create index audit_at on access.audit (at);

-- The one way to remove audit records: older than the retention, and never
-- younger than 30 days whatever the caller asks (SEC-045). It runs with the
-- owner's rights, so the API's own role needs no delete right on the table.
create function access.prune_audit(before timestamptz) returns bigint
    language plpgsql security definer set search_path = pg_catalog, access as $$
declare
    removed bigint;
begin
    if before > now() - interval '30 days' then
        raise exception 'audit records younger than 30 days are kept';
    end if;
    delete from access.audit where at < before;
    get diagnostics removed = row_count;
    return removed;
end;
$$;

revoke all on function access.prune_audit(timestamptz) from public;

insert into access.schema_version (version) values (1);
