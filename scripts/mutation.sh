#!/usr/bin/env bash
# mutation.sh pr BASE [REPORT] | full [REPORT] | shard K/N OUTDIR | gather OUTDIR [REPORT]
#
# Mutation testing of the domain, access and admission code (TST-002,
# issue #17) with Gremlins, scored by internal/testkit/mutgate against the
# scope of test/mutation/scope.tsv and the equivalents of
# test/mutation/equivalent.tsv.
#
#   pr BASE  the changed code of HEAD against BASE (a ref; the merge base is
#            used): the added or changed code lines of in-scope files, plus,
#            for a changed test file, every file of its package. Only the
#            scope roots the change touches are mutated. Fails below 80 %,
#            on zero mutants for changed in-scope code, and, when tests
#            changed, on a mutant the base kills and HEAD does not: the same
#            roots are mutated at the base too, so deleting an assertion is
#            red whatever the score.
#   full     every in-scope mutant (per minor release). Fails below 70 %,
#            and on a stale equivalent entry. It runs the units of the full
#            run one after another (mutgate -plan -shard 1/1).
#   shard K/N OUTDIR
#            the Gremlins runs of shard K of N of the full run's units, one
#            JSON report per unit (NAME.json) into OUTDIR, and no score:
#            mutation.yml runs the shards of a full run as parallel jobs. A
#            unit is a package under a scope root, or, for a costly package,
#            a group of its files or of one file's mutators; the units are
#            dealt to the shards by their estimated cost (mutgate -plan
#            -shard, internal/testkit/mutgate/units.go, with the seconds per
#            mutant of test/mutation/cost.tsv). Exit 2 on a bad K/N or a
#            shard with no unit, 1 when a unit's run wrote no report.
#   gather OUTDIR [REPORT]
#            the full-mode gate over the reports the shards wrote into OUTDIR
#            (it may hold subdirectories, one per shard): the same score and
#            the same findings as full, and exit 2 when a unit has no report
#            or has two.
#
# A change that touches no in-scope code mutates nothing and passes, and
# says so. The report (default mutation-report.md) carries the counts and
# every survivor. Gremlins runs as go run of v0.6.0, the way the other CI
# tools run: the version resolves through the tool's own go.mod and is
# verified against the checksum database, and it never enters this
# module's dependencies. GREMLINS names an installed binary instead.
#
# Exit 0 at or above the target, 1 below it or on a gate finding, 2 on a
# usage error or an input that cannot be read.
set -euo pipefail

cd "$(dirname "$0")/.."

mode=${1:-}
scope=test/mutation/scope.tsv
equivalent=test/mutation/equivalent.tsv
cost=test/mutation/cost.tsv
work=$(mktemp -d)
cleanup() {
  if [[ -d $work/base ]]; then git worktree remove --force "$work/base" >/dev/null 2>&1 || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

# A timeout counts against the score, so it must mean a hang, not a busy
# runner: Gremlins' default timeout timed out mutants whose tests fail in
# well under a second on a loaded machine; 30 times the clean run did not.
gremlins=(go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0)
if [[ -n ${GREMLINS:-} ]]; then gremlins=("$GREMLINS"); fi
# A scope row that names its files mutates only those files. Gremlins mutates
# every file of the package and the gate counts only the listed ones, so the
# rest were run and thrown away: the 0.4.0 release ran 841 mutants of
# internal/adapters/httpapi in 24.6 minutes for the 73 of auth.go, key.go and
# limits.go. Gremlins matches -E against the path in the package.
excludes() {
  local root=$1 files f base
  files=$(awk -F'\t' -v r="$root" '$0 !~ /^#/ && $1 == r { print $2; exit }' "$scope")
  [[ -n $files && $files != '*' ]] || return 0
  for f in "$root"/*.go; do
    base=${f##*/}
    [[ -e $f && $base != *_test.go ]] || continue
    [[ ",$files," == *",$base,"* ]] && continue
    printf '%s\n' -E "^${base//./\\.}\$"
  done
}
# -count=1: the timeout is 30 times Gremlins' coverage run, and a coverage
# run answered from the test cache (a package run twice, as the units of one
# package are, or tests run just before) takes milliseconds and times every
# mutant out.
unleash() {
  GOFLAGS="${GOFLAGS:+$GOFLAGS }-count=1" "${gremlins[@]}" unleash --silent --invert-logical --invert-loopctrl --timeout-coefficient 30 "$@"
}
mutate() {
  local ex=() a
  while IFS= read -r a; do ex+=("$a"); done < <(excludes "${2#./}")
  unleash ${ex[@]+"${ex[@]}"} --output "$1" "$2"
}
gate() { go run ./internal/testkit/mutgate -scope "$scope" -equivalent "$equivalent" -cost "$cost" "$@"; }
# mutate_unit OUTDIR LINE: the Gremlins run of a plan line of mutgate (the
# unit's name, its directory and its Gremlins arguments: the -E excludes and
# the mutators switched off, tab-separated) into OUTDIR/NAME.json.
mutate_unit() {
  local u
  IFS=$'\t' read -r -a u <<<"$2"
  echo "mutation: ${u[0]} (${u[1]})"
  unleash "${u[@]:2}" --output "$1/${u[0]}.json" "./${u[1]}"
  if [[ ! -f $1/${u[0]}.json ]]; then
    echo "mutation: the run of the unit ${u[0]} wrote no report" >&2
    return 1
  fi
}

case "$mode" in
  pr)
    base=${2:-}
    report=${3:-mutation-report.md}
    min=80
    if [[ -z $base ]]; then
      echo "usage: mutation.sh pr BASE [REPORT] | full [REPORT] | shard K/N OUTDIR | gather OUTDIR [REPORT]" >&2
      exit 2
    fi
    if ! from=$(git merge-base "$base" HEAD); then
      echo "mutation: no merge base of $base and HEAD; fetch the base with full history" >&2
      exit 2
    fi
    changed=$work/changed.tsv
    # Added or changed code lines; a comment or blank line is not code, and
    # a deletion marks the lines around it.
    git diff -U0 --no-color --no-ext-diff "$from" HEAD -- '*.go' ':!*_test.go' | awk '
      /^\+\+\+ / { f = ($2 == "/dev/null") ? "" : substr($2, 3); next }
      /^@@ / {
        split($3, a, ","); ln = substr(a[1], 2) + 0; n = (a[2] == "") ? 1 : a[2] + 0
        if (n == 0 && f != "") print f "\t" (ln > 0 ? ln : 1) "\t" ln + 1
        next
      }
      /^\+/ { if (f != "" && substr($0, 2) !~ /^[ \t]*(\/\/.*)?$/) print f "\t" ln "\t" ln; ln++ }
    ' >"$changed"
    # A changed test file puts its whole package under test.
    tests=0
    while IFS= read -r t; do
      tests=$((tests + 1))
      [[ -n $t ]] || continue
      for f in "$(dirname "$t")"/*.go; do
        [[ -e $f && $f != *_test.go ]] && printf '%s\t*\n' "$f" >>"$changed"
      done
    done < <(git diff --name-only --diff-filter=AMR "$from" HEAD -- '*_test.go')
    roots=()
    if [[ -s $changed ]]; then
      while IFS= read -r r; do roots+=("$r"); done < <(gate -mode pr -min "$min" -changed "$changed" -plan)
    fi
    if [[ ${#roots[@]} -eq 0 ]]; then
      echo "mutation: the change touches no in-scope code ($scope); nothing to mutate"
      printf '## Mutation testing, pr mode\n\nThe change touches no in-scope code; nothing was mutated.\n' >"$report"
      exit 0
    fi
    flags=(-mode pr -min "$min" -changed "$changed")
    if [[ $tests -gt 0 ]]; then
      git worktree add --detach --quiet "$work/base" "$from"
      for r in "${roots[@]}"; do
        if [[ -d $work/base/$r ]]; then
          out=$work/base-$(tr / _ <<<"$r").json
          echo "mutation: $r at the base"
          (cd "$work/base" && mutate "$out" "./$r")
          flags+=(-baseline "$r=$out")
        fi
      done
    fi
    ;;
  full)
    report=${2:-mutation-report.md}
    if ! planned=$(gate -mode full -min 70 -plan -shard 1/1); then
      exit 2
    fi
    args=()
    while IFS=$'\t' read -r name _; do
      args+=("$name=$work/$name.json")
    done <<<"$planned"
    while IFS= read -r line; do
      mutate_unit "$work" "$line"
    done <<<"$planned"
    gate -mode full -min 70 -report "$report" "${args[@]}"
    exit $?
    ;;
  shard)
    spec=${2:-}
    out=${3:-}
    if [[ -z $spec || -z $out ]]; then
      echo "usage: mutation.sh pr BASE [REPORT] | full [REPORT] | shard K/N OUTDIR | gather OUTDIR [REPORT]" >&2
      exit 2
    fi
    # mutgate refuses a bad K/N and an empty shard with exit 2, before anything is mutated.
    if ! planned=$(gate -mode full -min 70 -plan -shard "$spec"); then
      exit 2
    fi
    mkdir -p "$out"
    echo "mutation: shard $spec: $(wc -l <<<"$planned" | tr -d ' ') unit(s)"
    while IFS= read -r line; do
      mutate_unit "$out" "$line"
    done <<<"$planned"
    exit 0
    ;;
  gather)
    dir=${2:-}
    report=${3:-mutation-report.md}
    min=70
    if [[ -z $dir || ! -d $dir ]]; then
      echo "usage: mutation.sh pr BASE [REPORT] | full [REPORT] | shard K/N OUTDIR | gather OUTDIR [REPORT]" >&2
      exit 2
    fi
    if ! planned=$(gate -mode full -min "$min" -plan -shard 1/1); then
      exit 2
    fi
    args=()
    while IFS=$'\t' read -r name _; do
      found=()
      while IFS= read -r f; do found+=("$f"); done < <(find "$dir" -type f -name "$name.json")
      if [[ ${#found[@]} -ne 1 ]]; then
        echo "mutation: gather: ${#found[@]} report(s) for the unit $name in $dir, not one: a shard did not finish, or two ran it" >&2
        exit 2
      fi
      args+=("$name=${found[0]}")
    done <<<"$planned"
    gate -mode full -min "$min" -report "$report" "${args[@]}"
    exit $?
    ;;
  *)
    echo "usage: mutation.sh pr BASE [REPORT] | full [REPORT] | shard K/N OUTDIR | gather OUTDIR [REPORT]" >&2
    exit 2
    ;;
esac

args=()
for r in "${roots[@]}"; do
  out=$work/$(tr / _ <<<"$r").json
  echo "mutation: $r"
  mutate "$out" "./$r"
  args+=("$r=$out")
done
gate "${flags[@]}" -report "$report" "${args[@]}"
