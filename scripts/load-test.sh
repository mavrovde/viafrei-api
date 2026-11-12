#!/usr/bin/env bash
# load-test.sh PROFILE [REPORT]
#
# The soak, stress, spike and fault-injection scenarios on the test stack
# (TST-007, issue #19; docs/load-tests.md). PROFILE is pr (a few minutes,
# every pull request) or nightly (the long run of load-nightly.yml).
#
# It starts compose.yaml plus test/load/compose.yaml (the fault proxy and an
# access store in the throwaway database), issues two throwaway keys, runs
# internal/testkit/loadrun against that stack, writes the Markdown report to
# REPORT (default load-report.md) and its figures beside it (REPORT with .json
# for .md), and tears the stack and its volume down on every exit path. The
# keys and the generated passwords are never printed.
#
# LOADRUN_PREVIOUS, when set, names the previous main run's figures
# (scripts/load-previous.sh fetches them); the report compares this run with
# them and a p95 or p99 regression beyond the tolerance fails it (TST-021).
#
# Only this machine's loopback is a target: the runner refuses every host
# that is not on test/load/allow-list.txt, and this script refuses an
# LOADRUN_API or LOADRUN_PROXY that is not the loopback (TST-010).
#
# Exit 0 when every scenario passed, 1 when one failed, 2 on a usage error
# or a refused target.
set -euo pipefail

cd "$(dirname "$0")/.."

profile=${1:-}
report=${2:-load-report.md}
API_URL="${LOADRUN_API:-http://[::1]:8080}"
PROXY_URL="${LOADRUN_PROXY:-http://[::1]:8474}"
loopback='^http://(localhost|127\.0\.0\.1|\[::1\]):[0-9]+$'

[[ $profile == pr || $profile == nightly ]] || { echo "usage: load-test.sh pr|nightly [REPORT]" >&2; exit 2; }
[[ $API_URL =~ $loopback ]] || { echo "FAIL: LOADRUN_API must be this machine's loopback, not $API_URL" >&2; exit 2; }
[[ $PROXY_URL =~ $loopback ]] || { echo "FAIL: LOADRUN_PROXY must be this machine's loopback, not $PROXY_URL" >&2; exit 2; }

export COMPOSE_FILE=compose.yaml:test/load/compose.yaml
compose=(docker compose -f compose.yaml -f test/load/compose.yaml)

if [[ ! -f .env ]]; then
  export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-$(openssl rand -hex 16)}"
  export API_READER_PASSWORD="${API_READER_PASSWORD:-$(openssl rand -hex 16)}"
fi
: "${API_ACCESS_PASSWORD:=$(openssl rand -hex 16)}"
export API_ACCESS_PASSWORD
export VFAPI_AUTH_PEPPER="${VFAPI_AUTH_PEPPER:-$(openssl rand -hex 32)}"

teardown() {
  local code=$?
  if [[ $code -ne 0 ]]; then "${compose[@]}" logs --no-color --tail 60 || true; fi
  "${compose[@]}" down --volumes --remove-orphans >/dev/null 2>&1 || true
  exit "$code"
}
scripts/compose-guard.sh

trap teardown EXIT

"${compose[@]}" up --detach --build --wait --wait-timeout 300

sql() { "${compose[@]}" exec -T db psql -U owner -d contract -v ON_ERROR_STOP=1 -tAqc "$1"; }
keys() { "${compose[@]}" exec -T api /app keys "$@"; }

sources=$(sql "select string_agg(source_ref, ',' order by source_ref) from contract.source where review_signed_on is not null")
place=$(sql "select place_id from contract.place where withdrawn_at is null order by prominence desc, place_id limit 1")
[[ -n $sources && -n $place ]] || { echo "FAIL: the fixture set has no signed-off source or no place" >&2; exit 1; }

keys plan -name load -use-class commercial -tier standard -scopes places,energy,parking -sources "$sources" \
  -rate 100000 -burst 100000 -actor load-test >/dev/null
# The second line of keys issue is the key; it goes into the environment of
# the runner and nowhere else.
LOADRUN_KEY=$(keys issue -plan load -label load-warm -contact ops@example.test -actor load-test | sed -n 2p)
LOADRUN_COLD_KEY=$(keys issue -plan load -label load-cold -contact ops@example.test -actor load-test | sed -n 2p)
[[ -n $LOADRUN_KEY && -n $LOADRUN_COLD_KEY ]] || { echo "FAIL: the key tool issued no key" >&2; exit 1; }
export LOADRUN_KEY LOADRUN_COLD_KEY

# The breaker's settings of the instance under test, read from the compose
# file that sets them: the runner judges the breaker against these.
breaker() {
  local v
  v=$(sed -n "s/^ *VFAPI_BREAKER_$1: *\"\{0,1\}\([^\"]*\)\"\{0,1\} *$/\1/p" test/load/compose.yaml)
  [[ -n $v ]] || { echo "test/load/compose.yaml sets no VFAPI_BREAKER_$1" >&2; exit 2; }
  printf '%s' "$v"
}
brk_failures=$(breaker FAILURE_THRESHOLD) || exit 2
brk_open_for=$(breaker OPEN_FOR) || exit 2
brk_probes=$(breaker HALF_OPEN_PROBES) || exit 2
previous=()
[[ -n ${LOADRUN_PREVIOUS:-} ]] && previous=(-previous "$LOADRUN_PREVIOUS")
go run ./internal/testkit/loadrun -profile "$profile" -report "$report" -api "$API_URL" -proxy "$PROXY_URL" -place "$place" \
  -breaker-failures "$brk_failures" -breaker-open-for "$brk_open_for" -breaker-probes "$brk_probes" \
  -compose-files compose.yaml,test/load/compose.yaml "${previous[@]}"
