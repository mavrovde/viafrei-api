<div align="center">

# ViaFrei API

**Live German mobility data, through one clean REST interface.**

*The German Real-Time Mobility Context Platform for AI*

[Website](https://viafrei.de) · [Wiki](https://github.com/mavrovde/viafrei-api/wiki) · [Roadmap](https://github.com/mavrovde/viafrei-api/wiki/Roadmap) · [MCP server](https://github.com/mavrovde/viafrei-mcp)

![status](https://img.shields.io/badge/status-design%20phase-blue)
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

ViaFrei already serves this data to AI assistants through its [MCP server](https://github.com/mavrovde/viafrei-mcp). The API makes the same facts available to any software, through stable, documented HTTP endpoints.

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

ViaFrei API is in its **design phase**. Nothing is deployed yet.

| Step | State |
|---|---|
| Goals, principles and domain model | ✅ Written |
| Technology evaluation | ✅ Published in the [wiki](https://github.com/mavrovde/viafrei-api/wiki/Technology-Evaluation) |
| Technical specification | 📝 Draft for review, revision 1 — [published in the wiki](https://github.com/mavrovde/viafrei-api/wiki/Technical-Specification): protocol, requests and responses, errors, keys, limits, non-functional requirements |
| Logical data model | 📝 Draft — [Data Model](https://github.com/mavrovde/viafrei-api/wiki/Data-Model): entities, fields and diagrams per domain, mapped to API fields (never a copy of storage) |
| Testing strategy and test plans | 📝 Draft — [Testing Strategy](https://github.com/mavrovde/viafrei-api/wiki/Testing-Strategy); test infrastructure planned in #13–#20, no test has run yet |
| Architecture for Go, Rust and TypeScript | ✅ [Stack Comparison](https://github.com/mavrovde/viafrei-api/wiki/Stack-Comparison) |
| Stack decision: Go, Rust or TypeScript | 🔄 Benchmark planned ([ADR-0001](https://github.com/mavrovde/viafrei-api/wiki/Spec-Architecture-Decisions), proposed) |
| Project skeleton, CI and release pipeline | ⏳ Next |
| Keys, rate limits and priority tiers | ⏳ Planned |
| v1 feature endpoints | ⏳ Planned |

Follow progress in the [Roadmap](https://github.com/mavrovde/viafrei-api/wiki/Roadmap) and in [Issues](https://github.com/mavrovde/viafrei-api/issues).

## Documentation

The [wiki](https://github.com/mavrovde/viafrei-api/wiki) holds the full design:

- [Vision and Goals](https://github.com/mavrovde/viafrei-api/wiki/Vision-and-Goals) · [Principles](https://github.com/mavrovde/viafrei-api/wiki/Principles)
- [Domain Model](https://github.com/mavrovde/viafrei-api/wiki/Domain-Model) · [API Design](https://github.com/mavrovde/viafrei-api/wiki/API-Design) · [Architecture Overview](https://github.com/mavrovde/viafrei-api/wiki/Architecture-Overview)
- [Security and Access Keys](https://github.com/mavrovde/viafrei-api/wiki/Security-and-Access-Keys) · [Rate Limits and Priority](https://github.com/mavrovde/viafrei-api/wiki/Rate-Limits-and-Priority)
- [Technology Evaluation](https://github.com/mavrovde/viafrei-api/wiki/Technology-Evaluation) · [Stack Comparison](https://github.com/mavrovde/viafrei-api/wiki/Stack-Comparison) · [Benchmark Plan](https://github.com/mavrovde/viafrei-api/wiki/Benchmark-Plan)

**Technical specification** ([index](https://github.com/mavrovde/viafrei-api/wiki/Technical-Specification)), with numbered requirements traced in the [Traceability Matrix](https://github.com/mavrovde/viafrei-api/wiki/Spec-Traceability-Matrix):

- Interface: [Protocol](https://github.com/mavrovde/viafrei-api/wiki/Spec-Protocol) · [Common Envelope](https://github.com/mavrovde/viafrei-api/wiki/Spec-Common-Envelope) · [Resource Model](https://github.com/mavrovde/viafrei-api/wiki/Spec-Resource-Model) · [Errors](https://github.com/mavrovde/viafrei-api/wiki/Spec-Errors) · [Authentication](https://github.com/mavrovde/viafrei-api/wiki/Spec-Authentication-and-Authorisation) · [Rate Limiting](https://github.com/mavrovde/viafrei-api/wiki/Spec-Rate-Limiting-and-Quotas) · [Licensing and Attribution](https://github.com/mavrovde/viafrei-api/wiki/Spec-Data-Licensing-and-Attribution) · [Versioning](https://github.com/mavrovde/viafrei-api/wiki/Spec-Compatibility-and-Versioning) · [OpenAPI Skeleton](https://github.com/mavrovde/viafrei-api/wiki/Spec-OpenAPI-Skeleton)
- Quality: [Non-Functional Requirements](https://github.com/mavrovde/viafrei-api/wiki/Spec-Non-Functional-Requirements) · [Quality Scenarios](https://github.com/mavrovde/viafrei-api/wiki/Spec-Quality-Scenarios) · [Threat Model](https://github.com/mavrovde/viafrei-api/wiki/Spec-Threat-Model)
- Data model: [Overview](https://github.com/mavrovde/viafrei-api/wiki/Data-Model) · [Data Dictionary](https://github.com/mavrovde/viafrei-api/wiki/Data-Dictionary) · [API ↔ data mapping](https://github.com/mavrovde/viafrei-api/wiki/Data-Model-Mapping)
- Testing: [Testing Strategy](https://github.com/mavrovde/viafrei-api/wiki/Testing-Strategy) (ISO/IEC/IEEE 29119-3) · [Test Plan](https://github.com/mavrovde/viafrei-api/wiki/Test-Plan) per milestone · [Quality Gates](https://github.com/mavrovde/viafrei-api/wiki/Test-Environments-and-Quality-Gates) · [Security Test Plan](https://github.com/mavrovde/viafrei-api/wiki/Security-Test-Plan) · [Performance Test Plan](https://github.com/mavrovde/viafrei-api/wiki/Performance-Test-Plan); tests never touch a live data source or production
- Delivery: [Architecture Decisions](https://github.com/mavrovde/viafrei-api/wiki/Spec-Architecture-Decisions) · [Secure SDLC and Supply Chain](https://github.com/mavrovde/viafrei-api/wiki/Spec-Secure-SDLC-and-Supply-Chain) · [Test Strategy](https://github.com/mavrovde/viafrei-api/wiki/Spec-Test-Strategy) · [CI/CD and Release](https://github.com/mavrovde/viafrei-api/wiki/Spec-CI-CD-and-Release) · [Operations](https://github.com/mavrovde/viafrei-api/wiki/Spec-Operations) · [Developer Experience](https://github.com/mavrovde/viafrei-api/wiki/Spec-Developer-Experience)

## Security

Please do not report security issues in public issues. Use GitHub's [private vulnerability reporting](https://github.com/mavrovde/viafrei-api/security/advisories/new).

## Contributing

Ideas and questions are welcome in [Issues](https://github.com/mavrovde/viafrei-api/issues). Code contributions open once the project skeleton is in place; the process (pull requests, required checks, review) is described in [Development Process](https://github.com/mavrovde/viafrei-api/wiki/Development-Process).

## Related

- [viafrei.de](https://viafrei.de): the live site
- [viafrei-mcp](https://github.com/mavrovde/viafrei-mcp): connect ViaFrei to AI assistants through the Model Context Protocol

---

<div align="center"><sub>ViaFrei is in beta. Sources are still being added, and coverage can change.</sub></div>
