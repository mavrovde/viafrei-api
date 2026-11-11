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
expect 1 "an upgraded pa11y fails" "has pa11y 10.1.0, the reviewed licence exception is for 10.0.0" "$pins" "$tmp/up.json"
lock 10.0.0 6.0.0 > "$tmp/upci.json"
expect 1 "an upgraded pa11y-ci fails" "has pa11y-ci 6.0.0" "$pins" "$tmp/upci.json"
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
expect 0 "a base without the file is the first baseline" "this is the first baseline" r "$empty"
expect 2 "a base that is not a commit here is refused" "is not a commit here" r 0123456789abcdef0123456789abcdef01234567
echo '{"reference.html": {"color-contrast": "five"}}' > "$repo/$f"
expect 2 "an unreadable baseline is refused" "is not readable as {page: {rule: count}}" r "$base"
echo 'broken' > "$repo/$f" && g add "$f" && g commit -q -m broken
broken=$(g rev-parse HEAD)
echo '{"reference.html": {"color-contrast": 5}}' > "$repo/$f"
expect 2 "an unreadable base file is refused" "at $broken is not readable" r "$broken"

echo "docs-a11y guards self-test: $pass passed, $fail failed"
(( fail == 0 ))
