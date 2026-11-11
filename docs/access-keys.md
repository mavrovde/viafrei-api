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

The owner of the store applies the files once, in order, then sets the
role's password:

```sh
psql "$OWNER_URL" -v ON_ERROR_STOP=1 -f db/access/0001_access.sql -f db/access/0002_access_role.sql -f db/access/0003_limits.sql \
                                     -f db/access/0004_console.sql
psql "$OWNER_URL" -c "alter role api_access password '…'"
```

The service then needs `VFAPI_ACCESS_URL` (the `api_access` role) and
`VFAPI_AUTH_PEPPER` (at least 32 bytes), each from the environment or a
`_FILE`. The role may read and change plans and keys and append audit
records; it can neither change nor delete an audit record. Old records leave
only through `keys prune`, never younger than 30 days.

`0003_limits.sql` (schema version 2) adds each plan's rate, burst and
quotas and the usage table, one row per key, hour and operation. A store
upgraded from version 1 gives every existing plan a rate and burst of 1
and no quota until `keys plan` sets the real values. The role may add to
usage rows but not delete them; `keys prune` removes those older than
`metering.retention_days`, never younger than 30 days.

`0004_console.sql` (schema version 3) adds the owner console's passkeys
(public keys and counters only) and its enrolment codes (stored as their MAC
under `auth.pepper`), and lets the audit record the console's actions.

Pepper rotation: set the new value as `auth.pepper` and the old one as
`auth.pepper_previous`. A key that still matches under the old pepper is
re-MACed under the new one on its first use. An open console enrolment code
does not survive a pepper rotation; print a new one.

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
viafrei-api keys plan   -name standard -use-class commercial -tier standard -scopes roads,energy -sources src-a,src-b \
                        -rate 10 -burst 20 -quota-day 50000 -quota-month 1000000 -actor ops
viafrei-api keys issue  -plan standard -label "Example GmbH" -contact ops@example.test -actor ops
viafrei-api keys rotate -id <keyId> -actor ops          # the old secret stays valid for auth.rotation_overlap
viafrei-api keys revoke -id <keyId> -reason "leaked" -actor ops
viafrei-api keys move   -id <keyId> -plan pro -actor ops
viafrei-api keys accept -id <keyId> -source src-b -version 2026-03-01 -actor ops
viafrei-api keys prune  -actor ops                      # audit records past audit.retention_days, usage past metering.retention_days
viafrei-api keys usage  -id <keyId> -hours 24           # usage per hour and operation; without -id, every key
```

A key expires after `auth.default_expiry` unless `-expires` is given (at
most 1 095 days). A revocation, a plan move, a plan change or an acceptance
reaches every running instance within `auth.revocation_propagation`, and at
the latest after `auth.key_cache_ttl`.

A plan's `-rate` (requests per second) and `-burst` are its token bucket,
each 1 to 100 000; `-quota-day` and `-quota-month` are quota units, 0 for
none. `-tier` is `critical`, `standard` or `bulk`, the plan's priority in
front of storage. See [rate limits](rate-limits.md).

Exit status: 0 done, 1 refused by the store, 2 a usage or input error, 78 a
configuration error.

## The owner console

The same key lifecycle runs in a browser: the owner console, a small
server-rendered site on a second listener. It is off unless `admin.enabled`
is set, needs `access.url`, and listens on `admin.listen_addr`, which must
be a loopback address (`127.0.0.0/8` or `::1`); any other address is
refused at boot, and the listener checks again when it opens. It is never
on the public listener. Every `admin.*` key is in the
[configuration reference](configuration.md).

How to reach it depends on where the service runs. In each case you open
`admin.origin` (`http://localhost:8081` by default) in the browser.

- **The binary on a host:** forward a local port to the host's loopback
  over SSH (`ssh -L 8081:127.0.0.1:8081 <host>`).
- **A container with host networking** (`--network host`, Linux): the
  container's loopback is the host's, so the same SSH forward works.
- **A container on a bridge network:** run a forwarder inside the
  container's own network namespace (for example a sidecar that shares it,
  `--network container:<name>`). It accepts the SSH-forwarded connection
  and connects to the console's loopback address there.

Publishing the console's port (`-p`, or `ports:` in Compose) does not work,
and this is by design. A published port needs the listener on `0.0.0.0`,
which is refused at boot. The peer the console would see is the bridge
gateway, which is not a loopback address, so every request would be refused
too.

The owner signs in with a passkey (WebAuthn, user verification required,
discoverable credentials). A passkey is enrolled with a one-time code:

```sh
viafrei-api console enrol    -owner alice -actor ops   # prints the code once; valid once, for admin.enrolment_ttl
viafrei-api console passkeys                           # owner, id, fingerprint, enrolled, last used
viafrei-api console remove   -id <id> -actor ops       # removes one passkey
```

Open `/enrol` on the console, enter the code and create the passkey; then
sign in at `/signin`. A second passkey for the same owner is enrolled the
same way, under the owner's existing user handle.

The console shows the keys, plans and passkeys and the newest
`admin.audit_page` audit records, and offers every change of the key tool:
create or change a plan, issue (the key is shown once, on the page that
answers the issue), rotate (likewise), revoke with a reason, move, record a
terms acceptance, prune. Each change is asked for, shown on a confirmation
page, and made only when confirmed. It runs the same use cases as
`viafrei-api keys`, audited in the same transaction, with `console:<owner>`
as the actor. Enrolments, added and removed passkeys and sign-ins are
audited too.

What the console holds every request to:

- **The loopback and its own host.** A request from any other address is
  refused, and so is a request whose `Host` is not the host of
  `admin.origin`, which keeps a DNS-rebinding page out.
- **Its own origin for every change.** A `POST` without an `Origin` header
  equal to `admin.origin`, or with `Sec-Fetch-Site` other than
  `same-origin`, is refused.
- **A session and its CSRF token.** The session is a `__Host-` cookie
  (`Secure`, `HttpOnly`, `SameSite=Strict`) that lasts `admin.session_ttl`
  from sign-in and is held in memory only, so a restart signs everyone
  out; at most `admin.max_sessions` exist. Every change form carries the
  session's CSRF token, compared in constant time.
- **Single-use challenges and confirmations.** A sign-in or enrolment
  challenge lives `admin.challenge_ttl` and is gone after its first answer,
  right or wrong; a confirmation lives `admin.confirm_ttl`, is bound to
  the session that asked and is used once. At most `admin.max_pending` are
  held.
- **A sign-in limit.** At most `admin.signin_limit` sign-in and enrolment
  attempts (begun or failed) per `admin.signin_window`, from all browsers
  together; one more is answered `429` with `Retry-After`. The limit is
  global, not per client, because every client is on the loopback. Any
  local process that can reach the console can use up the limit and lock
  the owner out until the window ends, which is a local denial of service.
  It cannot sign in. If that happens, find the process on the host.
- **A passkey that verifies.** The client data must name `admin.origin`,
  the authenticator data `admin.rp_id`, the user must be verified, the
  user handle must be the passkey owner's, and a signature counter that
  does not move forward (a cloned authenticator, a replay, a concurrent
  sign-in) is refused. The store applies the same rule when it records the
  sign-in. An authenticator that keeps no counter answers 0 every time and
  is accepted.
- **A session no longer than its passkey.** A session is ended as soon as
  the passkey it was signed in with is removed; its next request is
  answered as signed out.
- **One passkey per authenticator.** Enrolling a further passkey for an
  owner excludes the passkeys the owner already holds, so an authenticator
  that has one is not enrolled twice.
- **A strict page.** `Content-Security-Policy` allows only the console's
  own script and style and no framing; every answer is `no-store` and
  sends no referrer.

No page, log line or audit record of the console carries the pepper, a
MAC, an enrolment code, a session or CSRF token, or a client address; the
full key appears once, on the page that answers its issue or rotation.
net/http's own error lines (a recovered panic, a TLS handshake error) name
the peer, so the console's server writes them with every network address
removed.

An enrolment code is used once. When a new code is offered, used and
expired codes are deleted.

Browsers treat `http://localhost` as a secure context, so the default
origin works through a tunnel. Older Safari/WebKit releases do not: they
refuse to store a `Secure` cookie, and so the `__Host-` session cookie,
from `http://localhost`. There the sign-in succeeds but the next page asks
you to sign in again. Use a current browser, or serve the console over
TLS as described next.

For another name, serve the console behind TLS on the loopback, set
`admin.origin` to its `https://` origin and `admin.rp_id` to that host or a
parent domain. A passkey works only under the relying-party id it was
enrolled with. The TLS terminator in front of the console must itself
accept loopback clients only. The console sees the terminator as its peer,
so a terminator that listens on another address extends the console to
whoever can reach that address, and the loopback guarantee no longer
holds.
