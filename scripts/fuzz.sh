#!/usr/bin/env bash
# fuzz.sh BUDGET [REPORT]
# fuzz.sh --list
# fuzz.sh --matrix BUDGET
# fuzz.sh --gather DIR BUDGET REPORT
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
# With FUZZ_TARGETS set (PACKAGE/TARGET, comma-separated, as --list prints
# them; a target name alone is not unique) only those targets run; one that
# is not a target of the inventory is a usage error. That is
# one job of fuzz-nightly.yml, which runs one target per job so that every
# job ends within 15 minutes. --list prints the inventory's targets as
# PACKAGE/TARGET, one per line. --matrix prints that job matrix as a GitHub
# output line, targets=[{"target": PACKAGE/TARGET, "name": PACKAGE-TARGET}],
# and refuses a BUDGET above 12m (one target, its build and the job's setup
# must end within 15 minutes; a target takes its budget plus about 20 s).
# --gather assembles the jobs' reports, DIR/fuzz-report-NAME.md, into one
# REPORT: a target whose job left no report, or whose row is not a pass,
# fails it.
#
# Exit 0 when every target ran and found nothing, 1 on a finding or a target
# that did not run, 2 on a usage error or an inventory with no target.
set -euo pipefail

cd "$(dirname "$0")/.."

budget=${1:-}
report=${2:-fuzz-report.md}
crashers=${FUZZ_CRASHERS:-fuzz-crashers}
inventory=test/fuzz/targets.tsv

targets=$(awk -F'\t' '!/^#/ && NF == 5 && $4 != "-" { print $2 "\t" $4 }' "$inventory")
if [[ -z $targets ]]; then
  echo "FAIL: $inventory lists no fuzz target; a run over nothing is not a pass" >&2
  exit 2
fi
case $budget in
  --list)
    tr '\t' / <<<"$targets"
    exit 0 ;;
  --matrix)
    b=${2:-}
    [[ $b =~ ^([1-9][0-9]*)(s|m|h)$ ]] || { echo "usage: fuzz.sh --matrix BUDGET   (a duration such as 10m)" >&2; exit 2; }
    secs=${BASH_REMATCH[1]}
    case ${BASH_REMATCH[2]} in m) secs=$((secs * 60)) ;; h) secs=$((secs * 3600)) ;; esac
    if ((secs > 720)); then
      echo "FAIL: a budget of $b per target does not fit a 15-minute job; at most 12m" >&2
      exit 2
    fi
    printf 'targets='
    tr '\t' / <<<"$targets" | jq -R -s -c 'split("\n") | map(select(length > 0)) | map({target: ., name: gsub("/"; "-")})'
    exit 0 ;;
  --gather)
    dir=${2:-} b=${3:-} out=${4:-}
    [[ -n $dir && -n $b && -n $out && $# -eq 4 ]] || { echo "usage: fuzz.sh --gather DIR BUDGET REPORT" >&2; exit 2; }
    {
      echo "## Fuzzing, $b per target, one job per target"
      echo
      echo "| Target | Package | Executions | New corpus entries | Crashes | Verdict |"
      echo "|---|---|---:|---:|---:|---|"
    } >"$out"
    n=0 failed=0
    while IFS=$'\t' read -r pkg target; do
      n=$((n + 1))
      f=$dir/fuzz-report-${pkg//\//-}-$target.md
      rows=$( { grep -E "^\| $target \| $pkg \| [0-9]+ \|" "$f" 2>/dev/null || true; } )
      if [[ -z $rows || $rows == *$'\n'* ]]; then
        echo "| $target | $pkg | | | | FAIL (its job left no report row) |" >>"$out"
        failed=$((failed + 1))
        continue
      fi
      echo "$rows" >>"$out"
      [[ $rows == *"| pass |" ]] || failed=$((failed + 1))
    done <<<"$targets"
    echo >>"$out"
    echo "$n target(s), $failed failed." >>"$out"
    echo "fuzz: gathered $n target(s), $failed failed; report in $out"
    [[ $failed -eq 0 ]]
    exit ;;
esac
if [[ -n ${FUZZ_TARGETS:-} ]]; then
  picked=
  IFS=, read -ra want <<<"$FUZZ_TARGETS"
  for w in "${want[@]}"; do
    line=$(awk -F'\t' -v t="$w" '$1 "/" $2 == t' <<<"$targets")
    [[ -n $line ]] || { echo "FAIL: FUZZ_TARGETS names $w, which is not a target of $inventory" >&2; exit 2; }
    picked+=$line$'\n'
  done
  targets=${picked%$'\n'}
fi
module=$(go list -m)

if [[ ! $budget =~ ^[1-9][0-9]*(s|m|h)$ ]]; then
  echo "usage: fuzz.sh BUDGET [REPORT]   (BUDGET: a duration such as 20s or 10m)" >&2
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
