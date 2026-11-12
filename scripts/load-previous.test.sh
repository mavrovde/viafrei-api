#!/usr/bin/env bash
# load-previous.test.sh
#
# The self-test of scripts/load-previous.sh (TST-021, issue #80), behind a gh
# stub that answers like GitHub: the newest run's figures are fetched; no
# run, and a run whose artifact has no figures, write nothing and say so; a
# run list or a download that fails, and an answer that is not a run id, are
# errors and never "no previous run"; a missing argument or REPO is a usage
# error. Every case's exit code and output are asserted.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pass=0 fail=0

# The stub: STUB_LIST is what the run list prints (STUB_LIST_FAIL=1 fails
# it); STUB_ARTIFACT is a directory whose files the download copies into
# -D (STUB_DOWNLOAD_FAIL=1 fails it). Every call is logged.
cat > "$tmp/gh" <<'EOF'
#!/usr/bin/env bash
echo "$*" >> "$STUB_LOG"
case "$1 $2" in
  "run list")
    [[ ${STUB_LIST_FAIL:-} == 1 ]] && exit 1
    printf '%s\n' "${STUB_LIST:-}" ;;
  "run download")
    [[ ${STUB_DOWNLOAD_FAIL:-} == 1 ]] && exit 1
    while [[ $# -gt 0 && $1 != -D ]]; do shift; done
    cp -R "$STUB_ARTIFACT/." "$2" ;;
esac
EOF
chmod +x "$tmp/gh"
export GH=$tmp/gh STUB_LOG=$tmp/log REPO=owner/repo
mkdir -p "$tmp/with" "$tmp/without"
echo '{"version":1}' > "$tmp/with/load-report.json"
echo 'report' > "$tmp/with/load-report.md"
echo 'report' > "$tmp/without/load-report.md"

# expect CODE NAME OUTPUT_FRAGMENT [VAR=VALUE...]: runs the script into a
# fresh directory with the given stub settings.
expect() {
  local code=$1 name=$2 want=$3 out got
  shift 3
  rm -rf "$tmp/prev" "$tmp/log"
  out=$(env "$@" bash "$here/load-previous.sh" "$tmp/prev" 2>&1)
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

expect 0 "the newest run's figures are fetched" "the figures of nightly load run 42 are in" STUB_LIST=42 STUB_ARTIFACT="$tmp/with"
check "the figures are the artifact's" cmp -s "$tmp/with/load-report.json" "$tmp/prev/load-report.json"
check "only successful runs of the nightly workflow on main are listed" \
  grep -q -- "run list -R owner/repo --workflow load-nightly.yml --branch main --status success --limit 1" "$tmp/log"
check "the load-nightly artifact of that run is downloaded" grep -q -- "run download 42 -R owner/repo -n load-nightly -D" "$tmp/log"

expect 0 "no successful run: nothing to compare with" "main has no successful nightly load run" STUB_LIST=
check "no run: no file" bash -c "! test -e '$tmp/prev/load-report.json'"
check "no run: no download" bash -c "! grep -q 'run download' '$tmp/log'"

expect 0 "a run without figures: nothing to compare with" "nightly load run 7 left no figures" STUB_LIST=7 STUB_ARTIFACT="$tmp/without"
check "no figures: no file" bash -c "! test -e '$tmp/prev/load-report.json'"

expect 1 "a failed run list is an error" "could not be listed" STUB_LIST_FAIL=1
expect 1 "an answer that is not a run id is an error" "not a run id" STUB_LIST="<html>"
expect 1 "a failed download is an error" "run 42 could not be downloaded" STUB_LIST=42 STUB_DOWNLOAD_FAIL=1
check "a failed download writes nothing" bash -c "! test -e '$tmp/prev/load-report.json'"

rm -rf "$tmp/prev"
out=$(bash "$here/load-previous.sh" 2>&1); got=$?
check "no directory is a usage error" test "$got" -eq 2 -a "${out#usage}" != "$out"
out=$(bash "$here/load-previous.sh" a b 2>&1); got=$?
check "two arguments are a usage error" test "$got" -eq 2
out=$(REPO='' bash "$here/load-previous.sh" "$tmp/prev" 2>&1); got=$?
check "no REPO is a usage error" test "$got" -eq 2 -a "${out#*REPO must}" != "$out"

echo "load-previous self-test: $pass passed, $fail failed"
[[ $fail -eq 0 && $pass -ge 16 ]]
