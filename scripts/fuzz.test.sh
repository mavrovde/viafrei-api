#!/usr/bin/env bash
# fuzz.test.sh
#
# The self-test of the job-matrix modes of scripts/fuzz.sh (TEST-006,
# TST-016; fuzz-nightly.yml runs one target per job, each within 15
# minutes): --list names every target of the inventory as PACKAGE/TARGET;
# --matrix gives one entry per target and refuses a budget that cannot fit a
# 15-minute job; FUZZ_TARGETS refuses a name that is not a target; --gather
# passes when every target's job left a passing row, and fails a missing
# report, a failed row and a report with two rows for one target. No target
# is fuzzed: nothing here runs go test.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
fuzz=$here/fuzz.sh
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pass=0 fail=0

check() {
  local what=$1
  shift
  if "$@"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    echo "FAIL: $what" >&2
  fi
}

# expect CODE WHAT TEXT CMD...: CMD exits CODE and prints TEXT (stdout or stderr).
expect() {
  local code=$1 what=$2 text=$3
  shift 3
  local got=0
  "$@" >"$tmp/out" 2>&1 || got=$?
  if [[ $got -eq $code ]] && grep -qF -- "$text" "$tmp/out"; then
    pass=$((pass + 1))
  else
    fail=$((fail + 1))
    echo "FAIL: $what: exit $got, want $code with \"$text\"" >&2
    sed 's/^/    /' "$tmp/out" >&2
  fi
}

inventory=$(awk -F'\t' '!/^#/ && NF == 5 && $4 != "-" { print $2 "/" $4 }' "$here/../test/fuzz/targets.tsv")
want=$(wc -l <<<"$inventory" | tr -d ' ')

"$fuzz" --list >"$tmp/list"
check "--list names every target of the inventory as PACKAGE/TARGET" diff -u <(echo "$inventory") "$tmp/list"
check "the inventory has at least three targets" test "$want" -ge 3

"$fuzz" --matrix 10m >"$tmp/matrix"
check "--matrix is one GitHub output line" grep -qE '^targets=\[' "$tmp/matrix"
check "--matrix has one entry per target" test "$(sed 's/^targets=//' "$tmp/matrix" | jq length)" -eq "$want"
check "--matrix names each entry's artifact without a slash" test "$(sed 's/^targets=//' "$tmp/matrix" | jq '[.[] | select(.name | contains("/"))] | length')" -eq 0
check "--matrix entries are the --list targets" diff -u "$tmp/list" <(sed 's/^targets=//' "$tmp/matrix" | jq -r '.[].target')
expect 0 "a 12m budget fits a 15-minute job" "targets=" "$fuzz" --matrix 12m
expect 2 "a 13m budget does not fit a 15-minute job" "does not fit a 15-minute job" "$fuzz" --matrix 13m
expect 2 "an hour does not fit a 15-minute job" "does not fit a 15-minute job" "$fuzz" --matrix 1h
expect 2 "--matrix without a budget" "usage: fuzz.sh --matrix" "$fuzz" --matrix
expect 2 "FUZZ_TARGETS naming no target" "FUZZ_TARGETS names nope/FuzzNope" env FUZZ_TARGETS=nope/FuzzNope "$fuzz" 1s "$tmp/r.md"
expect 2 "a target name alone is not a target" "which is not a target" env FUZZ_TARGETS="${inventory##*/}" "$fuzz" 1s "$tmp/r.md"
expect 2 "--gather without its arguments" "usage: fuzz.sh --gather" "$fuzz" --gather "$tmp"

# One passing report per target, as each job of fuzz-nightly.yml writes it.
mkdir -p "$tmp/all"
while IFS= read -r pt; do
  pkg=${pt%/*} target=${pt##*/}
  {
    echo "## Fuzzing, 10m per target"
    echo
    echo "| Target | Package | Executions | New corpus entries | Crashes | Verdict |"
    echo "|---|---|---:|---:|---:|---|"
    echo "| $target | $pkg | 1000 | 3 | 0 | pass |"
    echo
    echo "1 target(s), 0 failed."
  } >"$tmp/all/fuzz-report-${pt//\//-}.md"
done <<<"$inventory"
expect 0 "every target's job passed" "gathered $want target(s), 0 failed" "$fuzz" --gather "$tmp/all" 10m "$tmp/report.md"
check "the gathered report has one row per target" test "$(grep -c '| pass |$' "$tmp/report.md")" -eq "$want"
check "the gathered report names the budget and the split" grep -qF "## Fuzzing, 10m per target, one job per target" "$tmp/report.md"

first=$(head -n 1 <<<"$inventory")
f=$tmp/all/fuzz-report-${first//\//-}.md

cp -R "$tmp/all" "$tmp/missing" && rm "$tmp/missing/${f##*/}"
expect 1 "a job that left no report" "1 failed" "$fuzz" --gather "$tmp/missing" 10m "$tmp/report.md"
check "the missing report is named" grep -qF "| ${first##*/} | ${first%/*} | | | | FAIL (its job left no report row) |" "$tmp/report.md"

cp -R "$tmp/all" "$tmp/crash" && sed -i.bak 's/| 0 | pass |$/| 1 | FAIL (exit 1) |/' "$tmp/crash/${f##*/}"
expect 1 "a target with a crash" "1 failed" "$fuzz" --gather "$tmp/crash" 10m "$tmp/report.md"
check "the crash row is kept" grep -qF "| 1 | FAIL (exit 1) |" "$tmp/report.md"

cp -R "$tmp/all" "$tmp/twice" && grep '| pass |$' "$f" >>"$tmp/twice/${f##*/}"
expect 1 "a report with two rows for one target" "1 failed" "$fuzz" --gather "$tmp/twice" 10m "$tmp/report.md"

echo "fuzz.test.sh: $pass passed, $fail failed"
[[ $fail -eq 0 ]]
