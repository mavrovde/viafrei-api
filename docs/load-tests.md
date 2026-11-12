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
| `slow-storage` | latency beyond the deadline on every storage byte | `503 deadline-exhausted` by the deadline; 2xx after the repair |
| `hanging-query` | every feature table locked | reads cut at the deadline; afterwards a run of requests without a poisoned connection |
| `pool-exhausted` | every permit held, plus bursts | the admission refuses (`429 overloaded` with `Retry-After`), each refusal within the tier's `queue_max_wait` plus 50 ms (`-queue-max-wait`, default 400 ms) |
| `composed-part-timeout` | the parking tables locked | `200` with the parking part `timeout` and the charging part `ok` |
| `storage-killed` | the storage route cut | `503 upstream-unavailable` with `Retry-After`, p95 within 50 ms; 2xx after the repair |
| `access-store-down` | the access-store route cut | a cached key keeps working; an uncached key is `503 upstream-unavailable`; both work after the repair |
| `clock-skew` | the source clock 10 minutes ahead and 30 days behind | no negative age; ahead is fresh; behind is stale with `data_stale` |
| `instance-killed` | `SIGKILL` under load, then a start | ready within 30 s; 2xx again |

The report lists what each scenario does not assert yet:

- the circuit breaker (NFR-RES-002) and `served_stale` answers
  (NFR-RES-004), which do not exist in the service yet;
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
nightly run on `main` (`scripts/load-previous.sh`) and the report gains a
comparison section, also in the job summary: each figure, its previous value
and the change.

- A class's p95 or p99 more than `-regression-tolerance` (default 10 %) and
  more than `-regression-floor` (default 5 ms) above the previous run fails
  the run, and the report names the scenario, the class and the figure.
  Only a class with at least 20 2xx answers in both runs is judged; a
  percentile of fewer answers is a single answer.
- With no previous run, or a previous run that left no figures, the report
  names the absent file and the run is judged on the absolute budgets alone.
- A previous file that exists but cannot be read as figures fails the run;
  it is never read as "no change".
- Runs of different budgets (`pr` and `nightly`) are not compared, and the
  report says so.

The pull-request job writes its figures too but compares with nothing; its
report says so.

## Running it

```sh
scripts/load-test.sh pr                     # the pull-request budget, report in load-report.md
scripts/load-test.sh nightly night.md       # the full run, about two hours
LOADRUN_PREVIOUS=prev/load-report.json scripts/load-test.sh nightly   # compared with earlier figures
```

It needs Docker and the ports the stack publishes on the loopback. Run only one stack per machine.
