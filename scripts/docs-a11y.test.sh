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
# counted as distinct failures: one per colour pair for color-contrast, one
# per selector with its nth-child parts and numbers removed for other rules.
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

issue() { # issue TYPE CODE NEEDS_REVIEW [TARGET [FG BG]]
  jq -n --arg t "$1" --arg c "$2" --argjson r "$3" --arg target "${4:-p}" --arg fg "${5:-}" --arg bg "${6:-}" \
    '{type: $t, code: $c, message: "planted", selector: "p", runner: "axe",
      runnerExtras: ({needsFurtherReview: $r, target: $target}
        + (if $fg == "" then {} else {fgColor: $fg, bgColor: $bg} end))}'
}
report() { # report GUIDE_ISSUES_JSON REFERENCE_ISSUES_JSON
  jq -n --arg g "file://$s/index.html" --arg r "file://$s/reference.html" --argjson gi "$1" --argjson ri "$2" \
    '{total: 2, passes: 0, errors: 0, results: {($g): $gi, ($r): $ri}}' > "$STUB_REPORT"
}
cc=$(issue error color-contrast true)
green=$(issue error color-contrast true 'span' 'rgb(160, 251, 170)' 'rgb(17, 23, 26)')
green2=$(issue error color-contrast true 'pre > span:nth-child(3)' 'rgb(160, 251, 170)' 'rgb(17, 23, 26)')
red=$(issue error color-contrast true 'span' 'rgb(212, 31, 28)' 'rgb(17, 23, 26)')
white=$(issue error color-contrast true 'span' 'rgb(255, 255, 255)' 'rgb(17, 23, 26)')

report '[]' "[$green, $red]"
expect 0 "reference.html's notes at the baseline pass" "$s" "reference.html: 2 distinct failure(s) (2 result(s)) within the baseline"

report '[]' "[$green, $green2, $green, $red, $red]"
expect 0 "results of one colour pair are one failure, wherever they are" "$s" "reference.html: 2 distinct failure(s) (5 result(s)) within the baseline"

report '[]' "[$cc, $cc, $green]"
expect 0 "results whose colours axe could not tell are one failure" "$s" "reference.html: 2 distinct failure(s) (3 result(s)) within the baseline"

report '[]' "[$red]"
expect 0 "a count below the baseline passes and asks to lower it" "$s" "below the baseline of 2: lower it"

report '[]' "[$green, $red, $white]"
expect 1 "a new colour pair above the baseline fails" "$s" "FAIL: reference.html: color-contrast: 3 distinct failure(s), above the baseline of 2"

printf '{"reference.html": {"color-contrast": 2, "region": 1}}\n' > "$tmp/baseline.json"
row() { issue warning region false "#operation\\/getKey > div > table > tbody > tr:nth-child($1) > td.sc-x$1"; }
report '[]' "[$(row 1), $(row 2), $(row 10)]"
expect 0 "rows of one table are one failure (nth-child and numbers removed)" "$s" "reference.html: 1 distinct failure(s) (3 result(s)) within the baseline"
report '[]' "[$(row 1), $(issue warning region false '#operation\\/getMeta > div')]"
expect 1 "a region failure in another place is a new failure" "$s" "FAIL: reference.html: region: 2 distinct failure(s), above the baseline of 1"
printf '{"reference.html": {"color-contrast": 2}}\n' > "$tmp/baseline.json"

report '[]' "[$(issue warning region false)]"
expect 1 "a rule not in the baseline fails" "$s" "FAIL: reference.html: region: 1 distinct failure(s), a rule not in the baseline"

report '[{"message": "net::ERR_FILE_NOT_FOUND"}]' '[]'
expect 1 "a guide page that did not load fails" "$s" "FAIL: index.html: could not be checked: net::ERR_FILE_NOT_FOUND"

report '[]' '[{"message": "timed out"}]'
expect 1 "reference.html not loading fails" "$s" "FAIL: reference.html: could not be checked: timed out"

report "[$cc]" '[]'
expect 1 "a needs-review result on a guide page fails" "$s" "FAIL: index.html: color-contrast (review)"

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
(( fail == 0 && pass >= 22 ))
