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
`ratelimit.ip_state_ttl`, at most `ratelimit.ip_track_size` are held). It
charges every request that carries neither `Authorization` nor
`X-API-Key`, service endpoints included (SEC-051).

RATE-001 reads "every request". A keyed request is not charged here: it is
held by its own key's bucket (gate 2), and a request whose key fails is held
by the failed-authentication limit per address (SEC-052). Charging keyed
requests by address too would let one shared address (a company network, a
carrier NAT) starve every key behind it, and the key gates already bound
them. `ratelimit.anon_rps` of 0 switches the gate off.

The client address is the TCP peer. Only when the peer is inside
`server.trusted_proxies` is it the last entry of `X-Forwarded-For` instead:
the one the proxy itself appended. Earlier entries were written by the
client and are ignored. With the default empty list the header is never
read.

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
