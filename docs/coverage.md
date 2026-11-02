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
