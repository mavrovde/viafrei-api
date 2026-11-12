# Load and fault tests

Soak, stress, spike and fault-injection runs (TST-007; the
[Performance Test Plan](https://github.com/mavrovde/viafrei-api/wiki/Performance-Test-Plan))
run on the test stack only: the Compose stack of this repository, started by
the job that runs them, on the loopback. They never reach a deployment.

| Run | Where | Budget |
|---|---|---|
| Pull request | CI workflow, job `Load and faults (pull-request budget)` | design load 20 requests/s, a 30 s soak, faults held 6 s |
| Nightly | `load-nightly.yml` (and by hand: *Run workflow*) | design load 50 requests/s, a one-hour soak, faults held 1 min |

Both write `load-report.md`: one row per scenario with its request count,
its answers by status class, its p95 and its slowest answer, then every
assertion of every scenario. It is the job summary and an artifact; the
nightly artifact is kept 90 days for the release evidence.

## How it runs

`scripts/load-test.sh PROFILE [REPORT]` starts the stack with
`test/load/compose.yaml` on top of `compose.yaml`. The override puts a fault
proxy (Toxiproxy, pinned by digest) between the service and both of its
stores: the storage route and the access-store route. It also creates the
access-store role, issues two throwaway keys (one used throughout, one used
only while the access store is down, so it is not cached) and runs the
runner, `go run ./internal/testkit/loadrun`. The stack is removed on exit.

The override turns the result cache off (`cache.enabled: false`). The runner
repeats the same few requests, so with the cache on they would be answered
from memory and a fault on the storage route would reach none of them. With
it off, every request reaches storage and the path its scenario cuts. The
cache is tested by the unit and integration tests and by the benchmark leg.

A second instance of the same image, `api-cached`, runs with the cache on
behind the same fault proxy, published on the loopback at port 8081
(`-cached-api`). Only `storage-killed` speaks to it, to see stale answers
(NFR-RES-004). Its `cache.stale_if_error.parking` is 4 s, its
`cache.max_age.parking`, `cache.max_age.energy` and `cache.versions_ttl` are
1 s each, and `cache.no_stale_families` lists every charging family of the
fixture set. `scripts/load-test.sh` reads these four from
`test/load/compose.yaml` and passes them as `-stale-if-error`,
`-max-age-parking`, `-max-age-energy` and `-versions-ttl`. Within the max-age
a stored answer is replayed as fresh from memory, storage or not, so the
runner asks for stale answers one and a half times the longest of the three
after the cut; the allowance must be at least twice that, or the runner
refuses to start.

The runner speaks only to hosts on `test/load/allow-list.txt` (the loopback).
Any other host, any scheme but `http`, and a URL with credentials are refused
before a request is sent (exit 2). An empty allow-list is refused too. Faults
are injected through the fault proxy's API and through `docker compose` on the
stack's own project. The database is read through `docker compose exec`, never
over the network. The keys come from the environment and are never printed.

## What every scenario asserts

- It sent at least one request; a scenario that sent none fails.
- No answer later than `deadline.request` plus 50 ms (NFR-PERF-001).
- The database was sampled throughout the scenario:
  - never more storage connections than `db.pool_max` (NFR-CAP-002);
  - no storage statement still running later than the deadline plus
    `db.cancel_grace` plus 100 ms (NFR-PERF-002).
- The fault proxy was put back afterwards.

A scenario that cannot inject its fault or read its evidence fails. It is
never skipped.

| Scenario | Fault | Asserted |
|---|---|---|
| `soak` | none | every answer 2xx; p95 and p99 per class within `test/load/budgets.tsv` (NFR-PERF-003); resident memory grows at most 10 % after warm-up; no connection left busy |
| `spike` | four times design load | every refusal carries `Retry-After`; back to 2xx and within budget afterwards |
| `stress` | a ramp doubling the rate past the knee | the knee is reached; throughput above it stays at least 80 % of the peak; refusals are `429` with `Retry-After` within 50 ms (NFR-CAP-003) |
| `slow-storage` | latency beyond the deadline on every storage byte | `503 deadline-exhausted` by the deadline, or refused by the circuit breaker those failures open; 2xx after the repair |
| `hanging-query` | every feature table locked | reads cut at the deadline; afterwards a run of requests without a poisoned connection |
| `pool-exhausted` | every permit held, plus bursts | the admission refuses (`429 overloaded` with `Retry-After`), each refusal within the tier's `queue_max_wait` plus 50 ms (`-queue-max-wait`, default 400 ms) |
| `composed-part-timeout` | the parking tables locked | `200` with the parking part `timeout` and the charging part `ok` |
| `storage-killed` | the storage route cut | `503 upstream-unavailable` with `Retry-After`, p95 within 50 ms; the circuit breaker opens after at most `breaker.failure_threshold` reads reached storage, lets at most `breaker.half_open_probes` reads through in each half-open period, opens again on a failed probe, and a probe closes it after the repair (read from the instance's log; `-breaker-failures`, `-breaker-open-for`, `-breaker-probes` give the instance's settings, which `scripts/load-test.sh` reads from `test/load/compose.yaml`); 2xx after the repair. On `api-cached`: a parking search answered just before the cut is served `200` with `served_stale` and an `Age` no older than `-stale-if-error`, p95 within 50 ms; a parking search it never answered, one answered longer ago than the allowance, and a charging search (a family without a stale allowance) are `503 upstream-unavailable` with `Retry-After`; after the repair it answers from storage again. The instance without a cache serves nothing stale |
| `access-store-down` | the access-store route cut | a cached key keeps working; an uncached key is `503 upstream-unavailable`; both work after the repair |
| `clock-skew` | the source clock 10 minutes ahead and 30 days behind | no negative age; ahead is fresh; behind is stale with `data_stale` |
| `instance-killed` | `SIGKILL` under load, then a start | ready within 30 s; 2xx again |

The report lists what each scenario does not assert yet:

- metering buffering while the access store is down;
- a moved service clock (the service's distroless container has no way to
  move it; quota windows under a moved clock are unit-tested);
- the metering counts lost to a kill.

## Comparison with the previous run on main

Every run writes its figures beside the report, as JSON (`load-report.json`
for `load-report.md`): for each scenario and endpoint class, the requests,
the answers by status class, p50, p95 and p99 of the 2xx answers, the
slowest answer and the share refused under load, and for the soak the
resident memory's growth (TST-021).

The nightly run on `main` fetches the figures of the newest successful
nightly run on `main` of the same budget (`scripts/load-previous.sh`, which
passes over a newer dispatched run of the other budget, by name) and the
report gains a comparison section, also in the job summary: each figure, its
previous value and the change.

- A class's p95 or p99 more than `-regression-tolerance` (default 10 %) and
  more than `-regression-floor` (default 5 ms) above the previous run fails
  the run, and the report names the scenario, the class and the figure.
  Only a class with at least 20 2xx answers in both runs is judged; a
  percentile of fewer answers is a single answer. A class not judged is
  listed under the table with its counts.
- With no previous run, a previous run that left no figures, an expired
  artifact, or no run of the same budget among the newest ten, the script
  says which and the report names the absent file; the run is judged on the
  absolute budgets alone.
- A previous file that exists but cannot be read as figures fails the run;
  it is never read as "no change".
- Runs of different budgets (`pr` and `nightly`) are not compared, and the
  report says so.

The pull-request job writes its figures too but compares with nothing; its
report says so.

**Accepting a slowdown.** The baseline is the newest successful run, so a
slowdown that is accepted would fail every later run. Run the workflow by
hand on `main` with the `compare` input off: that run is judged on the
absolute budgets alone, and when it passes its figures are the next run's
baseline. The same run makes a new baseline once the previous one's
artifact has expired.

## Running it

```sh
scripts/load-test.sh pr                     # the pull-request budget, report in load-report.md
scripts/load-test.sh nightly night.md       # the full run, about two hours
LOADRUN_PREVIOUS=prev/load-report.json scripts/load-test.sh nightly   # compared with earlier figures
```

It needs Docker and the ports the stack publishes on the loopback. Run only one stack per machine.
