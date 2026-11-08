# API keys and the licence gate

How a request is authenticated, what a key may read, and how the operator
manages keys. The normative rules are on the wiki page
[Spec: Authentication and Authorisation](https://github.com/mavrovde/viafrei-api/wiki/Spec-Authentication-and-Authorisation);
every setting named here is listed with its default and bounds in
[configuration.md](configuration.md).

## Presenting a key

```http
GET /v1/key
Authorization: Bearer vf_live_<keyId>_<secret><checksum>
```

`X-API-Key: <key>` is accepted too. A key in the query string is refused
with `400 key-in-query` before anything else runs, and is never checked:
treat such a key as exposed and rotate it.

| Answer | When |
|---|---|
| `401 missing-key` | no key; carries `WWW-Authenticate: Bearer realm="viafrei-api"` |
| `401 invalid-key` | malformed, wrong checksum, unknown id, wrong secret, or a key of the other environment (`auth.environment`); one answer for all of them |
| `401 key-expired`, `401 key-revoked` | the key is past its expiry, or revoked |
| `403 scope-denied` | the operation's scope is not in the key's plan |
| `429 rate-limited` | more than `auth.failure_limit` failed checks from one address in `auth.failure_window`; `Retry-After` says when to try again |
| `404 feature-disabled` | the instance runs without an access store (`access.url` unset): every keyed operation is off |

`GET /v1/key` returns the key's id, plan, expiry and the source families the
licence gate lets it receive, with their attribution. During the last
`auth.expiry_warning_days` before expiry, `meta.warnings` carries
`key_expiring`.

## Storage

Keys, plans, terms acceptances and audit records live in the API's own
access store, schema `access` ([`db/access`](../db/access)). A key is stored
as HMAC-SHA-256 of its secret under `auth.pepper`, which is held outside the
store. The full key is printed once, at issue or rotation, and is not
recoverable afterwards by anyone.

The owner of the store applies the two files once, then sets the role's
password:

```sh
psql "$OWNER_URL" -v ON_ERROR_STOP=1 -f db/access/0001_access.sql -f db/access/0002_access_role.sql
psql "$OWNER_URL" -c "alter role api_access password '…'"
```

The service then needs `VFAPI_ACCESS_URL` (the `api_access` role) and
`VFAPI_AUTH_PEPPER` (at least 32 bytes), each from the environment or a
`_FILE`. The role may read and change plans and keys and append audit
records; it can neither change nor delete an audit record. Old records leave
only through `keys prune`, never younger than 30 days.

Pepper rotation: set the new value as `auth.pepper` and the old one as
`auth.pepper_previous`. A key that still matches under the old pepper is
re-MACed under the new one on its first use.

## The licence gate

A source family is served to a key only when all of these hold; anything
else is denied:

1. the family is in the key's plan;
2. the family's licence review is signed off;
3. the plan's use class (`consumer`, `non_commercial`, `commercial`) is one
   the family's terms permit;
4. when the terms require acceptance, the key has accepted the current terms
   version (the family's register version);
5. the family is not in `sources.disabled`.

The allowed set is passed into the storage read, so a denied family is never
read. Every returned item is checked against the set again; a violation
aborts the answer with `500 internal` and is logged as an error.

## The operator's key tool

Run on the host, with `VFAPI_ACCESS_URL` and `VFAPI_AUTH_PEPPER` set. Every
action and its audit record (action, key id, plan before and after, actor,
reason, instant; never the secret, the MAC or an address) are written in one
transaction.

```sh
viafrei-api keys plan   -name standard -use-class commercial -tier standard -scopes roads,energy -sources src-a,src-b -actor ops
viafrei-api keys issue  -plan standard -label "Example GmbH" -contact ops@example.test -actor ops
viafrei-api keys rotate -id <keyId> -actor ops          # the old secret stays valid for auth.rotation_overlap
viafrei-api keys revoke -id <keyId> -reason "leaked" -actor ops
viafrei-api keys move   -id <keyId> -plan pro -actor ops
viafrei-api keys accept -id <keyId> -source src-b -version 2026-03-01 -actor ops
viafrei-api keys prune  -actor ops                      # audit records past audit.retention_days
```

A key expires after `auth.default_expiry` unless `-expires` is given (at
most 1 095 days). A revocation, a plan move, a plan change or an acceptance
reaches every running instance within `auth.revocation_propagation`, and at
the latest after `auth.key_cache_ttl`.

Exit status: 0 done, 1 refused by the store, 2 a usage or input error, 78 a
configuration error.
