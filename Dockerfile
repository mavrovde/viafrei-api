# ViaFrei API container image.
#
#   docker buildx build --platform linux/amd64,linux/arm64 \
#     --build-arg VERSION=0.3.0-dev --build-arg COMMIT=$(git rev-parse --short=12 HEAD) .
#
# Stages:
#   build     compiles the static binary on the build platform for the target platform
#   fixtures  writes a synthetic fixture set in the read contract's shape (development
#             stack only; compose hands it to the database.pin release's test seed)
#   runtime   distroless static, non-root: the image that is published (the default target)
#
# Every base image is pinned by digest; Dependabot proposes updates.

FROM --platform=$BUILDPLATFORM golang:1.27.4-bookworm@sha256:4fa7c968ba93f1635670522dbb01a2c78150443e1385746924566079f36570ea AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=0.3.0-dev
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

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS runtime
ARG VERSION=0.3.0-dev
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
# The public listener, behind the caller allow-list (callers.allow_cidr,
# empty by default: every caller is refused until it is set), and the health
# listener (liveness, readiness, metadata), which the edge never routes.
ENV VFAPI_SERVER_LISTEN_ADDR=:8080 \
    VFAPI_HEALTH_LISTEN_ADDR=:8082
EXPOSE 8080 8082
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/app", "healthcheck"]
ENTRYPOINT ["/app"]
CMD ["serve"]
