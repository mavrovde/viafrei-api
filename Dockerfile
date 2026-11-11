# ViaFrei API container image.
#
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     --build-arg VERSION=0.2.0-dev --build-arg COMMIT=$(git rev-parse --short=12 HEAD) .
#
# Stages:
#   build     compiles the static binary on the build platform for the target platform
#   fixtures  writes a synthetic fixture set (development database only)
#   devdb     PostGIS with the read-contract stub, the read-only role and the fixtures
#   runtime   distroless static, non-root: the image that is published (the default target)
#
# Every base image is pinned by digest; Dependabot proposes updates.

FROM --platform=$BUILDPLATFORM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=0.2.0-dev
ARG COMMIT=unknown
ARG BUILD_DATE=""
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/mavrovde/viafrei-api/internal/buildinfo.version=${VERSION} -X github.com/mavrovde/viafrei-api/internal/buildinfo.commit=${COMMIT} -X github.com/mavrovde/viafrei-api/internal/buildinfo.date=${BUILD_DATE}" \
    -o /out/app ./cmd/viafrei-api

FROM build AS fixtures
ARG FIXTURE_PROFILE=tiny
ARG FIXTURE_SEED=1
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go run ./cmd/fixturegen -profile "${FIXTURE_PROFILE}" -seed "${FIXTURE_SEED}" -out /out/fixtures

# Development database only; never published. The upstream image is
# single-architecture (linux/amd64).
FROM postgis/postgis:18-3.6@sha256:4a43e3c3e5d55b9208240fc71db543b79c638766280c625a425bd0a2390989c4 AS devdb
COPY db/contract/0001_contract.sql /docker-entrypoint-initdb.d/20_contract.sql
COPY db/contract/0002_reader_role.sql /docker-entrypoint-initdb.d/21_reader_role.sql
COPY deploy/compose/30_seed.sh /docker-entrypoint-initdb.d/30_seed.sh
COPY db/contract/VERSION /seed/CONTRACT_VERSION
COPY --from=fixtures /out/fixtures/ /seed/

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS runtime
ARG VERSION=0.2.0-dev
ARG COMMIT=unknown
LABEL org.opencontainers.image.title="viafrei-api" \
      org.opencontainers.image.description="ViaFrei API: read-only REST access to German mobility data" \
      org.opencontainers.image.source="https://github.com/mavrovde/viafrei-api" \
      org.opencontainers.image.url="https://github.com/mavrovde/viafrei-api" \
      org.opencontainers.image.documentation="https://github.com/mavrovde/viafrei-api/blob/main/docs/container.md" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
COPY --from=build /out/app /app
USER nonroot:nonroot
ENV VFAPI_SERVER_LISTEN_ADDR=:8080
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/app", "healthcheck"]
ENTRYPOINT ["/app"]
CMD ["serve"]
