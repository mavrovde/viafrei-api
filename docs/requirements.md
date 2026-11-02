# Requirement report

Every test that verifies a numbered requirement of the
[technical specification](https://github.com/mavrovde/viafrei-api/wiki/Technical-Specification)
says so with its first line (TEST-001):

```go
func TestReadyzFailsWithoutStorage(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	...
}
```

`req.Covers` writes one `REQ-ID: <id>` line into the test's output, so
`go test -json` records it under the test's name. `cmd/reqreport` then maps
the test results to the requirement catalogue (TEST-002).

## Inputs

| File | What it is |
|---|---|
| `test/requirements/catalogue.tsv` | Every requirement of the [Traceability Matrix](https://github.com/mavrovde/viafrei-api/wiki/Spec-Traceability-Matrix): ID, keyword (MUST, SHOULD, MAY), verification method, test level and the page that defines it. Generated from a wiki checkout with `make catalogue WIKI=<path>`; never edited by hand. |
| `test/requirements/scope/X.Y.Z.txt` | The milestone's scope: the automated MUST rows (methods Unit, Handler, Contract, Mapping, Parity, Security, Config, Lint) that must carry a passing test from that milestone on. The newest file is the milestone in progress. A milestone adds IDs to the next file; it never removes one without a recorded decision. |
| `go test -json` output | One file per level: `unit` and `integration` in CI. |

## What fails the build

`reqreport check` exits 1 and names each finding:

- an in-scope requirement without a passing test;
- a test that carries an ID the catalogue does not hold (a typo guard);
- a scope entry that is not in the catalogue, not a MUST row, or verified by a
  method that does not run on every pull request (Load, Docs, Review, Ops,
  Threat);
- a failed test;
- a leaf test that ran longer than 2 s (TST-012). A parent test's time is the
  sum of its subtests, so only leaves are held to the bound.

It exits 2, never 0, when it cannot read an input: an empty or malformed
catalogue, an empty scope, a result file with no test result or with a line
that is not test JSON (TST-011). Its last line says what it read: the
catalogue rows, the scope, the tests and result files, and how many
requirements are covered.

## Outputs

The `Requirement report` job of CI runs after the unit and integration jobs on
every pull request and on `main`, prints the report into the job summary and
uploads the `requirement-report` artifact:

- `requirement-report.md`: the findings, every in-scope row with its test
  count, and every covered requirement with each test, its level, result and
  time.
- `test-completion-report.md`: a draft of the test completion report in the
  shape of the wiki's
  [Test Documentation Templates](https://github.com/mavrovde/viafrei-api/wiki/Test-Documentation-Templates),
  section 2, with every figure this run knows filled in and the rest marked
  *to complete*. The maintainers complete it for the release
  ([RELEASING.md](RELEASING.md)).

Locally, `make report` runs the unit tests and writes `requirement-report.md`.
Integration-only requirements show as uncovered there; CI reads both levels.
