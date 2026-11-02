# Changelog

All notable changes to this project are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html); while
the version is 0.x a `feat` raises the minor and a `fix` the patch.

Entries above `0.1.0` are written by release-please from the Conventional
Commit messages merged into `main`; do not edit a released section by hand.
See [docs/RELEASING.md](docs/RELEASING.md).

## [0.2.0](https://github.com/mavrovde/viafrei-api/compare/v0.1.0...v0.2.0) (2026-11-02)


### Added

* **container:** distroless multi-arch image and local compose stack ([#25](https://github.com/mavrovde/viafrei-api/issues/25)) ([74ad828](https://github.com/mavrovde/viafrei-api/commit/74ad8285d29397dc5b5c1b0aa2b00b966808ff6a))
* **fixtures:** seeded synthetic fixture generator ([#24](https://github.com/mavrovde/viafrei-api/issues/24)) ([f220f2d](https://github.com/mavrovde/viafrei-api/commit/f220f2d7ed51761ec02a63903c396e704b6660ca))
* service skeleton and configuration layer ([#23](https://github.com/mavrovde/viafrei-api/issues/23)) ([609bbd1](https://github.com/mavrovde/viafrei-api/commit/609bbd134c2b31acc6a236f444f2e2a22e6e1368))
* **test:** requirement-ID test report and draft completion report ([#28](https://github.com/mavrovde/viafrei-api/issues/28)) ([be43174](https://github.com/mavrovde/viafrei-api/commit/be43174d778b90a115b81cd1b7a5e0e82eeba02f))


### Dependencies

* PostgreSQL 18 / PostGIS 3.6 for the dev database and the tests; the platform's production database follows ([#38](https://github.com/mavrovde/viafrei-api/issues/38)) ([459c7fb](https://github.com/mavrovde/viafrei-api/commit/459c7fb4efff944e9010660816fa7bc7f4aaa516))
* validator, yaml, login-action, sonarqube-scan-action and codeql-action bumps (Dependabot [#30](https://github.com/mavrovde/viafrei-api/issues/30)-[#33](https://github.com/mavrovde/viafrei-api/issues/33), [#36](https://github.com/mavrovde/viafrei-api/issues/36), [#37](https://github.com/mavrovde/viafrei-api/issues/37), [#39](https://github.com/mavrovde/viafrei-api/issues/39)) ([#40](https://github.com/mavrovde/viafrei-api/issues/40)) ([88a9a83](https://github.com/mavrovde/viafrei-api/commit/88a9a83c456c7f7fa46787b3cfc09cb5de4483ab))


### Build and CI

* CI, image publishing, SonarCloud and the release process ([#26](https://github.com/mavrovde/viafrei-api/issues/26)) ([6dd38b5](https://github.com/mavrovde/viafrei-api/commit/6dd38b582c45c8d83f7ed18368c7341ce2d10470))
* SAST, secrets, dependency, licence and DAST checks, and Scorecard ([#27](https://github.com/mavrovde/viafrei-api/issues/27)) ([ee69c79](https://github.com/mavrovde/viafrei-api/commit/ee69c79ae80f884202b56e6f5061230edf818f22))

## 0.1.0 - 2026-11-01

### Added

- Architecture decision ADR-0001: the service is written in Go.
- Repository scaffolding: code owners, issue and pull-request templates.
