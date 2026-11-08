#!/usr/bin/env bash
# fuzz.sh BUDGET [REPORT]
#
# Runs every native fuzz target of test/fuzz/targets.tsv for BUDGET each
# (a Go duration such as 20s or 10m; TEST-006, TST-016) against the code
# under test only: go test -fuzz in this module, no network, no running
# service. Writes a Markdown report to REPORT (default fuzz-report.md) with,
# per target, the executions, the new corpus entries and the crashes.
#
# A crash fails the run, and its reproducer (the failing input Go writes to
# <package>/testdata/fuzz/<target>/) is copied to FUZZ_CRASHERS (default
# fuzz-crashers/<package>/testdata/fuzz/<target>/). Committed there, it runs
# as a unit test on every go test, which is how a finding becomes a test in
# the fixing pull request. A target that ran zero executions is a failure,
# never a pass. With FUZZ_CORPUS_OUT set, the new interesting inputs of each
# target are copied there in the same layout, ready to be committed to grow
# the seed corpus.
#
# Exit 0 when every target ran and found nothing, 1 on a finding or a target
# that did not run, 2 on a usage error or an inventory with no target.
set -euo pipefail

cd "$(dirname "$0")/.."

budget=${1:-}
report=${2:-fuzz-report.md}
crashers=${FUZZ_CRASHERS:-fuzz-crashers}
inventory=test/fuzz/targets.tsv
module=$(go list -m)

if [[ ! $budget =~ ^[1-9][0-9]*(s|m|h)$ ]]; then
  echo "usage: fuzz.sh BUDGET [REPORT]   (BUDGET: a duration such as 20s or 10m)" >&2
  exit 2
fi

targets=$(awk -F'\t' '!/^#/ && NF == 5 && $4 != "-" { print $2 "\t" $4 }' "$inventory")
if [[ -z $targets ]]; then
  echo "FAIL: $inventory lists no fuzz target; a run over nothing is not a pass" >&2
  exit 2
fi

cache=$(go env GOCACHE)/fuzz/$module
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

{
  echo "## Fuzzing, $budget per target"
  echo
  echo "| Target | Package | Executions | New corpus entries | Crashes | Verdict |"
  echo "|---|---|---:|---:|---:|---|"
} >"$report"

failed=0
n=0
while IFS=$'\t' read -r pkg target; do
  n=$((n + 1))
  log="$work/$target.log"
  code=0
  go test -run '^$' -fuzz "^${target}\$" -fuzztime "$budget" "./$pkg" >"$log" 2>&1 || code=$?
  last=$(grep -E '^fuzz: elapsed: .*execs: [0-9]+' "$log" | tail -n 1 || true)
  execs=$(sed -nE 's/.*execs: ([0-9]+).*/\1/p' <<<"$last")
  fresh=$(sed -nE 's/.*new interesting: ([0-9]+).*/\1/p' <<<"$last")
  execs=${execs:-0}
  fresh=${fresh:-0}
  crashes=0
  while IFS= read -r input; do
    [[ -n $input ]] || continue
    crashes=$((crashes + 1))
    mkdir -p "$crashers/$pkg/testdata/fuzz/$target"
    cp "$pkg/$input" "$crashers/$pkg/testdata/fuzz/$target/"
  done < <(sed -nE 's/.*Failing input written to (testdata\/fuzz\/[^ ]+).*/\1/p' "$log")
  verdict=pass
  if [[ $code -ne 0 || $crashes -gt 0 ]]; then
    verdict="FAIL (exit $code)"
  elif [[ $execs -eq 0 ]]; then
    verdict="FAIL (zero executions)"
  fi
  if [[ $verdict != pass ]]; then
    failed=$((failed + 1))
    echo "---- $target ($pkg): $verdict" >&2
    tail -n 40 "$log" >&2
  fi
  if [[ -n ${FUZZ_CORPUS_OUT:-} && -d $cache/$pkg/$target ]]; then
    mkdir -p "$FUZZ_CORPUS_OUT/$pkg/testdata/fuzz/$target"
    cp "$cache/$pkg/$target/"* "$FUZZ_CORPUS_OUT/$pkg/testdata/fuzz/$target/" 2>/dev/null || true
  fi
  echo "| $target | $pkg | $execs | $fresh | $crashes | $verdict |" >>"$report"
  echo "fuzz: $target: $execs executions, $fresh new corpus entries, $crashes crashes: $verdict"
done <<<"$targets"

echo >>"$report"
echo "$n target(s), $failed failed." >>"$report"
echo "fuzz: $n target(s) run for $budget each, $failed failed; report in $report"
[[ $failed -eq 0 ]]
