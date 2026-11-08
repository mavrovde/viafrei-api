# Security checks

Every check below runs on each pull request, on every push to `main` and
weekly on Monday (`.github/workflows/security.yml`), except where noted. All
of them run in CI only. None reaches a live data source or production: the
DAST scan targets the local compose stack.

Every scanner that can write SARIF uploads it to GitHub code scanning (the
`SARIF category` column), so its findings are listed in the Security tab and
annotated on the pull request. A pull request from a fork runs every check but
uploads nothing, because its token cannot write to code scanning. Scanners in
containers write their report inside the container and
`scripts/sarif-docker.sh` copies it out; a scanner that wrote no readable
SARIF fails its job. `scripts/zap-sarif.sh` turns the ZAP JSON report into
SARIF, each result located on `api/openapi.json`.

| Check | Tool (pinned) | Fails on | SARIF category | Requirement |
|---|---|---|---|---|
| SAST | CodeQL `security-extended` for Go | any alert at error level | `/language:go` | SSC-014 |
| Lint-level security | golangci-lint with gosec (CI workflow) | any finding | — (log) | SSC-014 |
| Secrets in the whole history | gitleaks 8.30.1 | any finding (redacted in the log and the report) | `gitleaks` | SSC-017 |
| Known vulnerabilities, reachable | govulncheck 1.9.0 (CI workflow) | a vulnerable function that is called | `govulncheck` | SSC-014 |
| Known vulnerabilities, image | Trivy 0.76.0 `image` (CI workflow) | HIGH or CRITICAL with a fix | `trivy-image` | SSC-014 |
| Known vulnerabilities, every dependency | Snyk Open Source, CLI 1.1308.1 (binary, sha256 checked) | high or critical; fails naming `SNYK_TOKEN` when the secret is absent | `snyk-open-source` | SSC-014 |
| New dependencies in a pull request | GitHub dependency review (pull requests only) | a high advisory, or a licence outside the allow-list | — (pull request comment) | SSC-003 |
| Licences of every module | go-licenses 2.0.1 | a licence outside the allow-list; the report is an artifact | — (artifact) | SSC-003 |
| Dockerfile | Hadolint 2.15.1 (CI workflow) | a warning or worse | `hadolint` | SSC-005 |
| Workflows and scripts | actionlint 1.7.7 and shellcheck (CI workflow) | any finding | — (log) | SSC-005, SSC-010 |
| OpenAPI document | Spectral 6.17.0 with `.spectral.yaml` (CI workflow) | a warning or worse | `spectral` | — |
| API fuzzing | Schemathesis against the compose stack (CI workflow, `Compose smoke`) | any failed property | — (log) | TST-010 |
| DAST | OWASP ZAP API scan (`zap-stable` 20261016) with the service's OpenAPI document, against the compose stack (`scripts/zap-scan.sh`), on every pull request, on `main` and weekly | a rule at FAIL level in `scripts/zap-rules.tsv`; WARN-level alerts are in the report artifact | `zap` | TST-010 |
| Quality and coverage | SonarCloud, CI-based analysis with the Go coverage report (CI workflow) | the quality gate; fails naming `SONAR_TOKEN` when the secret is absent | — (the `SonarCloud Code Analysis` check) | — |
| Repository posture | OpenSSF Scorecard (`scorecard.yml`, `main` and weekly only) | published to code scanning; target score 7.0 | Scorecard's own | SSC-012 |

## One tool per kind of check

Each kind of check is done by one tool, so a finding is reported once and a
run is not spent repeating another's work. `TestScannersUploadSARIF` holds the
SARIF uploads to exactly the set above: an upload under any other category
fails it.

| Kind of check | Kept | Removed, and why |
|---|---|---|
| SAST | CodeQL (`security-extended`), with gosec in golangci-lint as the lint-level pass | Semgrep: its Go and OWASP rulesets cover the classes CodeQL `security-extended` and gosec already report; its secrets, Dockerfile and workflow rulesets repeat gitleaks, Hadolint and actionlint |
| Known vulnerabilities in dependencies | govulncheck (reachability: fails only on vulnerable code that is called) and Snyk Open Source (a required check: every advisory against the dependency graph) | OSV-Scanner and Trivy `fs`: the same advisory databases over the same `go.mod`, a third and fourth report of each finding |
| Container image | Trivy `image` | Snyk Container: the same base-image and binary advisories, and a second token-metered test per run |
| Secrets | GitHub secret scanning with push protection (new pushes) and gitleaks (the whole history, which push protection does not scan) | Trivy `fs` secret scanner and Checkov's secrets framework |
| Dockerfile | Hadolint | Checkov's Dockerfile framework |
| Workflows | actionlint with shellcheck, and the workflow conformance tests (pinned actions, least-privilege permissions, timeouts) | Checkov's GitHub Actions framework |
| DAST | OWASP ZAP (security rules) and Schemathesis (the contract: properties of every operation) | none: they test different things |

Licence allow-list (SSC-003): MIT, Apache-2.0, BSD-2-Clause, BSD-3-Clause, ISC,
MPL-2.0, Unicode-3.0, Zlib, CC0-1.0, 0BSD.

## Suppressions

A suppression is written next to the code it covers, with its reason:

- `internal/fixtures`: `math/rand/v2` is required, because fixtures must be
  reproducible from a seed and nothing in them is a secret (`#nosec G404`).

## Running a check locally

```sh
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go run github.com/google/go-licenses/v2@v2.0.1 check ./... \
  --allowed_licenses=MIT,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MPL-2.0,Unicode-3.0,Zlib,CC0-1.0,0BSD \
  --ignore github.com/mavrovde/viafrei-api
scripts/zap-scan.sh   # needs Docker; starts and removes the compose stack
```
