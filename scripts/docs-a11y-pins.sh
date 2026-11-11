#!/usr/bin/env bash
# docs-a11y-pins.sh [LOCKFILE]
#
# The dependency review allows pa11y and pa11y-ci (LGPL-3.0) by NAME: the
# action ignores the version of an allow-dependencies-licenses entry. This
# check holds the version instead: LOCKFILE (scripts/a11y-tools/
# package-lock.json) must resolve node_modules/pa11y to exactly 10.0.0 and
# node_modules/pa11y-ci to exactly 5.0.0, so an upgrade fails here until the
# exception in .github/workflows/security.yml and these pins are reviewed
# together. An unreadable lockfile is refused (exit 2), never passed.
set -euo pipefail

lock=${1:-scripts/a11y-tools/package-lock.json}

jq -e '.packages | type == "object"' "$lock" >/dev/null 2>&1 ||
  { echo "FAIL: $lock is not readable as a package lock" >&2; exit 2; }
bad=0
for pin in pa11y=10.0.0 pa11y-ci=5.0.0; do
  name=${pin%%=*} want=${pin#*=}
  got=$(jq -r --arg k "node_modules/$name" '.packages[$k].version // "absent"' "$lock")
  if [[ $got == "$want" ]]; then
    echo "docs a11y pins: $name $got"
  else
    echo "FAIL: $lock has $name $got, the reviewed licence exception is for $want" >&2
    bad=1
  fi
done
exit "$bad"
