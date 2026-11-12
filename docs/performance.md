# Performance and caching

This page describes how the service keeps its answers fast and cheap to
repeat, and how that is tested. Every figure on it is a design target or an
assumption of the benchmark fixtures. None is a measurement of a deployed
service.

## Latency budgets (NFR-PERF-003)

Every operation belongs to an endpoint class. The class sets its p50, p95 and
p99 budget:

| Class | p50 | p95 | p99 |
|---|---|---|---|
| conditional (a 304) | 5 ms | 10 ms | 30 ms |
| lookup | 40 ms | 120 ms | 300 ms |
| near | 60 ms | 200 ms | 500 ms |
| along (a motorway's conditions) | 120 ms | 400 ms | 900 ms |
| composed | 300 ms | 800 ms | 1500 ms |

NFR-PERF-003 also sets budgets for two classes no operation has yet:
transit (80, 250, 600 ms) and history (150, 400, 1000 ms). Each is added to
the class file with its first operation. The road reads brought along in:
a motorway's conditions are along, the conditions near a position near, and
one road situation a lookup.

The budgets come from two files:

- `test/perf/classes.tsv` holds the class table.
- `test/perf/operations.tsv` gives every operation its class.

`internal/perf` reads both files and refuses three kinds of class:

- one out of order (p50 ≤ p95 ≤ p99 is required);
- one that reaches the configured request deadline (`deadline.request`, at most 2 s);
- one that no operation uses (the conditional class is every operation's 304).

`TestEveryOperationHasABudget` fails on any of these:

- an operation without a class;
- a class row for an operation that does not exist;
- a catalogue where half or fewer of the operations have a p95 within 200 ms.

**Adding an operation:**

1. Add a row to `operations.tsv`.
2. Add a request for the operation to `targetsOf` in `internal/perf/perf_integration_test.go`. The benchmark fails on an operation it cannot ask.

## The benchmark leg

The `Benchmark leg` job of CI runs `internal/perf` against PostGIS with the
CI fixture profile and `-perf.assert`.

**How it measures:**

- Each request goes through the real handler, store and middleware.
- The HTTP result cache is off, so every request reads storage.
- There is a warm-up first, then the timed requests. Four requests run at a time.
- The leg reports p50 and asserts p95 and p99 for each operation. For the
  conditional class it asserts the 304 of a data operation.
- The integration job runs the same tests with the race detector as a smoke
  test. It reports the latencies but does not assert them.

**What the leg does not measure:**

- the network or TLS;
- compression on a real link;
- a database much larger than the fixtures;
- the open arrival model of a load generator.

Its percentiles are the service boundary's on a CI runner. With the `ci`
profile they are a regression guard, not a capacity statement. The
measurement that counts for a release is the run with the release profile,
`-perf.profile=benchmark`, whose data volume is the one the budgets are set
for.

Flags (after `-args`): `-perf.assert`, `-perf.samples` (timed requests per
operation) and `-perf.profile` (`tiny`, `ci` or `benchmark`).

## Conditional requests and the result cache (NFR-PERF-005, NFR-PERF-006)

**Validators.** A data answer carries an `ETag` derived from four things:

- the request: the operation, path, query, `Accept`, `Accept-Language` and the caller's licence permissions;
- the version of every data family the answer was read from, which is the
  family's own version together with its data instant (`dataAsOf`), so a
  family whose data instant moves has a new version even when nothing else
  about it changed;
- the licence register the answer was read under: a fingerprint of every
  family's terms (use classes, acceptance, register version), attribution,
  licence, publisher and notes;
- what the answer states that the clock alone changes: each family's
  `stale` flag, each charge point's shown state, the sites a `free_min`
  filter kept, and a road answer's lamp with its reason.

A road conditions answer rests on more than the families it attributes: its
lamp reads every roads and weather family the caller may use, whether or not
one contributed. Its validator also carries, for each of those families, the
data version and the version of its newest confirmation (the instant, the
interval and whether its updates are paused), and a fingerprint of the
lamp's reference rows: which families cover each state's traffic messages,
and each junction's state. A family it read that has no data yet is recorded
as such, so its first data ends the answer.

The versions and the register fingerprint are read in one snapshot,
**before** the operation reads its data, and the answer is tagged with
them. An answer is therefore never tagged with versions newer than its data:
at worst it is tagged with older ones, and the next check reads it again.

A repeated request with `If-None-Match` is answered `304 Not Modified` while
all of these hold:

- no family it read has a new version (for a road conditions answer: no
  family its lamp read has a new data or confirmation version, and the
  lamp's reference fingerprint is the same);
- the register fingerprint is the same;
- the clock has not reached the instant the answer changes by itself: a
  family turning stale (its data older than `staleAfterS`), a charge point's
  report passing the status age limit (`charging.status_max_age` or its
  family's threshold), a site's report leaving a `free_min` filter, or a
  report dated after the read taking effect (storage reads no report dated
  after now; the earliest such report of the points shown, or of any
  parking site within the radius whether `free_min` keeps it or not, ends
  the answer). For the road answers: a family the lamp read turning stale,
  its confirmation lapsing (three intervals after it), a situation or a
  weather warning in the scope starting or ending, or an ended situation
  leaving `roads.ended_retention_h`.

A 304 runs no feature read. The versions are read once per
`cache.versions_ttl` for all requests, as the service's own read: it carries
no caller, so it is admitted as a service read and never under the tier of
the request that happened to ask. It does take a storage permit like any
other read, and in the storage budget shared with other readers it is a
standard read (NFR-CAP-002), the same as readiness: see
[rate-limits.md](rate-limits.md), Gate 5, step 2.

**The result cache.** A full answer is kept in memory, within these limits:

- `cache.max_bytes` for the whole cache, counting each entry's key,
  validator, versions, headers and body, plus a fixed overhead per entry;
- `cache.max_entry_bytes` for one body;
- `cache.max_entries` entries, with or without a body.

The least recently used entry goes first. A body is kept until its max-age
ends, or until its stale allowance ends when that is later (below); the
entry stays without it, so its validator still answers a 304.

A repeat with no validator is served from memory under the same conditions
as a 304. A replayed answer carries its own `requestId`, but the
`generatedAt`, `dataAsOf` and `ageS` of the request that built it. It also
carries an `Age` header: the whole seconds since that read (RFC 9111). The
data's age now is `ageS` plus `Age`. An answer read for this request carries
no `Age`, and neither does a 304: it tells the client its copy is current
now, so its max-age counts from the 304. (An `Age` counted from the first
read would pass max-age once the stored body expired, and the client would
revalidate on every request.)

**Identical concurrent requests** share one storage read. The first runs it,
and the others wait for its answer within their own deadline. A request
whose deadline ends while it waits is answered `deadline-exhausted`; it
starts no read of its own. The others take the first one's answer whether it
is stored or not: an answer that is never stored (below) or whose body is
over `cache.max_entry_bytes` still answers every request that waited for it,
each under its own `requestId`.

**What is never stored or validated:**

- an error;
- an answer from a family without a version;
- an answer read when the versions could not be read first;
- an overview with a part that is not ok (meta.parts).

Such an answer carries no `ETag` and `Cache-Control: no-store`, so no cache
downstream keeps it either, and an `If-None-Match` never matches it.

**Headers on a data answer:**

- `Cache-Control: private, max-age=N` (or `no-store` for an answer that is
  never stored, above), where N is the smallest
  `cache.max_age.<domain>` among the domains it answers from (for a road
  conditions answer, also those its lamp read), and never
  past the instant the answer changes by itself;
- `Last-Modified` from the newest data instant;
- `Vary` on the credentials, `Accept`, `Accept-Language` and `Accept-Encoding`.

`cache.enabled: false` sends `Cache-Control: no-store` and turns off the
validators and the result cache.

## Serving a stored answer while storage is down (NFR-RES-004, PROT-047)

When storage cannot answer a request (the answer would be `503`
`upstream-unavailable` or `deadline-exhausted`), the stored answer of the
same request is sent instead, as a `200`, if it was read no longer than
its domain's stale allowance ago: `cache.stale_if_error.<domain>`, with the
domains of `cache.max_age` (default five minutes each). An answer read from
several domains takes the smallest. It carries:

- the `served_stale` warning in `meta.warnings`, whose detail says how many
  seconds ago it was read;
- an `Age` header with those seconds, so the data's age now is still
  `ageS` plus `Age`;
- this request's `requestId`;
- `Cache-Control: no-store` and no `ETag`, so nothing downstream keeps it or
  revalidates against it.

To make this possible, a body is kept for the stale allowance even after its
max-age ends; it is never replayed as fresh past its max-age. Such bodies
count against `cache.max_bytes` like any other and are dropped least recently
used first, so a longer allowance than max-age holds more bodies at once: with
the defaults, an answer's body stays up to five minutes instead of 30 to 60
seconds. A cache that fills drops the bodies asked for least recently, which
then cannot be served stale; raise `cache.max_bytes` or shorten the allowance
of a domain whose bodies crowd the others out.

Requests waiting on the same read as a request storage could not answer are
answered stale with it, when they may be, and never ask storage again; one
whose own deadline ends while it waits is answered stale too, when it may be.

Nothing is served stale:

- when the stale allowance of a domain the answer was read from is `0s`;
- for an answer read from any family listed in `cache.no_stale_families`;
- when the answer was read under another licence register than the one last
  read successfully, because the answer must then pass the licence gate
  again: while storage is down the register cannot be read, and the last one
  read is what the answer is held to;
- before any register was read at all;
- for any other refusal: an overloaded `503`, a `403`, a `404`;
- for a request that never had an answer stored.

In each of these cases the request gets the answer it would get without the
cache.

## Compression

A body of at least `http.compress_min_bytes` is compressed:

- with Brotli or gzip, whichever the client prefers;
- at `http.compress_level`.

A smaller body, a 304 and an already encoded answer are left alone.

## Storage access (NFR-PERF-004)

**Partitioning.** Time-series relations of the data contract are partitioned
by observation time. The stub in `db/contract` mirrors that:

- `0001_contract.sql` declares the partitioning;
- `0003_physical.sql` adds a default partition and the indexes the reads use:
  - the newest observation per item;
  - GiST indexes on the locations;
  - child lookups.

`TestNoSequentialScanOfTimeSeries` runs every operation once and captures
each read statement it sends. It then explains each statement twice, as a
custom plan with its real arguments and as a generic plan. It fails on any
of these:

- a sequential scan of a time partition;
- no statement reading a time series at all.

It explains with `enable_seqscan = off`, which prices sequential scans out:
the planner then takes one only when no other path exists. The test
therefore proves that an index path exists for every read of a time series,
not that the planner picks it at normal prices. On the small fixture tables
a sequential scan is often the cheaper plan.

**Pool settings.** `db.query_exec_mode` and `db.statement_cache_capacity`
choose how statements are prepared and how many prepared statements are
kept per connection.

All keys named here are documented, with defaults and bounds, in
[configuration.md](configuration.md). The conformance test
`TestEveryKeyChangesBehaviour` proves that each key changes what the service
does.
