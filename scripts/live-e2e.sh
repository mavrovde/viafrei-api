#!/usr/bin/env bash
# The pre-release refresh run (make live-e2e): the API built here, the
# viafrei-database release database.pin names and the live refresher
# release the live pin names (internal/release/livepin), together, with a
# provider stub (test/live-e2e/stub.mjs) in place of every provider. It
# proves, from the API's POST /v1/refreshes to the settled answer read back
# with GET /v1/refreshes/{id}:
#
#   1. a fuel refresh of a Gemeinde (ags:<AGS>),
#   2. a fuel refresh of a motorway anchor (a point the API resolves to
#      anchor:<LCL>),
#   3. a train board refresh (eva),
#
# each settled refreshed by the live refresher, with the stub having
# answered its requests. The stack is torn down with its volumes on every
# exit path.
#
# The live refresher's image is private: this runs on a maintainer's machine
# before a release (docs/RELEASING.md), never in public CI.
#
#   LIVE_SOURCES  required: the source configs of the pinned live refresher
#                 release, the sources/ directory of its checkout at that
#                 tag. They are copied, and each copy's provider host is
#                 rewritten to the stub; the run refuses a copy that still
#                 names another host.
#   LIVE_IMAGE    optional: the image to run instead of the pinned one, for
#                 a run before the release exists (a local build).
#   API_URL       optional: the API's loopback address (default
#                 http://localhost:8080).
set -euo pipefail

cd "$(dirname "$0")/.."

API_URL="${API_URL:-http://localhost:8080}"
SETTLE_S="${LIVE_E2E_SETTLE_S:-240}" # a second fuel refresh waits out the source's one request a minute
[[ $API_URL =~ ^http://(localhost|127\.0\.0\.1|\[::1\]):[0-9]+$ ]] || { echo "FAIL: API_URL must be this machine's loopback, not $API_URL" >&2; exit 2; }
[[ -n ${LIVE_SOURCES:-} && -d $LIVE_SOURCES ]] || { echo "FAIL: LIVE_SOURCES must name the source configs of the pinned live refresher release" >&2; exit 2; }
[[ $SETTLE_S =~ ^[1-9][0-9]*$ ]] || { echo "FAIL: LIVE_E2E_SETTLE_S must be a positive count of seconds" >&2; exit 2; }

if [[ -z ${LIVE_IMAGE:-} ]]; then
  # The pinned release, refused while it is unreleased or a placeholder.
  LIVE_IMAGE=$(go run ./internal/release/pinguard -live) || exit 2
fi
export LIVE_IMAGE

work=$(mktemp -d)
export LIVE_E2E_TLS="$work/tls" LIVE_E2E_SOURCES="$work/sources"
mkdir -p "$LIVE_E2E_TLS" "$LIVE_E2E_SOURCES"

# The stub's certificate, for this run alone; the live refresher trusts it
# through NODE_EXTRA_CA_CERTS.
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj "/CN=provider.stub" \
  -addext "subjectAltName=DNS:provider.stub" -keyout "$LIVE_E2E_TLS/key.pem" -out "$LIVE_E2E_TLS/cert.pem" 2>/dev/null
chmod 0644 "$LIVE_E2E_TLS/key.pem" "$LIVE_E2E_TLS/cert.pem"

# Each source config, flat as <id>.yaml, its provider host rewritten to the
# stub: the hosts list and the base URL's host. A copy naming any other host
# stops the run before anything starts.
copied=0
while IFS= read -r -d '' f; do
  id=$(sed -n 's/^id: *//p' "$f")
  [[ $id =~ ^[a-z0-9-]+$ ]] || { echo "FAIL: $f has no id" >&2; exit 2; }
  sed -E -e 's#^hosts: *\[.*\]#hosts: [provider.stub]#' -e 's#^( *base_url: *https://)[^/]+#\1provider.stub#' "$f" > "$LIVE_E2E_SOURCES/$id.yaml"
  if grep -E '^ *(base_url|hosts):' "$LIVE_E2E_SOURCES/$id.yaml" | grep -vq 'provider\.stub'; then
    echo "FAIL: $id.yaml still names a provider host after the rewrite" >&2; exit 2
  fi
  copied=$((copied + 1))
done < <(find "$LIVE_SOURCES" -name '*.yaml' -print0)
[[ $copied -gt 0 ]] || { echo "FAIL: no source config under $LIVE_SOURCES" >&2; exit 2; }

if [[ ! -f .env ]]; then
  export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-$(openssl rand -hex 16)}"
  export API_READER_PASSWORD="${API_READER_PASSWORD:-$(openssl rand -hex 16)}"
fi
export ACCESS_PASSWORD="$(openssl rand -hex 16)" LIVE_PASSWORD="$(openssl rand -hex 16)" AUTH_PEPPER="$(openssl rand -hex 32)"

compose=(docker compose -f compose.yaml -f test/live-e2e/compose.yaml)
fail() { echo "FAIL: $*" >&2; exit 1; }
teardown() {
  local code=$?
  if [[ $code -ne 0 ]]; then "${compose[@]}" logs --no-color --tail 80 live stub api || true; fi
  "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  rm -rf "$work"
  exit "$code"
}
scripts/compose-guard.sh
trap teardown EXIT

# The database at the pinned release, seeded; then the two roles' passwords.
"${compose[@]}" run --rm --build db-seed
psql_owner() { "${compose[@]}" exec -T db psql -X -q -tA -v ON_ERROR_STOP=1 -U owner -d test_contract "$@"; }
psql_owner -v access="$ACCESS_PASSWORD" -v live="$LIVE_PASSWORD" <<'SQL'
alter role api_access login password :'access';
alter role viafrei_live_ingestor login password :'live';
SQL

# An own key of a development plan (recorded as requested by dev).
keys() { "${compose[@]}" run --rm --no-deps api keys "$@"; }
keys plan -name e2e -use-class commercial -tier standard -scopes energy,transit -sources "" -rate 10 -burst 20 -actor e2e >/dev/null
key=$(keys issue -plan e2e -label "live e2e" -contact e2e@example.test -audience own -actor e2e | sed -n 2p | tr -d '\r')
[[ -n $key ]] || fail "no key was issued"

"${compose[@]}" up --detach --build --wait --wait-timeout 300 api stub live

# The subjects, through the read contract: a Gemeinde, a BASt LCL junction
# (the point the API resolves to its anchor) and a station.
ags=$(psql_owner -c "select official_key from contract.place where source_ref = 'bkg_gvisys' and place_kind = 'municipality' and withdrawn_at is null and official_key ~ '^[0-9]{8}$' order by official_key limit 1")
anchor=$(psql_owner -c "select round(st_y(location::geometry)::numeric, 4) || ',' || round(st_x(location::geometry)::numeric, 4) from contract.junction where source_ref = 'bast_lcl' and location is not null order by junction_id limit 1")
eva=$(psql_owner -c "select eva_no from contract.stop_place where withdrawn_at is null and eva_no ~ '^[0-9]{7}$' order by eva_no limit 1")
[[ -n $ags && -n $anchor && -n $eva ]] || fail "the seeded database lacks a subject: ags '$ags', anchor '$anchor', eva '$eva'"

# refresh BODY WANT_SUBJECT: POST it, read it back until it settles, and
# require it refreshed under the subject the request table holds.
checks=0
refresh() {
  local body=$1 want=$2 out code id state subject deadline
  out=$(curl --silent --show-error --max-time 5 -w '\n%{http_code}' -H "Authorization: Bearer $key" \
    -H 'Content-Type: application/json' -d "$body" "$API_URL/v1/refreshes")
  code=${out##*$'\n'}
  out=${out%$'\n'*}
  [[ $code == 201 || $code == 202 ]] || fail "POST $body answered $code: $out"
  id=$(jq -r .data.id <<<"$out")
  state=$(jq -r .data.state <<<"$out")
  deadline=$((SECONDS + SETTLE_S))
  while [[ $state == pending ]]; do
    ((SECONDS < deadline)) || fail "refresh $id ($body) still pending after ${SETTLE_S}s"
    sleep 2
    out=$(curl --silent --show-error --fail-with-body --max-time 5 -H "Authorization: Bearer $key" "$API_URL/v1/refreshes/$id") || fail "GET refresh $id: $out"
    state=$(jq -r .data.state <<<"$out")
  done
  [[ $state == refreshed ]] || fail "refresh $id ($body) settled $state: $out"
  subject=$(psql_owner -c "select subject::text from live.refresh_requests where id = $id")
  [[ $subject =~ $want ]] || fail "refresh $id was recorded for $subject, want $want"
  echo "refresh $id: $body settled refreshed (subject $subject)"
  checks=$((checks + 1))
}

refresh "{\"source\":\"fuel-prices-de\",\"subject\":{\"at\":\"ags:$ags\"}}" "\"ags:$ags\""
refresh "{\"source\":\"fuel-prices-de\",\"subject\":{\"at\":\"pt:$anchor\",\"anchor\":true}}" '"anchor:[0-9]{1,6}"'
refresh "{\"source\":\"train-board-de\",\"subject\":{\"eva\":\"$eva\"}}" "\"$eva\""

# Every answer came from the stub.
log=$("${compose[@]}" logs --no-color stub)
grep -qE 'stub GET /.*/(list|prices)\.php' <<<"$log" || fail "the stub served no fuel request"
grep -qE 'stub GET /.*/plan/[0-9]{7}/' <<<"$log" || fail "the stub served no timetable request"

echo "live e2e: $checks of 3 refreshes settled refreshed (fuel ags:, fuel anchor:, train board) against $LIVE_IMAGE"
