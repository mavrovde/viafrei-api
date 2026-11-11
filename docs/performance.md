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
| transit | 80 ms | 250 ms | 600 ms |
| along | 120 ms | 400 ms | 900 ms |
| composed | 300 ms | 800 ms | 1500 ms |
| history | 150 ms | 400 ms | 1000 ms |

The budgets come from two files:

- `test/perf/classes.tsv` holds the class table.
- `test/perf/operations.tsv` gives every operation its class.

`internal/perf` reads both files and refuses three kinds of class:

- one out of order (p50 ≤ p95 ≤ p99 is required);
- one that reaches the configured request deadline (`deadline.request`, at most 2 s);
- one that no operation could use.

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

Its percentiles are the service boundary's on a CI runner. They are a
regression guard, not a capacity statement.

Flags (after `-args`): `-perf.assert`, `-perf.samples` (timed requests per
operation) and `-perf.profile` (`tiny`, `ci` or `benchmark`).

## Conditional requests and the result cache (NFR-PERF-005, NFR-PERF-006)

**Validators.** A data answer carries an `ETag` derived from two things:

- the request: the operation, path, query, `Accept`, `Accept-Language` and the caller's licence permissions;
- the version of every data family the answer was read from.

A repeated request with `If-None-Match` is answered `304 Not Modified`, as
long as no family it read has a new version. That costs no storage read: the
family versions are read once per `cache.versions_ttl` for all requests.

**The result cache.** A full answer is kept in memory, within these limits:

- `cache.max_bytes` for the whole cache;
- `cache.max_entry_bytes` for one entry.

A repeat with no validator is served from memory while its families are
unchanged.

**Identical concurrent requests** share one storage read. The first runs it,
and the others wait for its answer within their own deadline.

**What is never stored or validated:**

- an error;
- an answer from a family without a version.

**Headers on a data answer:**

- `Cache-Control: private, max-age=N`, where N is the smallest
  `cache.max_age.<domain>` among the domains it answers from;
- `Last-Modified` from the newest data instant;
- `Vary` on the credentials, `Accept`, `Accept-Language` and `Accept-Encoding`.

`cache.enabled: false` sends `Cache-Control: no-store` and turns off the
validators and the result cache.

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

**Pool settings.** `db.query_exec_mode` and `db.statement_cache_capacity`
choose how statements are prepared and how many prepared statements are
kept per connection.

All keys named here are documented, with defaults and bounds, in
[configuration.md](configuration.md). The conformance test
`TestEveryKeyChangesBehaviour` proves that each key changes what the service
does.
