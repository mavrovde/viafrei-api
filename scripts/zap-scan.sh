#!/usr/bin/env bash
# DAST: start the local compose stack and run the OWASP ZAP API scan against
# the API container, driven by the service's own OpenAPI document. Only the
# API container of this compose project is scanned (TST-010): ZAP reaches it
# by its compose name on the project network, and -O pins the target so the
# production servers entry of the document is never followed.
#
# A rule at FAIL level fails the run; WARN-level alerts are listed in the
# report and do not fail it. Per-rule decisions live in scripts/zap-rules.tsv.
# The HTML and JSON reports are written to ./zap-report/.
set -euo pipefail

cd "$(dirname "$0")/.."

ZAP_IMAGE="zaproxy/zap-stable:20261016@sha256:5ccfff9a411743ab2615136a2edd7416bec9ce41572fa94e935df38c14adb2b6"
PROJECT=viafrei-api

if [[ ! -f .env ]]; then
  export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-$(openssl rand -hex 16)}"
  export API_READER_PASSWORD="${API_READER_PASSWORD:-$(openssl rand -hex 16)}"
fi

# shellcheck disable=SC2317,SC2329 # run by the EXIT trap (SC2317 before shellcheck 0.10, SC2329 after)
teardown() {
  local code=$?
  if [[ $code -ne 0 ]]; then docker compose logs --no-color --tail 80 || true; fi
  [[ -n "${zap_cid:-}" ]] && docker rm --force "$zap_cid" >/dev/null 2>&1 || true
  docker compose down --volumes --remove-orphans >/dev/null 2>&1 || true
  exit "$code"
}
trap teardown EXIT

docker compose up --detach --build --wait --wait-timeout 300

# No host directory is shared with the ZAP container (its user is not the
# runner's, and a world-writable mount is not an answer): the rules go in on
# stdin, the reports are copied out of the stopped container.
rm -rf zap-report
mkdir -p zap-report
zap_cid="${PROJECT}-zap-$$"

set +e
docker run --interactive --name "$zap_cid" --network "${PROJECT}_frontend" --entrypoint sh "$ZAP_IMAGE" -c \
  'mkdir -p /zap/wrk && cat >/zap/wrk/rules.tsv && exec zap-api-scan.py -t http://api:8080/v1/openapi.json -f openapi -O http://api:8080 -c rules.tsv -r zap.html -J zap.json -I' \
  <scripts/zap-rules.tsv
code=$?
set -e
docker cp "$zap_cid:/zap/wrk/." zap-report/ || true

[[ -s zap-report/zap.json ]] || { echo "FAIL: ZAP wrote no report (exit $code)" >&2; exit 1; }
alerts=$(jq '[.site[].alerts[]] | length' zap-report/zap.json)
echo "zap: exit $code, $alerts alert type(s); report in zap-report/"
# zap-api-scan exits 1 when a FAIL-level rule fired (-I ignores warnings) and
# 3 on an internal error; both fail the run.
exit "$code"
