#!/usr/bin/env bash
# openapi-diff.sh BASE_REF FILE
#
# Compares FILE with its version at BASE_REF using oasdiff: prints the
# changelog and fails on a breaking change (VER-001, VER-002). A base that
# does not hold the file yet is said out loud and is the only case that passes
# without a comparison; an unreadable base ref is a failure (TST-011).
#
# api/oasdiff-levels.txt sets oasdiff's levels to the specification's table
# of what is breaking (Spec-Compatibility-and-Versioning, section 2):
# - a value ADDED to an enum in a response is info. Every enumeration of the
#   public schema is open (ENV-050): a client must tolerate a value it does
#   not know, so adding one is not breaking;
# - a value REMOVED from an enum in a response is an error (VER-002,
#   TEST-004); oasdiff's own level for it is info.
# Every other check, including any change to an enum a client sends, keeps
# oasdiff's own level.
set -euo pipefail

OASDIFF="go run github.com/oasdiff/oasdiff@v1.34.0"
base=${1:-}
file=${2:?usage: openapi-diff.sh BASE_REF FILE}
levels="$(cd "$(dirname "$0")/.." && pwd)/api/oasdiff-levels.txt"
[[ -s $levels ]] || { echo "FAIL: $levels is missing or empty" >&2; exit 2; }

if [[ -z $base || $base =~ ^0+$ ]]; then
  base=$(git rev-parse HEAD^)
  echo "no base ref given; comparing with the parent commit $base"
fi
git cat-file -e "$base^{commit}" 2>/dev/null || { echo "FAIL: base ref $base is not a commit here (fetch-depth: 0?)" >&2; exit 2; }
[[ -s $file ]] || { echo "FAIL: $file is missing or empty" >&2; exit 2; }

if ! git cat-file -e "$base:$file" 2>/dev/null; then
  echo "base $base has no $file: this is the first contract, nothing to compare"
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
git show "$base:$file" > "$tmp/base.json"

ops=$(jq '[.paths[] | keys[]] | length' "$file")
echo "comparing $file ($ops operations) with $base"
$OASDIFF changelog --severity-levels "$levels" "$tmp/base.json" "$file"
$OASDIFF breaking --fail-on ERR --severity-levels "$levels" "$tmp/base.json" "$file"
echo "no breaking change"
