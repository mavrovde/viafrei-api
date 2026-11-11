#!/usr/bin/env bash
# docs-a11y-ratchet.sh BASE_REF [BASELINE]
#
# The reference's accessibility baseline (scripts/docs-a11y-baseline.json,
# read by scripts/docs-a11y.sh) may only go down: every count in BASELINE
# must be at most its count at BASE_REF, and no page or rule may be added
# that BASE_REF does not list.
#
# A deliberate raise (a Redoc upgrade that brings a new failure) is accepted
# only when a commit in BASE_REF..HEAD that changes the baseline file carries
# a trailer naming exactly that page, rule and new count, with a reason:
#
#   A11y-Baseline-Raise: reference.html color-contrast 12 — <reason>
#
# The accepted raises are printed with their reasons. A raise with no such
# trailer, or a trailer for another count, fails.
#
# Fails closed: a base ref that is not a commit here (a shallow clone), a
# base file or a baseline that cannot be read as {page: {rule: count}} is
# refused (exit 2), never passed. A base that holds neither the baseline nor
# scripts/docs-a11y.sh is the first baseline: said out loud, and the only
# case that passes without a comparison. A base that holds the check but no
# baseline is refused (exit 2): the comparison cannot be skipped by deleting
# the file. An empty or all-zero BASE_REF (a push that opened a branch)
# compares with the parent commit, like scripts/openapi-diff.sh.
set -euo pipefail

base=${1:-}
file=${2:-scripts/docs-a11y-baseline.json}
shape='type == "object" and all(.[]; type == "object" and all(.[]; type == "number" and . >= 0))'

if [[ -z $base || $base =~ ^0+$ ]]; then
  base=$(git rev-parse HEAD^)
  echo "no base ref given; comparing with the parent commit $base"
fi
git cat-file -e "$base^{commit}" 2>/dev/null || { echo "FAIL: base ref $base is not a commit here (fetch-depth: 0?)" >&2; exit 2; }
jq -e "$shape" "$file" >/dev/null 2>&1 || { echo "FAIL: $file is not readable as {page: {rule: count}}" >&2; exit 2; }

if ! git cat-file -e "$base:$file" 2>/dev/null; then
  if git cat-file -e "$base:scripts/docs-a11y.sh" 2>/dev/null; then
    echo "FAIL: base $base has scripts/docs-a11y.sh but no $file: refusing to pass without a comparison" >&2
    exit 2
  fi
  echo "base $base has no $file and no scripts/docs-a11y.sh: this is the first baseline, nothing to compare"
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
git show "$base:$file" > "$tmp/base.json"
jq -e "$shape" "$tmp/base.json" >/dev/null 2>&1 || { echo "FAIL: $file at $base is not readable as {page: {rule: count}}" >&2; exit 2; }

# One line per rise: page, rule, new count, and what the base says.
rises=$(jq -r --slurpfile b "$tmp/base.json" '
  to_entries[] | .key as $page | .value | to_entries[] | .key as $rule | .value as $n
  | ($b[0][$page][$rule]) as $was
  | if $was == null then [$page, $rule, $n, "a rule the base does not list"]
    elif $n > $was then [$page, $rule, $n, "above the base'"'"'s \($was)"]
    else empty end | @tsv' "$file")

# The raise trailers of the commits in BASE..HEAD that change the file.
git log --format='%(trailers:key=A11y-Baseline-Raise,valueonly,unfold)' "$base..HEAD" -- "$file" |
  grep -v '^$' > "$tmp/trailers" || true

failed=0 accepted=0
while IFS=$'\t' read -r page rule n why; do
  [[ -n $page ]] || continue
  reason=$(awk -v p="$page" -v r="$rule" -v n="$n" '
    { prefix = p " " r " " n " \342\200\224 " }
    index($0, prefix) == 1 && length($0) > length(prefix) { print substr($0, length(prefix) + 1); exit }' "$tmp/trailers")
  if [[ -n $reason ]]; then
    echo "accepted raise: $page: $rule: $n ($why): $reason"
    accepted=$((accepted + 1))
  else
    echo "FAIL: $page: $rule: $n, $why (no A11y-Baseline-Raise: $page $rule $n — <reason> trailer in $base..HEAD)" >&2
    failed=1
  fi
done <<<"$rises"
if (( failed )); then
  echo "FAIL: the baseline may only go down (compared with $base), unless a raise is declared with its reason" >&2
  exit 1
fi
n=$(jq '[.[] | length] | add // 0' "$file")
if (( accepted > 0 )); then
  echo "docs a11y baseline: $n rule count(s), above $base only by the $accepted declared raise(s) above"
else
  echo "docs a11y baseline: $n rule count(s), none above $base"
fi
