# Security policy

## Reporting a vulnerability

Please do not report security issues in public issues, pull requests or
discussions. Use GitHub's
[private vulnerability reporting](https://github.com/mavrovde/viafrei-api/security/advisories/new).

- **Acknowledgement** within 3 working days.
- **Coordinated disclosure:** we publish an advisory once a fix is released,
  and at the latest 90 days after your report unless we agree on another date
  with you.
- **Remediation targets** from confirmation: critical 2 days, high 7 days,
  medium 30 days, low with the next minor release.

## Supported versions

Until 1.0.0 only the newest release receives fixes. A fix ships as a patch
release; see [docs/RELEASING.md](docs/RELEASING.md) for the hotfix path.

## Verifying what you run

Every published image is signed with cosign (keyless) and carries SLSA build
provenance and an SBOM. The commands are in
[docs/container.md](docs/container.md#verify-an-image) and on each release page.

## Automated checks

The checks that run on every pull request, on `main` and weekly are listed in
[docs/security-checks.md](docs/security-checks.md).
