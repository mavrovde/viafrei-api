#!/usr/bin/env bash
# docs-a11y.test.sh: the self-test of scripts/docs-a11y.sh.
#
# With Chrome (CHROME_PATH or on PATH): a site without pages is refused
# before any browser starts, a clean page passes and is named, and a planted
# critical finding (an image without a text alternative) and a planted
# moderate one (a skipped heading level) each fail.
#
# With a stub in place of pa11y-ci (DOCS_A11Y_PA11Y), every fail-closed path
# and the classification: a page that did not load, fewer pages reported
# than the site has, an unreadable report, an unreadable baseline, a
# needs-review result, a moderate and a minor result on a guide page, and
# reference.html's notes held against the baseline in each direction,
# counted as distinct failures: one per place, the selector with its
# nth-child parts and numbers removed, whatever colours a result carries.
# A needs-review contrast result whose reason is partial obscuring and whose
# computed pair meets its WCAG ratio is decided (a pass), and each near miss
# of that rule (another reason, no reason, a pair from axe, a ratio under or
# missing, a violation) is still a finding.
# A critical or serious result on reference.html fails whatever the baseline.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pass=0 fail=0

expect() { # expect WANT_EXIT NAME DIR WANT_TEXT
  local out status
  out=$("$here/docs-a11y.sh" "$3" 2>&1)
  status=$?
  if [[ $status != "$1" ]] || ! grep -qF -- "$4" <<<"$out"; then
    echo "not ok: $2 (exit $status, want $1)"; while IFS= read -r l; do echo "    $l"; done <<<"$out"; fail=$((fail + 1))
  else
    echo "ok: $2"; pass=$((pass + 1))
  fi
}

page() { # page FILE BODY
  printf '<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Planted page</title></head><body><main><h1>Planted page</h1>%s</main></body></html>\n' "$2" > "$1"
}

# --- with Chrome --------------------------------------------------------

mkdir -p "$tmp/empty"
expect 2 "a site without pages is refused" "$tmp/empty" "no page found"

mkdir -p "$tmp/clean"
page "$tmp/clean/index.html" '<p>Nothing to find here.</p>'
expect 0 "a clean page passes and is named" "$tmp/clean" "docs a11y: found index.html"

mkdir -p "$tmp/broken"
page "$tmp/broken/index.html" '<p>Nothing to find here.</p>'
page "$tmp/broken/diagram.html" '<img src="diagram.png">'
expect 1 "an image without alt fails" "$tmp/broken" "FAIL: diagram.html: image-alt"

mkdir -p "$tmp/moderate"
page "$tmp/moderate/index.html" '<h3>A skipped level</h3><p>Text.</p>'
expect 1 "a moderate finding on a guide page fails" "$tmp/moderate" "FAIL: index.html: heading-order"

# --- with a stub in place of pa11y-ci -----------------------------------

cat > "$tmp/stub" <<'EOF'
#!/usr/bin/env bash
cat "$STUB_REPORT"
EOF
chmod +x "$tmp/stub"
export DOCS_A11Y_PA11Y=$tmp/stub STUB_REPORT=$tmp/report.json
printf '{"reference.html": {"color-contrast": 2}}\n' > "$tmp/baseline.json"
export DOCS_A11Y_BASELINE=$tmp/baseline.json

s=$tmp/stubsite
mkdir -p "$s"
page "$s/index.html" '<p>Guide.</p>'
page "$s/reference.html" '<p>Reference.</p>'
s=$(cd "$s" && pwd)

issue() { # issue TYPE CODE NEEDS_REVIEW [TARGET [FG BG [SOURCE]]]
  local type=$1 code=$2 review=$3 target=${4:-p} fg=${5:-} bg=${6:-} source=${7:-computed}
  jq -n --arg t "$type" --arg c "$code" --argjson r "$review" --arg target "$target" --arg fg "$fg" --arg bg "$bg" --arg src "$source" \
    '{type: $t, code: $c, message: "planted", selector: "p", runner: "axe",
      runnerExtras: ({needsFurtherReview: $r, target: $target}
        + (if $fg == "" then {} else {fgColor: $fg, bgColor: $bg, colourSource: $src} end))}'
}
report() { # report GUIDE_ISSUES_JSON REFERENCE_ISSUES_JSON
  local guide=$1 reference=$2
  jq -n --arg g "file://$s/index.html" --arg r "file://$s/reference.html" --argjson gi "$guide" --argjson ri "$reference" \
    '{total: 2, passes: 0, errors: 0, results: {($g): $gi, ($r): $ri}}' > "$STUB_REPORT"
}
cc=$(issue warning color-contrast true)
green=$(issue warning color-contrast true 'span' 'rgb(160, 251, 170)' 'rgb(17, 23, 26)')
red=$(issue warning color-contrast true 'span' 'rgb(212, 31, 28)' 'rgb(17, 23, 26)')
white=$(issue warning color-contrast true 'span' 'rgb(255, 255, 255)' 'rgb(17, 23, 26)')
other=$(issue warning color-contrast true 'pre > code' 'rgb(160, 251, 170)' 'rgb(17, 23, 26)')

report '[]' "[$green, $other]"
expect 0 "reference.html's notes at the baseline pass" "$s" "reference.html: 2 distinct failure(s) (2 result(s)) within the baseline"

report '[]' "[$green, $red, $white, $other]"
expect 0 "results in one place are one failure, whatever their colours" "$s" "reference.html: 2 distinct failure(s) (4 result(s)) within the baseline"

report '[]' "[$cc, $cc, $green]"
expect 0 "undetermined results in one place are one failure" "$s" "reference.html: 2 distinct failure(s) (3 result(s)) within the baseline"

undet() { # undet TARGET: a contrast result whose pair could not be read
  local target=$1
  jq -n --arg target "$target" '{type: "warning", code: "color-contrast", message: "planted", selector: "p",
    runnerExtras: {needsFurtherReview: true, target: $target, colourSource: "undetermined", fgColor: null, bgColor: null}}'
}
report '[]' "[$(undet '#a > span:nth-child(1)'), $(undet '#a > span:nth-child(4)'), $(undet '#b > span')]"
expect 0 "undetermined results are counted per place, not as one bucket" "$s" "reference.html: 2 distinct failure(s) (3 result(s)) within the baseline"
report '[]' "[$(undet '#a > span'), $(undet '#b > span'), $(undet '#c > span')]"
expect 1 "a third place is above the baseline" "$s" "FAIL: reference.html: color-contrast: 3 distinct failure(s), above the baseline of 2"

report '[]' "[$red]"
expect 0 "a count below the baseline passes and asks to lower it" "$s" "below the baseline of 2: lower it"

printf '{"reference.html": {"color-contrast": 2, "region": 1}}\n' > "$tmp/baseline.json"
row() { # row N: a region result in the Nth row of one table
  local n=$1
  issue warning region false "#operation\\/getKey > div > table > tbody > tr:nth-child($n) > td.sc-x$n"
}
report '[]' "[$(row 1), $(row 2), $(row 10)]"
expect 0 "rows of one table are one failure (nth-child and numbers removed)" "$s" "reference.html: 1 distinct failure(s) (3 result(s)) within the baseline"
report '[]' "[$(row 1), $(issue warning region false '#operation\\/getMeta > div')]"
expect 1 "a region failure in another place is a new failure" "$s" "FAIL: reference.html: region: 2 distinct failure(s), above the baseline of 1"
printf '{"reference.html": {"color-contrast": 2}}\n' > "$tmp/baseline.json"

review_fail='FAIL: index.html: color-contrast (review)'

# A needs-review contrast result whose computed pair meets its WCAG ratio is
# decided: a pass on any page, counted on its own line. Each near miss is not.
measured() { # measured REVIEW SOURCE RATIO REQUIRED [MESSAGE_KEY]: '-' for no key
  local review=$1 source=$2 ratio=$3 required=$4 key=${5:-elmPartiallyObscured}
  jq -n --argjson r "$review" --arg src "$source" --argjson ratio "$ratio" --argjson req "$required" --arg key "$key" \
    '{type: "error", code: "color-contrast",
    message: "planted", selector: "p", runnerExtras: ({needsFurtherReview: $r, target: "td > span", colourSource: $src,
    fgColor: "rgb(0, 0, 0)", bgColor: "rgb(255, 255, 255)", contrastRatio: $ratio, requiredRatio: $req}
      + (if $key == "-" then {} else {messageKey: $key} end))}'
}
report "[$(measured true computed 4.67 4.5), $(measured true computed 3 3 elmPartiallyObscuring)]" "[$(measured true computed 21 4.5)]"
expect 0 "needs-review results partly obscured whose computed pair meets its ratio are decided" "$s" "docs a11y: 3 contrast result(s) left for review by axe are decided"
report '[]' "[$(measured true computed 21 4.5), $green, $other]"
expect 0 "a decided result is not counted against the baseline" "$s" "reference.html: 2 distinct failure(s) (2 result(s)) within the baseline"
report "[$(measured true computed 21 4.5 bgOverlap)]" '[]'
expect 1 "a result axe left for another reason is not decided" "$s" "$review_fail"
report "[$(measured true computed 21 4.5 -)]" '[]'
expect 1 "a result with no reason is not decided" "$s" "$review_fail"
report "[$(measured true computed 4.49 4.5)]" '[]'
expect 1 "a computed pair under its ratio is still a finding" "$s" "$review_fail"
report "[$(measured true computed null 4.5)]" '[]'
expect 1 "a pair with no measured ratio is still a finding" "$s" "$review_fail"
report "[$(measured true axe 21 4.5)]" '[]'
expect 1 "only a computed pair decides, not one axe compared" "$s" "$review_fail"
report "[$(measured false computed 21 4.5)]" '[]'
expect 1 "a violation axe decided is never overruled" "$s" "FAIL: index.html: color-contrast (error)"

# A critical or serious result on reference.html fails, even under a rule
# the baseline holds; so does an undetermined serious contrast result.
report '[]' "[$(issue error color-contrast false 'span' 'rgb(1, 1, 1)' 'rgb(2, 2, 2)' axe)]"
expect 1 "a serious result on reference.html fails whatever the baseline holds" "$s" "FAIL: reference.html: color-contrast (error): a critical or serious result, which no baseline holds"
report '[]' "[$(issue error color-contrast true)]"
expect 1 "a serious needs-review result on reference.html that is not decided fails" "$s" "FAIL: reference.html: color-contrast (review): a critical or serious result"

report '[]' "[$(issue warning region false)]"
expect 1 "a rule not in the baseline fails" "$s" "FAIL: reference.html: region: 1 distinct failure(s), a rule not in the baseline"

report '[{"message": "net::ERR_FILE_NOT_FOUND"}]' '[]'
expect 1 "a guide page that did not load fails" "$s" "FAIL: index.html: could not be checked: net::ERR_FILE_NOT_FOUND"

report '[]' '[{"message": "timed out"}]'
expect 1 "reference.html not loading fails" "$s" "FAIL: reference.html: could not be checked: timed out"

report "[$cc]" '[]'
expect 1 "a needs-review result on a guide page fails" "$s" "$review_fail"

report "[$(issue warning heading-order false)]" '[]'
expect 1 "a moderate result on a guide page fails" "$s" "FAIL: index.html: heading-order (warning)"

report "[$(issue notice region false)]" '[]'
expect 1 "a minor result on a guide page fails" "$s" "FAIL: index.html: region (notice)"

jq -n --arg g "file://$s/index.html" '{results: {($g): []}}' > "$STUB_REPORT"
expect 2 "fewer pages reported than the site has is refused" "$s" "pa11y-ci reported 1 page(s) of 2"

jq -n --arg g "file://$s/index.html" --arg o "file://$s/other.html" '{results: {($g): [], ($o): []}}' > "$STUB_REPORT"
expect 2 "a report about another page is refused" "$s" "not reported:"

echo 'not a report' > "$STUB_REPORT"
expect 2 "an unreadable report is refused" "$s" "pa11y-ci wrote no readable report"

jq -n --arg g "file://$s/index.html" --arg r "file://$s/reference.html" '{results: {($g): {}, ($r): []}}' > "$STUB_REPORT"
expect 2 "a report whose page entry is not a list is refused" "$s" "pa11y-ci wrote no readable report"

report '[]' '[]'
echo '{"reference.html": {"color-contrast": "two"}}' > "$tmp/baseline.json"
expect 2 "an unreadable baseline is refused" "$s" "is not readable as {page: {rule: count}}"

echo "docs-a11y self-test: $pass passed, $fail failed"
(( fail == 0 && pass >= 33 ))
