#!/usr/bin/env bash
# openapi-released.sh FILE
#
# Compares FILE with the document of the last release, the newest final
# vX.Y.Z tag reachable from HEAD, and fails on a breaking change: a removed
# operation, member or enum value, or a tightened parameter (TEST-004,
# VER-002). It runs beside the comparison with the base branch, because a
# breaking change can reach main in two steps that each look harmless.
#
# Before the first release there is nothing to compare: that is said out loud
# and passes. A shallow clone cannot tell whether a release exists and is a
# refusal (exit 2), never a pass (TST-011).
set -euo pipefail

file=${1:?usage: openapi-released.sh FILE}
here=$(cd "$(dirname "$0")" && pwd)

if [[ $(git rev-parse --is-shallow-repository) == true ]]; then
  echo "FAIL: a shallow clone cannot tell which release is the last one (fetch-depth: 0)" >&2
  exit 2
fi

tag=$(git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --merged HEAD --sort=-v:refname | grep -v -- - | head -n 1 || true)
if [[ -z $tag ]]; then
  echo "no release tag is reachable from HEAD: there is no released document to compare with yet"
  exit 0
fi

echo "last release: $tag"
exec "$here/openapi-diff.sh" "$tag" "$file"
