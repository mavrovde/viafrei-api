#!/usr/bin/env bash
# mutation.sh pr BASE [REPORT] | full [REPORT]
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
#            and on a stale equivalent entry.
#
# A change that touches no in-scope code mutates nothing and passes, and
# says so. The report (default mutation-report.md) carries the counts and
# every survivor. Gremlins is built from tools/gremlins, whose go.mod and
# go.sum lock its version (v0.6.0) and every dependency; GREMLINS names
# another binary instead.
#
# Exit 0 at or above the target, 1 below it or on a gate finding, 2 on a
# usage error or an input that cannot be read.
set -euo pipefail

cd "$(dirname "$0")/.."

mode=${1:-}
scope=test/mutation/scope.tsv
equivalent=test/mutation/equivalent.tsv
work=$(mktemp -d)
gremlins=${GREMLINS:-$work/gremlins}
cleanup() {
  if [[ -d $work/base ]]; then git worktree remove --force "$work/base" >/dev/null 2>&1 || true; fi
  rm -rf "$work"
}
trap cleanup EXIT
if [[ -z ${GREMLINS:-} ]]; then
  go -C tools/gremlins build -o "$gremlins" github.com/go-gremlins/gremlins/cmd/gremlins
fi

# A timeout counts against the score, so it must mean a hang, not a busy
# runner: Gremlins' default timeout timed out mutants whose tests fail in
# well under a second on a loaded machine; 30 times the clean run did not.
mutate() { "$gremlins" unleash --silent --invert-logical --invert-loopctrl --timeout-coefficient 30 --output "$1" "$2"; }
gate() { go run ./internal/testkit/mutgate -scope "$scope" -equivalent "$equivalent" "$@"; }

case "$mode" in
  pr)
    base=${2:-}
    report=${3:-mutation-report.md}
    min=80
    if [[ -z $base ]]; then
      echo "usage: mutation.sh pr BASE [REPORT] | full [REPORT]" >&2
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
    min=70
    roots=()
    while IFS=$'\t' read -r r _; do roots+=("$r"); done < <(grep -v '^#' "$scope" | grep -v '^[[:space:]]*$')
    flags=(-mode full -min "$min")
    ;;
  *)
    echo "usage: mutation.sh pr BASE [REPORT] | full [REPORT]" >&2
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
