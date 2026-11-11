#!/usr/bin/env bash
# docs-a11y.sh SITE_DIR
#
# The automated accessibility check of the built documentation portal (#20,
# NFR-A11Y-001): every page is loaded in headless Chrome and checked by axe
# (pa11y-ci with the axe runner) against WCAG 2.0, 2.1 and 2.2 level A and AA.
# A critical or serious finding fails the check; moderate and minor findings,
# and findings axe could not decide by itself, are printed as notes for the
# manual pass (docs/accessibility.md) and do not fail it.
#
# Prints every page it read and fails closed: no page, a page pa11y-ci did
# not report, or a report it could not read is a failure, never a pass.
#
# Chrome is the system's: CHROME_PATH, else the first of google-chrome,
# chromium, chromium-browser on PATH, else the macOS application. pa11y-ci is
# pinned and run without install scripts, so no browser is downloaded.
set -euo pipefail

site=${1:?usage: docs-a11y.sh SITE_DIR}
pa11y=(npm exec --yes --ignore-scripts --package=pa11y-ci@5.0.0 -- pa11y-ci)

site=$(cd "$site" && pwd)
pages=()
while IFS= read -r f; do pages+=("$f"); done < <(find "$site" -maxdepth 1 -name '*.html' -type f | sort)
(( ${#pages[@]} > 0 )) || { echo "FAIL: no page found in $site: refusing to report a clean site" >&2; exit 2; }

chrome=${CHROME_PATH:-}
if [[ -z $chrome ]]; then
  for c in google-chrome chromium chromium-browser; do
    chrome=$(command -v "$c" || true)
    [[ -n $chrome ]] && break
  done
fi
[[ -n $chrome ]] || chrome="/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
[[ -x $chrome ]] || { echo "FAIL: no Chrome found; set CHROME_PATH" >&2; exit 2; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# WCAG 2.2 AA: the axe runner selects the 2.0 and 2.1 A/AA tags; the 2.2 AA
# rule is enabled by name.
jq -n --arg chrome "$chrome" '{
  defaults: {
    runners: ["axe"],
    standard: "WCAG2AA",
    rules: ["target-size"],
    includeWarnings: true,
    includeNotices: true,
    timeout: 60000,
    chromeLaunchConfig: {executablePath: $chrome, args: ["--no-sandbox", "--disable-dev-shm-usage"]}
  },
  urls: $ARGS.positional
}' --args "${pages[@]/#/file://}" > "$tmp/config.json"

for p in "${pages[@]}"; do echo "docs a11y: read ${p#"$site"/}"; done

set +e
"${pa11y[@]}" --config "$tmp/config.json" --json > "$tmp/report.json" 2> "$tmp/stderr"
status=$?
set -e
jq -e '.results | type == "object"' "$tmp/report.json" >/dev/null 2>&1 ||
  { echo "FAIL: pa11y-ci wrote no readable report (exit $status)" >&2; cat "$tmp/stderr" >&2; exit 2; }

reported=$(jq '.results | length' "$tmp/report.json")
[[ $reported == "${#pages[@]}" ]] ||
  { echo "FAIL: pa11y-ci reported $reported page(s) of ${#pages[@]}" >&2; exit 2; }

# axe's impact critical or serious is pa11y's "error". A finding axe could
# not decide (needsFurtherReview, such as a contrast over a background it
# cannot compute) is printed for the manual pass, not counted. A page that
# could not be loaded is reported as an error object: that is a failure too.
jq -r --arg site "$site/" '
  .results | to_entries[] | (.key | sub("^file://"; "") | sub("^" + $site; "")) as $page | .value[]
  | if (.type == null) then "fail\t\($page)\tcould not be checked: \(.message)"
    elif .type == "error" and (.runnerExtras.needsFurtherReview | not) then "fail\t\($page)\t\(.code): \(.message) [\(.selector)]"
    elif .type == "error" then "review\t\($page)\t\(.code)"
    else "\(.type)\t\($page)\t\(.code)" end' "$tmp/report.json" > "$tmp/issues.tsv"

fails=$(awk -F'\t' '$1 == "fail"' "$tmp/issues.tsv" | wc -l | tr -d ' ')
others=$(awk -F'\t' '$1 != "fail"' "$tmp/issues.tsv" | wc -l | tr -d ' ')
awk -F'\t' '$1 != "fail" { print "note (" $1 "): " $2 ": " $3 }' "$tmp/issues.tsv" | sort | uniq -c
if (( fails > 0 )); then
  awk -F'\t' '$1 == "fail" { print "FAIL: " $2 ": " $3 }' "$tmp/issues.tsv" >&2
  echo "FAIL: $fails critical or serious finding(s) over ${#pages[@]} page(s)" >&2
  exit 1
fi
echo "docs a11y: ${#pages[@]} page(s), 0 critical or serious finding(s); $others note(s) for the manual pass"
