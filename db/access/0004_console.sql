-- The access store, version 3: the owner console's passkeys and enrolment
-- codes (Spec: Threat Model, THR-018).
--
-- Applied by the store's owner after 0003. A passkey is stored as its public
-- key only; an enrolment code as its MAC under the pepper, like a key secret
-- (SEC-010). Console sessions and challenges are held in memory by the
-- service and never stored.

create table access.console_passkey (
    credential_id bytea primary key check (length(credential_id) between 16 and 1023),
    owner_name    text not null check (owner_name ~ '^[a-z][a-z0-9._-]{0,31}$'),
    user_handle   bytea not null check (length(user_handle) = 32),
    public_key    bytea not null check (length(public_key) between 1 and 2048),
    sign_count    bigint not null check (sign_count between 0 and 4294967295),
    flags         smallint not null check (flags between 0 and 255),
    created_at    timestamptz not null,
    last_used_at  timestamptz
);

create index console_passkey_owner on access.console_passkey (owner_name);

create table access.console_enrolment (
    code_mac   bytea primary key check (length(code_mac) = 32),
    owner_name text not null check (owner_name ~ '^[a-z][a-z0-9._-]{0,31}$'),
    expires_at timestamptz not null,
    used_at    timestamptz
);

-- Console actions are audited like every lifecycle action; the actor of a
-- console change is the signed-in owner.
alter table access.audit drop constraint audit_action_check;
alter table access.audit add constraint audit_action_check
    check (action in ('plan_put', 'plan_move', 'issue', 'rotate', 'revoke', 'accept_terms', 'audit_pruned', 'usage_pruned',
                      'console_enrolment', 'console_passkey_added', 'console_passkey_removed', 'console_sign_in'));

grant select, insert, update, delete on access.console_passkey to api_access;
grant select, insert, update on access.console_enrolment to api_access;

insert into access.schema_version (version) values (3);
