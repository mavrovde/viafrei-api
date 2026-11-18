# Changelog

All notable changes to this project are documented in this file. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project uses [Semantic Versioning](https://semver.org/spec/v2.0.0.html); while
the version is 0.x a `feat` raises the minor and a `fix` the patch.

Entries above `0.1.0` are written by release-please from the Conventional
Commit messages merged into `main`; do not edit a released section by hand.
See [docs/RELEASING.md](docs/RELEASING.md).

## [0.3.0](https://github.com/mavrovde/viafrei-api/compare/v0.2.1...v0.3.0) (2026-11-18)


### ⚠ BREAKING CHANGES

* release candidates and builds of main publish no image; verify release images with the narrowed identity regexp in docs/container.md.
* the API needs viafrei-database 0.3.3 or later (schema head 316): it reads the 0.3 read contract and migration 0316's lookups; it runs no DDL itself.

### Added

* **auth:** API keys and the licence gate ([#7](https://github.com/mavrovde/viafrei-api/issues/7)) ([#60](https://github.com/mavrovde/viafrei-api/issues/60)) ([b328629](https://github.com/mavrovde/viafrei-api/commit/b328629b8d4fda066d923a8bf5d27cf73e9d22ab))
* **cache:** serve stored answers with served_stale while storage is down ([#79](https://github.com/mavrovde/viafrei-api/issues/79)) ([#89](https://github.com/mavrovde/viafrei-api/issues/89)) ([a68e955](https://github.com/mavrovde/viafrei-api/commit/a68e955e74e91185be1d2a750c63d9839879f442))
* **console:** owner console for keys with passkey sign-in on a loopback admin listener ([#74](https://github.com/mavrovde/viafrei-api/issues/74)) ([06b84d8](https://github.com/mavrovde/viafrei-api/commit/06b84d8e6aa79b942eecb1b4a4bfa4936f1bdc25)), closes [#61](https://github.com/mavrovde/viafrei-api/issues/61)
* contract 0.3, serve everything the site and the MCP server ask for ([#137](https://github.com/mavrovde/viafrei-api/issues/137)) ([e628721](https://github.com/mavrovde/viafrei-api/commit/e6287212b7f347a3c8e78d9a55ba428abf88bf89))
* **endpoints:** charging and parking near a position, and a composed place overview ([#64](https://github.com/mavrovde/viafrei-api/issues/64)) ([4d18138](https://github.com/mavrovde/viafrei-api/commit/4d1813840bbbe05c482cc1c5baa817032c85df0c)), closes [#10](https://github.com/mavrovde/viafrei-api/issues/10)
* **limits:** rate limits, quotas, priority tiers, load shedding and usage meter ([#65](https://github.com/mavrovde/viafrei-api/issues/65)) ([fa1f49f](https://github.com/mavrovde/viafrei-api/commit/fa1f49f0049c53765d0bdbf91ecce2bf88ef55c7))
* **load:** each run writes its figures; the nightly run on main is compared with the previous one (TST-021) ([#86](https://github.com/mavrovde/viafrei-api/issues/86)) ([ffb3c0a](https://github.com/mavrovde/viafrei-api/commit/ffb3c0a05a48ef28b7326cbfc6a04294be725c3f))
* **perf:** latency budgets, conditional requests, a result cache and plan checks ([#77](https://github.com/mavrovde/viafrei-api/issues/77)) ([9c572bd](https://github.com/mavrovde/viafrei-api/commit/9c572bd182494d24b8103c412c343721443838ce))
* **release:** version sync with the ViaFrei platform, /v1/meta reports the platform version ([#6](https://github.com/mavrovde/viafrei-api/issues/6)) ([#58](https://github.com/mavrovde/viafrei-api/issues/58)) ([15101fd](https://github.com/mavrovde/viafrei-api/commit/15101fddad33bbc3208fed9aa3ee3bb08661308c))
* **roads:** road conditions on a motorway, near a position and by id ([#71](https://github.com/mavrovde/viafrei-api/issues/71)) ([e99fbad](https://github.com/mavrovde/viafrei-api/commit/e99fbadeb10b2bdddfda1b9591825172b7620da1))
* **store:** a circuit breaker in front of storage (NFR-RES-002) ([#84](https://github.com/mavrovde/viafrei-api/issues/84)) ([86abfa9](https://github.com/mavrovde/viafrei-api/commit/86abfa92958f5569d0ebb85ea8503ed1997ae176))
* **store:** share the storage concurrency budget with the other readers of the store ([#69](https://github.com/mavrovde/viafrei-api/issues/69)) ([a5fc447](https://github.com/mavrovde/viafrei-api/commit/a5fc447803daee57a78f0c68cd88ba6a3fa68a6a)), closes [#8](https://github.com/mavrovde/viafrei-api/issues/8)
* **weather,rules:** weather warnings at a position and the driving rules on a topic ([#76](https://github.com/mavrovde/viafrei-api/issues/76)) ([23b3f24](https://github.com/mavrovde/viafrei-api/commit/23b3f24c8f08b86b8c073a16faab8c9d1c222dde))


### Fixed

* **charging:** a reserved charge point is answered occupied, as the MCP capability answers it ([#18](https://github.com/mavrovde/viafrei-api/issues/18)) ([#87](https://github.com/mavrovde/viafrei-api/issues/87)) ([3b4ad4a](https://github.com/mavrovde/viafrei-api/commit/3b4ad4a978917de9c8109ecc172011c42e4c31eb))
* **charging:** a stale or unknown report is no availability, min_kw limits the listed points, and parity is compared at platform 2.6.8 ([#68](https://github.com/mavrovde/viafrei-api/issues/68)) ([a518283](https://github.com/mavrovde/viafrei-api/commit/a518283bb73983102df2757d5702ba5a34080734)), closes [#10](https://github.com/mavrovde/viafrei-api/issues/10)
* **deps:** build with Go 1.27.4, which fixes GO-2026-6859 in net/http ([#72](https://github.com/mavrovde/viafrei-api/issues/72)) ([81f49bc](https://github.com/mavrovde/viafrei-api/commit/81f49bc1837fb1bdfa30d9dcd1e8d891a6bf6637))
* **docs:** the portal loads nothing from a third party (no Google fonts, Redoc served from the site) ([#83](https://github.com/mavrovde/viafrei-api/issues/83)) ([3b12b8c](https://github.com/mavrovde/viafrei-api/commit/3b12b8cc9226a4edc13926818c9517b6a60eecaa))
* **review:** independent review findings on [#64](https://github.com/mavrovde/viafrei-api/issues/64) and [#66](https://github.com/mavrovde/viafrei-api/issues/66) ([#67](https://github.com/mavrovde/viafrei-api/issues/67)) ([627689c](https://github.com/mavrovde/viafrei-api/commit/627689c6c101502cc961f0e548403d62a8ca3140)), closes [#10](https://github.com/mavrovde/viafrei-api/issues/10) [#18](https://github.com/mavrovde/viafrei-api/issues/18)


### Dependencies

* bump github.com/fxamacker/cbor/v2 from 2.9.6 to 2.10.0 ([#135](https://github.com/mavrovde/viafrei-api/issues/135)) ([8cd9f06](https://github.com/mavrovde/viafrei-api/commit/8cd9f06b146c7363a2eb89aff520c17a1c142ca9))
* bump the testcontainers group across 1 directory with 2 updates ([#133](https://github.com/mavrovde/viafrei-api/issues/133)) ([a70ef78](https://github.com/mavrovde/viafrei-api/commit/a70ef786ec281e3320346b1b9dffb57ab2fd020e))
* keep in step with ViaFrei platform 2.6.12 ([#82](https://github.com/mavrovde/viafrei-api/issues/82)) ([dabb42b](https://github.com/mavrovde/viafrei-api/commit/dabb42b4116bb6f500d0cf8c99ab3b9638b51d93))
* keep in step with ViaFrei platform 2.6.13 ([#90](https://github.com/mavrovde/viafrei-api/issues/90)) ([d9f8073](https://github.com/mavrovde/viafrei-api/commit/d9f80734457a5a8d683fe153105f81cc04fe9752))


### Documentation

* developer portal with guide, recorded examples, attribution rules, error codes and versioning policy ([#70](https://github.com/mavrovde/viafrei-api/issues/70)) ([51deafd](https://github.com/mavrovde/viafrei-api/commit/51deafd7faae33ef3ac213c5b7e0d139970d2159))


### Build and CI

* bump docker/login-action from 4.7.0 to 4.8.0 ([#134](https://github.com/mavrovde/viafrei-api/issues/134)) ([c8829fe](https://github.com/mavrovde/viafrei-api/commit/c8829fe774ca0fefa586bab603e18bb86be97d19))
* bump sigstore/cosign-installer from 4.1.2 to 4.1.3 ([#131](https://github.com/mavrovde/viafrei-api/issues/131)) ([e4deb0c](https://github.com/mavrovde/viafrei-api/commit/e4deb0c085eb143cf9e02ee1a4ea0e3a8c080da4))
* fuzz and load run weekly instead of nightly, platform sync weekly instead of daily; Scorecard and Security no longer run on every push to main (owner: GitHub by minimum) ([#97](https://github.com/mavrovde/viafrei-api/issues/97)) ([9d2b8a0](https://github.com/mavrovde/viafrei-api/commit/9d2b8a09ffa2722754aee0e47a9d187eb26f06e0))
* **mutation:** mutation testing of the domain, access and admission code ([#17](https://github.com/mavrovde/viafrei-api/issues/17)) ([#62](https://github.com/mavrovde/viafrei-api/issues/62)) ([20dcbeb](https://github.com/mavrovde/viafrei-api/commit/20dcbeb3f7439f692846469525960f70a9947279))
* **security:** one tool per kind of check ([#12](https://github.com/mavrovde/viafrei-api/issues/12), [#5](https://github.com/mavrovde/viafrei-api/issues/5)) ([#54](https://github.com/mavrovde/viafrei-api/issues/54)) ([1309955](https://github.com/mavrovde/viafrei-api/commit/1309955749ae2f5d3fe272b71f875adf08a70d25))
* Snyk runs weekly on main and once per release, before publish ([#99](https://github.com/mavrovde/viafrei-api/issues/99)) ([9f83e93](https://github.com/mavrovde/viafrei-api/commit/9f83e93a50719038641a10dd8a9c3744fa4588a0))

### In detail

The API serves what the site and the MCP server answer today, from the read
contract of viafrei-database 0.3.3 (schema head 316). Every operation below is new since 0.2; the parity harness
compares each one with the platform's own answers (bundle 2.6.22).

#### Added

* **access:** `getKey` (the presenting key), `listSources` (the sources a key may read: attribution, licence, freshness), `getPressure` (whether the service is saturated now).
* **places:** `listPlaces`, `getPlaceOverview`, `getPlacesCoverage` (with each kind's newest load), `listAddresses`, `listPois`, `listNearby`, `describeLocation` (with the low-emission zone the position lies in).
* **roads:** `listRoadConditions`, `getRoadSituation`, `listMotorways`, `getMotorwayConditions`, `listMotorwayParking`, `listStateRoadConditions`, `listSpeedCameras` (OpenStreetMap's cameras cut by Gemeinde as the platform cuts them), `listLorryRules`, `getDrivingRules`.
* **energy:** `listFuelOptions`, `getFuelStation`, `listChargingOptions`, `getChargingSite`, `listHydrogenOptions`, `listParkingOptions`, `listParkingBays` (reserved and sensor bays near a position), `listSharingOptions`.
* **transit:** `listStations`, `listStationDepartures` (platform changes as the platform states them), `listStationFacilities`, `listTransitDisruptions`, `listAffectedStations` (read from the contract's station disruption), `listStateServiceAlerts`.
* **weather:** `getWeatherRisk`, `listStateWeatherWarnings`.
* **composed:** `getNationalSummary` (Germany now), `getWatch` (a repeated read of a road, a place, a station, a region, the weather or a charger).
* **live:** `createRefresh` and `getRefresh`, for keys of ViaFrei's own audience: a person's view asks for a refresh of one subject of a live source (`fuel-prices-de` by Gemeinde or point, `train-board-de` by EVA number or station name), keyed by the live source registry's key rules (`refresh.sources_file`); a request stores only the public id its key names: a point is resolved to its Gemeinde's AGS in memory, and one no Gemeinde answers is refused with 400 and nothing recorded; requests are kept 30 days after they were asked, in any state, pruned hourly; ids are decimal and an answer states `reused`; concurrent asks about one subject share one request, a request settled inside the source's freshness window answers at once, and the answer waits up to `refresh.wait` (1.2 s) on one listening connection per instance: 201 settled (`refreshed`, `none_due`, `refused`, `failed`) or 202 `pending` with its place in the queue. `getTrainBoard` serves the boards the live refresher stores. `testkit/fakelive` and `cmd/fakelive` settle refreshes by a script for tests and local runs.
* **motorways:** each motorway of `listMotorways` states its lamp, and the catalogue credits the families its index credits.
* **refreshes:** a motorway anchor is stored as `anchor:<BASt LCL code>` (digits only, at most six); `requested_by` names the own service that asked (`site`, `mcp`, or `dev` for any other own key), never anything about the person.

#### Changed

* **database:** tests, the local stack and CI migrate with the signed viafrei-database 0.3.3 image (`database.pin`, by digest); this repository runs no DDL.
* **live:** the on-demand refreshes are proven with the live refresher's release 0.1.0, pinned by digest, which `make live-e2e` runs against the same database release (0.3.3) before a release.
* **sources:** own keys see unreviewed families listed in `listSources`; other keys do not.
* **freshness:** a family with no fixed interval states `staleAfterS` 9007199254740991 (2^53 − 1, an age no data reaches) and `stale` false, also when its `dataAsOf` is null; the field stays a required integer, as in 0.2.
* **tests:** the performance package (`internal/perf`) runs alone (`-p 1`) after the other integration packages, into its own coverage profile; its budgets are unchanged.

#### Fixed

* **places:** the name fold builds its diacritics chain per call; one chain shared by concurrent requests panicked.
* **roads:** a road the register does not hold answers its situations and what was read, never 404; a weather warning is on a motorway when its area holds one of the road's junctions.
* **http:** `/healthz` and `/readyz` stay on the public listener as deprecated aliases.

## [0.2.1](https://github.com/mavrovde/viafrei-api/compare/v0.2.0...v0.2.1) (2026-11-08)


### Fixed

* **release:** create dist/ before the image is built, so the SBOM step can write its file ([#5](https://github.com/mavrovde/viafrei-api/issues/5)) ([#56](https://github.com/mavrovde/viafrei-api/issues/56)) ([b6ac2fa](https://github.com/mavrovde/viafrei-api/commit/b6ac2facbdd3ce9da390ad50d81086a5f39febcd))

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

* PostgreSQL 18 / PostGIS 3.6 for the dev database and the tests ([#38](https://github.com/mavrovde/viafrei-api/issues/38)) ([459c7fb](https://github.com/mavrovde/viafrei-api/commit/459c7fb4efff944e9010660816fa7bc7f4aaa516))
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
