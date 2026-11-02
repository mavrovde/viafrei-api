#!/usr/bin/env bash
# Compose smoke test: build and start the local stack, check /healthz, /readyz
# and /v1/meta, run a short Schemathesis pass against the running API, and
# tear the stack and its volume down on every exit path.
#
# It targets only the API container of this compose project, never a host on
# the internet: Schemathesis gets --url explicitly, because the servers entry
# of the OpenAPI document names production (TST-010).
#
# Passwords come from the environment or .env; when neither sets them, the
# script generates throwaway ones for this run.
set -euo pipefail

cd "$(dirname "$0")/.."

SCHEMATHESIS_IMAGE="schemathesis/schemathesis:4.31.1@sha256:e6d34d7defc8c8a5c7a684255648a43b169cfe0c1e51b2bae7c68c310f3b0ab7"
API_URL="${API_URL:-http://localhost:8080}"
EXAMPLES="${SMOKE_MAX_EXAMPLES:-25}"
PROJECT=viafrei-api

if [[ ! -f .env ]]; then
  export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-$(openssl rand -hex 16)}"
  export API_READER_PASSWORD="${API_READER_PASSWORD:-$(openssl rand -hex 16)}"
fi

checks=0
fail() { echo "FAIL: $*" >&2; exit 1; }
teardown() {
  local code=$?
  if [[ $code -ne 0 ]]; then docker compose logs --no-color --tail 80 || true; fi
  docker compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  exit "$code"
}
scripts/compose-guard.sh

trap teardown EXIT

docker compose up --detach --build --wait --wait-timeout 300

get() { curl --silent --show-error --fail-with-body --max-time 3 "$API_URL$1"; }

body=$(get /healthz) || fail "/healthz: $body"
[[ $(jq -r .status <<<"$body") == ok ]] || fail "/healthz said $body"
checks=$((checks + 1))

body=$(get /readyz) || fail "/readyz: $body"
[[ $(jq -r .status <<<"$body") == ready ]] || fail "/readyz said $body"
checks=$((checks + 1))

body=$(get /v1/meta) || fail "/v1/meta: $body"
want=$(tr -d '[:space:]' < db/contract/VERSION)
got=$(jq -r .data.contractVersion <<<"$body")
[[ $got == "$want" ]] || fail "/v1/meta contractVersion $got, want $want"
[[ $(jq -r .data.apiVersion <<<"$body") != null ]] || fail "/v1/meta has no apiVersion: $body"
checks=$((checks + 1))

docker run --rm --network "${PROJECT}_frontend" "$SCHEMATHESIS_IMAGE" \
  run http://api:8080/v1/openapi.json --url http://api:8080 \
  --checks not_a_server_error,status_code_conformance,content_type_conformance,response_schema_conformance \
  --max-examples "$EXAMPLES" --request-timeout 2.05 --no-color
checks=$((checks + 1))

echo "compose smoke: $checks of 4 checks passed (healthz, readyz, meta, schemathesis with up to $EXAMPLES examples per operation)"
