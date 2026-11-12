#!/usr/bin/env bash
# load-previous.test.sh
#
# The self-test of scripts/load-previous.sh (TST-021, issue #80), behind a gh
# stub that answers like GitHub and applies the script's own --jq filters
# with jq: the newest run of the asked budget is fetched, a newer run of the
# other budget passed over; no run, a run whose artifact has no figures, an
# expired artifact, no artifact and no run of the budget write nothing and
# say so; a run list, an artifact list or a download that fails, and an
# answer that is not a run id, are errors and never "no previous run"; a
# missing argument, an unknown budget or no REPO is a usage error. Every
# case's exit code and output are asserted.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pass=0 fail=0

# The stub. STUB_LIST is the run list's JSON (STUB_LIST_FAIL=1 fails it).
# Run N's artifact is the directory $STUB_RUNS/N, whose files the download
# copies into -D (STUB_DOWNLOAD_FAIL=1 fails it); its artifact list is
# $STUB_RUNS/N.artifacts.json when present, else one live load-nightly
# artifact (STUB_API_FAIL=1 fails it). Every --jq filter is applied with jq,
# and every call is logged.
cat > "$tmp/gh" <<'EOF'
#!/usr/bin/env bash
echo "$*" >> "$STUB_LOG"
filter=.
args=("$@")
for ((i = 0; i < ${#args[@]}; i++)); do
  [[ ${args[i]} == --jq ]] && filter=${args[i + 1]}
done
case "$1 $2" in
  "run list")
    [[ ${STUB_LIST_FAIL:-} == 1 ]] && exit 1
    printf '%s' "${STUB_LIST:-[]}" | jq -r "$filter" ;;
  "api "*)
    [[ ${STUB_API_FAIL:-} == 1 ]] && exit 1
    id=${2#*/runs/}; id=${id%%/*}
    f=$STUB_RUNS/$id.artifacts.json
    if [[ -f $f ]]; then jq -r "$filter" "$f"; else
      echo '{"artifacts":[{"name":"load-report","expired":true},{"name":"load-nightly","expired":false}]}' | jq -r "$filter"
    fi ;;
  "run download")
    [[ ${STUB_DOWNLOAD_FAIL:-} == 1 ]] && exit 1
    id=$3
    while [[ $# -gt 0 && $1 != -D ]]; do shift; done
    mkdir -p "$2"
    cp -R "$STUB_RUNS/$id/." "$2" ;;
  *) exit 3 ;;
esac
EOF
chmod +x "$tmp/gh"
runs=$tmp/runs
export GH=$tmp/gh STUB_LOG=$tmp/log STUB_RUNS=$runs REPO=owner/repo
# Run 42: nightly figures. 43: pr figures. 7: no figures. 8: figures that
# are not JSON. 9: an expired artifact. 10: no load-nightly artifact.
mkdir -p "$runs"/{42,43,7,8,9,10}
echo '{"version":1,"profile":"nightly"}' > "$runs/42/load-report.json"
echo '{"version":1,"profile":"pr"}' > "$runs/43/load-report.json"
echo 'not json' > "$runs/8/load-report.json"
for r in 42 43 7 8 9 10; do echo 'report' > "$runs/$r/load-report.md"; done
echo '{"artifacts":[{"name":"load-nightly","expired":true}]}' > "$runs/9.artifacts.json"
echo '{"artifacts":[{"name":"other","expired":false}]}' > "$runs/10.artifacts.json"
echo '{"artifacts":[{"name":"load-nightly","expired":"maybe"}]}' > "$runs/11.artifacts.json"

list() { # list ID...: the run list's JSON, newest first
  local out='[' sep=''
  for id in "$@"; do out+="$sep{\"databaseId\":$id}"; sep=,; done
  printf '%s]' "$out"
}

# expect CODE NAME OUTPUT_FRAGMENT PROFILE [VAR=VALUE...]: runs the script
# into a fresh directory with the given stub settings.
expect() {
  local code=$1 name=$2 want=$3 prof=$4 out got
  shift 4
  rm -rf "$tmp/prev" "$tmp/log"
  out=$(env "$@" bash "$here/load-previous.sh" "$tmp/prev" "$prof" 2>&1)
  got=$?
  if [[ $got -eq $code && $out == *"$want"* ]]; then
    pass=$((pass + 1)); echo "ok: $name"
  else
    fail=$((fail + 1)); echo "FAIL: $name: exit $got, want $code; output: $out"
  fi
}
check() { # check NAME CONDITION...
  local name=$1
  shift
  if "$@"; then pass=$((pass + 1)); echo "ok: $name"; else fail=$((fail + 1)); echo "FAIL: $name"; fi
}
nofile() { ! test -e "$tmp/prev/load-report.json"; }

expect 0 "the newest run's figures are fetched" "the figures of nightly load run 42 (nightly budget) are in" nightly \
  STUB_LIST="$(list 42 7)"
check "the figures are the artifact's" cmp -s "$runs/42/load-report.json" "$tmp/prev/load-report.json"
check "the newest ten successful runs of the nightly workflow on main are listed" \
  grep -q -- "run list -R owner/repo --workflow load-nightly.yml --branch main --status success --limit 10" "$tmp/log"
check "the run's artifacts are asked for" grep -q -- "api repos/owner/repo/actions/runs/42/artifacts" "$tmp/log"
check "the load-nightly artifact of that run is downloaded" grep -q -- "run download 42 -R owner/repo -n load-nightly -D" "$tmp/log"
check "an older run is not looked at" bash -c "! grep -q 'runs/7/' '$tmp/log'"

expect 0 "a newer run of the other budget is passed over" "run 43 is of the pr budget, not nightly; looking at an older run" nightly \
  STUB_LIST="$(list 43 42)"
check "the older run of the budget is fetched" cmp -s "$runs/42/load-report.json" "$tmp/prev/load-report.json"
expect 0 "the pr budget fetches the pr run" "run 43 (pr budget)" pr STUB_LIST="$(list 42 43)"
check "the pr figures are fetched" cmp -s "$runs/43/load-report.json" "$tmp/prev/load-report.json"
expect 0 "no run of the budget among those looked at" "none of the newest 10 successful nightly load runs of main is of the pr budget" pr \
  STUB_LIST="$(list 42)"
check "no run of the budget: no file" nofile

expect 0 "no successful run: nothing to compare with" "main has no successful nightly load run" nightly STUB_LIST='[]'
check "no run: no file" nofile
check "no run: no download" bash -c "! grep -q 'run download' '$tmp/log'"

expect 0 "a run without figures: nothing to compare with" "nightly load run 7 left no figures" nightly STUB_LIST="$(list 7 42)"
check "no figures: no file, and no older run read" bash -c "! test -e '$tmp/prev/load-report.json' && ! grep -q 'runs/42/' '$tmp/log'"

expect 0 "an expired artifact is its own state" "the artifact of nightly load run 9 has expired; nothing to compare with" nightly \
  STUB_LIST="$(list 9 42)"
check "expired: no file, no download" bash -c "! test -e '$tmp/prev/load-report.json' && ! grep -q 'run download' '$tmp/log'"
expect 0 "a run with no load-nightly artifact" "nightly load run 10 has no load-nightly artifact" nightly STUB_LIST="$(list 10)"
check "no artifact: no file" nofile

expect 0 "a file that is not figures is fetched for the runner to judge" "run 8 (nightly budget)" nightly STUB_LIST="$(list 8)"
check "the unreadable file is the artifact's" cmp -s "$runs/8/load-report.json" "$tmp/prev/load-report.json"

expect 1 "a failed run list is an error" "could not be listed" nightly STUB_LIST_FAIL=1
expect 1 "an answer that is not a run id is an error" "not a run id" nightly STUB_LIST='[{"databaseId":"<html>"}]'
expect 1 "a failed artifact list is an error" "artifacts of nightly load run 42 could not be listed" nightly \
  STUB_LIST="$(list 42)" STUB_API_FAIL=1
expect 1 "an artifact list that answers neither is an error" "answered neither expired nor not" nightly STUB_LIST="$(list 11)"
expect 1 "a failed download is an error" "run 42 could not be downloaded" nightly STUB_LIST="$(list 42)" STUB_DOWNLOAD_FAIL=1
check "a failed download writes nothing" nofile

rm -rf "$tmp/prev"
out=$(bash "$here/load-previous.sh" 2>&1); got=$?
check "no directory is a usage error" test "$got" -eq 2 -a "${out#usage}" != "$out"
out=$(bash "$here/load-previous.sh" "$tmp/prev" 2>&1); got=$?
check "no budget is a usage error" test "$got" -eq 2
out=$(bash "$here/load-previous.sh" "$tmp/prev" soak 2>&1); got=$?
check "an unknown budget is a usage error" test "$got" -eq 2
out=$(bash "$here/load-previous.sh" "$tmp/prev" nightly x 2>&1); got=$?
check "three arguments are a usage error" test "$got" -eq 2
out=$(REPO='' bash "$here/load-previous.sh" "$tmp/prev" nightly 2>&1); got=$?
check "no REPO is a usage error" test "$got" -eq 2 -a "${out#*REPO must}" != "$out"

echo "load-previous self-test: $pass passed, $fail failed"
[[ $fail -eq 0 && $pass -ge 34 ]]
