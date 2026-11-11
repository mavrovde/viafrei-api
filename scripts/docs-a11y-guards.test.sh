#!/usr/bin/env bash
# docs-a11y-guards.test.sh: the self-test of scripts/docs-a11y-pins.sh (the
# pinned versions behind the dependency review's licence exception) and of
# scripts/docs-a11y-ratchet.sh (the reference baseline may only go down), on
# planted lockfiles and in a throwaway git repository.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pass=0 fail=0

expect() { # expect WANT_EXIT NAME WANT_TEXT COMMAND...
  local want=$1 name=$2 text=$3 out status
  shift 3
  out=$("$@" 2>&1)
  status=$?
  if [[ $status != "$want" ]] || ! grep -qF -- "$text" <<<"$out"; then
    echo "not ok: $name (exit $status, want $want)"; while IFS= read -r l; do echo "    $l"; done <<<"$out"; fail=$((fail + 1))
  else
    echo "ok: $name"; pass=$((pass + 1))
  fi
}

# --- pins ---------------------------------------------------------------

lock() { # lock PA11Y PA11Y_CI > file
  jq -n --arg a "$1" --arg b "$2" '{packages: {"": {}, "node_modules/pa11y": {version: $a}, "node_modules/pa11y-ci": {version: $b}}}'
}
pins=$here/docs-a11y-pins.sh
expect 0 "the shipped lockfile holds the reviewed versions" "pa11y-ci 5.0.0" "$pins" "$here/a11y-tools/package-lock.json"
lock 10.0.0 5.0.0 > "$tmp/ok.json"
expect 0 "the reviewed versions pass" "docs a11y pins: pa11y 10.0.0" "$pins" "$tmp/ok.json"
lock 10.1.0 5.0.0 > "$tmp/up.json"
expect 1 "an upgraded pa11y fails" "has pa11y 10.1.0 at node_modules/pa11y, the reviewed licence exception is for 10.0.0" "$pins" "$tmp/up.json"
lock 10.0.0 6.0.0 > "$tmp/upci.json"
expect 1 "an upgraded pa11y-ci fails" "has pa11y-ci 6.0.0" "$pins" "$tmp/upci.json"
jq '.packages["node_modules/pa11y-ci/node_modules/pa11y"] = {version: "9.0.0"}' "$tmp/ok.json" > "$tmp/nested.json"
expect 1 "a nested pa11y at another version fails" "has pa11y 9.0.0 at node_modules/pa11y-ci/node_modules/pa11y" "$pins" "$tmp/nested.json"
jq '.packages["node_modules/other/node_modules/pa11y-ci"] = {version: "5.0.0"}' "$tmp/ok.json" > "$tmp/nestedok.json"
expect 0 "a nested copy at the reviewed version passes and is named" "pa11y-ci 5.0.0 (node_modules/other/node_modules/pa11y-ci)" "$pins" "$tmp/nestedok.json"
jq -n '{packages: {"node_modules/pa11y": {version: "10.0.0"}}}' > "$tmp/gone.json"
expect 1 "a lockfile without pa11y-ci fails" "has pa11y-ci absent" "$pins" "$tmp/gone.json"
echo 'not json' > "$tmp/bad.json"
expect 2 "an unreadable lockfile is refused" "is not readable as a package lock" "$pins" "$tmp/bad.json"

# --- ratchet ------------------------------------------------------------

ratchet=$here/docs-a11y-ratchet.sh
repo=$tmp/repo
git init -q "$repo"
g() { git -C "$repo" -c user.email=test@example.org -c user.name=test "$@"; }
f=scripts/docs-a11y-baseline.json
mkdir -p "$repo/scripts"
echo 'readme' > "$repo/README"
g add README && g commit -q -m empty
empty=$(g rev-parse HEAD)
echo '{"reference.html": {"color-contrast": 5, "region": 2}}' > "$repo/$f"
g add "$f" && g commit -q -m base
base=$(g rev-parse HEAD)
r() { (cd "$repo" && "$ratchet" "$@"); }

expect 0 "an unchanged baseline passes" "none above" r "$base"
echo '{"reference.html": {"color-contrast": 4, "region": 2}}' > "$repo/$f"
expect 0 "a lowered count passes" "none above" r "$base"
echo '{"reference.html": {"color-contrast": 5}}' > "$repo/$f"
expect 0 "a rule removed (fixed) passes" "none above" r "$base"
echo '{"reference.html": {"color-contrast": 6, "region": 2}}' > "$repo/$f"
expect 1 "a raised count fails" "reference.html: color-contrast: 6, above the base's 5" r "$base"
echo '{"reference.html": {"color-contrast": 5, "region": 2, "list": 1}}' > "$repo/$f"
expect 1 "an added rule fails" "reference.html: list: 1, a rule the base does not list" r "$base"
echo '{"reference.html": {"color-contrast": 5, "region": 2}, "index.html": {"region": 1}}' > "$repo/$f"
expect 1 "an added page fails" "index.html: region: 1, a rule the base does not list" r "$base"
echo '{"reference.html": {"color-contrast": 5}}' > "$repo/$f"
expect 0 "a base without the file or the check is the first baseline" "this is the first baseline" r "$empty"
expect 2 "a base that is not a commit here is refused" "is not a commit here" r 0123456789abcdef0123456789abcdef01234567
echo '{"reference.html": {"color-contrast": "five"}}' > "$repo/$f"
expect 2 "an unreadable baseline is refused" "is not readable as {page: {rule: count}}" r "$base"
# The parent-commit fallback: an empty or all-zero base compares with HEAD^.
echo '{"reference.html": {"color-contrast": 4, "region": 2}}' > "$repo/$f"
g add "$f" && g commit -q -m lower
expect 0 "an empty base compares with the parent commit" "comparing with the parent commit $base" r ""
expect 0 "an all-zero base compares with the parent commit" "comparing with the parent commit $base" r 0000000000000000000000000000000000000000
echo '{"reference.html": {"color-contrast": 6, "region": 2}}' > "$repo/$f"
g add "$f" && g commit -q -m rise
expect 1 "a rise over the parent commit fails on the fallback path" "color-contrast: 6, above the base's 4" r ""

# Deliberate raises: a trailer naming the page, rule and exact count.
g reset -q --hard "$base"
raise() { # raise COUNT TRAILER: commit the baseline at COUNT with TRAILER
  echo "{\"reference.html\": {\"color-contrast\": $1, \"region\": 2}}" > "$repo/$f"
  g add "$f" && g commit -q -m "raise" -m "$2"
}
raise 7 'A11y-Baseline-Raise: reference.html color-contrast 7 — Redoc 3 draws two new code colours'
expect 0 "a raise with a matching trailer passes" "accepted raise: reference.html: color-contrast: 7 (above the base's 5): Redoc 3 draws two new code colours" r "$base"
g reset -q --hard "$base"
raise 7 'A11y-Baseline-Raise: reference.html color-contrast 6 — the count is wrong'
expect 1 "a raise whose trailer names another count fails" "color-contrast: 7, above the base's 5 (no A11y-Baseline-Raise" r "$base"
g reset -q --hard "$base"
raise 7 'Refs: #20'
expect 1 "a raise without a trailer fails" "color-contrast: 7, above the base's 5 (no A11y-Baseline-Raise" r "$base"
g reset -q --hard "$base"
raise 7 'A11y-Baseline-Raise: reference.html color-contrast 7 — '
expect 1 "a trailer with no reason fails" "(no A11y-Baseline-Raise" r "$base"
g reset -q --hard "$base"
echo other > "$repo/OTHER"
g add OTHER && g commit -q -m other -m 'A11y-Baseline-Raise: reference.html color-contrast 7 — on a commit that does not touch the file'
echo '{"reference.html": {"color-contrast": 7, "region": 2}}' > "$repo/$f"
g add "$f" && g commit -q -m raise
expect 1 "a trailer on a commit that does not change the baseline fails" "(no A11y-Baseline-Raise" r "$base"
g reset -q --hard "$base"

# A base that has the check but no baseline must not pass as a first one.
g checkout -q -b nofile "$empty"
mkdir -p "$repo/scripts" && echo 'check' > "$repo/scripts/docs-a11y.sh"
g add scripts/docs-a11y.sh && g commit -q -m check
checkonly=$(g rev-parse HEAD)
echo '{"reference.html": {"color-contrast": 5}}' > "$repo/$f"
expect 2 "a base with the check but no baseline is refused" "has scripts/docs-a11y.sh but no $f" r "$checkonly"
rm -f "$repo/$f"
g checkout -q -

echo 'broken' > "$repo/$f" && g add "$f" && g commit -q -m broken
broken=$(g rev-parse HEAD)
echo '{"reference.html": {"color-contrast": 5}}' > "$repo/$f"
expect 2 "an unreadable base file is refused" "at $broken is not readable" r "$broken"

echo "docs-a11y guards self-test: $pass passed, $fail failed"
(( fail == 0 && pass >= 27 ))
