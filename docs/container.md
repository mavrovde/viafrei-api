# Container image and local stack

The design and its reasons are on the wiki page
[Container and Deployment](https://github.com/mavrovde/viafrei-api/wiki/Container-and-Deployment).
This page shows how to use the image.

## The image

| Property | Value |
|---|---|
| Registry | `ghcr.io/mavrovde/viafrei-api` |
| Platforms | `linux/amd64`, `linux/arm64` |
| Base | `gcr.io/distroless/static-debian12:nonroot`, pinned by digest. There is no shell and no package manager. |
| Build | multi-stage: a pinned Go builder, then `CGO_ENABLED=0`, `-trimpath`, and the version and commit stamped in with `-ldflags` |
| User | `nonroot` (65532). The root filesystem can be mounted read-only, because the service writes no file. |
| Ports | 8080, the public listener (`VFAPI_SERVER_LISTEN_ADDR=:8080`), behind the caller allow-list; 8082, the health listener (`VFAPI_HEALTH_LISTEN_ADDR=:8082`): `/healthz`, `/readyz` and `/v1/meta`, never routed by the edge. See [Private preview](#private-preview). |
| Entry point | `/app serve`. `/app healthcheck` exits 0 when `/healthz` on the health listener answers `ok` within `health.probe_timeout`. |
| Secrets | none in the image. The database URL is given at run time as `VFAPI_DB_URL` or `VFAPI_DB_URL_FILE`. |
| Labels | `org.opencontainers.image.source`, `.revision`, `.version`, `.title`, `.description`, `.url`, `.documentation` |

### Tags

Images are published for **confirmed releases only**: an annotated tag
`vX.Y.Z` whose version equals `version.txt`, on a commit CI passed
([RELEASING.md](RELEASING.md)). A push to `main` and a release candidate
publish no image; CI builds, lints, scans and smoke-tests the image of every
commit without pushing it.

| Tag | Set by | Moves |
|---|---|---|
| `X.Y.Z`, for example `0.2.1` | a release | never |
| `X.Y` | a release | to the newest patch of that minor |
| `latest` | a release | to the newest release |

Only confirmed releases are published. Builds of `main` are tested in CI but
never pushed, and the earlier `sha-<12 hex>` and `edge` images were removed.

Deploy by digest (`ghcr.io/mavrovde/viafrei-api@sha256:…`), not by a moving tag.

## Run with Docker

```sh
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  -p "[::1]:8080:8080" -p "[::1]:8082:8082" \
  -e VFAPI_CALLERS_ALLOW_CIDR=0.0.0.0/0,::/0 \
  -e VFAPI_DB_URL_FILE=/run/secrets/db_url -v "$PWD/db_url:/run/secrets/db_url:ro" \
  ghcr.io/mavrovde/viafrei-api:latest
```

The configuration keys are the same as for the binary ([configuration.md](configuration.md)). Every key is set with a `VFAPI_` environment variable. An unknown variable or a value out of bounds stops the container at once with exit status 78.

The owner console (`admin.enabled`) cannot be reached through a published port, and this is by design. It listens on a loopback address only. A published port would need it on `0.0.0.0`, which is refused at boot, and the console's peer would be the bridge gateway, which is refused on every request. To use the console with the container, run it with host networking (`--network host`, Linux), or run a forwarder inside the container's network namespace. The forwarder must accept connections from the host's loopback only (publish its own port, not the console's, on the host's loopback, for example `-p 127.0.0.1:8083:8083`), because the console sees every connection it forwards as a loopback peer. See [The owner console](access-keys.md#the-owner-console).

Resource limits for one instance are design targets, not yet measured: 256 MiB of memory, one CPU and 128 processes. The benchmark plan replaces them with measured values.

## Private preview

The first deployment answers its maintainer alone: the public listener admits
the callers of `callers.allow_cidr` and refuses every other one. Three
settings govern it ([configuration.md](configuration.md)):

| Setting | Meaning |
|---|---|
| `VFAPI_CALLERS_ALLOW_CIDR` | The networks (CIDR, comma-separated) whose callers are answered. **Empty, the default, refuses every caller**: the listener fails closed, and the service logs a warning at boot. `0.0.0.0/0,::/0` admits everyone, an explicit act. |
| `VFAPI_CALLERS_TRUSTED_PROXY_HEADER` | The header in which the edge in front of the service writes the caller's address, such as `X-Real-IP`. Empty: the socket peer is the caller. |
| `VFAPI_CALLERS_TRUSTED_PROXY_CIDR` | The networks of that edge. The header is believed only from a peer inside them; from any other peer it is ignored and the peer's own address is judged, so a forged header gains nothing. Set together with the header. |

When the header is believed, the rightmost address in its last line is the
caller: the one the edge wrote. A value that is no address is refused, never
guessed, and so is a trusted edge that sends no header and is not itself on
the list.

A refused caller gets `403` with an empty body and no header of the service,
on every route, the OpenAPI document and unknown paths included, so a refusal
says nothing about which paths exist. The log line says that a caller was
refused, with the method and path, and never the address.

**Two ports.** Liveness (`/healthz`), readiness (`/readyz`) and the service
metadata (`/v1/meta`) are served on the health listener, `health.listen_addr`,
whose port must differ from the public one. It has no allow-list: the edge
never routes it, so the platform's probes reach it without being on the list.
The public listener still answers `/healthz` and `/readyz` with the same
answer, as deprecated aliases marked with a `Deprecation: true` header; probe
the health listener. The OpenAPI document describes the public listener only. `/app healthcheck` and
`/app meta-check` ask the health listener.

## Deploy to a cluster

What one instance needs, whatever the orchestrator:

| Need | Value |
|---|---|
| Image | `ghcr.io/mavrovde/viafrei-api@sha256:<digest>` of a confirmed release; verify it first ([Verify an image](#verify-an-image)). |
| User | non-root, uid and gid 65532; no privilege escalation; every capability dropped. |
| Root filesystem | read-only. The service writes no file and needs no writable volume. |
| Ports | 8080 to the edge (TLS ends there); 8082 to the probes only, never routed. |
| Probes | liveness `GET :8082/healthz` (never touches storage); readiness `GET :8082/readyz` (storage reachable and the read-contract version accepted). |
| Required settings | `VFAPI_DB_URL` or `VFAPI_DB_URL_FILE`: the read-only role's connection; `VFAPI_CALLERS_ALLOW_CIDR`, and the proxy pair when an edge rewrites the peer. |
| Optional settings | `VFAPI_ACCESS_URL(_FILE)` and `VFAPI_AUTH_PEPPER(_FILE)` for keys; `VFAPI_SERVER_PUBLIC_BASE_URL`; every other key of [configuration.md](configuration.md) has a default. |
| Database role | a login role that may `SELECT` the read contract and nothing else, read-only by default, with a statement timeout and a connection limit at least `db.pool_max`. |
| Resources | see the targets above; shutdown drains within `server.shutdown_grace`, so the termination grace period should exceed it. |

## Verify an image

Release images are signed with cosign (keyless, GitHub OIDC). They also carry an SBOM and a SLSA build-provenance attestation.

```sh
IMAGE=ghcr.io/mavrovde/viafrei-api@sha256:<digest>

# Signature: made by this repository's release workflow for a vX.Y.Z release
# (a release cut from main runs it at refs/heads/main)
cosign verify "$IMAGE" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/mavrovde/viafrei-api/\.github/workflows/release\.yml@refs/(heads/main|tags/v[0-9]+\.[0-9]+\.[0-9]+)$'

# Build provenance (SLSA)
gh attestation verify "oci://$IMAGE" --repo mavrovde/viafrei-api

# SBOM (SPDX), attached to the image by BuildKit
docker buildx imagetools inspect "$IMAGE" --format '{{ json .SBOM }}'
```

Each release page also lists the digest, the tags, these commands and the SBOM as an asset ([RELEASING.md](RELEASING.md)).

## Local development with Compose

`compose.yaml` runs five services:

- **db**: PostgreSQL 16 with PostGIS 3.4 (`postgis/postgis:16-3.4`, the platform's own, pinned by digest). Its database is `test_contract`: it only ever holds synthetic data. It sits on an internal network, publishes no port and keeps its data in the named volume `db16-data`; a volume of an earlier PostgreSQL major is not read (`make reset` removes it).
- **db-migrate**: the viafrei-database release `database.pin` names, run once per start: it migrates the database to the platform's schema, with the read contract, the access store and the settings it gives each role in this database, makes the daily partitions, must report `READY` at the pinned head, and gives the read-only role `api_reader` its password. A database already at the head is left as it is.
- **fixtures**: writes a synthetic fixture set in the read contract's own shape (`tiny`, seed 1, set with `FIXTURE_PROFILE` and `FIXTURE_SEED`) into the volume `seed`.
- **db-seed**: the same viafrei-database release's test seed writes that set into the platform's tables, once; the API reads it back through the contract's views, as it reads production. A database that already holds a set is left as it is (`make reset` starts over).
- **api**: the runtime image. It connects as `api_reader`, runs read-only with every capability dropped, and is published on the IPv6 loopback address only: its public listener on `[::1]:8080`, which admits every caller that reaches it (`VFAPI_CALLERS_ALLOW_CIDR=0.0.0.0/0,::/0`), and its health listener on `[::1]:8082`.

```sh
cp .env.example .env        # replace both placeholders
make up                     # build, start, wait until both are healthy
curl -s localhost:8080/v1/meta
curl -s localhost:8082/readyz   # the health listener
make logs                   # follow the logs
make down                   # stop, keep the database
make reset                  # stop and delete this project's volume; the fixtures reload on the next up
make compose-test           # the CI smoke: healthz, readyz, meta and Schemathesis, then teardown
```

On a host without IPv6 loopback, set `API_PUBLISH` and `API_HEALTH_PUBLISH` in `.env` to the IPv4 loopback address followed by `:8080` and `:8082`. `make up` and `make compose-test` run `scripts/compose-guard.sh` first (SSC-019). The guard does not parse `.env` itself. It asks Compose for the configuration it will run (`docker compose config --format json`), so Compose's own precedence and `.env` syntax apply. It refuses (exit 2) in these cases:

- a published port of any service has a host address other than `::1` or an IPv4 loopback address;
- a published port has no fixed port;
- the api service publishes no port;
- it cannot get an answer: `docker` or `jq` is missing, or `docker compose config` fails or answers something that is not JSON.

The upstream PostGIS image is published for `linux/amd64` only. On an arm64 machine it runs under emulation, which is slower to start.

## Build locally

```sh
make image                  # this machine's platform, loaded as viafrei-api:local
make image-multiarch        # amd64 and arm64, not loaded and not pushed
```
