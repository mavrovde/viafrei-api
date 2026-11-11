# Rate limits, quotas and priority

Every request passes up to four gates, in this order. The rules are in the
specification, [Rate Limiting and Quotas](https://github.com/mavrovde/viafrei-api/wiki/Spec-Rate-Limiting-and-Quotas)
(RATE-001 to RATE-017); every number below is a configuration key with a
default, listed in [configuration](configuration.md).

| Gate | Applies to | Refusal |
|---|---|---|
| 1. Per address | requests that present no key | `429 rate-limited`, `Retry-After` |
| 2. Per key | every keyed request, from the key's plan | `429 rate-limited`, `Retry-After` |
| 3. Quotas | every keyed request, per day and per month | `429 quota-exceeded`, `Retry-After` until the reset |
| 4. Admission | every storage read, by the plan's tier | `429 overloaded` (or `503`, `admission.shed_status`), `Retry-After` |

Each refusal is an RFC 9457 problem; its type names the gate.

## Gate 1: per client address

A token bucket per client address, held in memory only and never stored
(`ratelimit.anon_rps`, `ratelimit.anon_burst`; idle entries expire after
`ratelimit.ip_state_ttl`, at most `ratelimit.ip_track_size` are held; when
the table is full, the least recently seen address goes, at constant cost).
An IPv6 address is counted by its prefix, `ratelimit.ipv6_prefix` (default
64): one subscriber is routinely given a whole /64, so every address inside
it shares one bucket and one table entry.

The gate is decided per operation. It charges every request to a service
endpoint (`/healthz`, `/readyz`, `/v1/meta`), to an unknown path and to a
switched-off operation, whatever headers it carries, and every request to a
keyed operation that presents neither `Authorization` nor `X-API-Key`
(SEC-051). A header sent where no key is checked is checked by nothing, so
it buys no exemption.

RATE-001 reads "every request". A key presented to a keyed operation is not
charged here: it is held by its own key's bucket (gate 2), and a request
whose key fails is held by the failed-authentication limit per address
(SEC-052). Charging keyed requests by address too would let one shared
address (a company network, a carrier NAT) starve every key behind it, and
the key gates already bound them. Without an access store no operation
checks a key, so every request is charged. `ratelimit.anon_rps` of 0
switches the gate off.

The client address is the TCP peer. Only when the peer is inside
`server.trusted_proxies` is `X-Forwarded-For` read: it is walked from the
right, the end each proxy appends to, past every address inside
`server.trusted_proxies`, and the first address outside them is the client.
Entries left of it were written by the client and are ignored; an entry on
the way that is no address leaves the peer as the client. With the default
empty list the header is never read.

## Gates 2 and 3: per key

The plan sets a token bucket (`keys plan -rate -burst`) and, optionally, a
day and a month quota (`-quota-day`, `-quota-month`). The bucket refuses
first and charges no quota unit. A quota unit is a request that passed the
bucket and its key check: a `304`, a scope refusal and a gate-4 refusal each
cost one, a `429` from gates 2 or 3 costs none.

The day quota resets at midnight and the month quota on the first of the
month, both in `quota.reset_tz`. The counts live in each instance's memory
and are seeded, the first time a key is seen in a window, from the recorded
usage, so a restarted instance does not hand a key a fresh quota. Several
instances each hold their own counts between metering flushes; a shared
counter is left for later.

With `ratelimit.headers` on, every keyed answer carries `RateLimit-Policy`
and `RateLimit` in the syntax of draft-ietf-httpapi-ratelimit-headers-09:
one item for the bucket and one for each quota the plan has, for example

```
RateLimit-Policy: "burst";q=20;w=2, "day";q=50000;w=86400
RateLimit: "burst";r=19;t=1, "day";r=49012;t=37200
```

## Gate 4: priority admission

Storage reads take a permit; there are `db.pool_max` of them, so no request
ever waits inside the connection pool (RATE-008). A request waits instead in
the queue of its plan's tier, `critical`, `standard` or `bulk`:

- a free permit goes to the highest tier with a waiter, the oldest waiter
  first; `tiers.*.reserved` permits are kept for a tier and the ones above
  it, and the bulk tier holds at most `tiers.bulk.max_inflight` at once
  (RATE-009);
- a waiter older than `admission.aging_after` counts one tier higher, so no
  tier starves (RATE-010);
- a request is refused at once when the estimated wait, from a moving
  average of storage time (`admission.ewma_alpha`), does not fit its tier's
  `queue_max_wait` or what is left of its deadline; a full queue
  (`tiers.*.queue_len`) refuses the next one (RATE-011);
- no key holds more than `tiers.per_key_inflight` permits (RATE-016).

`Retry-After` on an `overloaded` refusal is the estimated time for the
queues to drain, at least one second.

A read that gets its permit too late to keep `db.statement_timeout_floor`
before its deadline is refused with `503 deadline-exhausted` without
touching storage, and the statement timeout of every read is what is left of
the deadline: no query runs past it, on the server either (NFR-PERF-004).

## Gate 5: the storage budget shared with other readers

The permits of gate 4 count this service's reads only. The store has other
readers, and together they must not run more statements at once than it can
serve (NFR-CAP-002). `db.shared_budget.slots` is that number for all readers
together; `0` (the default) switches the gate off.

The budget is a set of transaction-scoped advisory locks with the two-key
form `(lock_space, n)`, `n` from 1 to `slots`, and every reader that shares
it follows the same protocol:

1. inside its read-only transaction, before its first statement, take the
   first free slot with exactly this statement:
   `select s from generate_series(1, <open>) s where pg_try_advisory_xact_lock(<lock_space>, s) limit 1`.
   That it takes one lock at most rests on PostgreSQL evaluating the WHERE
   filter lazily, row by row, and stopping at LIMIT 1. The PostgreSQL manual
   warns that advisory locks in a query with LIMIT are not guaranteed to
   behave so in general: an `ORDER BY`, a materialised CTE, or any other
   shape whose plan runs the filter on more rows than it returns would take
   several slots and hold them to the end of the transaction. Use no other
   shape. The integration test
   `TestAReadHoldsOneSharedSlotUntilItsTransactionEnds` pins it: a read
   holds exactly one slot while it runs;
2. `<open>` is `slots` for a critical read and `slots - critical_reserved`
   for any other, so the highest `critical_reserved` slots stay free for
   critical reads. A service read with no caller (readiness, which needs no
   key, and background reads) counts as standard here, never critical;
3. no row means every open slot is held: try again after `retry_interval`,
   spread at random over ±50 % so waiting readers do not retry in step,
   for at most `max_wait`, then refuse the request as overloaded with
   `Retry-After: retry_after`;
4. the slot is released when the transaction ends, or with the session if
   the reader dies, so a slot is never lost.

A reader joins the budget by using the same `lock_space` and `slots`; it needs
no table, no grant beyond connecting, and no change to the data contract. A
read that waited for a slot sets its statement timeout again to what is left
of its deadline, and `max_wait` must be less than `deadline.request`, so the
wait for a slot never outlives the request.

## Usage metering

Each keyed request is counted per key, hour (its start, UTC) and operation:
requests, `304`s, each refusal by its reason, and the storage time. The
counts are held in memory and written to the access store in one batch every
`metering.flush_interval` and once more on shutdown; no request writes to the
store. A crash loses at most the counts of one interval. A failed write keeps
its counts for the next one; when more than `metering.max_keys_buffered`
counters are held, the counts of a new one are dropped and the drop is
logged. No client address is recorded.

The maintainers read it with the key tool:

```sh
viafrei-api keys usage -id <keyId> -hours 24   # one key; without -id, every key
viafrei-api keys prune -actor ops              # also removes usage past metering.retention_days
```

The output is tab-separated, one line per key, hour and operation.
