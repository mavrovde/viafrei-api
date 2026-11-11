#!/usr/bin/env bash
# docs-html.test.sh: the self-test of the link rewrite in scripts/docs-html.pl.
# A link to a page becomes the page, a link to an existing repository file
# becomes its GitHub URL, and a misspelt page or a missing file is refused:
# exit 1, the path named on stderr, from any working directory.
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
pass=0 fail=0
perl=$(command -v perl)
script=$here/docs-html.pl

expect() { # expect NAME HREF WANT_EXIT WANT_HREF [WANT_STDERR]
  local out err status got
  out=$(cd "$tmp" && DOCS_PAGES="docs/errors.md=errors.html" DOCS_SITE_FILES="openapi.json" \
    PATH=${run_path:-$PATH} "$perl" "$script" docs <<<"<p><a href=\"$2\">x</a></p>" 2>"$tmp/stderr")
  status=$?
  err=$(cat "$tmp/stderr")
  got=$(sed -n 's/.*href="\([^"]*\)".*/\1/p' <<<"$out")
  if [[ $status != "$3" || $got != "$4" ]] || { [[ -n ${5:-} ]] && ! grep -qF -- "$5" <<<"$err"; }; then
    echo "not ok: $1 (exit $status, href $got)"; [[ -n $err ]] && echo "    $err"; fail=$((fail + 1))
  else
    echo "ok: $1"; pass=$((pass + 1))
  fi
}

expect "a page link becomes the page, keeping its fragment" "errors.md#problem-types" 0 "errors.html#problem-types"
expect "a site file is left as it is" "openapi.json" 0 "openapi.json"
expect "an existing script becomes its GitHub URL" "../scripts/docs-build.sh" 0 \
  "https://github.com/mavrovde/viafrei-api/blob/main/scripts/docs-build.sh"
expect "an existing directory becomes its GitHub URL" "../db" 0 "https://github.com/mavrovde/viafrei-api/blob/main/db"
expect "an external link is left as it is" "https://example.org/x" 0 "https://example.org/x"
expect "a misspelt page (docs/eror.md) is refused" "eror.md" 1 "missing-in-repo/docs/eror.md" \
  "FAIL: link to docs/eror.md: neither a page of the portal nor a file of the repository"
expect "a missing script (../scripts/nope.sh) is refused" "../scripts/nope.sh" 1 "missing-in-repo/scripts/nope.sh" \
  "FAIL: link to scripts/nope.sh"

# A copy of the script in a throwaway repository: a file git does not track
# is no file of the repository, and without git the script refuses.
repo=$tmp/repo
mkdir -p "$repo/scripts" "$repo/docs"
cp "$here/docs-html.pl" "$repo/scripts/"
echo tracked > "$repo/scripts/tracked.sh"
git init -q "$repo" && git -C "$repo" add scripts && git -C "$repo" -c user.email=t@example.org -c user.name=t commit -q -m t
echo untracked > "$repo/scripts/untracked.sh"
script=$repo/scripts/docs-html.pl
expect "a tracked file of the repository becomes its GitHub URL" "../scripts/tracked.sh" 0 \
  "https://github.com/mavrovde/viafrei-api/blob/main/scripts/tracked.sh"
expect "an untracked file is refused" "../scripts/untracked.sh" 1 "missing-in-repo/scripts/untracked.sh" \
  "FAIL: link to scripts/untracked.sh"
rm -rf "$repo/.git"
# No repository above $tmp may answer for it, even when TMPDIR lies inside a
# checkout.
export GIT_CEILING_DIRECTORIES=$(cd "$tmp" && pwd -P)
expect "outside a git repository the script refuses" "../scripts/tracked.sh" 2 "" \
  "git could not list the files of the repository"
script=$here/docs-html.pl run_path=/nonexistent
expect "without git the script refuses" "../scripts/docs-build.sh" 2 "" "cannot run git"
run_path=

echo "docs-html self-test: $pass passed, $fail failed"
(( fail == 0 && pass >= 11 ))
