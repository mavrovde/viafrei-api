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
| Port | 8080 (`VFAPI_SERVER_LISTEN_ADDR=:8080`) |
| Entry point | `/app serve`. `/app healthcheck` exits 0 when `/healthz` answers `ok` within `health.probe_timeout`. |
| Secrets | none in the image. The database URL is given at run time as `VFAPI_DB_URL` or `VFAPI_DB_URL_FILE`. |
| Labels | `org.opencontainers.image.source`, `.revision`, `.version`, `.title`, `.description`, `.url`, `.documentation` |

### Tags

| Tag | Set by | Moves |
|---|---|---|
| `X.Y.Z`, for example `0.2.0` | a release tag `vX.Y.Z` | never |
| `X.Y` | a release tag | to the newest patch of that minor |
| `latest` | a release tag, but not a release candidate | to the newest release |
| `X.Y.Z-rc.N` | a release-candidate tag | never |
| `sha-<12 hex>` | every push to `main` once CI passed on it, and every release | never |
| `edge` | a push to `main` once CI passed on it (`image.yml` follows the CI run) | to the newest `main` only, never back to an older commit |

Deploy by digest (`ghcr.io/mavrovde/viafrei-api@sha256:…`), not by a moving tag.

## Run with Docker

```sh
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  -p "[::1]:8080:8080" \
  -e VFAPI_DB_URL_FILE=/run/secrets/db_url -v "$PWD/db_url:/run/secrets/db_url:ro" \
  ghcr.io/mavrovde/viafrei-api:edge
```

The configuration keys are the same as for the binary ([configuration.md](configuration.md)). Every key is set with a `VFAPI_` environment variable. An unknown variable or a value out of bounds stops the container at once with exit status 78.

Resource limits for one instance are design targets, not yet measured: 256 MiB of memory, one CPU and 128 processes. The benchmark plan replaces them with measured values.

## Verify an image

Images built from `main` and from release tags are signed with cosign (keyless, GitHub OIDC). They also carry an SBOM and a SLSA build-provenance attestation.

```sh
IMAGE=ghcr.io/mavrovde/viafrei-api@sha256:<digest>

# Signature: made by this repository's workflows, for main or a v* tag
cosign verify "$IMAGE" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/mavrovde/viafrei-api/\.github/workflows/(image|release)\.yml@refs/(heads/main|tags/v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?)$'

# Build provenance (SLSA)
gh attestation verify "oci://$IMAGE" --repo mavrovde/viafrei-api

# SBOM (SPDX), attached to the image by BuildKit
docker buildx imagetools inspect "$IMAGE" --format '{{ json .SBOM }}'
```

Each release page also lists the digest, the tags, these commands and the SBOM as an asset ([RELEASING.md](RELEASING.md)).

## Local development with Compose

`compose.yaml` runs two services:

- **db**: PostGIS 16 with PostGIS 3.4, built from the `devdb` stage. On first start it builds the read-contract stub (`db/contract`), creates the read-only role `api_reader`, publishes the contract version and loads a synthetic fixture set (`tiny`, seed 1, set with `FIXTURE_PROFILE` and `FIXTURE_SEED`). It sits on an internal network, publishes no port and keeps its data in the named volume `db-data`.
- **api**: the runtime image. It connects as `api_reader`, runs read-only with every capability dropped, and is published on the IPv6 loopback address `[::1]:8080` only.

```sh
cp .env.example .env        # replace both placeholders
make up                     # build, start, wait until both are healthy
curl -s localhost:8080/v1/meta
make logs                   # follow the logs
make down                   # stop, keep the database
make reset                  # stop and delete this project's volume; the fixtures reload on the next up
make compose-test           # the CI smoke: healthz, readyz, meta and Schemathesis, then teardown
```

On a host without IPv6 loopback, set `API_PUBLISH` in `.env` to the IPv4 loopback address followed by `:8080`.

The upstream PostGIS image is published for `linux/amd64` only. On an arm64 machine it runs under emulation, which is slower to start.

## Build locally

```sh
make image                  # this machine's platform, loaded as viafrei-api:local
make image-multiarch        # amd64 and arm64, not loaded and not pushed
```
