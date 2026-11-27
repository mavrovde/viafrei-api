#!/usr/bin/env bash
# release-api-notes.sh BREAKING_JSON CHANGELOG_TXT PREV
#
# Prints the release page's API section from oasdiff's results against the
# previous release PREV. BREAKING_JSON is `oasdiff breaking --format json`:
# a non-empty array is a breaking change and heads "Breaking API changes";
# an empty array (or null) with a changelog that lists changes heads
# "API changes (non-breaking)"; no change prints nothing. The text of
# `oasdiff breaking` is never read: it is non-empty even when it reports no
# breaking change ("No breaking changes to report, but the specs are
# different"). An unreadable BREAKING_JSON is a failure, never "no break".
set -euo pipefail
breaking_json=${1:?usage: release-api-notes.sh BREAKING_JSON CHANGELOG_TXT PREV}
changelog=${2:?usage: release-api-notes.sh BREAKING_JSON CHANGELOG_TXT PREV}
prev=${3:?usage: release-api-notes.sh BREAKING_JSON CHANGELOG_TXT PREV}
[[ -f $breaking_json ]] || { echo "FAIL: $breaking_json is missing" >&2; exit 2; }
n=$(jq 'if . == null then 0 elif type == "array" then length else error("not an array") end' "$breaking_json") \
  || { echo "FAIL: $breaking_json is not oasdiff's JSON array" >&2; exit 2; }
# An empty file (oasdiff died after the redirect created it) gives jq no
# value and no error: it is a failure, never "no break".
[[ $n =~ ^[0-9]+$ ]] || { echo "FAIL: $breaking_json holds no JSON value" >&2; exit 2; }
if (( n > 0 )); then
  echo "## ⚠ Breaking API changes"
  echo
  echo "oasdiff reports $n breaking change(s) against $prev; see \`openapi-breaking.txt\`."
  echo
elif [[ -f $changelog ]] && grep -q '^[0-9][0-9]* changes:' "$changelog" && ! grep -q '^0 changes:' "$changelog"; then
  echo "## API changes (non-breaking)"
  echo
  echo "oasdiff reports no breaking change against $prev; the changes are in \`openapi-changelog.txt\`."
  echo
fi
