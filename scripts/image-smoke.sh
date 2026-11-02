#!/usr/bin/env bash
# image-smoke.sh IMAGE
#
# Runs IMAGE the way production does (read-only root, no capabilities,
# non-root) with a database URL that resolves nowhere, and checks that
#   - /healthz answers ok (it never touches storage),
#   - /readyz answers 503 not_ready (storage is unreachable),
#   - /v1/meta reports the version the image was built with,
#   - the image's own HEALTHCHECK turns healthy,
#   - the process runs as uid 65532.
# The container is removed on every exit path.
set -euo pipefail

image=${1:?usage: image-smoke.sh IMAGE}
name=vfapi-smoke-$$
port=${SMOKE_PORT:-18080}
# A throwaway password for a database that does not exist; never a real one.
pass=$(openssl rand -hex 12)
checks=0

cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; }
trap cleanup EXIT

docker run -d --name "$name" --read-only --cap-drop ALL --security-opt no-new-privileges \
  -p "$port:8080" \
  -e VFAPI_DB_URL=postgres://api_reader:$pass@db.invalid:5432/contract \
  "$image" >/dev/null

url="http://localhost:$port"
for _ in $(seq 1 50); do
  curl -fsS --max-time 1 "$url/healthz" >/dev/null 2>&1 && break
  sleep 0.2
done

[[ $(curl -fsS --max-time 3 "$url/healthz" | jq -r .status) == ok ]] || { docker logs "$name"; echo "FAIL: /healthz" >&2; exit 1; }
checks=$((checks + 1))

code=$(curl -s -o /tmp/readyz.$$ -w '%{http_code}' --max-time 3 "$url/readyz")
[[ $code == 503 && $(jq -r .status /tmp/readyz.$$) == not_ready ]] || { echo "FAIL: /readyz answered $code" >&2; exit 1; }
rm -f /tmp/readyz.$$
checks=$((checks + 1))

meta=$(curl -fsS --max-time 3 "$url/v1/meta")
want=$(docker image inspect "$image" --format '{{ index .Config.Labels "org.opencontainers.image.version" }}')
[[ $(jq -r .data.apiVersion <<<"$meta") == "$want" ]] || { echo "FAIL: /v1/meta apiVersion is not $want: $meta" >&2; exit 1; }
checks=$((checks + 1))

uid=$(docker inspect "$name" --format '{{ .Config.User }}')
[[ $uid == nonroot:nonroot || $uid == 65532* ]] || { echo "FAIL: runs as '$uid'" >&2; exit 1; }
checks=$((checks + 1))

for _ in $(seq 1 30); do
  state=$(docker inspect "$name" --format '{{ .State.Health.Status }}')
  [[ $state == healthy ]] && break
  sleep 1
done
[[ $state == healthy ]] || { echo "FAIL: HEALTHCHECK state is $state" >&2; exit 1; }
checks=$((checks + 1))

echo "image smoke: $checks of 5 checks passed for $image"
