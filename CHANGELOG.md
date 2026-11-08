# Changelog

All notable changes to this project are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html); while
the version is 0.x a `feat` raises the minor and a `fix` the patch.

Entries above `0.1.0` are written by release-please from the Conventional
Commit messages merged into `main`; do not edit a released section by hand.
See [docs/RELEASING.md](docs/RELEASING.md).

## [0.2.0](https://github.com/mavrovde/viafrei-api/compare/v0.1.0...v0.2.0) (2026-11-08)


### Added

* **container:** distroless multi-arch image and local compose stack ([#25](https://github.com/mavrovde/viafrei-api/issues/25)) ([74ad828](https://github.com/mavrovde/viafrei-api/commit/74ad8285d29397dc5b5c1b0aa2b00b966808ff6a))
* **contract:** the OpenAPI conformance harness, in both directions, and the comparison with the last released document (TEST-003, TEST-004, TST-015) ([#44](https://github.com/mavrovde/viafrei-api/issues/44)) ([a3b6ba0](https://github.com/mavrovde/viafrei-api/commit/a3b6ba0986cf48b7cc1520c67d0948379bbf37fe)), closes [#15](https://github.com/mavrovde/viafrei-api/issues/15)
* **fixtures:** recorded checksums verified on every machine, and the generator held to its own inputs (TST-014) ([#43](https://github.com/mavrovde/viafrei-api/issues/43)) ([f1e0831](https://github.com/mavrovde/viafrei-api/commit/f1e0831d1de66adb2df19bfd59951a1d883ed02d)), closes [#13](https://github.com/mavrovde/viafrei-api/issues/13)
* **fixtures:** seeded synthetic fixture generator ([#24](https://github.com/mavrovde/viafrei-api/issues/24)) ([f220f2d](https://github.com/mavrovde/viafrei-api/commit/f220f2d7ed51761ec02a63903c396e704b6660ca))
* service skeleton and configuration layer ([#23](https://github.com/mavrovde/viafrei-api/issues/23)) ([609bbd1](https://github.com/mavrovde/viafrei-api/commit/609bbd134c2b31acc6a236f444f2e2a22e6e1368))
* **test:** requirement-ID test report and draft completion report ([#28](https://github.com/mavrovde/viafrei-api/issues/28)) ([be43174](https://github.com/mavrovde/viafrei-api/commit/be43174d778b90a115b81cd1b7a5e0e82eeba02f))


### Fixed

* **release:** approve the release pull request's own CI and Security runs instead of dispatching new ones ([#5](https://github.com/mavrovde/viafrei-api/issues/5)) ([#51](https://github.com/mavrovde/viafrei-api/issues/51)) ([6490a62](https://github.com/mavrovde/viafrei-api/commit/6490a62814994023a0e92fc28f2b8249a5692dde))
* **release:** check the repository out before the approval script runs ([#5](https://github.com/mavrovde/viafrei-api/issues/5)) ([#52](https://github.com/mavrovde/viafrei-api/issues/52)) ([3ff0af6](https://github.com/mavrovde/viafrei-api/commit/3ff0af62b30f20a3547b5242a4f1294b35db5b7b))


### Dependencies

* PostgreSQL 18 / PostGIS 3.6 for the dev database and the tests; the platform's production database follows ([#38](https://github.com/mavrovde/viafrei-api/issues/38)) ([459c7fb](https://github.com/mavrovde/viafrei-api/commit/459c7fb4efff944e9010660816fa7bc7f4aaa516))
* validator, yaml, login-action, sonarqube-scan-action and codeql-action bumps (Dependabot [#30](https://github.com/mavrovde/viafrei-api/issues/30)-[#33](https://github.com/mavrovde/viafrei-api/issues/33), [#36](https://github.com/mavrovde/viafrei-api/issues/36), [#37](https://github.com/mavrovde/viafrei-api/issues/37), [#39](https://github.com/mavrovde/viafrei-api/issues/39)) ([#40](https://github.com/mavrovde/viafrei-api/issues/40)) ([88a9a83](https://github.com/mavrovde/viafrei-api/commit/88a9a83c456c7f7fa46787b3cfc09cb5de4483ab))


### Build and CI

* CI, image publishing, SonarCloud and the release process ([#26](https://github.com/mavrovde/viafrei-api/issues/26)) ([6dd38b5](https://github.com/mavrovde/viafrei-api/commit/6dd38b582c45c8d83f7ed18368c7341ce2d10470))
* **release:** open the release pull request with GITHUB_TOKEN and dispatch CI and Security on it ([#5](https://github.com/mavrovde/viafrei-api/issues/5)) ([#45](https://github.com/mavrovde/viafrei-api/issues/45)) ([838b487](https://github.com/mavrovde/viafrei-api/commit/838b4870c1173b235ea9882191e3dfb768e883f6))
* **release:** publish the docs (OpenAPI reference and guide) to GitHub Pages on each release ([#5](https://github.com/mavrovde/viafrei-api/issues/5)) ([#53](https://github.com/mavrovde/viafrei-api/issues/53)) ([65bab5f](https://github.com/mavrovde/viafrei-api/commit/65bab5fe4e31af447f736ea00c2e72be2ad36a56))
* SAST, secrets, dependency, licence and DAST checks, and Scorecard ([#27](https://github.com/mavrovde/viafrei-api/issues/27)) ([ee69c79](https://github.com/mavrovde/viafrei-api/commit/ee69c79ae80f884202b56e6f5061230edf818f22))
* **security:** every scanner uploads SARIF to code scanning, Snyk Container on the image, Semgrep's OWASP ruleset ([#12](https://github.com/mavrovde/viafrei-api/issues/12)) ([#46](https://github.com/mavrovde/viafrei-api/issues/46)) ([951bffb](https://github.com/mavrovde/viafrei-api/commit/951bffb432846996077bd6ba7035fba0a4af6f7a))

## 0.1.0 - 2026-11-01

### Added

- Architecture decision ADR-0001: the service is written in Go.
- Repository scaffolding: code owners, issue and pull-request templates.
