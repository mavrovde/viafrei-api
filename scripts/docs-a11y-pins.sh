#!/usr/bin/env bash
# docs-a11y-pins.sh [LOCKFILE]
#
# The dependency review allows pa11y and pa11y-ci (LGPL-3.0) by NAME: the
# action ignores the version of an allow-dependencies-licenses entry. This
# check holds the version instead: in LOCKFILE (scripts/a11y-tools/
# package-lock.json) EVERY copy of pa11y must be exactly 10.0.0 and every
# copy of pa11y-ci exactly 5.0.0 — the top-level node_modules/<name> and any
# nested .../node_modules/<name> another package brings — so an upgrade, or
# a second version pulled in beside the reviewed one, fails here until the
# exception in .github/workflows/security.yml and these pins are reviewed
# together. A lockfile with no copy of either is a failure; an unreadable
# lockfile is refused (exit 2), never passed.
set -euo pipefail

lock=${1:-scripts/a11y-tools/package-lock.json}

jq -e '.packages | type == "object"' "$lock" >/dev/null 2>&1 ||
  { echo "FAIL: $lock is not readable as a package lock" >&2; exit 2; }
bad=0
for pin in pa11y=10.0.0 pa11y-ci=5.0.0; do
  name=${pin%%=*} want=${pin#*=}
  copies=$(jq -r --arg n "$name" '
    .packages | to_entries[] | select(.key | test("(^|/)node_modules/" + $n + "$"))
    | "\(.key)\t\(.value.version // "no version")"' "$lock")
  if [[ -z $copies ]]; then
    echo "FAIL: $lock has $name absent, the reviewed licence exception is for $want" >&2
    bad=1
    continue
  fi
  while IFS=$'\t' read -r key got; do
    if [[ $got == "$want" ]]; then
      echo "docs a11y pins: $name $got ($key)"
    else
      echo "FAIL: $lock has $name $got at $key, the reviewed licence exception is for $want" >&2
      bad=1
    fi
  done <<<"$copies"
done
exit "$bad"
