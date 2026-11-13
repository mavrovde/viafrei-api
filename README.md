<div align="center">

# ViaFrei API

**Live German mobility data, through one clean REST interface.**

*The German Real-Time Mobility Context Platform for AI*

[Website](https://viafrei.de) · [Documentation](https://mavrovde.github.io/viafrei-api/) · [Wiki](https://github.com/mavrovde/viafrei-api/wiki) · [Roadmap](https://github.com/mavrovde/viafrei-api/wiki/Roadmap) · [MCP server](https://github.com/mavrovde/viafrei-mcp)

![status](https://img.shields.io/badge/status-skeleton-blue)
![api](https://img.shields.io/badge/API-v1%20planned-lightgrey)
![openapi](https://img.shields.io/badge/OpenAPI-3.1-green)

</div>

---

## What it is

ViaFrei API gives applications keyed, read-only access to live mobility information for Germany:

| Domain | What you can ask |
|---|---|
| Roads | Conditions along a motorway or a route, closures, roadworks ahead |
| Energy | Where to refuel or charge near a place or along a route, with current prices and availability |
| Parking | Car and lorry parking near a place, with occupancy where it is published |
| Public transport | Departures and disruptions at a stop or station |
| Accessibility | Lifts and escalators at stations, in service or not |
| Weather risk | Official weather warnings for a place or along a route |
| Places | Search and resolve places and addresses across Germany |

ViaFrei already serves this data to AI assistants through its [MCP server](https://github.com/mavrovde/viafrei-mcp), which is live ([releases](https://github.com/mavrovde/viafrei-mcp/releases)). The API will make the same facts available to any software, through stable, documented HTTP endpoints; it is not deployed yet.

## Design principles

- **Built around what you need, not around storage.** Endpoints follow use cases ("what is disrupted at this station now", "where can I charge along this route"), never the shape of a database.
- **Licences and attribution respected on every response.** Each answer names its publisher and licence, and carries any condition the data's licence requires.
- **Fast or honest.** Every request has a hard deadline of 2 seconds. When the service is busy, it says so with `429` and `Retry-After` instead of making you wait.
- **Secure by default.** API keys are stored only as hashes, scoped per data family, rotatable and revocable at once.
- **Fair under load.** Rate limits, quotas and priority tiers keep the service responsive for everyone.
- **Configurable, not hard-coded.** Every limit, timeout and cache rule is a documented setting.
- **Open process.** Design, decisions, code review, CI and releases happen in public, in this repository.

## Planned API

```http
GET /v1/route-briefing?from=Hamburg&to=Hannover
Authorization: Bearer <your key>
```

```jsonc
{
  "route": { "from": "Hamburg", "to": "Hannover" },
  "roads": { "closures": [ ... ], "roadworks": [ ... ] },
  "energy": { "fuel": [ ... ], "charging": [ ... ] },
  "weather": { "warnings": [ ... ] },
  "attribution": [ { "publisher": "...", "licence": "..." } ]
}
```

*Illustrative only: the v1 endpoint list is being designed in the open. See [API Design](https://github.com/mavrovde/viafrei-api/wiki/API-Design).*

Standards we build on: OpenAPI 3.1, RFC 9457 problem details for errors, cursor pagination, GeoJSON for geometry, ETag caching.

## Status

ViaFrei API has a **service skeleton** in Go with its first data endpoints. The API is not deployed yet; the [MCP server](https://github.com/mavrovde/viafrei-mcp) is live ([releases](https://github.com/mavrovde/viafrei-mcp/releases)).

| Step | State |
|---|---|
| Goals, principles and domain model | ✅ Written |
| Technology evaluation | ✅ Published in the [wiki](https://github.com/mavrovde/viafrei-api/wiki/Technology-Evaluation) |
| Technical specification | 📝 Draft for review, revision 1 — [published in the wiki](https://github.com/mavrovde/viafrei-api/wiki/Technical-Specification): protocol, requests and responses, errors, keys, limits, non-functional requirements |
| Logical data model | 📝 Draft — [Data Model](https://github.com/mavrovde/viafrei-api/wiki/Data-Model): entities, fields and diagrams per domain, mapped to API fields (never a copy of storage) |
| Testing strategy and test plans | 📝 Draft — [Testing Strategy](https://github.com/mavrovde/viafrei-api/wiki/Testing-Strategy); unit, handler and integration tests run, 100 % statement coverage per package is enforced ([coverage](docs/coverage.md)); test infrastructure continues in #14–#20 |
| Synthetic fixture generator | 🔄 In review — seeded, Germany-wide, fictional ([docs/test-data.md](docs/test-data.md)) |
| Architecture for Go, Rust and TypeScript | ✅ [Stack Comparison](https://github.com/mavrovde/viafrei-api/wiki/Stack-Comparison) |
| Stack decision | ✅ **Go** — [ADR-0001](https://github.com/mavrovde/viafrei-api/wiki/Spec-Architecture-Decisions) accepted on 2026-11-01 |
| Configuration layer | ✅ Every tunable is a documented key with bounds — [docs/configuration.md](docs/configuration.md) |
| Service skeleton: `/healthz`, `/readyz`, `/v1/meta`, `/v1/openapi.json` | 🔄 In review (milestone 0.2.0) |
| Container image (distroless, multi-arch) and local Compose stack | 🔄 In review — [docs/container.md](docs/container.md) |
| CI, security checks and release pipeline | 🔄 In review — CI on every pull request; SAST, secrets, dependency, licence and DAST checks ([docs/security-checks.md](docs/security-checks.md)); release-please, signed images and release pages ([docs/RELEASING.md](docs/RELEASING.md)) |
| Keys and the licence gate: hashed keys, scopes, rotation, revocation, audit log, `GET /v1/key` | 🔄 In review (milestone 0.4.0) — [docs/access-keys.md](docs/access-keys.md) |
| Rate limits, quotas, priority tiers, deadlines, load shedding, usage metering | 🔄 In review (milestone 0.5.0) — [docs/rate-limits.md](docs/rate-limits.md) |
| Developer documentation: guide, endpoints with recorded examples, attribution rules, error codes, versioning policy | 🔄 In review — [documentation portal](https://mavrovde.github.io/viafrei-api/), built from [docs/](docs/getting-started.md) |
| v1 feature endpoints | 🔄 In progress (milestone 0.6.0) — first slice: `GET /v1/charging-options`, `GET /v1/parking-options`, `GET /v1/places/{placeId}/overview`; road conditions: `GET /v1/motorways/{road}/conditions`, `GET /v1/road-conditions`, `GET /v1/road-situations/{situationId}`; weather and rules: `GET /v1/weather-risk`, `GET /v1/driving-rules` |

Follow progress in the [Roadmap](https://github.com/mavrovde/viafrei-api/wiki/Roadmap) and in [Issues](https://github.com/mavrovde/viafrei-api/issues).

## Build and run

Requirements: Go 1.27 (the exact toolchain is pinned in `go.mod`), and a container runtime for the integration tests.

```sh
make build              # bin/viafrei-api, static, version and commit stamped in
make test               # unit and handler tests
make test-integration   # adds tests against a throwaway PostGIS container
make coverage           # unit + integration + boot profiles, 100 % per package or it fails
make generate           # regenerate sqlc code, docs/configuration.md, api/openapi.json
make fixtures PROFILE=ci SEED=7   # synthetic test data, see docs/test-data.md
```

The service reads its configuration from environment variables with the prefix `VFAPI_` and, optionally, a YAML file named by `VFAPI_CONFIG_FILE`. Every key, its default and its bounds are listed in [docs/configuration.md](docs/configuration.md). An unknown key or a value out of bounds stops the service at start-up with exit status 78. The one required value is the database URL of the service's read-only role:

```sh
export VFAPI_DB_URL_FILE=/run/secrets/db_url   # or VFAPI_DB_URL=postgres://...
bin/viafrei-api config check                    # validate and exit
bin/viafrei-api                                 # serve on localhost:8080
curl -s localhost:8080/healthz                  # {"status":"ok"}
curl -s localhost:8080/v1/meta                  # API, build, read-contract and platform versions
```

Other subcommands: `healthcheck` (exit 0 when `/healthz` answers, for container healthchecks), `meta-check` (compare the running service's versions with the expected platform and read-contract versions, [releasing](docs/RELEASING.md#version-sync-with-the-platform)), `config show` (effective values, secrets hidden), `config docs`, `openapi`, `keys` (the operator's key tool, [docs/access-keys.md](docs/access-keys.md)), `version`.

The database the service reads is described by a versioned read contract in [`db/contract/`](db/contract): logical names only, created with a read-only role. Tests build it in a throwaway container. They never reach a live data source or production.

## Run with Docker

The image is distroless, runs as non-root and is built for `linux/amd64` and `linux/arm64`. Confirmed releases, and only those, are published to `ghcr.io/mavrovde/viafrei-api` as `:X.Y.Z`, `:X.Y` and `:latest`, signed with cosign, with an SBOM and build provenance.

```sh
docker run --rm --read-only -p "[::1]:8080:8080" \
  -e VFAPI_DB_URL_FILE=/run/secrets/db_url -v "$PWD/db_url:/run/secrets/db_url:ro" \
  ghcr.io/mavrovde/viafrei-api:latest
```

[docs/container.md](docs/container.md) covers the tags, the `healthcheck` subcommand, resource limits and how to verify an image's signature, SBOM and provenance before running it.

## Local development with Compose

`compose.yaml` starts the API and a PostGIS database that holds the read-contract stub, the read-only role and a synthetic fixture set. Only the API is published, and only on the loopback interface.

```sh
cp .env.example .env    # replace both placeholders
make up                 # build, start, wait until healthy
curl -s localhost:8080/v1/meta
make compose-test       # smoke test with Schemathesis, then teardown
make down               # or: make reset to drop the database volume
```

## Documentation

The **developer documentation** is published with every release at [mavrovde.github.io/viafrei-api](https://mavrovde.github.io/viafrei-api/) (`make docs` builds it locally into `site/`):

- [Getting started](docs/getting-started.md): from nothing to a first answer
- [Endpoints](https://mavrovde.github.io/viafrei-api/endpoints.html): every operation with a request, the recorded answer and its attribution rule, generated from [`api/openapi.json`](api/openapi.json); the full [API reference](https://mavrovde.github.io/viafrei-api/reference.html)
- [Attribution and licence rules](docs/attribution.md) · [Error codes](docs/errors.md) · [Versioning and deprecation policy](docs/versioning.md) · [Rate limits](docs/rate-limits.md) · [API keys](docs/access-keys.md)
- [Examples](docs/examples.md): how every example is recorded from synthetic fixtures and checked in CI
- [Accessibility](docs/accessibility.md): the portal's WCAG 2.2 AA, link and diagram checks, and the manual pass per minor release

The [wiki](https://github.com/mavrovde/viafrei-api/wiki) holds the full design:

- [Vision and Goals](https://github.com/mavrovde/viafrei-api/wiki/Vision-and-Goals) · [Principles](https://github.com/mavrovde/viafrei-api/wiki/Principles)
- [Domain Model](https://github.com/mavrovde/viafrei-api/wiki/Domain-Model) · [API Design](https://github.com/mavrovde/viafrei-api/wiki/API-Design) · [Architecture Overview](https://github.com/mavrovde/viafrei-api/wiki/Architecture-Overview)
- [Security and Access Keys](https://github.com/mavrovde/viafrei-api/wiki/Security-and-Access-Keys) · [Rate Limits and Priority](https://github.com/mavrovde/viafrei-api/wiki/Rate-Limits-and-Priority)
- [Technology Evaluation](https://github.com/mavrovde/viafrei-api/wiki/Technology-Evaluation) · [Stack Comparison](https://github.com/mavrovde/viafrei-api/wiki/Stack-Comparison) · [Benchmark Plan](https://github.com/mavrovde/viafrei-api/wiki/Benchmark-Plan)

**Technical specification** ([index](https://github.com/mavrovde/viafrei-api/wiki/Technical-Specification)), with numbered requirements traced in the [Traceability Matrix](https://github.com/mavrovde/viafrei-api/wiki/Spec-Traceability-Matrix):

- Interface: [Protocol](https://github.com/mavrovde/viafrei-api/wiki/Spec-Protocol) · [Common Envelope](https://github.com/mavrovde/viafrei-api/wiki/Spec-Common-Envelope) · [Resource Model](https://github.com/mavrovde/viafrei-api/wiki/Spec-Resource-Model) · [Errors](https://github.com/mavrovde/viafrei-api/wiki/Spec-Errors) · [Authentication](https://github.com/mavrovde/viafrei-api/wiki/Spec-Authentication-and-Authorisation) · [Rate Limiting](https://github.com/mavrovde/viafrei-api/wiki/Spec-Rate-Limiting-and-Quotas) · [Licensing and Attribution](https://github.com/mavrovde/viafrei-api/wiki/Spec-Data-Licensing-and-Attribution) · [Versioning](https://github.com/mavrovde/viafrei-api/wiki/Spec-Compatibility-and-Versioning) · [OpenAPI Skeleton](https://github.com/mavrovde/viafrei-api/wiki/Spec-OpenAPI-Skeleton)
- Quality: [Non-Functional Requirements](https://github.com/mavrovde/viafrei-api/wiki/Spec-Non-Functional-Requirements) · [Quality Scenarios](https://github.com/mavrovde/viafrei-api/wiki/Spec-Quality-Scenarios) · [Threat Model](https://github.com/mavrovde/viafrei-api/wiki/Spec-Threat-Model)
- Data model: [Overview](https://github.com/mavrovde/viafrei-api/wiki/Data-Model) · [Data Dictionary](https://github.com/mavrovde/viafrei-api/wiki/Data-Dictionary) · [API ↔ data mapping](https://github.com/mavrovde/viafrei-api/wiki/Data-Model-Mapping)
- Testing: [Testing Strategy](https://github.com/mavrovde/viafrei-api/wiki/Testing-Strategy) (ISO/IEC/IEEE 29119-3) · [Test Plan](https://github.com/mavrovde/viafrei-api/wiki/Test-Plan) per milestone · [Quality Gates](https://github.com/mavrovde/viafrei-api/wiki/Test-Environments-and-Quality-Gates) · [Security Test Plan](https://github.com/mavrovde/viafrei-api/wiki/Security-Test-Plan) · [Performance Test Plan](https://github.com/mavrovde/viafrei-api/wiki/Performance-Test-Plan); tests never touch a live data source or production; CI maps every test to the requirements it verifies ([docs/requirements.md](docs/requirements.md))
- Delivery: [Architecture Decisions](https://github.com/mavrovde/viafrei-api/wiki/Spec-Architecture-Decisions) · [Secure SDLC and Supply Chain](https://github.com/mavrovde/viafrei-api/wiki/Spec-Secure-SDLC-and-Supply-Chain) · [Test Strategy](https://github.com/mavrovde/viafrei-api/wiki/Spec-Test-Strategy) · [CI/CD and Release](https://github.com/mavrovde/viafrei-api/wiki/Spec-CI-CD-and-Release) · [Operations](https://github.com/mavrovde/viafrei-api/wiki/Spec-Operations) · [Developer Experience](https://github.com/mavrovde/viafrei-api/wiki/Spec-Developer-Experience)

## Releases

Releases are cut by [release-please](https://github.com/googleapis/release-please) from Conventional Commits: merging the release pull request tags `vX.Y.Z` (annotated) and publishes the release page with the signed multi-arch image `ghcr.io/mavrovde/viafrei-api:X.Y.Z`, its SBOM and provenance, `openapi.json` with its diff against the previous release, and checksums. Every 0.x release is a pre-release. The [CHANGELOG](CHANGELOG.md) lists the changes; [docs/RELEASING.md](docs/RELEASING.md) is the maintainer checklist, including rollback, hotfixes and yanking.

## Security

Please do not report security issues in public issues. Use GitHub's [private vulnerability reporting](https://github.com/mavrovde/viafrei-api/security/advisories/new). See [SECURITY.md](SECURITY.md) for response times; the automated checks are listed in [docs/security-checks.md](docs/security-checks.md).

## Contributing

Ideas and questions are welcome in [Issues](https://github.com/mavrovde/viafrei-api/issues). Code contributions are welcome through pull requests; the process (pull requests, required checks, review) is described in [Development Process](https://github.com/mavrovde/viafrei-api/wiki/Development-Process).

## Related

- [viafrei.de](https://viafrei.de): the live site
- [viafrei-mcp](https://github.com/mavrovde/viafrei-mcp): connect ViaFrei to AI assistants through the Model Context Protocol; live ([releases](https://github.com/mavrovde/viafrei-mcp/releases))

---

<div align="center"><sub>ViaFrei is in beta. Sources are still being added, and coverage can change.</sub></div>
