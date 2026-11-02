# Security policy

## Reporting a vulnerability

Please do not report security issues in public issues, pull requests or
discussions. Use GitHub's
[private vulnerability reporting](https://github.com/mavrovde/viafrei-api/security/advisories/new).
You will get an answer within five working days.

## Supported versions

Until 1.0.0 only the newest release receives fixes. A fix ships as a patch
release; see [docs/RELEASING.md](docs/RELEASING.md) for the hotfix path.

## Verifying what you run

Every published image is signed with cosign (keyless) and carries SLSA build
provenance and an SBOM. The commands are in
[docs/container.md](docs/container.md#verify-an-image) and on each release page.
