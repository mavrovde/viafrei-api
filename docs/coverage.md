# Test coverage

Every package of every layer is held at **100 % of its statements**: domain,
application, HTTP adapters and middleware, repositories, configuration, the
command lines and the test kit itself. CI fails below that, package by
package, in the **Coverage gate** job.

## How it is measured

Three profiles are merged. A block counts as covered when any of them ran it.

| Profile | Produced by | Reaches |
|---|---|---|
| `coverage-unit.out` | `go test -coverpkg=./...` (Unit and handler tests job) | everything a unit or handler test can reach |
| `coverage-integration.out` | `go test -tags integration -coverpkg=./...` against a throwaway PostGIS container (testcontainers) | the store, the generated queries and the container harness |
| `coverage-boot.out` | `scripts/boot-coverage.sh`: every main package built with `-cover` and run once | `func main()` itself |

`internal/testkit/covergate` merges them, prints the table per package (also in
the job summary) and writes the merged `coverage.out`, which is the profile
SonarCloud reads. A profile with no block fails the gate rather than passing it,
and a main package the boot script has no invocation for fails the script.

Run it locally with `make coverage` (the integration leg needs a container
runtime).

## Exclusions

Only generated code may be left out, and only through
`test/coverage-exclusions.txt`: one line per entry, a glob over the
module-relative file path followed by the reason. An entry without a reason, or
one that no longer matches a measured file, fails the gate. The file lists
nothing today.

## Making unreachable branches go away

A branch that cannot fail by construction (marshalling a value the program
built, a constant URL) goes through `internal/invariant` (`Must`, `Check`),
which panics with the cause, instead of an error return that no test can
reach.

## Mutation testing

Coverage shows which lines ran, not whether a test would notice a wrong
answer. Mutation testing does (TST-002): [Gremlins](https://github.com/go-gremlins/gremlins)
v0.6.0 changes one operator at a time (a negated condition, a moved
boundary, `&&` for `||`, `continue` for `break`, …), runs the package's tests,
and a mutant the tests do not kill is reported.

| Run | When | Mutants scored | Fails |
|---|---|---|---|
| pull request | every pull request (`mutation.yml`) | the changed code lines of in-scope files; a changed test file puts its whole package under test | below 80 %, or zero mutants for changed in-scope code |
| full | the release pull request of a minor version (`X.Y.0`), or a manual run of `mutation.yml` | every in-scope mutant | below 70 %, or a stale equivalent entry |

The scope is `test/mutation/scope.tsv`: the domain, the authenticator and
key lifecycle, and the key check and gated answer at the HTTP edge; the
admission code (#8) joins it. A change that touches no in-scope code mutates
nothing and says so.

`internal/testkit/mutgate` scores the Gremlins reports:
score = killed ÷ (generated − equivalent). Not viable mutants (they do not
compile) are reported, not scored. A mutant that lived, was not covered or
timed out is listed as not detected in the report and the job summary; a
timeout never raises the score, because it is more often a busy runner than
a hang (the script gives each mutant 30 times the clean test time).

An **equivalent mutant** changes no behaviour, so no test can kill it. It is
listed in `test/mutation/equivalent.tsv` with its location
(`file:line:column`, as the report prints it), the mutator, the reason and
the reviewer who agreed; an entry without a reason or a reviewer is refused,
and one naming a mutant the tests kill fails the gate. Prefer a refactor
that removes the equivalent mutant: the file lists nothing today, because
each survivor of the first run was either killed by a new test or removed
by restructuring (a `switch`'s case expressions are reported as not covered,
so the rules use `if` chains).

Run it locally with `scripts/mutation.sh pr origin/main` or
`scripts/mutation.sh full`. The script runs Gremlins as
`go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0`, as CI runs its
other tools: the version is verified against the checksum database and never
enters the service's dependencies.
