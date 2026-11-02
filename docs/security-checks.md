# Security checks

Every check below runs on each pull request, on every push to `main` and
weekly on Monday (`.github/workflows/security.yml`), except where noted. All
of them run in CI only. None reaches a live data source or production: the
DAST scan targets the local compose stack.

| Check | Tool (pinned) | Fails on | Requirement |
|---|---|---|---|
| SAST | CodeQL `security-extended` for Go | any alert at error level, shown in code scanning | SSC-014 |
| SAST | Semgrep 1.181.0 (`p/golang`, `p/secrets`, `p/dockerfile`, `p/github-actions`) | any finding | SSC-014 |
| Lint-level security | golangci-lint with gosec (CI workflow) | any finding | SSC-014 |
| Secrets in the whole history | gitleaks 8.30.1 | any finding (redacted in the log) | SSC-017 |
| Known vulnerabilities, reachable | govulncheck 1.9.0 (CI workflow) | a vulnerable function that is called | SSC-014 |
| Known vulnerabilities, modules | OSV-Scanner 2.7.0 | any advisory against `go.mod` | SSC-014 |
| Known vulnerabilities, repository | Trivy 0.76.0 `fs` (vuln, secret) | HIGH or CRITICAL with a fix | SSC-014 |
| Known vulnerabilities, image | Trivy 0.76.0 `image` (CI workflow) | HIGH or CRITICAL with a fix | SSC-014 |
| Dependencies | Snyk CLI 1.1308.1 (binary, sha256 checked) | high or critical; fails naming `SNYK_TOKEN` when the secret is absent | SSC-014 |
| New dependencies in a pull request | GitHub dependency review | a high advisory, or a licence outside the allow-list | SSC-003 |
| Licences of every module | go-licenses 2.0.1 | a licence outside the allow-list; the report is an artifact | SSC-003 |
| Dockerfile and workflows | Checkov 3.3.29, Hadolint 2.15.1 (CI workflow), actionlint and shellcheck (CI workflow) | any failed check | SSC-005, SSC-010 |
| DAST | OWASP ZAP API scan (`zap-stable` 20261016) with the service's OpenAPI document, against the compose stack (`scripts/zap-scan.sh`) | a rule at FAIL level in `scripts/zap-rules.tsv`; WARN-level alerts are in the report artifact | TST-010 |
| Quality and coverage | SonarCloud, CI-based analysis with the Go coverage report (CI workflow) | the quality gate; fails naming `SONAR_TOKEN` when the secret is absent | — |
| Repository posture | OpenSSF Scorecard (`scorecard.yml`, `main` and weekly only) | published to code scanning; target score 7.0 | SSC-012 |

Licence allow-list (SSC-003): MIT, Apache-2.0, BSD-2-Clause, BSD-3-Clause, ISC,
MPL-2.0, Unicode-3.0, Zlib, CC0-1.0, 0BSD.

## Suppressions

A suppression is written next to the code it covers, with its reason:

- `internal/fixtures`: `math/rand/v2` is required, because fixtures must be
  reproducible from a seed and nothing in them is a secret (`#nosec G404`,
  `nosemgrep`).

## Running a check locally

```sh
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go run github.com/google/osv-scanner/v2/cmd/osv-scanner@v2.7.0 scan source --recursive .
go run github.com/google/go-licenses/v2@v2.0.1 check ./... \
  --allowed_licenses=MIT,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MPL-2.0,Unicode-3.0,Zlib,CC0-1.0,0BSD \
  --ignore github.com/mavrovde/viafrei-api
scripts/zap-scan.sh   # needs Docker; starts and removes the compose stack
```
