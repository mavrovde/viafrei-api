#!/usr/bin/env bash
# docs-a11y-ratchet.sh BASE_REF [BASELINE]
#
# The reference's accessibility baseline (scripts/docs-a11y-baseline.json,
# read by scripts/docs-a11y.sh) may only go down: every count in BASELINE
# must be at most its count at BASE_REF, and no page or rule may be added
# that BASE_REF does not list.
#
# Fails closed: a base ref that is not a commit here (a shallow clone), a
# base file or a baseline that cannot be read as {page: {rule: count}} is
# refused (exit 2), never passed. A base that does not hold the file yet is
# said out loud and is the only case that passes without a comparison. An
# empty or all-zero BASE_REF (a push that opened a branch) compares with the
# parent commit, like scripts/openapi-diff.sh.
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
  echo "base $base has no $file: this is the first baseline, nothing to compare"
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
git show "$base:$file" > "$tmp/base.json"
jq -e "$shape" "$tmp/base.json" >/dev/null 2>&1 || { echo "FAIL: $file at $base is not readable as {page: {rule: count}}" >&2; exit 2; }

rises=$(jq -r --slurpfile b "$tmp/base.json" '
  to_entries[] | .key as $page | .value | to_entries[] | .key as $rule | .value as $n
  | ($b[0][$page][$rule]) as $was
  | if $was == null then "\($page): \($rule): \($n), a rule the base does not list"
    elif $n > $was then "\($page): \($rule): \($n), above the base'"'"'s \($was)"
    else empty end' "$file")
if [[ -n $rises ]]; then
  while IFS= read -r r; do echo "FAIL: $r" >&2; done <<<"$rises"
  echo "FAIL: the baseline may only go down (compared with $base)" >&2
  exit 1
fi
echo "docs a11y baseline: $(jq '[.[] | length] | add // 0' "$file") rule count(s), none above $base"
